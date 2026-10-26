package player

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/rmscoal/turnecho/internal/paths"
	"github.com/rmscoal/turnecho/internal/tts"
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

func TestStopIgnoresUnrelatedPid(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	command := exec.Command("sleep", "30")
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { command.Process.Kill(); command.Wait() }()
	pidPath, _ := paths.PlayerPid()
	os.MkdirAll(filepath.Dir(pidPath), 0700)
	os.WriteFile(pidPath, []byte(strconv.Itoa(command.Process.Pid)), 0600)
	stopped, err := Stop()
	if err != nil || stopped {
		t.Fatalf("stop=%v error=%v", stopped, err)
	}
	if err := unix.Kill(command.Process.Pid, 0); err != nil {
		t.Fatalf("unrelated process killed: %v", err)
	}
}

func TestStopOwnedPlayback(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	script := filepath.Join(t.TempDir(), "player")
	os.WriteFile(script, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700)
	done := make(chan error, 1)
	go func() { done <- PlayWith(script, "ignored.wav") }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		stopped, err := Stop()
		if err != nil {
			t.Fatal(err)
		}
		if stopped {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("playback control not ready")
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled playback returned success")
		}
	case <-time.After(time.Second):
		t.Fatal("player not reaped")
	}
}

func TestHungPlaybackTimesOut(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	script := filepath.Join(t.TempDir(), "player")
	os.WriteFile(script, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700)
	started := time.Now()
	if err := playWithTimeout(script, "ignored.wav", 30*time.Millisecond); err == nil {
		t.Fatal("hung player succeeded")
	}
	if time.Since(started) > time.Second {
		t.Fatal("hung player retained resources")
	}
}

func TestPlaybackTimeoutUsesWAVDuration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audio.wav")
	if err := tts.WriteWAV(path, make([]int16, tts.SampleRate*2)); err != nil {
		t.Fatal(err)
	}
	if got := playbackTimeout(path); got != 32*time.Second {
		t.Fatalf("timeout=%s", got)
	}
}

func TestConcurrentPlaybackIsRejected(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	script := filepath.Join(t.TempDir(), "player")
	os.WriteFile(script, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700)
	done := make(chan error, 1)
	go func() { done <- playWithTimeout(script, "ignored.wav", time.Second) }()
	socket, _ := controlSocket()
	deadline := time.Now().Add(time.Second)
	for {
		if _, err := os.Stat(socket); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("socket missing")
		}
		time.Sleep(time.Millisecond)
	}
	if err := PlayWith(script, "ignored.wav"); err == nil {
		t.Error("concurrent player accepted")
	}
	Stop()
	<-done
	if _, err := os.Stat(socket); !os.IsNotExist(err) {
		t.Error("socket retained")
	}
}

func TestStopAfterPlayerExitDoesNotRetainControl(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	for i := 0; i < 10; i++ {
		if err := PlayWith("/usr/bin/true", "ignored.wav"); err != nil {
			t.Fatal(err)
		}
		if stopped, err := Stop(); err != nil || stopped {
			t.Fatalf("stop=%v error=%v", stopped, err)
		}
	}
}
