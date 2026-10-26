//go:build sherpa && cgo && (darwin || linux)

package worker

import (
	"encoding/binary"
	"os"
	"testing"
	"time"

	"github.com/rmscoal/turnecho/internal/queue"
	"github.com/rmscoal/turnecho/internal/tts"
)

// This opt-in smoke check synthesizes real queued summaries without speakers.
func TestWorkerKokoroIntegration(t *testing.T) {
	dir := os.Getenv("TURNECHO_TEST_MODEL_DIR")
	if dir == "" {
		t.Skip("set TURNECHO_TEST_MODEL_DIR to run native queue synthesis")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv(tts.ModelDirEnv, dir)
	db, dbPath := openQueue(t)
	if _, err := db.Insert("codex", "s", "one", "The database connection is ready. We can test the worker now.", "speaker-0", 1); err != nil {
		t.Fatal(err)
	}
	opens, plays := 0, 0
	err := Process(Dependencies{
		Queue: db,
		OpenBackend: func(model string) (tts.Engine, error) {
			opens++
			return tts.Open(model)
		},
		Play: func(path string) error {
			plays++
			wav, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			if len(wav) <= 44 || string(wav[:4]) != "RIFF" || string(wav[8:12]) != "WAVE" || binary.LittleEndian.Uint32(wav[24:28]) != tts.SampleRate {
				t.Fatal("worker produced invalid WAV")
			}
			nonzero := false
			for _, b := range wav[44:] {
				if b != 0 {
					nonzero = true
					break
				}
			}
			if !nonzero {
				t.Fatal("worker produced silent WAV")
			}
			return nil
		},
		IdleTimeout:  20 * time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if opens != 1 || plays != 2 {
		t.Fatalf("opens=%d plays=%d", opens, plays)
	}
	if status, message := jobState(t, dbPath, "one"); status != queue.Success {
		t.Fatalf("status=%s error=%s", status, message)
	}
}
