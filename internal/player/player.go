// Package player speaks WAV files through an OS-native player subprocess.
package player

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

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

// PlayWith speaks one file through an explicit player binary.
func PlayWith(player, wavPath string) error {
	pidPath, err := paths.PlayerPid()
	if err != nil {
		return err
	}
	command := exec.Command(player, wavPath)
	if err := command.Start(); err != nil {
		return err
	}
	// Best effort: a stale pid file only affects turnecho stop.
	_ = os.WriteFile(pidPath, []byte(strconv.Itoa(command.Process.Pid)), 0o600)
	waitErr := command.Wait()
	_ = os.Remove(pidPath)
	return waitErr
}

// Stop silences active playback, reporting whether anything was playing.
//
// The pid file is standard practice but cannot fully rule out pid reuse;
// Step 8 replaces this with worker-mediated stop alongside chunk playback.
func Stop() (bool, error) {
	pidPath, err := paths.PlayerPid()
	if err != nil {
		return false, err
	}
	content, err := os.ReadFile(pidPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil || pid <= 0 {
		return false, fmt.Errorf("invalid playback pid file at %s", pidPath)
	}
	if err := unix.Kill(pid, 0); err != nil {
		// ESRCH means the player already exited; anything else is real.
		if err == unix.ESRCH {
			_ = os.Remove(pidPath)
			return false, nil
		}
		return false, err
	}
	if err := unix.Kill(pid, unix.SIGKILL); err != nil {
		if err == unix.ESRCH {
			_ = os.Remove(pidPath)
			return false, nil
		}
		return false, err
	}
	_ = os.Remove(pidPath)
	return true, nil
}
