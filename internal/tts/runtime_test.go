package tts

import (
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestModelDir(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(ModelDirEnv, "")
	dir, err := ModelDir("kokoro")
	if err != nil || dir != filepath.Join(home, ".local", "share", "turnecho", "runtimes", "kokoro-en-v0_19") {
		t.Fatalf("dir=%q error=%v", dir, err)
	}
	t.Setenv(ModelDirEnv, "/custom/model")
	if dir, err := ModelDir("kokoro"); err != nil || dir != "/custom/model" {
		t.Fatalf("override dir=%q error=%v", dir, err)
	}
	if _, err := ModelDir("unknown"); err == nil {
		t.Fatal("unknown model accepted")
	}
}

func TestValidateModelDir(t *testing.T) {
	dir := t.TempDir()
	if err := validateModelDir(dir); err == nil {
		t.Fatal("missing model accepted")
	}
	for _, name := range []string{"model.onnx", "voices.bin", "tokens.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("fixture"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "espeak-ng-data"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateModelDir(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(filepath.Join(dir, "model.onnx"), 0); err != nil {
		t.Fatal(err)
	}
	if err := validateModelDir(dir); err == nil {
		t.Fatal("empty model accepted")
	}
}

func TestValidateSpeech(t *testing.T) {
	if sid, err := validateSpeech("Hello", "speaker-10", 1.25); err != nil || sid != 10 {
		t.Fatalf("sid=%d error=%v", sid, err)
	}
	for _, test := range []struct {
		text, voice string
		speed       float64
	}{
		{" ", "speaker-0", 1},
		{"hello\x00world", "speaker-0", 1},
		{"Hello", "speaker-11", 1},
		{"Hello", "speaker-01", 1},
		{"Hello", "speaker-0", 0.4},
		{"Hello", "speaker-0", 2.1},
		{"Hello", "speaker-0", math.NaN()},
		{"Hello", "speaker-0", math.Inf(1)},
	} {
		if _, err := validateSpeech(test.text, test.voice, test.speed); err == nil {
			t.Errorf("invalid speech accepted: %+v", test)
		}
	}
}

func TestPCM16(t *testing.T) {
	got, err := pcm16([]float32{-2, -1, -0.5, 0, 0.5, 1, 2})
	want := []int16{-32768, -32768, -16384, 0, 16384, 32767, 32767}
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("pcm=%v error=%v", got, err)
	}
	for _, samples := range [][]float32{nil, {float32(math.NaN())}, {float32(math.Inf(1))}} {
		if _, err := pcm16(samples); err == nil {
			t.Fatal("invalid audio accepted")
		}
	}
}

func TestBundledModelDirectory(t *testing.T) {
	root := t.TempDir()
	executable := filepath.Join(root, "bin", "turnecho")
	want := filepath.Join(root, "share", "turnecho", "runtimes", "kokoro-en-v0_19")
	os.MkdirAll(want, 0755)
	if got := bundledModelDir(executable, "kokoro-en-v0_19"); got != want {
		t.Fatalf("got=%s want=%s", got, want)
	}
	if got := bundledModelDir(executable, "missing"); got != "" {
		t.Fatalf("missing model resolved: %s", got)
	}
}
