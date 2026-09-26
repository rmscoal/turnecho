package worker

import (
	"bytes"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rmscoal/turnecho/internal/logging"
	"github.com/rmscoal/turnecho/internal/paths"
	"github.com/rmscoal/turnecho/internal/queue"
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
