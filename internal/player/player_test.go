package player

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/rmscoal/turnecho/internal/paths"
)

func playerName() string {
	if runtime.GOOS == "darwin" {
		return "afplay"
	}
	return "aplay"
}

func installFakePlayer(t *testing.T) string {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\necho \"$@\" >> \"$TURNECHO_CALL_LOG\"\n"
	if err := os.WriteFile(filepath.Join(bin, playerName()), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return bin
}

func TestProbeFindsPlayer(t *testing.T) {
	bin := installFakePlayer(t)
	got, err := Probe()
	if err != nil {
		t.Fatalf("probe failed: %v", err)
	}
	if got != filepath.Join(bin, playerName()) {
		t.Errorf("probe = %q", got)
	}
}

func TestProbeReportsMissingPlayer(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	if _, err := Probe(); err == nil {
		t.Fatal("probe succeeded with no player")
	} else if !strings.Contains(err.Error(), playerName()) {
		t.Errorf("error %q does not name the player", err)
	}
}

func TestPlayWithRunsPlayer(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	bin := installFakePlayer(t)
	log := filepath.Join(t.TempDir(), "calls.log")
	t.Setenv("TURNECHO_CALL_LOG", log)
	wav := filepath.Join(t.TempDir(), "out.wav")
	if err := os.WriteFile(wav, []byte("RIFF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := PlayWith(filepath.Join(bin, playerName()), wav); err != nil {
		t.Fatalf("play failed: %v", err)
	}
	content, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(content)) != wav {
		t.Errorf("player received %q, want %q", strings.TrimSpace(string(content)), wav)
	}
	pidPath, _ := paths.PlayerPid()
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("pid file left behind after playback")
	}
}

func TestStopWithoutPlayback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	stopped, err := Stop()
	if err != nil || stopped {
		t.Errorf("stop = %v, %v; want false, nil", stopped, err)
	}
}

func TestStopKillsPlayer(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	command := exec.Command("sleep", "30")
	if err := command.Start(); err != nil {
		t.Skipf("sleep unavailable: %v", err)
	}
	pidPath, err := paths.PlayerPid()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(command.Process.Pid)), 0o600); err != nil {
		t.Fatal(err)
	}
	stopped, err := Stop()
	if err != nil || !stopped {
		t.Errorf("stop = %v, %v; want true, nil", stopped, err)
	}
	_ = command.Wait()
	if err := unix.Kill(command.Process.Pid, 0); err != unix.ESRCH {
		t.Errorf("player still alive: %v", err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("pid file left behind after stop")
	}
}

func TestStopCleansStalePid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	command := exec.Command("sh", "-c", "exit 0")
	if err := command.Run(); err != nil {
		t.Fatal(err)
	}
	pidPath, err := paths.PlayerPid()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		t.Fatal(err)
	}
	dead := command.ProcessState.Pid()
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(dead)), 0o600); err != nil {
		t.Fatal(err)
	}
	stopped, err := Stop()
	if err != nil || stopped {
		t.Errorf("stop = %v, %v; want false, nil", stopped, err)
	}
	if _, err := os.Stat(pidPath); !os.IsNotExist(err) {
		t.Error("stale pid file left behind")
	}
}

func TestStopRejectsGarbagePid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	pidPath, err := paths.PlayerPid()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pidPath, []byte("nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Stop(); err == nil {
		t.Error("garbage pid file accepted")
	}
}
