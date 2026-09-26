package queue

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/rmscoal/turnecho/internal/config"
)

func openTestDB(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "queue.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("open failed: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db, path
}

func TestSplitStatementsStripsComments(t *testing.T) {
	got := splitStatements("-- header; with semicolon\nCREATE TABLE a (x);\n-- tail\nCREATE INDEX i ON a(x);")
	want := []string{"CREATE TABLE a (x)", "CREATE INDEX i ON a(x)"}
	if len(got) != len(want) {
		t.Fatalf("got %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("statement %d = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestOpenMigratesAndReopens(t *testing.T) {
	_, path := openTestDB(t)
	db, err := Open(path)
	if err != nil {
		t.Fatalf("reopen failed: %v", err)
	}
	defer db.Close()
	pending, err := db.HasPending()
	if err != nil || pending {
		t.Errorf("fresh db pending=%v, err=%v", pending, err)
	}
}

func TestOpenRejectsGarbageFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	if err := os.WriteFile(path, []byte("not a database"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(path); err == nil {
		t.Error("garbage database opened")
	}
}

func TestInsertDedupesTurns(t *testing.T) {
	db, _ := openTestDB(t)
	first, err := db.Insert("codex", "s1", "t1", "Hello.", "speaker-0", 1.0)
	if err != nil || !first {
		t.Fatalf("first insert = %v, %v", first, err)
	}
	second, err := db.Insert("codex", "s1", "t1", "Again.", "speaker-1", 1.5)
	if err != nil || second {
		t.Fatalf("repeat insert = %v, %v", second, err)
	}
	other, err := db.Insert("codex", "s1", "t2", "Other.", "speaker-0", 1.0)
	if err != nil || !other {
		t.Fatalf("new turn insert = %v, %v", other, err)
	}
}

func TestInsertAcceptsEveryVoice(t *testing.T) {
	db, _ := openTestDB(t)
	for i, voice := range config.Voices {
		inserted, err := db.Insert("codex", "s1", fmt.Sprintf("t%d", i), "Hi.", voice, 1.0)
		if err != nil || !inserted {
			t.Fatalf("voice %q rejected: %v", voice, err)
		}
	}
	if _, err := db.Insert("codex", "s1", "bad", "Hi.", "Nobody", 1.0); err == nil {
		t.Error("unknown voice accepted")
	}
	if _, err := db.Insert("codex", "s1", "bad", "Hi.", "speaker-0", 9.0); err == nil {
		t.Error("out-of-range speed accepted")
	}
}

func TestInsertRejectsBlankFields(t *testing.T) {
	db, _ := openTestDB(t)
	cases := []struct {
		name    string
		host    string
		session string
		turn    string
		message string
	}{
		{"blank host", "", "s1", "t1", "Hi."},
		{"blank session", "codex", "  ", "t1", "Hi."},
		{"blank turn", "codex", "s1", "", "Hi."},
		{"blank message", "codex", "s1", "t1", "   "},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := db.Insert(tc.host, tc.session, tc.turn, tc.message, "speaker-0", 1.0); err == nil {
				t.Error("blank field accepted")
			}
		})
	}
}

func TestClaimOldestFirst(t *testing.T) {
	db, _ := openTestDB(t)
	db.Insert("codex", "s1", "t1", "First.", "speaker-0", 1.0)
	db.Insert("codex", "s1", "t2", "Second.", "speaker-0", 1.0)
	first, err := db.Claim()
	if err != nil || first == nil || first.TurnID != "t1" || first.Status != Processing {
		t.Fatalf("first claim = %+v, %v", first, err)
	}
	if first.StartedAt == nil {
		t.Error("claimed job has no start time")
	}
	second, err := db.Claim()
	if err != nil || second == nil || second.TurnID != "t2" {
		t.Fatalf("second claim = %+v, %v", second, err)
	}
	third, err := db.Claim()
	if err != nil || third != nil {
		t.Errorf("empty claim = %+v, %v", third, err)
	}
}

func TestConcurrentClaimsAreExclusive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "queue.db")
	setup, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 4 {
		setup.Insert("codex", "s1", fmt.Sprintf("t%d", i), "Hi.", "speaker-0", 1.0)
	}
	setup.Close()

	var group sync.WaitGroup
	claimed := make(chan string, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			db, err := Open(path)
			if err != nil {
				t.Errorf("open failed: %v", err)
				return
			}
			defer db.Close()
			job, err := db.Claim()
			if err != nil {
				t.Errorf("claim failed: %v", err)
				return
			}
			if job != nil {
				claimed <- job.TurnID
			}
		}()
	}
	group.Wait()
	close(claimed)
	seen := map[string]bool{}
	for turn := range claimed {
		if seen[turn] {
			t.Fatalf("turn %q claimed twice", turn)
		}
		seen[turn] = true
	}
	if len(seen) != 4 {
		t.Errorf("claimed %d jobs, want 4", len(seen))
	}
}

func TestRequeueAndFinish(t *testing.T) {
	db, _ := openTestDB(t)
	db.Insert("codex", "s1", "t1", "Hi.", "speaker-0", 1.0)
	job, err := db.Claim()
	if err != nil || job == nil {
		t.Fatalf("claim failed: %v", job)
	}
	pending, _ := db.HasPending()
	if pending {
		t.Fatal("claimed job still pending")
	}
	moved, err := db.RequeueProcessing()
	if err != nil || moved != 1 {
		t.Fatalf("requeue moved %d, err %v", moved, err)
	}
	again, err := db.Claim()
	if err != nil || again == nil || again.ID != job.ID {
		t.Fatalf("requeued claim = %+v, %v", again, err)
	}
	if again.StartedAt == nil {
		t.Error("reclaimed job lost its start time")
	}
	now := int64(1700000000)
	again.Status = Success
	again.CompletedAt = &now
	again.Error = nil
	done, err := db.Finish(again)
	if err != nil || !done {
		t.Fatalf("finish = %v, %v", done, err)
	}
	stored := readJobStatus(t, db.db, again.ID)
	if stored != Success {
		t.Errorf("stored status = %q, want success", stored)
	}
}

func readJobStatus(t *testing.T, db *sql.DB, id string) string {
	t.Helper()
	var status string
	if err := db.QueryRow(`SELECT processing_status FROM turnecho_jobs WHERE id = ?`, id).Scan(&status); err != nil {
		t.Fatal(err)
	}
	return status
}

func TestMigrationMismatchFailsSafe(t *testing.T) {
	_, path := openTestDB(t)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`UPDATE turnecho_schema_migrations SET checksum = 'bad'`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()
	_, err = Open(path)
	migrationErr, ok := err.(*MigrationError)
	if !ok {
		t.Fatalf("got %v (%T), want a migration error", err, err)
	}
	if migrationErr.Reason == "" {
		t.Error("migration error has no reason")
	}
}

func TestUnknownMigrationFailsSafe(t *testing.T) {
	_, path := openTestDB(t)
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(
		`INSERT INTO turnecho_schema_migrations (version, name, checksum, applied_at) VALUES (999, 'future', 'x', 1)`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("unknown migration accepted")
	} else if _, ok := err.(*MigrationError); !ok {
		t.Fatalf("got %v (%T), want a migration error", err, err)
	}
}
