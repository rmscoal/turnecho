// Package queue persists spoken summaries in a local SQLite database.
package queue

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"fmt"
	"math"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"

	"github.com/rmscoal/turnecho/internal/config"
	"github.com/rmscoal/turnecho/internal/paths"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// Job statuses.
const (
	Pending    = "pending"
	Processing = "processing"
	Failed     = "failed"
	Success    = "success"
)

// BusyTimeoutMillis is how long SQLite waits on locked tables.
const BusyTimeoutMillis = 5000

// MigrationError reports inconsistent database migration state.
type MigrationError struct {
	Reason string
}

// Error implements the error interface.
func (e *MigrationError) Error() string {
	return e.Reason
}

// Job is one queued summary and its processing state.
type Job struct {
	ID          string
	Host        string
	SessionID   string
	TurnID      string
	Message     string
	Voice       string
	Speed       float64
	Status      string
	CreatedAt   int64
	StartedAt   *int64
	CompletedAt *int64
	Error       *string
}

// DB is an open queue database.
type DB struct {
	db *sql.DB
}

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

func discoverMigrations() ([]migration, error) {
	entries, err := migrationFiles.ReadDir("migrations")
	if err != nil {
		return nil, err
	}
	var found []migration
	for _, entry := range entries {
		version, name, ok := strings.Cut(entry.Name(), "_")
		if !ok || !strings.HasSuffix(name, ".sql") {
			continue
		}
		number, err := strconv.Atoi(version)
		if err != nil {
			continue
		}
		content, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, err
		}
		trimmed := strings.TrimSpace(string(content))
		if trimmed == "" {
			return nil, &MigrationError{Reason: "Migration is empty: " + entry.Name()}
		}
		sum := sha256.Sum256([]byte(trimmed))
		found = append(found, migration{
			version:  number,
			name:     strings.TrimSuffix(name, ".sql"),
			sql:      trimmed,
			checksum: fmt.Sprintf("%x", sum),
		})
	}
	sort.Slice(found, func(i, j int) bool { return found[i].version < found[j].version })
	if len(found) == 0 {
		return nil, &MigrationError{Reason: "TurnEcho contains no packaged database migrations."}
	}
	for i := 1; i < len(found); i++ {
		if found[i].version == found[i-1].version {
			return nil, &MigrationError{Reason: "TurnEcho contains duplicate migration versions."}
		}
	}
	return found, nil
}

// splitStatements splits a migration into runnable statements. Line comments
// are stripped before splitting; string literals must still hold no
// semicolons.
func splitStatements(sqlText string) []string {
	var code strings.Builder
	for line := range strings.Lines(sqlText) {
		statement, _, _ := strings.Cut(line, "--")
		code.WriteString(statement)
	}
	var statements []string
	for _, part := range strings.Split(code.String(), ";") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			statements = append(statements, trimmed)
		}
	}
	return statements
}

func runMigrations(db *sql.DB, dbPath string) error {
	migrations, err := discoverMigrations()
	if err != nil {
		return err
	}
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return err
	}
	committed := false
	defer func() {
		if !committed {
			conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	if _, err := conn.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS turnecho_schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			checksum TEXT NOT NULL,
			applied_at INTEGER NOT NULL
		)`); err != nil {
		return err
	}
	rows, err := conn.QueryContext(ctx,
		`SELECT version, name, checksum FROM turnecho_schema_migrations`)
	if err != nil {
		return err
	}
	applied := map[int]migration{}
	for rows.Next() {
		var stored migration
		if err := rows.Scan(&stored.version, &stored.name, &stored.checksum); err != nil {
			rows.Close()
			return err
		}
		applied[stored.version] = stored
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}

	packaged := map[int]bool{}
	for _, item := range migrations {
		packaged[item.version] = true
	}
	var unknown []string
	for version := range applied {
		if !packaged[version] {
			unknown = append(unknown, fmt.Sprintf("%03d", version))
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return &MigrationError{Reason: "Database contains migrations unknown to this TurnEcho version: " +
			strings.Join(unknown, ", ")}
	}

	for _, item := range migrations {
		existing, ok := applied[item.version]
		if ok {
			if existing.name != item.name || existing.checksum != item.checksum {
				return &MigrationError{Reason: fmt.Sprintf(
					"Applied migration %03d no longer matches the packaged migration "+
						"(stale database file at %s; delete it to start fresh, "+
						"v2 does not migrate v1 data).", item.version, dbPath)}
			}
			continue
		}
		for _, statement := range splitStatements(item.sql) {
			if _, err := conn.ExecContext(ctx, statement); err != nil {
				return err
			}
		}
		if _, err := conn.ExecContext(ctx, `
			INSERT INTO turnecho_schema_migrations (version, name, checksum, applied_at)
			VALUES (?, ?, ?, ?)`,
			item.version, item.name, item.checksum, time.Now().Unix()); err != nil {
			return err
		}
	}

	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return err
	}
	committed = true
	return nil
}

// Open creates the parent directory, opens the database, and migrates it.
func Open(dbPath string) (*DB, error) {
	file, err := paths.OpenPrivateFile(dbPath, os.O_CREATE|os.O_RDWR)
	if err != nil {
		return nil, err
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		sidecar, err := paths.OpenPrivateFile(dbPath+suffix, os.O_RDWR)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		if err := sidecar.Close(); err != nil {
			return nil, err
		}
	}
	absolute, err := filepath.Abs(dbPath)
	if err != nil {
		absolute = dbPath
	}
	dsn := url.URL{
		Scheme: "file",
		Path:   absolute,
		RawQuery: "_pragma=busy_timeout(" + strconv.Itoa(BusyTimeoutMillis) + ")" +
			"&_pragma=journal_mode(WAL)",
	}
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	// Single-writer SQLite: one connection keeps manual BEGIN IMMEDIATE safe.
	db.SetMaxOpenConns(1)
	if err := runMigrations(db, dbPath); err != nil {
		db.Close()
		return nil, err
	}
	return &DB{db: db}, nil
}

// Close releases the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// Insert adds a pending job, returning false when the turn already exists.
func (d *DB) Insert(host, sessionID, turnID, message, voice string, speed float64) (bool, error) {
	fields := []struct {
		name  string
		value string
	}{
		{"host", host},
		{"session_id", sessionID},
		{"turn_id", turnID},
		{"message", message},
	}
	for _, field := range fields {
		if strings.TrimSpace(field.value) == "" {
			return false, fmt.Errorf("turnecho job %s must not be blank", field.name)
		}
	}
	if !slices.Contains(config.Voices, voice) {
		return false, fmt.Errorf("unsupported TurnEcho voice: %s", voice)
	}
	if math.IsNaN(speed) || math.IsInf(speed, 0) ||
		speed < config.MinSpeed || speed > config.MaxSpeed {
		return false, fmt.Errorf("unsupported TurnEcho speed: %v", speed)
	}
	result, err := d.db.Exec(`
		INSERT INTO turnecho_jobs (
			id, host, session_id, turn_id, message, voice, speed,
			processing_status, created_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(host, session_id, turn_id) DO NOTHING`,
		uuid.NewString(), host, sessionID, turnID, message, voice, speed,
		Pending, time.Now().Unix())
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func scanJobRow(scanner interface {
	Scan(...any) error
}) (*Job, error) {
	var job Job
	var started, completed sql.NullInt64
	var message sql.NullString
	err := scanner.Scan(
		&job.ID, &job.Host, &job.SessionID, &job.TurnID, &job.Message,
		&job.Voice, &job.Speed, &job.Status, &job.CreatedAt,
		&started, &completed, &message)
	if err != nil {
		return nil, err
	}
	if started.Valid {
		job.StartedAt = &started.Int64
	}
	if completed.Valid {
		job.CompletedAt = &completed.Int64
	}
	if message.Valid {
		job.Error = &message.String
	}
	return &job, nil
}

// Claim atomically takes the oldest pending job.
func (d *DB) Claim() (*Job, error) {
	ctx := context.Background()
	conn, err := d.db.Conn(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			conn.ExecContext(ctx, "ROLLBACK")
		}
	}()
	row := conn.QueryRowContext(ctx, `
		UPDATE turnecho_jobs
		SET processing_status = ?, started_at = ?
		WHERE rowid = (
			SELECT rowid
			FROM turnecho_jobs
			WHERE processing_status = ?
			ORDER BY created_at, rowid
			LIMIT 1
		)
		AND processing_status = ?
		RETURNING id, host, session_id, turn_id, message, voice, speed,
			processing_status, created_at, started_at, completed_at, error_message`,
		Processing, time.Now().Unix(), Pending, Pending)
	job, err := scanJobRow(row)
	if err == sql.ErrNoRows {
		if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
			return nil, err
		}
		committed = true
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if _, err := conn.ExecContext(ctx, "COMMIT"); err != nil {
		return nil, err
	}
	committed = true
	return job, nil
}

// HasPending reports whether the queue contains work for a worker.
func (d *DB) HasPending() (bool, error) {
	var one int
	err := d.db.QueryRow(
		`SELECT 1 FROM turnecho_jobs WHERE processing_status = ? LIMIT 1`,
		Pending).Scan(&one)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// RequeueProcessing resets jobs abandoned by a previous worker.
func (d *DB) RequeueProcessing() (int64, error) {
	result, err := d.db.Exec(`
		UPDATE turnecho_jobs
		SET processing_status = CASE WHEN playback_started = 1 THEN ? ELSE ? END,
            started_at = CASE WHEN playback_started = 1 THEN started_at ELSE NULL END,
            completed_at = CASE WHEN playback_started = 1 THEN ? ELSE NULL END,
            error_message = CASE WHEN playback_started = 1 THEN 'playback interrupted; automatic replay suppressed' ELSE NULL END
        WHERE processing_status = ?`, Failed, Pending, time.Now().Unix(), Processing)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

// Finish persists worker-owned status and completion details.
func (d *DB) Finish(job *Job) (bool, error) {
	var completed sql.NullInt64
	if job.CompletedAt != nil {
		completed = sql.NullInt64{Int64: *job.CompletedAt, Valid: true}
	}
	var message sql.NullString
	if job.Error != nil {
		message = sql.NullString{String: *job.Error, Valid: true}
	}
	result, err := d.db.Exec(`
		UPDATE turnecho_jobs
		SET processing_status = ?, completed_at = ?, error_message = ?
		WHERE id = ?`, job.Status, completed, message, job.ID)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

// MarkPlayback records a durable intent before the external audio side effect.
func (d *DB) MarkPlayback(id string) error {
	result, err := d.db.Exec(`UPDATE turnecho_jobs SET playback_started = 1 WHERE id = ? AND processing_status = ?`, id, Processing)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("job is not owned for playback")
	}
	return nil
}
