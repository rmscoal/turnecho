//go:build sherpa && cgo && (darwin || linux)

package tts

import (
	"os"
	"strings"
	"testing"
)

func TestKokoroMissingRuntime(t *testing.T) {
	t.Setenv(ModelDirEnv, t.TempDir())
	if _, err := Open("kokoro"); err == nil || !strings.Contains(err.Error(), "runtime missing") {
		t.Fatalf("missing model error = %v", err)
	}
}

// Opt-in only: normal tests never load a real model or produce audio.
func TestKokoroIntegration(t *testing.T) {
	dir := os.Getenv("TURNECHO_TEST_MODEL_DIR")
	if dir == "" {
		t.Skip("set TURNECHO_TEST_MODEL_DIR to run native synthesis")
	}
	t.Setenv(ModelDirEnv, dir)
	engine, err := Open("kokoro")
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	for _, voice := range []string{"speaker-0", "speaker-1"} {
		samples, err := engine.Synthesize("The database connection is ready.", voice, 1.25)
		if err != nil || len(samples) < SampleRate/10 {
			t.Fatalf("voice=%s samples=%d error=%v", voice, len(samples), err)
		}
		audible := false
		for _, sample := range samples {
			if sample != 0 {
				audible = true
				break
			}
		}
		if !audible {
			t.Fatal("native synthesis produced only silence")
		}
	}
	engine.Close()
	if _, err := engine.Synthesize("Hello", "speaker-0", 1); err == nil {
		t.Fatal("closed engine accepted synthesis")
	}
}
