package tts

import (
	"bytes"
	"encoding/binary"
	"errors"
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

type countedWriter struct {
	bytes.Buffer
	writes int
}

func (w *countedWriter) Write(p []byte) (int, error) { w.writes++; return w.Buffer.Write(p) }
func TestPCMUsesBoundedBatchWrites(t *testing.T) {
	w := &countedWriter{}
	samples := make([]int16, SampleRate*10)
	for i := range samples {
		samples[i] = int16(i)
	}
	if err := writePCM(w, samples); err != nil {
		t.Fatal(err)
	}
	if w.writes > 16 {
		t.Fatalf("%d writes for ten seconds", w.writes)
	}
	for i, want := range samples {
		if got := int16(binary.LittleEndian.Uint16(w.Bytes()[2*i:])); got != want {
			t.Fatalf("sample %d", i)
		}
	}
}
func TestStreamingWAVHeaderAndFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "out.wav")
	writer, err := CreateWAV(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := writer.Write([]int16{1, -2}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write([]int16{3}); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if len(data) != 50 || binary.LittleEndian.Uint32(data[40:44]) != 6 {
		t.Fatalf("invalid streaming header: %v", data)
	}
	if err := writer.Write([]int16{1}); err == nil {
		t.Fatal("write after close accepted")
	}
	if _, err := CreateWAV(t.TempDir()); err == nil {
		t.Fatal("directory accepted")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }
func TestPCMPropagatesWriteError(t *testing.T) {
	if err := writePCM(failingWriter{}, []int16{1}); err == nil {
		t.Fatal("write error hidden")
	}
}
