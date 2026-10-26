// Package worker speaks queued summaries sequentially behind one lock.
package worker

import (
	"errors"
	"fmt"
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
	if err := paths.EnsurePrivateDir(filepath.Dir(lockPath)); err != nil {
		return nil, err
	}
	lockFile, err := paths.OpenPrivateFile(lockPath, os.O_CREATE|os.O_RDWR)
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
		remaining := time.Until(deadline)
		if remaining <= 0 {
			lockFile.Close()
			return nil, ErrAlreadyRunning
		}
		time.Sleep(min(remaining, PollInterval))
	}
}

// SpawnBackground starts a detached worker logging to the worker log.
func SpawnBackground() error {
	logPath, err := paths.WorkerLog()
	if err != nil {
		return err
	}
	if err := paths.EnsurePrivateDir(filepath.Dir(logPath)); err != nil {
		return err
	}
	logFile, err := paths.OpenPrivateFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY)
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
	for {
		release, err := HoldLock()
		if err != nil {
			return err
		}
		err = func() error { defer release(); return processOwned(deps) }()
		if err != nil {
			return err
		}
		// Check after native teardown and unlock. Hooks that lost a lock race
		// during teardown may already have returned, so this owner drains their work.
		pending, err := deps.Queue.HasPending()
		if err != nil || !pending {
			return err
		}
	}
}

func processOwned(deps Dependencies) error {
	logger := deps.logger()

	logger.Debug("worker started")
	moved, err := deps.Queue.RequeueProcessing()
	if err != nil {
		return err
	}
	if moved > 0 {
		logger.Info("recovered abandoned jobs", "count", moved)
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
	open := deps.OpenBackend
	if open == nil {
		open = tts.Open
	}
	cache := &modelCache{open: open, logger: logger}
	defer cache.close()

	selectBackend := cache.backend
	if deps.Backend != nil {
		selectBackend = func(string) (tts.Backend, error) { return deps.Backend, nil }
	}

	lastActivity := time.Now()
	for {
		job, err := deps.Queue.Claim()
		if err != nil {
			return err
		}
		if job == nil {
			// Nothing to keep warm after a missing-runtime/load failure.
			if !cache.loaded() && deps.Backend == nil {
				return nil
			}
			if time.Since(lastActivity) >= deps.idleTimeout() {
				logger.Info("idle timeout reached, exiting")
				return nil
			}
			time.Sleep(deps.pollInterval())
			continue
		}
		if err := speakJob(deps, job, selectBackend); err != nil {
			return err
		}
		// Inactivity begins after synthesis and playback finish, not at claim.
		lastActivity = time.Now()
	}
}

// modelCache keeps one TTS model warm across jobs.
type modelCache struct {
	open   func(model string) (tts.Engine, error)
	logger *slog.Logger

	engine tts.Engine
	model  string
}

// backend returns the cached engine for a model, loading it on demand.
func (c *modelCache) backend(model string) (tts.Backend, error) {
	if c.engine != nil && c.model == model {
		return c.engine, nil
	}
	c.close()

	replacement, err := c.open(model)
	if err != nil {
		return nil, err
	}
	c.engine, c.model = replacement, model
	c.logger.Debug("model loaded", "model", model)
	return c.engine, nil
}

func (c *modelCache) close() {
	if c.engine != nil {
		c.engine.Close()
		c.engine = nil
		c.model = ""
	}
}

// loaded reports whether a model is currently cached.
func (c *modelCache) loaded() bool {
	return c.engine != nil
}

// speakJob synthesizes and plays one job, always persisting the outcome.
func speakJob(deps Dependencies, job *queue.Job, selectBackend func(string) (tts.Backend, error)) error {
	// Never log the message text; it may be sensitive.
	logger := deps.logger().With("job_id", job.ID, "host", job.Host, "turn_id", job.TurnID)
	logger.Debug("job claimed")

	persistOutcome := func() error {
		done, err := deps.Queue.Finish(job)
		if err != nil {
			return fmt.Errorf("persist job completion: %w", err)
		}
		if !done {
			return errors.New("job disappeared before completion")
		}
		if job.Error != nil {
			logger.Error("job finished", "status", job.Status, "error", *job.Error)
		} else {
			logger.Info("job finished", "status", job.Status)
		}
		return nil
	}
	failJob := func(err error) error {
		now := time.Now().Unix()
		message := err.Error()
		job.Status = queue.Failed
		job.CompletedAt = &now
		job.Error = &message
		return persistOutcome()
	}

	// A corrupt config must fail the job rather than speak with stale state.
	cfg, err := config.Load()
	if err != nil {
		return failJob(err)
	}
	backend, err := selectBackend(cfg.Model)
	if err != nil {
		return failJob(err)
	}
	chunks := speak.Chunks(job.Message)
	if len(chunks) == 0 {
		return failJob(errors.New("speech text must be nonempty"))
	}

	playbackMarked := false
	for _, chunk := range chunks {
		samples, err := backend.Synthesize(chunk, job.Voice, job.Speed)
		if err != nil {
			return failJob(err)
		}
		wav, err := tts.WriteTempWAV(samples)
		if err != nil {
			return failJob(err)
		}
		if !playbackMarked {
			if err := deps.Queue.MarkPlayback(job.ID); err != nil {
				os.Remove(wav)
				return failJob(err)
			}
			playbackMarked = true
		}
		err = deps.play()(wav)
		os.Remove(wav)
		if err != nil {
			return failJob(err)
		}
	}

	now := time.Now().Unix()
	job.Status = queue.Success
	job.CompletedAt = &now
	job.Error = nil
	return persistOutcome()
}

// ResumePending restarts work that arrived while a manual speech command held
// ownership. Call only after closing the engine and releasing its lock.
func ResumePending() error {
	path, err := paths.Database()
	if err != nil {
		return err
	}
	db, err := queue.Open(path)
	if err != nil {
		return err
	}
	defer db.Close()
	pending, err := db.HasPending()
	if err != nil || !pending {
		return err
	}
	return SpawnBackground()
}
