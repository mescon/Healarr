package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const scanSchema = `
CREATE TABLE scans (
	id                 INTEGER PRIMARY KEY AUTOINCREMENT,
	path               TEXT NOT NULL,
	path_id            INTEGER,
	status             TEXT NOT NULL,
	files_scanned      INTEGER DEFAULT 0,
	corruptions_found  INTEGER DEFAULT 0,
	total_files        INTEGER DEFAULT 0,
	current_file_index INTEGER DEFAULT 0,
	file_list          TEXT,
	detection_config   TEXT,
	auto_remediate     INTEGER DEFAULT 0,
	dry_run            INTEGER DEFAULT 0,
	error_message      TEXT,
	started_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	completed_at       TIMESTAMP
);
`

func newScanTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(scanSchema); err != nil {
		t.Fatalf("create scan schema: %v", err)
	}
	return db
}

// seedScan inserts a scans row directly for test setup. Returns the new id.
func seedScan(t *testing.T, db *sql.DB, path, status string, files, corruptions int, completedAt string) int64 {
	t.Helper()
	var res sql.Result
	var err error
	if completedAt == "" {
		res, err = db.Exec(
			`INSERT INTO scans (path, status, files_scanned, corruptions_found) VALUES (?, ?, ?, ?)`,
			path, status, files, corruptions)
	} else {
		res, err = db.Exec(
			`INSERT INTO scans (path, status, files_scanned, corruptions_found, completed_at) VALUES (?, ?, ?, ?, ?)`,
			path, status, files, corruptions, completedAt)
	}
	if err != nil {
		t.Fatalf("seed scan: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestScanRepository_Count(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	if n, _ := repo.Count(context.Background()); n != 0 {
		t.Errorf("Count empty = %d, want 0", n)
	}
	seedScan(t, db, "/a", "completed", 10, 1, "2026-05-01 00:00:00")
	seedScan(t, db, "/b", "running", 5, 0, "")
	if n, _ := repo.Count(context.Background()); n != 2 {
		t.Errorf("Count = %d, want 2", n)
	}
}

func TestScanRepository_GetByID(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	id := seedScan(t, db, "/path", "completed", 100, 3, "2026-05-01 00:00:00")

	got, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Path != "/path" || got.Status != "completed" || got.FilesScanned != 100 || got.CorruptionsFound != 3 {
		t.Errorf("unexpected row: %+v", got)
	}
}

func TestScanRepository_GetByID_notFound(t *testing.T) {
	t.Parallel()
	repo := NewScanRepository(newScanTestDB(t))
	if _, err := repo.GetByID(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID = %v, want ErrNotFound", err)
	}
}

func TestScanRepository_Exists(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	id := seedScan(t, db, "/p", "completed", 1, 0, "")

	if ok, err := repo.Exists(context.Background(), id); err != nil || !ok {
		t.Errorf("Exists existing = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := repo.Exists(context.Background(), 999); err != nil || ok {
		t.Errorf("Exists missing = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestScanRepository_ListPaged(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	for i := 0; i < 5; i++ {
		seedScan(t, db, "/p", "completed", i*10, 0, "")
	}

	rows, err := repo.ListPaged(context.Background(), "ORDER BY id ASC", 3, 0)
	if err != nil || len(rows) != 3 {
		t.Fatalf("ListPaged page 1 = (%d, %v), want (3, nil)", len(rows), err)
	}

	rows, err = repo.ListPaged(context.Background(), "ORDER BY id ASC", 3, 3)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListPaged page 2 = (%d, %v), want (2, nil)", len(rows), err)
	}
}

func TestScanRepository_GetScanStats(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	// Seed some scans with timestamps that fall in today/this-week ranges.
	if _, err := db.Exec(
		`INSERT INTO scans (path, status, files_scanned, started_at) VALUES
		 ('/a', 'running',   10, datetime('now')),
		 ('/b', 'completed', 20, datetime('now')),
		 ('/c', 'completed', 30, datetime('now', '-3 days')),
		 ('/d', 'completed', 40, datetime('now', '-10 days'))`,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	stats, err := repo.GetScanStats(context.Background())
	if err != nil {
		t.Fatalf("GetScanStats: %v", err)
	}
	if stats.ActiveScans != 1 || stats.TotalScans != 4 {
		t.Errorf("active/total = %d/%d, want 1/4", stats.ActiveScans, stats.TotalScans)
	}
	if stats.FilesScannedToday != 30 {
		t.Errorf("today = %d, want 30 (10 + 20)", stats.FilesScannedToday)
	}
	if stats.FilesScannedWeek != 60 {
		t.Errorf("week = %d, want 60 (10 + 20 + 30)", stats.FilesScannedWeek)
	}
}

func TestScanRepository_GetLastCompletedScan(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	seedScan(t, db, "/older", "completed", 0, 0, "2026-05-01 00:00:00")
	seedScan(t, db, "/newer", "completed", 0, 0, "2026-05-10 00:00:00")
	seedScan(t, db, "/running", "running", 0, 0, "")

	last, err := repo.GetLastCompletedScan(context.Background())
	if err != nil {
		t.Fatalf("GetLastCompletedScan: %v", err)
	}
	if !last.Path.Valid || last.Path.String != "/newer" {
		t.Errorf("Path = %+v, want /newer", last.Path)
	}
}

func TestScanRepository_GetLastCompletedScan_empty(t *testing.T) {
	t.Parallel()
	repo := NewScanRepository(newScanTestDB(t))
	if _, err := repo.GetLastCompletedScan(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty DB = %v, want ErrNotFound", err)
	}
}

func TestScanRepository_GetLastCompletedScanByPathID(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	// Two paths, two completed scans each — the per-path query should pick
	// the newer one for the queried path, not the global newest.
	if _, err := db.Exec(`
		INSERT INTO scans (path, path_id, status, completed_at) VALUES
		('/a', 1, 'completed', '2026-05-01 00:00:00'),
		('/a', 1, 'completed', '2026-05-05 00:00:00'),
		('/b', 2, 'completed', '2026-05-10 00:00:00')
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	last, err := repo.GetLastCompletedScanByPathID(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetLastCompletedScanByPathID: %v", err)
	}
	// modernc.org/sqlite normalizes timestamps to RFC3339-ish form
	// (2026-05-05T00:00:00Z); just check the date portion to keep the test
	// portable across SQLite drivers.
	if !last.CompletedAt.Valid || last.CompletedAt.String[:10] != "2026-05-05" {
		t.Errorf("CompletedAt = %+v, want date 2026-05-05", last.CompletedAt)
	}
}

func TestScanRepository_Create_andStatusTransitions(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id, err := repo.Create(ctx, CreateScanParams{
		Path: "/movies", PathID: 1, TotalFiles: 100,
		FileListJSON: `["/a.mkv","/b.mkv"]`, DetectionConfigJSON: `{"method":"ffprobe"}`,
		AutoRemediate: true, DryRun: false,
	})
	if err != nil || id == 0 {
		t.Fatalf("Create = (%d, %v), want (>0, nil)", id, err)
	}

	got, _ := repo.GetByID(ctx, id)
	if got.Status != "running" || got.Path != "/movies" {
		t.Errorf("after Create: %+v, want running /movies", got)
	}

	if err := repo.SetStatus(ctx, id, "paused"); err != nil {
		t.Fatalf("SetStatus: %v", err)
	}
	if got, _ := repo.GetByID(ctx, id); got.Status != "paused" {
		t.Errorf("after SetStatus: status = %q, want paused", got.Status)
	}

	if err := repo.Finalize(ctx, id, "completed", 100); err != nil {
		t.Fatalf("Finalize: %v", err)
	}
	got, _ = repo.GetByID(ctx, id)
	if got.Status != "completed" || got.FilesScanned != 100 || !got.CompletedAt.Valid {
		t.Errorf("after Finalize: %+v, want completed/100/non-null-completed_at", got)
	}
}

func TestScanRepository_MarkInterrupted_Paused_Aborted(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id, _ := repo.Create(ctx, CreateScanParams{Path: "/p", PathID: 1, TotalFiles: 50, FileListJSON: "[]"})

	if err := repo.MarkInterrupted(ctx, id, 17); err != nil {
		t.Fatalf("MarkInterrupted: %v", err)
	}
	var status string
	var idx int
	_ = db.QueryRow(`SELECT status, current_file_index FROM scans WHERE id = ?`, id).Scan(&status, &idx)
	if status != "interrupted" || idx != 17 {
		t.Errorf("MarkInterrupted: status/idx = %q/%d, want interrupted/17", status, idx)
	}

	if err := repo.MarkPaused(ctx, id, 23); err != nil {
		t.Fatalf("MarkPaused: %v", err)
	}
	_ = db.QueryRow(`SELECT status, current_file_index FROM scans WHERE id = ?`, id).Scan(&status, &idx)
	if status != "paused" || idx != 23 {
		t.Errorf("MarkPaused: status/idx = %q/%d, want paused/23", status, idx)
	}

	if err := repo.MarkAborted(ctx, id, "mount lost"); err != nil {
		t.Fatalf("MarkAborted: %v", err)
	}
	var errMsg string
	_ = db.QueryRow(`SELECT status, error_message FROM scans WHERE id = ?`, id).Scan(&status, &errMsg)
	if status != "aborted" || errMsg != "mount lost" {
		t.Errorf("MarkAborted: status/msg = %q/%q, want aborted/mount lost", status, errMsg)
	}
}

func TestScanRepository_UpdateProgress_andIncrementCorruptions(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()
	id, _ := repo.Create(ctx, CreateScanParams{Path: "/p", PathID: 1, TotalFiles: 50, FileListJSON: "[]"})

	if err := repo.UpdateProgress(ctx, id, 30, 28); err != nil {
		t.Fatalf("UpdateProgress: %v", err)
	}
	var idx, scanned int
	_ = db.QueryRow(`SELECT current_file_index, files_scanned FROM scans WHERE id = ?`, id).Scan(&idx, &scanned)
	if idx != 30 || scanned != 28 {
		t.Errorf("UpdateProgress: idx/scanned = %d/%d, want 30/28", idx, scanned)
	}

	for i := 0; i < 3; i++ {
		if err := repo.IncrementCorruptions(id); err != nil {
			t.Fatalf("IncrementCorruptions: %v", err)
		}
	}
	var found int
	_ = db.QueryRow(`SELECT corruptions_found FROM scans WHERE id = ?`, id).Scan(&found)
	if found != 3 {
		t.Errorf("corruptions_found = %d, want 3", found)
	}
}

func TestScanRepository_ListInterrupted(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	// Interrupted with a file_list → should be returned.
	resumable, _ := repo.Create(ctx, CreateScanParams{
		Path: "/resumable", PathID: 1, TotalFiles: 10,
		FileListJSON: `["/x.mkv"]`, DetectionConfigJSON: `{"method":"ffprobe"}`, AutoRemediate: true,
	})
	_ = repo.MarkInterrupted(ctx, resumable, 5)

	// Interrupted but file_list IS NULL → excluded.
	mustExecScan(t, db, `INSERT INTO scans (path, status, file_list) VALUES ('/nofiles', 'interrupted', NULL)`)
	// Running → excluded.
	_, _ = repo.Create(ctx, CreateScanParams{Path: "/running", PathID: 1, TotalFiles: 1, FileListJSON: "[]"})

	rows, err := repo.ListInterrupted(ctx)
	if err != nil {
		t.Fatalf("ListInterrupted: %v", err)
	}
	if len(rows) != 1 || rows[0].Path != "/resumable" {
		t.Fatalf("ListInterrupted = %+v, want only /resumable", rows)
	}
	r := rows[0]
	if r.CurrentFileIndex != 5 || r.FileListJSON != `["/x.mkv"]` || !r.AutoRemediate {
		t.Errorf("resume shape wrong: %+v", r)
	}
	if !r.DetectionConfigJSON.Valid || r.DetectionConfigJSON.String != `{"method":"ffprobe"}` {
		t.Errorf("detection_config = %+v", r.DetectionConfigJSON)
	}
}

func mustExecScan(t *testing.T, db *sql.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func TestScanRepository_MarkCancelled(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	// Active row gets cancelled.
	id, _ := repo.Create(ctx, CreateScanParams{Path: "/a", PathID: 1, TotalFiles: 10, FileListJSON: "[]"})
	ok, err := repo.MarkCancelled(ctx, id, "user cancel")
	if err != nil || !ok {
		t.Fatalf("MarkCancelled active: got (%v, %v), want (true, nil)", ok, err)
	}
	var status, errMsg string
	var completedAt sql.NullString
	_ = db.QueryRow(`SELECT status, error_message, completed_at FROM scans WHERE id = ?`, id).Scan(&status, &errMsg, &completedAt)
	if status != "cancelled" || errMsg != "user cancel" || !completedAt.Valid {
		t.Errorf("active row: status/msg/completed = %q/%q/%v, want cancelled/user cancel/non-null", status, errMsg, completedAt)
	}

	// Already-terminal row is a no-op (guard prevents clobbering completion).
	done, _ := repo.Create(ctx, CreateScanParams{Path: "/b", PathID: 1, TotalFiles: 5, FileListJSON: "[]"})
	if err := repo.Finalize(ctx, done, "completed", 5); err != nil {
		t.Fatalf("Finalize setup: %v", err)
	}
	ok, err = repo.MarkCancelled(ctx, done, "should not apply")
	if err != nil || ok {
		t.Errorf("MarkCancelled terminal: got (%v, %v), want (false, nil)", ok, err)
	}
	var doneStatus string
	_ = db.QueryRow(`SELECT status FROM scans WHERE id = ?`, done).Scan(&doneStatus)
	if doneStatus != "completed" {
		t.Errorf("terminal row was clobbered: status = %q, want completed", doneStatus)
	}

	// Nonexistent id is a no-op (false, no error).
	ok, err = repo.MarkCancelled(ctx, 999999, "nope")
	if err != nil || ok {
		t.Errorf("MarkCancelled nonexistent: got (%v, %v), want (false, nil)", ok, err)
	}
}

func TestScanRepository_MarkOrphansCancelled(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	// Two orphans in active statuses, plus one each in paused / interrupted /
	// completed - only the two orphans must be touched.
	running, _ := repo.Create(ctx, CreateScanParams{Path: "/run", PathID: 1, TotalFiles: 10, FileListJSON: "[]"})
	mustExecScan(t, db, `UPDATE scans SET status='running' WHERE id = ?`, running)
	enumerating, _ := repo.Create(ctx, CreateScanParams{Path: "/enum", PathID: 1, TotalFiles: 10, FileListJSON: "[]"})
	mustExecScan(t, db, `UPDATE scans SET status='enumerating' WHERE id = ?`, enumerating)
	paused, _ := repo.Create(ctx, CreateScanParams{Path: "/pause", PathID: 1, TotalFiles: 10, FileListJSON: "[]"})
	if err := repo.MarkPaused(ctx, paused, 3); err != nil {
		t.Fatalf("MarkPaused setup: %v", err)
	}
	interrupted, _ := repo.Create(ctx, CreateScanParams{Path: "/intr", PathID: 1, TotalFiles: 10, FileListJSON: "[]"})
	if err := repo.MarkInterrupted(ctx, interrupted, 5); err != nil {
		t.Fatalf("MarkInterrupted setup: %v", err)
	}
	completed, _ := repo.Create(ctx, CreateScanParams{Path: "/done", PathID: 1, TotalFiles: 10, FileListJSON: "[]"})
	if err := repo.Finalize(ctx, completed, "completed", 10); err != nil {
		t.Fatalf("Finalize setup: %v", err)
	}

	n, err := repo.MarkOrphansCancelled(ctx)
	if err != nil || n != 2 {
		t.Fatalf("MarkOrphansCancelled: got (%d, %v), want (2, nil)", n, err)
	}

	check := func(id int64, want string) {
		var got string
		_ = db.QueryRow(`SELECT status FROM scans WHERE id = ?`, id).Scan(&got)
		if got != want {
			t.Errorf("scan %d: status = %q, want %q", id, got, want)
		}
	}
	check(running, "cancelled")
	check(enumerating, "cancelled")
	check(paused, "paused")           // spared
	check(interrupted, "interrupted") // spared
	check(completed, "completed")     // untouched

	// Idempotent: a second call finds nothing to cancel.
	n, err = repo.MarkOrphansCancelled(ctx)
	if err != nil || n != 0 {
		t.Errorf("MarkOrphansCancelled (second call): got (%d, %v), want (0, nil)", n, err)
	}
}
