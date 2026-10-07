// Package worker speaks queued summaries sequentially behind one lock.
package worker

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/logging"
	"github.com/rmscoal/turnecho/internal/paths"
	"github.com/rmscoal/turnecho/internal/player"
	"github.com/rmscoal/turnecho/internal/queue"
	"github.com/rmscoal/turnecho/internal/speak"
	"github.com/rmscoal/turnecho/internal/tts"
)

// ErrAlreadyRunning reports that another worker owns the lock.
var ErrAlreadyRunning = errors.New("turnecho worker is already running")

// osExecutable locates the running binary for worker respawn. Tests override it.
var osExecutable = os.Executable

const (
	// PollInterval is the delay between empty queue polls.
	PollInterval = 250 * time.Millisecond
	// IdleTimeout exits the worker after this long without a job.
	IdleTimeout = 600 * time.Second
	// LockRetry is how long a worker waits for a contended lock.
	LockRetry = 1 * time.Second
)

// HoldLock takes the cross-process worker lock, retrying briefly.
func HoldLock() (func(), error) {
	lockPath, err := paths.WorkerLock()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o700); err != nil {
		return nil, err
	}
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(LockRetry)
	for {
		err := unix.Flock(int(lockFile.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			return func() {
				unix.Flock(int(lockFile.Fd()), unix.LOCK_UN)
				lockFile.Close()
			}, nil
		}
		if err != unix.EWOULDBLOCK {
			lockFile.Close()
			return nil, err
		}
		if remaining := time.Until(deadline); remaining <= 0 {
			lockFile.Close()
			return nil, ErrAlreadyRunning
		} else {
			time.Sleep(min(remaining, PollInterval))
		}
	}
}

// SpawnBackground starts a detached worker logging to the worker log.
func SpawnBackground() error {
	logPath, err := paths.WorkerLog()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o700); err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer logFile.Close()
	executable, err := osExecutable()
	if err != nil {
		return err
	}
	devNull, err := os.Open(os.DevNull)
	if err != nil {
		return err
	}
	defer devNull.Close()
	command := exec.Command(executable, "worker")
	command.Stdin = devNull
	command.Stdout = logFile
	command.Stderr = logFile
	command.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := command.Start(); err != nil {
		return err
	}
	return command.Process.Release()
}

// Dependencies wires the worker loop for production and tests.
type Dependencies struct {
	Queue        *queue.DB
	Backend      tts.Backend
	OpenBackend  func(model string) (tts.Engine, error)
	Play         func(wavPath string) error
	PollInterval time.Duration
	IdleTimeout  time.Duration
	Logger       *slog.Logger
}

func (d Dependencies) play() func(string) error {
	if d.Play != nil {
		return d.Play
	}
	return player.Play
}

func (d Dependencies) pollInterval() time.Duration {
	if d.PollInterval > 0 {
		return d.PollInterval
	}
	return PollInterval
}

func (d Dependencies) idleTimeout() time.Duration {
	if d.IdleTimeout > 0 {
		return d.IdleTimeout
	}
	return IdleTimeout
}

func (d Dependencies) logger() *slog.Logger {
	if d.Logger != nil {
		return d.Logger
	}
	return logging.New(os.Stderr)
}

// Process requeues abandoned jobs and speaks pending work until idle.
func Process(deps Dependencies) error {
	logger := deps.logger()
	release, err := HoldLock()
	if err != nil {
		return err
	}
	defer release()

	logger.Debug("worker started")
	moved, err := deps.Queue.RequeueProcessing()
	if err != nil {
		return err
	}
	if moved > 0 {
		logger.Info("requeued abandoned jobs", "count", moved)
	}
	pending, err := deps.Queue.HasPending()
	if err != nil {
		return err
	}
	if !pending {
		logger.Debug("queue empty, exiting")
		return nil
	}

	// Keep one model warm across jobs, but never load it for an empty queue.
	var engine tts.Engine
	var currentModel string
	defer func() {
		if engine != nil {
			engine.Close()
		}
	}()
	selectBackend := func(model string) (tts.Backend, error) {
		if deps.Backend != nil {
			return deps.Backend, nil
		}
		if engine != nil && currentModel == model {
			return engine, nil
		}
		open := deps.OpenBackend
		if open == nil {
			open = tts.Open
		}
		if engine != nil {
			engine.Close()
			engine = nil
			currentModel = ""
		}
		replacement, err := open(model)
		if err != nil {
			return nil, err
		}
		engine, currentModel = replacement, model
		logger.Debug("model loaded", "model", model)
		return engine, nil
	}

	lastActivity := time.Now()
	for {
		job, err := deps.Queue.Claim()
		if err != nil {
			return err
		}
		if job == nil {
			// Nothing to keep warm after a missing-runtime/load failure.
			if engine == nil && deps.Backend == nil {
				return nil
			}
			if time.Since(lastActivity) >= deps.idleTimeout() {
				logger.Info("idle timeout reached, exiting")
				return nil
			}
			time.Sleep(deps.pollInterval())
			continue
		}
		speakJob(deps, job, selectBackend)
		// Inactivity begins after synthesis and playback finish, not at claim.
		lastActivity = time.Now()
	}
}

// speakJob synthesizes and plays one job, always persisting the outcome.
func speakJob(deps Dependencies, job *queue.Job, selectBackend func(string) (tts.Backend, error)) {
	// Never log the message text; it may be sensitive.
	logger := deps.logger().With("job_id", job.ID, "host", job.Host, "turn_id", job.TurnID)
	logger.Debug("job claimed")
	failure := func(err error) {
		logger.Error("job finished", "status", queue.Failed, "error", err)
		now := time.Now().Unix()
		message := err.Error()
		job.Status = queue.Failed
		job.CompletedAt = &now
		job.Error = &message
		deps.Queue.Finish(job)
	}

	// A corrupt config must fail the job rather than speak with stale state.
	cfg, err := config.Load()
	if err != nil {
		failure(err)
		return
	}
	backend, err := selectBackend(cfg.Model)
	if err != nil {
		failure(err)
		return
	}
	chunks := speak.Chunks(job.Message)
	if len(chunks) == 0 {
		failure(errors.New("speech text must be nonempty"))
		return
	}
	for _, chunk := range chunks {
		samples, err := backend.Synthesize(chunk, job.Voice, job.Speed)
		if err != nil {
			failure(err)
			return
		}
		wav, err := writeTempWAV(samples)
		if err != nil {
			failure(err)
			return
		}
		err = deps.play()(wav)
		os.Remove(wav)
		if err != nil {
			failure(err)
			return
		}
	}
	logger.Info("job finished", "status", queue.Success)
	now := time.Now().Unix()
	job.Status = queue.Success
	job.CompletedAt = &now
	job.Error = nil
	deps.Queue.Finish(job)
}

func writeTempWAV(samples []int16) (string, error) {
	temporary, err := os.CreateTemp("", "turnecho-*.wav")
	if err != nil {
		return "", err
	}
	path := temporary.Name()
	if err := temporary.Close(); err != nil {
		os.Remove(path)
		return "", err
	}
	if err := tts.WriteWAV(path, samples); err != nil {
		os.Remove(path)
		return "", err
	}
	return path, nil
}
