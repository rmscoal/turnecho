// Package worker speaks queued summaries sequentially behind one lock.
package worker

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/paths"
	"github.com/rmscoal/turnecho/internal/player"
	"github.com/rmscoal/turnecho/internal/queue"
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
		if time.Now().After(deadline) {
			lockFile.Close()
			return nil, ErrAlreadyRunning
		}
		time.Sleep(PollInterval)
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
	Play         func(wavPath string) error
	PollInterval time.Duration
	IdleTimeout  time.Duration
	Stderr       io.Writer
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

func (d Dependencies) stderr() io.Writer {
	if d.Stderr != nil {
		return d.Stderr
	}
	return os.Stderr
}

// Process requeues abandoned jobs and speaks pending work until idle.
func Process(deps Dependencies) error {
	release, err := HoldLock()
	if err != nil {
		return err
	}
	defer release()

	if _, err := deps.Queue.RequeueProcessing(); err != nil {
		return err
	}
	pending, err := deps.Queue.HasPending()
	if err != nil {
		return err
	}
	if !pending {
		return nil
	}

	lastActivity := time.Now()
	for {
		job, err := deps.Queue.Claim()
		if err != nil {
			return err
		}
		if job == nil {
			if time.Since(lastActivity) >= deps.idleTimeout() {
				return nil
			}
			time.Sleep(deps.pollInterval())
			continue
		}
		lastActivity = time.Now()
		speakJob(deps, job)
	}
}

// speakJob synthesizes and plays one job, always persisting the outcome.
func speakJob(deps Dependencies, job *queue.Job) {
	failure := func(err error) {
		// Never log the message text; it may be sensitive.
		fmt.Fprintln(deps.stderr(), err)
		now := time.Now().Unix()
		message := err.Error()
		job.Status = queue.Failed
		job.CompletedAt = &now
		job.Error = &message
		deps.Queue.Finish(job)
	}

	if _, err := config.Load(); err != nil {
		failure(err)
		return
	}
	samples, err := deps.Backend.Synthesize(job.Message, job.Voice, job.Speed)
	if err != nil {
		failure(err)
		return
	}
	wav, err := writeTempWAV(samples)
	if err != nil {
		failure(err)
		return
	}
	defer os.Remove(wav)
	if err := deps.play()(wav); err != nil {
		failure(err)
		return
	}
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
