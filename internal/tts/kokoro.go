//go:build sherpa && cgo && (darwin || linux)

package tts

import (
	"fmt"
	"path/filepath"
	"runtime"

	sherpa "github.com/k2-fsa/sherpa-onnx-go/sherpa_onnx"
)

type kokoro struct {
	model *sherpa.OfflineTts
}

// Open loads the selected local model. Callers must close it when finished.
func Open(model string) (Engine, error) {
	dir, err := ModelDir(model)
	if err != nil {
		return nil, err
	}
	if err := validateModelDir(dir); err != nil {
		return nil, err
	}
	cfg := sherpa.OfflineTtsConfig{
		Model: sherpa.OfflineTtsModelConfig{
			Kokoro: sherpa.OfflineTtsKokoroModelConfig{
				Model:       filepath.Join(dir, "model.onnx"),
				Voices:      filepath.Join(dir, "voices.bin"),
				Tokens:      filepath.Join(dir, "tokens.txt"),
				DataDir:     filepath.Join(dir, "espeak-ng-data"),
				LengthScale: 1,
			},
			Provider:   "cpu",
			NumThreads: min(runtime.NumCPU(), 4),
		},
		MaxNumSentences: 1,
	}
	native := sherpa.NewOfflineTts(&cfg)
	if native == nil {
		return nil, fmt.Errorf("cannot load Kokoro model at %s", dir)
	}
	engine := &kokoro{model: native}
	if native.SampleRate() != SampleRate || native.NumSpeakers() != 11 {
		engine.Close()
		return nil, fmt.Errorf("Kokoro runtime must provide 11 speakers at %d Hz", SampleRate)
	}
	return engine, nil
}

func (k *kokoro) Synthesize(text, voice string, speed float64) ([]int16, error) {
	if k.model == nil {
		return nil, fmt.Errorf("Kokoro model is closed")
	}
	sid, err := validateSpeech(text, voice, speed)
	if err != nil {
		return nil, err
	}
	audio := k.model.Generate(text, sid, float32(speed))
	if audio == nil || audio.SampleRate != SampleRate {
		return nil, fmt.Errorf("Kokoro failed to generate %d Hz audio", SampleRate)
	}
	return pcm16(audio.Samples)
}

func (k *kokoro) Close() {
	if k.model != nil {
		sherpa.DeleteOfflineTts(k.model)
		k.model = nil
	}
}
