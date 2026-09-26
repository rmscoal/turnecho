package tts

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
)

func TestSilentBackend(t *testing.T) {
	samples, err := SilentBackend{}.Synthesize("hello", "speaker-0", 1.0)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(samples) != SampleRate {
		t.Errorf("got %d samples, want one second (%d)", len(samples), SampleRate)
	}
	for _, sample := range samples {
		if sample != 0 {
			t.Fatal("silent backend produced sound")
		}
	}
}

func TestWriteWAV(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.wav")
	samples := []int16{0, 1000, -1000, 32767, -32768}
	if err := WriteWAV(path, samples); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(content) != 44+2*len(samples) {
		t.Fatalf("wav size = %d, want %d", len(content), 44+2*len(samples))
	}
	checks := map[string]string{
		string(content[0:4]):   "RIFF",
		string(content[8:12]):  "WAVE",
		string(content[12:16]): "fmt ",
		string(content[36:40]): "data",
	}
	for got, want := range checks {
		if got != want {
			t.Errorf("marker %q, want %q", got, want)
		}
	}
	if rate := binary.LittleEndian.Uint32(content[24:28]); rate != SampleRate {
		t.Errorf("sample rate = %d, want %d", rate, SampleRate)
	}
	if channels := binary.LittleEndian.Uint16(content[22:24]); channels != 1 {
		t.Errorf("channels = %d, want 1", channels)
	}
	if bits := binary.LittleEndian.Uint16(content[34:36]); bits != 16 {
		t.Errorf("bits = %d, want 16", bits)
	}
	for i, want := range samples {
		got := int16(binary.LittleEndian.Uint16(content[44+2*i:]))
		if got != want {
			t.Errorf("sample %d = %d, want %d", i, got, want)
		}
	}
}
