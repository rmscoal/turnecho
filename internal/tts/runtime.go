package tts

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/rmscoal/turnecho/internal/config"
)

// ModelDirEnv overrides the model directory for development and runtime checks.
const ModelDirEnv = "TURNECHO_MODEL_DIR"

// ModelDir resolves the pinned, local model directory. It never downloads files.
func ModelDir(model string) (string, error) {
	id, ok := config.Models[model]
	if !ok {
		return "", fmt.Errorf("unsupported TTS model %q", model)
	}
	if dir := os.Getenv(ModelDirEnv); dir != "" {
		return dir, nil
	}
	if executable, err := os.Executable(); err == nil {
		if dir := bundledModelDir(executable, id); dir != "" {
			return dir, nil
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share", "turnecho", "runtimes", id), nil
}

func validateModelDir(dir string) error {
	for _, name := range []string{"model.onnx", "voices.bin", "tokens.txt", "espeak-ng-data"} {
		path := filepath.Join(dir, name)
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Errorf("Kokoro runtime missing or unreadable at %s: %w (see docs/speech-runtime.md)", path, err)
		}
		if name == "espeak-ng-data" {
			if !info.IsDir() {
				return fmt.Errorf("Kokoro data path is not a directory: %s", path)
			}
		} else if !info.Mode().IsRegular() || info.Size() == 0 {
			return fmt.Errorf("Kokoro model file is not a nonempty regular file: %s", path)
		}
	}
	return nil
}

func speakerID(voice string) (int, error) {
	if sid := slices.Index(config.Voices, voice); sid >= 0 {
		return sid, nil
	}
	return 0, fmt.Errorf("unsupported Kokoro voice %q", voice)
}

func validateSpeech(text, voice string, speed float64) (int, error) {
	if strings.TrimSpace(text) == "" || strings.ContainsRune(text, 0) {
		return 0, fmt.Errorf("speech text must be nonempty and contain no NUL bytes")
	}
	if math.IsNaN(speed) || math.IsInf(speed, 0) || speed < config.MinSpeed || speed > config.MaxSpeed {
		return 0, fmt.Errorf("speech speed must be between %g and %g", config.MinSpeed, config.MaxSpeed)
	}
	return speakerID(voice)
}

// pcm16 converts normalized model output without integer overflow.
func pcm16(samples []float32) ([]int16, error) {
	if len(samples) == 0 {
		return nil, fmt.Errorf("Kokoro produced no audio")
	}
	pcm := make([]int16, len(samples))
	for i, sample := range samples {
		value := float64(sample)
		if math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, fmt.Errorf("Kokoro produced non-finite audio")
		}
		pcm[i] = int16(math.Round(max(-32768, min(32767, value*32768))))
	}
	return pcm, nil
}

func bundledModelDir(executable, id string) string {
	// Resolve a command symlink so the model follows the installed bundle.
	if resolved, err := filepath.EvalSymlinks(executable); err == nil {
		executable = resolved
	}
	dir := filepath.Join(filepath.Dir(executable), "..", "share", "turnecho", "runtimes", id)
	if _, err := os.Stat(dir); err == nil {
		return filepath.Clean(dir)
	}
	return ""
}
