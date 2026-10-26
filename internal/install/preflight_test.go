package install

import (
	"errors"
	"testing"

	"github.com/rmscoal/turnecho/internal/tts"
)

// silentOpen loads no model and produces headed silence.
func silentOpen(_ string) (tts.Engine, error) {
	return tts.SilentBackend{}, nil
}

func okProbe() (string, error) {
	return "/fake/afplay", nil
}

// emptyEngine synthesizes nothing.
type emptyEngine struct{ tts.SilentBackend }

func (emptyEngine) Synthesize(_, _ string, _ float64) ([]int16, error) {
	return nil, nil
}

func TestPreflightPasses(t *testing.T) {
	if err := Preflight("kokoro", "speaker-0", 1, silentOpen, okProbe); err != nil {
		t.Fatal(err)
	}
}

func TestPreflightFailsOnOpen(t *testing.T) {
	want := errors.New("locked")
	open := func(_ string) (tts.Engine, error) { return nil, want }
	if err := Preflight("kokoro", "speaker-0", 1, open, okProbe); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}

func TestPreflightFailsOnSilence(t *testing.T) {
	open := func(_ string) (tts.Engine, error) { return emptyEngine{}, nil }
	if err := Preflight("kokoro", "speaker-0", 1, open, okProbe); err == nil {
		t.Fatal("empty synthesis accepted")
	}
}

func TestPreflightFailsOnProbe(t *testing.T) {
	want := errors.New("no player")
	probe := func() (string, error) { return "", want }
	if err := Preflight("kokoro", "speaker-0", 1, silentOpen, probe); !errors.Is(err, want) {
		t.Fatalf("error = %v, want %v", err, want)
	}
}
