package install

import (
	"fmt"

	"github.com/rmscoal/turnecho/internal/player"
	"github.com/rmscoal/turnecho/internal/tts"
)

// PreflightPhrase is the spoken check proving the runtime works.
const PreflightPhrase = "TurnEcho install check."

// OpenFunc loads a speech engine. Callers pass the owned opener so
// install preflight serializes with the worker like doctor and say do.
type OpenFunc func(model string) (tts.Engine, error)

// ProbeFunc locates the OS audio player.
type ProbeFunc func() (string, error)

// Preflight loads the model, synthesizes one phrase, and probes the
// player without playing sound.
func Preflight(model, voice string, speed float64, open OpenFunc, probe ProbeFunc) error {
	engine, err := open(model)
	if err != nil {
		return err
	}
	defer engine.Close()
	samples, err := engine.Synthesize(PreflightPhrase, voice, speed)
	if err != nil {
		return err
	}
	if len(samples) == 0 {
		return fmt.Errorf("TTS runtime produced no audio")
	}
	_, err = probe()
	return err
}

// DefaultProbe is player.Probe, named for call sites.
func DefaultProbe() (string, error) {
	return player.Probe()
}
