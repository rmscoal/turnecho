package worker

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/logging"
	"github.com/rmscoal/turnecho/internal/paths"
	"github.com/rmscoal/turnecho/internal/queue"
	"github.com/rmscoal/turnecho/internal/tts"
)

// realGoEnv captures Go directories before tests redirect HOME.
func realGoEnv(t *testing.T) map[string]string {
	t.Helper()
	output, err := exec.Command("go", "env", "GOMODCACHE", "GOCACHE", "GOPATH").Output()
	if err != nil {
		t.Fatalf("go env failed: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if len(lines) != 3 {
		t.Fatalf("unexpected go env output: %q", output)
	}
	return map[string]string{"GOMODCACHE": lines[0], "GOCACHE": lines[1], "GOPATH": lines[2]}
}

type recordingEngine struct {
	texts  []string
	voices []string
	speeds []float64
	closes int
}

func (e *recordingEngine) Synthesize(text, voice string, speed float64) ([]int16, error) {
	e.texts = append(e.texts, text)
	e.voices = append(e.voices, voice)
	e.speeds = append(e.speeds, speed)
	return []int16{1, -1}, nil
}

func (e *recordingEngine) Close() { e.closes++ }

func TestWarmModelChunksAndIdleCleanup(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, dbPath := openQueue(t)
	if _, err := db.Insert("codex", "s", "one", "First sentence. Second sentence.", "speaker-2", 1.25); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Insert("codex", "s", "two", "Third sentence.", "speaker-3", 0.75); err != nil {
		t.Fatal(err)
	}
	engine := &recordingEngine{}
	opens, plays := 0, 0
	var paths []string
	var lastPlayback time.Time
	idle := 30 * time.Millisecond
	err := Process(Dependencies{
		Queue: db,
		OpenBackend: func(model string) (tts.Engine, error) {
			opens++
			if model != "kokoro" {
				t.Fatalf("model = %q", model)
			}
			return engine, nil
		},
		Play: func(path string) error {
			plays++
			if len(engine.texts) != plays {
				t.Fatal("synthesized ahead of sequential playback")
			}
			paths = append(paths, path)
			// Playback exceeds the idle timeout. The timer must start after it.
			time.Sleep(40 * time.Millisecond)
			lastPlayback = time.Now()
			return nil
		},
		IdleTimeout:  idle,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(lastPlayback); elapsed < idle {
		t.Fatalf("exited after %s, want at least %s of inactivity", elapsed, idle)
	}
	if IdleTimeout != 10*time.Minute {
		t.Fatalf("default timeout = %s", IdleTimeout)
	}
	if opens != 1 || engine.closes != 1 {
		t.Fatalf("opens=%d closes=%d", opens, engine.closes)
	}
	if !reflect.DeepEqual(engine.texts, []string{"First sentence.", "Second sentence.", "Third sentence."}) {
		t.Fatalf("chunks = %q", engine.texts)
	}
	if !reflect.DeepEqual(engine.voices, []string{"speaker-2", "speaker-2", "speaker-3"}) || !reflect.DeepEqual(engine.speeds, []float64{1.25, 1.25, 0.75}) {
		t.Fatal("job voice/speed snapshots changed")
	}
	for _, path := range paths {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("temporary audio remains: %s", path)
		}
	}
	for _, turn := range []string{"one", "two"} {
		if status, _ := jobState(t, dbPath, turn); status != queue.Success {
			t.Fatalf("%s status=%s", turn, status)
		}
	}
	// The process released its lock together with its model.
	release, err := HoldLock()
	if err != nil {
		t.Fatal(err)
	}
	release()
}

func TestEmptyQueueDoesNotOpenModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, _ := openQueue(t)
	if err := Process(Dependencies{Queue: db, OpenBackend: func(string) (tts.Engine, error) { t.Fatal("empty queue loaded a model"); return nil, nil }}); err != nil {
		t.Fatal(err)
	}
}

func TestModelLoadFailureIsRecorded(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, path := openQueue(t)
	db.Insert("codex", "s", "one", "Hello.", "speaker-0", 1)
	err := Process(Dependencies{
		Queue:        db,
		OpenBackend:  func(string) (tts.Engine, error) { return nil, errors.New("runtime missing") },
		Play:         func(string) error { t.Fatal("played after load failure"); return nil },
		IdleTimeout:  time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if status, message := jobState(t, path, "one"); status != queue.Failed || !strings.Contains(message, "runtime missing") {
		t.Fatalf("status=%s error=%s", status, message)
	}
}

func TestPlaybackFailureStopsRemainingChunksAndClosesModel(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, path := openQueue(t)
	db.Insert("codex", "s", "one", "First. Second.", "speaker-0", 1)
	engine := &recordingEngine{}
	var wav string
	err := Process(Dependencies{
		Queue:        db,
		OpenBackend:  func(string) (tts.Engine, error) { return engine, nil },
		Play:         func(path string) error { wav = path; return errors.New("playback stopped") },
		IdleTimeout:  time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(engine.texts) != 1 || engine.closes != 1 {
		t.Fatalf("texts=%q closes=%d", engine.texts, engine.closes)
	}
	if _, err := os.Stat(wav); !os.IsNotExist(err) {
		t.Fatal("failed playback retained wav")
	}
	if status, _ := jobState(t, path, "one"); status != queue.Failed {
		t.Fatalf("status=%s", status)
	}
}

func TestModelChangeReloadsBeforeNextJob(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	config.Models["test-model"] = "test-runtime"
	t.Cleanup(func() { delete(config.Models, "test-model") })
	db, _ := openQueue(t)
	db.Insert("codex", "s", "one", "First.", "speaker-0", 1)
	db.Insert("codex", "s", "two", "Second.", "speaker-0", 1)
	first, second := &recordingEngine{}, &recordingEngine{}
	var models []string
	err := Process(Dependencies{
		Queue: db,
		OpenBackend: func(model string) (tts.Engine, error) {
			models = append(models, model)
			if model == "kokoro" {
				return first, nil
			}
			return second, nil
		},
		Play: func(string) error {
			_, err := config.Update(func(cfg *config.Config) { cfg.Model = "test-model" })
			return err
		},
		IdleTimeout:  time.Millisecond,
		PollInterval: time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(models, []string{"kokoro", "test-model"}) || first.closes != 1 || second.closes != 1 {
		t.Fatalf("models=%v closes=%d,%d", models, first.closes, second.closes)
	}
}

type countingBackend struct {
	calls int
	fail  error
}

func (b *countingBackend) Synthesize(_, _ string, _ float64) ([]int16, error) {
	b.calls++
	if b.fail != nil {
		return nil, b.fail
	}
	return []int16{1, -1}, nil
}

func openQueue(t *testing.T) (*queue.DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue.db")
	db, err := queue.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func jobState(t *testing.T, dbPath, turnID string) (string, string) {
	t.Helper()
	raw, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	var status string
	var message sql.NullString
	if err := raw.QueryRow(
		`SELECT processing_status, error_message FROM turnecho_jobs WHERE turn_id = ?`,
		turnID).Scan(&status, &message); err != nil {
		t.Fatal(err)
	}
	return status, message.String
}

func TestHoldLockContention(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	release, err := HoldLock()
	if err != nil {
		t.Fatalf("first lock failed: %v", err)
	}
	if _, err := HoldLock(); !errors.Is(err, ErrAlreadyRunning) {
		t.Errorf("second lock = %v, want ErrAlreadyRunning", err)
	}
	release()
	again, err := HoldLock()
	if err != nil {
		t.Fatalf("lock after release failed: %v", err)
	}
	again()
}

func TestProcessEmptyQueueSkipsBackend(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv(logging.LevelEnv, "info")
	db, _ := openQueue(t)
	backend := &countingBackend{}
	played := 0
	var stderr bytes.Buffer
	err := Process(Dependencies{
		Queue:        db,
		Backend:      backend,
		Play:         func(string) error { played++; return nil },
		PollInterval: time.Millisecond,
		IdleTimeout:  20 * time.Millisecond,
		Logger:       logging.New(&stderr),
	})
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if backend.calls != 0 || played != 0 {
		t.Errorf("empty queue touched backend (%d) or player (%d)", backend.calls, played)
	}
	if stderr.String() != "" {
		t.Errorf("empty queue logged at info level: %q", stderr.String())
	}
}

func TestProcessSpeaksJob(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, dbPath := openQueue(t)
	db.Insert("codex", "s1", "t1", "Hello there.", "speaker-2", 1.25)
	backend := &countingBackend{}
	var played []string
	var stderr bytes.Buffer
	err := Process(Dependencies{
		Queue:        db,
		Backend:      backend,
		Play:         func(path string) error { played = append(played, path); return nil },
		PollInterval: time.Millisecond,
		IdleTimeout:  20 * time.Millisecond,
		Logger:       logging.New(&stderr),
	})
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if backend.calls != 1 || len(played) != 1 {
		t.Fatalf("backend calls=%d played=%d, want 1 each", backend.calls, len(played))
	}
	status, _ := jobState(t, dbPath, "t1")
	if status != queue.Success {
		t.Errorf("status = %q, want success", status)
	}
	if !strings.Contains(stderr.String(), "job finished") {
		t.Errorf("missing finish log: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "level=ERROR") {
		t.Errorf("unexpected error log: %q", stderr.String())
	}
}

func TestProcessRecordsFailure(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, dbPath := openQueue(t)
	db.Insert("codex", "s1", "t1", "Secret message words.", "speaker-0", 1.0)
	backend := &countingBackend{fail: errors.New("synth broke")}
	var stderr bytes.Buffer
	err := Process(Dependencies{
		Queue:        db,
		Backend:      backend,
		Play:         func(string) error { return nil },
		PollInterval: time.Millisecond,
		IdleTimeout:  20 * time.Millisecond,
		Logger:       logging.New(&stderr),
	})
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	status, message := jobState(t, dbPath, "t1")
	if status != queue.Failed || !strings.Contains(message, "synth broke") {
		t.Errorf("status=%q error=%q, want failed with cause", status, message)
	}
	if !strings.Contains(stderr.String(), "level=ERROR") || !strings.Contains(stderr.String(), "synth broke") {
		t.Errorf("stderr missing error cause: %q", stderr.String())
	}
	if strings.Contains(stderr.String(), "Secret message words.") {
		t.Errorf("stderr leaked message text: %q", stderr.String())
	}
}

func TestProcessRequeuesAbandoned(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	db, dbPath := openQueue(t)
	db.Insert("codex", "s1", "t1", "Hi.", "speaker-0", 1.0)
	claimed, err := db.Claim()
	if err != nil || claimed == nil {
		t.Fatalf("setup claim failed: %v", claimed)
	}
	backend := &countingBackend{}
	played := 0
	var stderr bytes.Buffer
	err = Process(Dependencies{
		Queue:        db,
		Backend:      backend,
		Play:         func(string) error { played++; return nil },
		PollInterval: time.Millisecond,
		IdleTimeout:  20 * time.Millisecond,
		Logger:       logging.New(&stderr),
	})
	if err != nil {
		t.Fatalf("process failed: %v", err)
	}
	if played != 1 {
		t.Errorf("abandoned job played %d times, want 1", played)
	}
	status, _ := jobState(t, dbPath, "t1")
	if status != queue.Success {
		t.Errorf("status = %q, want success", status)
	}
}

func TestSpawnedWorkerExitsQuietly(t *testing.T) {
	goEnv := realGoEnv(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	binary := filepath.Join(t.TempDir(), "turnecho")
	build := exec.Command("go", "build", "-o", binary, "github.com/rmscoal/turnecho/cmd/turnecho")
	// Keep the inner build on the real module cache: HOME now points at a
	// temp dir and the cache must neither move there nor be rebuilt.
	build.Env = append(os.Environ(),
		"GOMODCACHE="+goEnv["GOMODCACHE"],
		"GOCACHE="+goEnv["GOCACHE"],
		"GOPATH="+goEnv["GOPATH"],
	)
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build failed: %v\n%s", err, output)
	}
	previous := osExecutable
	osExecutable = func() (string, error) { return binary, nil }
	t.Cleanup(func() { osExecutable = previous })

	if err := SpawnBackground(); err != nil {
		t.Fatalf("spawn failed: %v", err)
	}
	lockPath, err := paths.WorkerLock()
	if err != nil {
		t.Fatal(err)
	}
	logPath, err := paths.WorkerLog()
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for {
		if _, err := os.Stat(lockPath); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("worker never started (no lock file), log at %s", logPath)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// The spawned worker exits quietly on its empty queue; wait for release.
	for {
		release, err := HoldLock()
		if err == nil {
			release()
			return
		}
		if !errors.Is(err, ErrAlreadyRunning) {
			t.Fatalf("lock failed: %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("worker never exited")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
