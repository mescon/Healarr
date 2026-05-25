package repository

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const rescanSchema = `
CREATE TABLE pending_rescans (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	file_path       TEXT NOT NULL UNIQUE,
	path_id         INTEGER,
	error_type      TEXT NOT NULL,
	error_message   TEXT,
	retry_count     INTEGER DEFAULT 0,
	max_retries     INTEGER DEFAULT 5,
	first_seen_at   TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	last_attempt_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	next_retry_at   TIMESTAMP,
	status          TEXT DEFAULT 'pending' CHECK (status IN ('pending', 'resolved', 'abandoned')),
	resolved_at     TIMESTAMP,
	resolution      TEXT
);
`

func newRescanTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(rescanSchema); err != nil {
		t.Fatalf("create pending_rescans schema: %v", err)
	}
	return db
}

func TestRescanRepository_Queue_insertsAndReQueues(t *testing.T) {
	t.Parallel()
	db := newRescanTestDB(t)
	repo := NewRescanRepository(db)
	ctx := context.Background()

	if err := repo.Queue(ctx, "/a.mkv", 1, "MountLost", "mount gone"); err != nil {
		t.Fatalf("Queue: %v", err)
	}

	// Re-queueing the same file_path bumps retry_count via ON CONFLICT.
	if err := repo.Queue(ctx, "/a.mkv", 1, "Timeout", "slow"); err != nil {
		t.Fatalf("Queue (conflict): %v", err)
	}

	var retryCount int
	var errType string
	if err := db.QueryRow(`SELECT retry_count, error_type FROM pending_rescans WHERE file_path = ?`, "/a.mkv").
		Scan(&retryCount, &errType); err != nil {
		t.Fatalf("query: %v", err)
	}
	if retryCount != 1 {
		t.Errorf("retry_count = %d, want 1 after one conflict", retryCount)
	}
	if errType != "Timeout" {
		t.Errorf("error_type = %q, want Timeout (latest wins)", errType)
	}
}

func TestRescanRepository_ListReady(t *testing.T) {
	t.Parallel()
	db := newRescanTestDB(t)
	repo := NewRescanRepository(db)
	ctx := context.Background()

	// Ready: pending, due in the past, under max_retries.
	mustExec(t, db, `INSERT INTO pending_rescans (file_path, error_type, retry_count, max_retries, status, next_retry_at)
		VALUES ('/ready.mkv', 'MountLost', 0, 5, 'pending', datetime('now', '-1 minute'))`)
	// Not due yet.
	mustExec(t, db, `INSERT INTO pending_rescans (file_path, error_type, retry_count, max_retries, status, next_retry_at)
		VALUES ('/future.mkv', 'MountLost', 0, 5, 'pending', datetime('now', '+1 hour'))`)
	// Exhausted retries.
	mustExec(t, db, `INSERT INTO pending_rescans (file_path, error_type, retry_count, max_retries, status, next_retry_at)
		VALUES ('/exhausted.mkv', 'MountLost', 5, 5, 'pending', datetime('now', '-1 minute'))`)
	// Already resolved.
	mustExec(t, db, `INSERT INTO pending_rescans (file_path, error_type, retry_count, max_retries, status, next_retry_at)
		VALUES ('/done.mkv', 'MountLost', 0, 5, 'resolved', datetime('now', '-1 minute'))`)

	ready, err := repo.ListReady(ctx, 50)
	if err != nil {
		t.Fatalf("ListReady: %v", err)
	}
	if len(ready) != 1 || ready[0].FilePath != "/ready.mkv" {
		t.Errorf("ListReady = %+v, want only /ready.mkv", ready)
	}
}

func TestRescanRepository_ListReady_respectsLimit(t *testing.T) {
	t.Parallel()
	db := newRescanTestDB(t)
	repo := NewRescanRepository(db)
	for _, p := range []string{"/a", "/b", "/c"} {
		mustExec(t, db, `INSERT INTO pending_rescans (file_path, error_type, retry_count, max_retries, status, next_retry_at)
			VALUES (?, 'MountLost', 0, 5, 'pending', datetime('now', '-1 minute'))`, p)
	}
	ready, err := repo.ListReady(context.Background(), 2)
	if err != nil || len(ready) != 2 {
		t.Fatalf("ListReady(limit=2) = (%d, %v), want (2, nil)", len(ready), err)
	}
}

func TestRescanRepository_MarkResolved(t *testing.T) {
	t.Parallel()
	db := newRescanTestDB(t)
	repo := NewRescanRepository(db)
	ctx := context.Background()
	_ = repo.Queue(ctx, "/a.mkv", 1, "MountLost", "gone")
	var id int64
	_ = db.QueryRow(`SELECT id FROM pending_rescans WHERE file_path = ?`, "/a.mkv").Scan(&id)

	if err := repo.MarkResolved(ctx, id, "abandoned", "max retries"); err != nil {
		t.Fatalf("MarkResolved: %v", err)
	}
	var status, resolution string
	_ = db.QueryRow(`SELECT status, resolution FROM pending_rescans WHERE id = ?`, id).Scan(&status, &resolution)
	if status != "abandoned" || resolution != "max retries" {
		t.Errorf("status/resolution = %q/%q, want abandoned/max retries", status, resolution)
	}

	// Resolved entries are excluded from ListReady.
	ready, _ := repo.ListReady(ctx, 50)
	if len(ready) != 0 {
		t.Errorf("resolved entry still in ListReady: %+v", ready)
	}
}

func TestRescanRepository_BumpRetry(t *testing.T) {
	t.Parallel()
	db := newRescanTestDB(t)
	repo := NewRescanRepository(db)
	ctx := context.Background()
	mustExec(t, db, `INSERT INTO pending_rescans (file_path, error_type, retry_count, max_retries, status, next_retry_at)
		VALUES ('/a.mkv', 'MountLost', 0, 5, 'pending', datetime('now', '-1 minute'))`)
	var id int64
	_ = db.QueryRow(`SELECT id FROM pending_rescans WHERE file_path = ?`, "/a.mkv").Scan(&id)

	if err := repo.BumpRetry(ctx, id, "Timeout", "still slow"); err != nil {
		t.Fatalf("BumpRetry: %v", err)
	}
	var retryCount int
	var errType string
	_ = db.QueryRow(`SELECT retry_count, error_type FROM pending_rescans WHERE id = ?`, id).Scan(&retryCount, &errType)
	if retryCount != 1 || errType != "Timeout" {
		t.Errorf("retry_count/error_type = %d/%q, want 1/Timeout", retryCount, errType)
	}
}

func TestRescanRepository_Stats(t *testing.T) {
	t.Parallel()
	db := newRescanTestDB(t)
	repo := NewRescanRepository(db)
	mustExec(t, db, `INSERT INTO pending_rescans (file_path, error_type, status) VALUES ('/p1', 'X', 'pending')`)
	mustExec(t, db, `INSERT INTO pending_rescans (file_path, error_type, status) VALUES ('/p2', 'X', 'pending')`)
	mustExec(t, db, `INSERT INTO pending_rescans (file_path, error_type, status) VALUES ('/a1', 'X', 'abandoned')`)
	mustExec(t, db, `INSERT INTO pending_rescans (file_path, error_type, status) VALUES ('/r1', 'X', 'resolved')`)

	stats, err := repo.Stats(context.Background())
	if err != nil {
		t.Fatalf("Stats: %v", err)
	}
	if stats.Pending != 2 || stats.Abandoned != 1 || stats.Resolved != 1 {
		t.Errorf("stats = %+v, want pending=2 abandoned=1 resolved=1", stats)
	}
}

func mustExec(t *testing.T, db *sql.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}
