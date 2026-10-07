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

func playbackTimeout(path string) time.Duration {
	file, err := os.Open(path)
	if err != nil {
		return 5 * time.Minute
	}
	defer file.Close()
	var header [44]byte
	if _, err := io.ReadFull(file, header[:]); err != nil {
		return 5 * time.Minute
	}
	rate := binary.LittleEndian.Uint32(header[28:32])
	if string(header[:4]) != "RIFF" || string(header[36:40]) != "data" || rate == 0 {
		return 5 * time.Minute
	}
	duration := time.Duration(binary.LittleEndian.Uint32(header[40:44])) * time.Second / time.Duration(rate)
	return min(duration+30*time.Second, 10*time.Minute)
}

func playWithTimeout(player, wavPath string, timeout time.Duration) error {
	dir, err := paths.ConfigDir()
	if err != nil {
		return err
	}
	if err := paths.EnsurePrivateDir(dir); err != nil {
		return err
	}
	lock, err := paths.OpenPrivateFile(filepath.Join(dir, "player.lock"), os.O_CREATE|os.O_RDWR)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("playback already active: %w", err)
	}
	defer unix.Flock(int(lock.Fd()), unix.LOCK_UN)
	socket, err := controlSocket()
	if err != nil {
		return err
	}
	socketDir := filepath.Dir(socket)
	if err := paths.EnsurePrivateDir(socketDir); err != nil {
		return err
	}
	// Only the lock owner may remove an abandoned socket.
	if err := os.Remove(socket); err != nil && !os.IsNotExist(err) {
		return err
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: socket, Net: "unix"})
	if err != nil {
		return err
	}
	defer listener.Close()
	if err := os.Chmod(socket, 0600); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	command := exec.CommandContext(ctx, player, wavPath)
	// Use os/exec's default cancellation, which signals the owned os.Process
	// and guards against signalling a reused PID after the child is reaped.
	if err := command.Start(); err != nil {
		return err
	}
	controlled := make(chan struct{})
	go func() {
		defer close(controlled)
		for {
			conn, err := listener.AcceptUnix()
			if err != nil {
				return
			}
			conn.SetDeadline(time.Now().Add(time.Second))
			var request [4]byte
			if _, err := io.ReadFull(conn, request[:]); err == nil && string(request[:]) == "stop" {
				cancel()
				conn.Write([]byte("ok"))
			}
			conn.Close()
		}
	}()
	err = command.Wait()
	listener.Close()
	<-controlled
	if ctx.Err() != nil {
		return fmt.Errorf("playback cancelled or timed out: %w", ctx.Err())
	}
	return err
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
	if _, err := conn.Write([]byte("stop")); err != nil {
		return false, err
	}
	var response [2]byte
	if _, err := io.ReadFull(conn, response[:]); err != nil {
		return false, err
	}
	return string(response[:]) == "ok", nil
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
