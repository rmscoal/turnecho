package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rmscoal/turnecho/internal/tts"
)

func TestSelfcheckWritesWAVAndJSON(t *testing.T) {
	isolateHome(t)
	previous := selfcheckOpen
	selfcheckOpen = func(string) (tts.Engine, error) { return tts.SilentBackend{}, nil }
	t.Cleanup(func() { selfcheckOpen = previous })
	out := filepath.Join(t.TempDir(), "check.wav")

	code, stdout, _ := run(t, "", "selfcheck", "--model-dir", t.TempDir(), "--output", out)
	if code != 0 {
		t.Fatalf("exit = %d, stdout = %q", code, stdout)
	}
	var report struct {
		OK         bool   `json:"ok"`
		Samples    int    `json:"samples"`
		SampleRate int    `json:"sample_rate"`
		ModelDir   string `json:"model_dir"`
	}
	if err := json.Unmarshal([]byte(stdout), &report); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, stdout)
	}
	if !report.OK || report.Samples != tts.SampleRate || report.SampleRate != tts.SampleRate {
		t.Fatalf("report = %+v", report)
	}
	raw, err := os.ReadFile(out)
	if err != nil || len(raw) <= 44 || string(raw[:4]) != "RIFF" {
		t.Fatalf("wav missing: %v", err)
	}
}

func TestSelfcheckRequiresFlags(t *testing.T) {
	isolateHome(t)
	if code, _, _ := run(t, "", "selfcheck"); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
}

func TestSelfcheckFailsOnSilentBackend(t *testing.T) {
	isolateHome(t)
	previous := selfcheckOpen
	selfcheckOpen = func(string) (tts.Engine, error) { return silentSelfcheck{}, nil }
	t.Cleanup(func() { selfcheckOpen = previous })
	out := filepath.Join(t.TempDir(), "check.wav")
	if code, _, _ := run(t, "", "selfcheck", "--model-dir", t.TempDir(), "--output", out); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if _, err := os.Stat(out); !os.IsNotExist(err) {
		t.Fatal("silent run left a wav behind")
	}
}

type silentSelfcheck struct{ tts.SilentBackend }

func (silentSelfcheck) Synthesize(_, _ string, _ float64) ([]int16, error) {
	return nil, nil
}

func TestSelfcheckIsHidden(t *testing.T) {
	found := false
	for _, cmd := range NewRoot().Commands() {
		if cmd.Name() == "selfcheck" {
			found = true
			if !strings.Contains(cmd.Short, "upgrade") {
				t.Fatalf("short help hides purpose: %q", cmd.Short)
			}
		}
	}
	if !found {
		t.Fatal("selfcheck not wired")
	}
}
