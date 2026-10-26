// Package player speaks WAV files through an OS-native player subprocess.
package player

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rmscoal/turnecho/internal/paths"
)

// Probe returns the audio player for this platform.
func Probe() (string, error) {
	switch runtime.GOOS {
	case "darwin":
		return lookPath("afplay")
	case "linux":
		return lookPath("aplay")
	default:
		return "", fmt.Errorf("unsupported platform %q: no audio player", runtime.GOOS)
	}
}

func lookPath(name string) (string, error) {
	path, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("no audio player found (missing %s)", name)
	}
	return path, nil
}

// Play speaks one file and waits for playback to finish.
func Play(wavPath string) error {
	player, err := Probe()
	if err != nil {
		return err
	}
	return PlayWith(player, wavPath)
}

// PlayWith speaks one file with a deadline based on its audio length.
func PlayWith(player, wavPath string) error {
	return playWithTimeout(player, wavPath, playbackTimeout(wavPath))
}

const (
	// fallbackTimeout caps playback when the WAV header is unreadable.
	fallbackTimeout = 5 * time.Minute
	// playbackGracePeriod extends the deadline past the audio length.
	playbackGracePeriod = 30 * time.Second
	// maxPlaybackTimeout caps playback of long audio.
	maxPlaybackTimeout = 10 * time.Minute
)

// Control protocol messages between turnecho stop and the playback owner.
const (
	controlStop = "stop"
	controlOK   = "ok"
)

func playbackTimeout(path string) time.Duration {
	file, err := os.Open(path)
	if err != nil {
		return fallbackTimeout
	}
	defer file.Close()

	var header [44]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return fallbackTimeout
	}
	rate := binary.LittleEndian.Uint32(header[28:32])
	if string(header[:4]) != "RIFF" || string(header[36:40]) != "data" || rate == 0 {
		return fallbackTimeout
	}
	duration := time.Duration(binary.LittleEndian.Uint32(header[40:44])) * time.Second / time.Duration(rate)
	return min(duration+playbackGracePeriod, maxPlaybackTimeout)
}

func playWithTimeout(player, wavPath string, timeout time.Duration) error {
	unlock, err := holdPlayerLock()
	if err != nil {
		return err
	}
	defer unlock()

	listener, err := listenControlSocket()
	if err != nil {
		return err
	}
	defer listener.Close()

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, player, wavPath)
	// Use os/exec's default cancellation, which signals the owned os.Process
	// and guards against signalling a reused PID after the child is reaped.
	if err := command.Start(); err != nil {
		return err
	}

	done := serveControl(listener, cancel)
	err = command.Wait()
	listener.Close()
	<-done
	if ctx.Err() != nil {
		return fmt.Errorf("playback cancelled or timed out: %w", ctx.Err())
	}
	return err
}

// holdPlayerLock takes the cross-process playback lock, refusing a second
// concurrent playback.
func holdPlayerLock() (func(), error) {
	dir, err := paths.ConfigDir()
	if err != nil {
		return nil, err
	}
	if err := paths.EnsurePrivateDir(dir); err != nil {
		return nil, err
	}
	lock, err := paths.OpenPrivateFile(filepath.Join(dir, "player.lock"), os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		lock.Close()
		return nil, fmt.Errorf("playback already active: %w", err)
	}
	return func() {
		unix.Flock(int(lock.Fd()), unix.LOCK_UN)
		lock.Close()
	}, nil
}

// listenControlSocket binds the stop-request socket owned by this playback.
func listenControlSocket() (*net.UnixListener, error) {
	socket, err := controlSocket()
	if err != nil {
		return nil, err
	}
	if err := paths.EnsurePrivateDir(filepath.Dir(socket)); err != nil {
		return nil, err
	}
	// Only the lock owner may remove an abandoned socket.
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(socket, 0600); err != nil {
		listener.Close()
		return nil, err
	}
	return listener, nil
}

// serveControl answers stop requests until the listener closes, then closes done.
func serveControl(listener *net.UnixListener, cancel context.CancelFunc) <-chan struct{} {
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(time.Second))
			var request [len(controlStop)]byte
			if _, err := io.ReadFull(conn, request[:]); err == nil && string(request[:]) == controlStop {
				cancel()
				conn.Write([]byte(controlOK))
			}
			conn.Close()
		}
	}()
	return done
}

// Stop asks the playback owner to cancel its own child. A PID file is never
// trusted to signal a process, so stale or forged PIDs cannot kill other apps.
func Stop() (bool, error) {
	socket, err := controlSocket()
	if err != nil {
		return false, err
	}
	conn, err := net.DialTimeout("unix", socket, time.Second)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) || errors.Is(err, unix.ECONNREFUSED) {
			return false, nil
		}
		return false, err
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(time.Second))
	if _, err := conn.Write([]byte(controlStop)); err != nil {
		return false, err
	}
	var response [len(controlOK)]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil {
		return false, err
	}
	return string(response[:]) == controlOK, nil
}

// Unix socket addresses are limited to about 100 bytes. Hash the configuration
// path into a short per-user directory, even when HOME is deeply nested.
func controlSocket() (string, error) {
	dir, err := paths.ConfigDir()
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(dir))
	return filepath.Join("/tmp", fmt.Sprintf("turnecho-%d-%x", os.Getuid(), sum[:8]), "player.sock"), nil
}
