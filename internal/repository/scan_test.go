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

func TestScanRepository_GetLastScan(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	seedScan(t, db, "/older", "completed", 10, 0, "2026-05-01 00:00:00")
	seedScan(t, db, "/newer", "completed", 20, 0, "2026-05-10 00:00:00")
	seedScan(t, db, "/running", "running", 5, 0, "")

	last, err := repo.GetLastScan(context.Background())
	if err != nil {
		t.Fatalf("GetLastScan: %v", err)
	}
	if !last.Path.Valid || last.Path.String != "/newer" {
		t.Errorf("Path = %+v, want /newer", last.Path)
	}
}

// The reported bug: a scan that processed thousands of files before being
// cancelled is real scan activity, but the old completed-only query ignored
// it, leaving the dashboard saying "Never scanned" while /scans listed it.
// GetLastScan must return such a scan.
func TestScanRepository_GetLastScan_CountsCancelledWithFiles(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	// A cancelled scan that scanned 4031 files (exactly the prod scenario).
	seedScan(t, db, "/media/TV", "cancelled", 4031, 0, "2026-06-03 16:31:10")
	// A later cancelled scan that scanned 0 files (cancelled during
	// enumeration) must NOT win — it scanned nothing.
	seedScan(t, db, "/media/Movies/HD", "cancelled", 0, 0, "2026-06-05 09:31:42")

	last, err := repo.GetLastScan(context.Background())
	if err != nil {
		t.Fatalf("GetLastScan: %v", err)
	}
	if !last.Path.Valid || last.Path.String != "/media/TV" {
		t.Errorf("Path = %+v, want /media/TV (the cancelled scan that scanned 4031 files)", last.Path)
	}
}

func TestScanRepository_GetLastScan_empty(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	// A scan that scanned zero files does not count as scan activity.
	seedScan(t, db, "/zero", "cancelled", 0, 0, "2026-05-01 00:00:00")
	if _, err := repo.GetLastScan(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Errorf("no scan with files = %v, want ErrNotFound", err)
	}
}

func TestScanRepository_GetLastScanByPathID(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	// Two paths; the per-path query should pick the newer scan-with-files for
	// the queried path, not the global newest. The middle row is cancelled
	// but scanned files, so it still counts.
	if _, err := db.Exec(`
		INSERT INTO scans (path, path_id, status, files_scanned, completed_at) VALUES
		('/a', 1, 'completed', 100, '2026-05-01 00:00:00'),
		('/a', 1, 'cancelled', 250, '2026-05-05 00:00:00'),
		('/b', 2, 'completed', 300, '2026-05-10 00:00:00')
	`); err != nil {
		t.Fatalf("seed: %v", err)
	}

	last, err := repo.GetLastScanByPathID(context.Background(), 1)
	if err != nil {
		t.Fatalf("GetLastScanByPathID: %v", err)
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

	// running + enumerating cancel; the paused row (it has progress via
	// MarkPaused) demotes to interrupted — pause state cannot survive a
	// restart, so a paused-with-progress row is resumable, not spared.
	n, err := repo.MarkOrphansCancelled(ctx)
	if err != nil || n != 3 {
		t.Fatalf("MarkOrphansCancelled: got (%d, %v), want (3, nil)", n, err)
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
	check(paused, "interrupted")      // paused with progress: demoted for resume
	check(interrupted, "interrupted") // spared
	check(completed, "completed")     // untouched

	// Idempotent: a second call finds nothing to cancel.
	n, err = repo.MarkOrphansCancelled(ctx)
	if err != nil || n != 0 {
		t.Errorf("MarkOrphansCancelled (second call): got (%d, %v), want (0, nil)", n, err)
	}
}

// ReconcileOrphans must demote rows with real persisted progress to
// 'interrupted' (resumable) rather than 'cancelled' (terminal). This is the
// case where a long-running scan was killed by an abrupt container restart
// before its SIGTERM handler could MarkInterrupted — historically the row
// was left dead, so a multi-hour run had to be restarted from zero.
func TestScanRepository_ReconcileOrphans_ResumesProgress(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	// Resumable orphan: real progress + file_list saved.
	resumable, _ := repo.Create(ctx, CreateScanParams{Path: "/resumable", PathID: 1, TotalFiles: 100, FileListJSON: `["a","b","c"]`})
	mustExecScan(t, db, `UPDATE scans SET status='running', current_file_index=42 WHERE id=?`, resumable)

	// Zero-progress orphan: running but never got past file 0 (e.g. crashed in setup).
	zero, _ := repo.Create(ctx, CreateScanParams{Path: "/zero", PathID: 1, TotalFiles: 100, FileListJSON: `["a","b","c"]`})
	mustExecScan(t, db, `UPDATE scans SET status='running', current_file_index=0 WHERE id=?`, zero)

	// Enumerating: file list not built yet — never resumable.
	enumerating, _ := repo.Create(ctx, CreateScanParams{Path: "/enum", PathID: 1, TotalFiles: 0, FileListJSON: ""})
	mustExecScan(t, db, `UPDATE scans SET status='enumerating', current_file_index=0, file_list=NULL WHERE id=?`, enumerating)

	// Spared statuses (must not be touched at all).
	paused, _ := repo.Create(ctx, CreateScanParams{Path: "/pause", PathID: 1, TotalFiles: 10, FileListJSON: `["x"]`})
	if err := repo.MarkPaused(ctx, paused, 5); err != nil {
		t.Fatalf("MarkPaused setup: %v", err)
	}
	completed, _ := repo.Create(ctx, CreateScanParams{Path: "/done", PathID: 1, TotalFiles: 10, FileListJSON: `["x"]`})
	if err := repo.Finalize(ctx, completed, "completed", 10); err != nil {
		t.Fatalf("Finalize setup: %v", err)
	}

	out, err := repo.ReconcileOrphans(ctx)
	if err != nil {
		t.Fatalf("ReconcileOrphans: %v", err)
	}
	if out.Interrupted != 2 {
		t.Errorf("Interrupted count: got %d, want 2 (running-with-progress + paused-with-progress)", out.Interrupted)
	}
	if out.Cancelled != 2 {
		t.Errorf("Cancelled count: got %d, want 2 (zero-progress running + enumerating)", out.Cancelled)
	}

	check := func(id int64, want string) {
		var got string
		_ = db.QueryRow(`SELECT status FROM scans WHERE id=?`, id).Scan(&got)
		if got != want {
			t.Errorf("scan %d: status = %q, want %q", id, got, want)
		}
	}
	check(resumable, "interrupted") // demoted, resume will pick up
	check(zero, "cancelled")        // no progress to resume
	check(enumerating, "cancelled") // file list not built
	check(paused, "interrupted")    // paused with progress: resumable after restart
	check(completed, "completed")   // untouched

	// Resumable row's progress fields must survive so resume can use them.
	var currentFileIndex int
	var fileList sql.NullString
	if err := db.QueryRow(`SELECT current_file_index, file_list FROM scans WHERE id=?`, resumable).Scan(&currentFileIndex, &fileList); err != nil {
		t.Fatalf("read resumable row: %v", err)
	}
	if currentFileIndex != 42 {
		t.Errorf("current_file_index reset: got %d, want 42", currentFileIndex)
	}
	if !fileList.Valid || fileList.String == "" {
		t.Errorf("file_list cleared on reconcile (must be preserved for resume)")
	}

	// Idempotent: second call is a no-op.
	out2, err := repo.ReconcileOrphans(ctx)
	if err != nil || out2.Interrupted != 0 || out2.Cancelled != 0 {
		t.Errorf("second ReconcileOrphans: got (%d intr, %d can, %v), want (0,0,nil)", out2.Interrupted, out2.Cancelled, err)
	}
}

// =============================================================================
// Regression tests for #274: zombie cancelled-then-interrupted scans
// =============================================================================

// A row that was cancelled (completed_at set) and later overwritten with
// status='interrupted' by a graceful shutdown must NOT be picked up by
// ListInterrupted: the user already decided they were done with it.
func TestScanRepository_ListInterrupted_SkipsCancelledThenInterrupted(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	// Build the #274 row shape directly: status='interrupted' AND
	// completed_at IS NOT NULL AND file_list IS NOT NULL.
	mustExecScan(t, db, `
		INSERT INTO scans (path, status, file_list, completed_at, error_message)
		VALUES ('/zombie', 'interrupted', '["/x.mkv"]', datetime('now', '-1 hour'), 'cancelled by user')
	`)

	rows, err := repo.ListInterrupted(ctx)
	if err != nil {
		t.Fatalf("ListInterrupted: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("zombie row returned: %+v - the completed_at IS NULL filter is missing", rows)
	}
}

// MarkCancelled must catch an inconsistent active-status row that already
// has completed_at set (the #274 zombie shape). The old completed_at-based
// guard rejected this update, leaving the row stuck forever.
func TestScanRepository_MarkCancelled_RecoversInconsistentZombieRow(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	// Inconsistent row: status='running' but completed_at is set.
	mustExecScan(t, db, `
		INSERT INTO scans (id, path, status, file_list, completed_at, error_message)
		VALUES (42, '/zombie', 'running', '["/x.mkv"]', datetime('now', '-1 hour'), 'cancelled by user')
	`)

	ok, err := repo.MarkCancelled(ctx, 42, "user cancel retry")
	if err != nil || !ok {
		t.Fatalf("MarkCancelled zombie: got (%v, %v), want (true, nil)", ok, err)
	}
	var status, errMsg string
	_ = db.QueryRow(`SELECT status, error_message FROM scans WHERE id = 42`).Scan(&status, &errMsg)
	if status != "cancelled" || errMsg != "user cancel retry" {
		t.Errorf("after recovery: status/msg = %q/%q, want cancelled/user cancel retry", status, errMsg)
	}
}

// MarkCancelled must still no-op on a fresh-cancelled row (idempotency)
// and on rows that reached other terminal states.
func TestScanRepository_MarkCancelled_NoOpOnTerminalStates(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	// already cancelled
	mustExecScan(t, db, `INSERT INTO scans (id, path, status, completed_at) VALUES (1, '/c', 'cancelled', datetime('now'))`)
	// completed
	mustExecScan(t, db, `INSERT INTO scans (id, path, status, completed_at) VALUES (2, '/d', 'completed', datetime('now'))`)
	// aborted
	mustExecScan(t, db, `INSERT INTO scans (id, path, status) VALUES (3, '/e', 'aborted')`)

	for _, id := range []int64{1, 2, 3} {
		ok, err := repo.MarkCancelled(ctx, id, "should not apply")
		if err != nil || ok {
			t.Errorf("MarkCancelled terminal id=%d: got (%v, %v), want (false, nil)", id, ok, err)
		}
	}
}

// MarkOrphansCancelled must catch zombie active-with-completed_at rows
// at startup (the #274 fix). Interrupted is still spared; paused is now an
// orphan too (pause state lives in process memory and cannot survive a
// restart) — without progress it cancels, with progress it resumes.
func TestScanRepository_MarkOrphansCancelled_CatchesZombieRows(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	// The #274 zombie: status='running' with completed_at set.
	mustExecScan(t, db, `
		INSERT INTO scans (id, path, status, completed_at, error_message)
		VALUES (10, '/zombie', 'running', datetime('now', '-1 hour'), 'cancelled by user')
	`)
	// A clean orphan: status='running', completed_at NULL.
	mustExecScan(t, db, `INSERT INTO scans (id, path, status) VALUES (11, '/clean', 'running')`)
	// Paused without progress: orphaned by restart, nothing to resume -> cancelled.
	mustExecScan(t, db, `INSERT INTO scans (id, path, status) VALUES (12, '/p', 'paused')`)
	// Interrupted: must NOT be touched.
	mustExecScan(t, db, `INSERT INTO scans (id, path, status) VALUES (13, '/i', 'interrupted')`)

	n, err := repo.MarkOrphansCancelled(ctx)
	if err != nil || n != 3 {
		t.Fatalf("MarkOrphansCancelled: got (%d, %v), want (3, nil)", n, err)
	}

	check := func(id int64, want string) {
		var got string
		_ = db.QueryRow(`SELECT status FROM scans WHERE id = ?`, id).Scan(&got)
		if got != want {
			t.Errorf("scan %d: status = %q, want %q", id, got, want)
		}
	}
	check(10, "cancelled")   // zombie reaped
	check(11, "cancelled")   // clean orphan reaped
	check(12, "cancelled")   // paused without progress: nothing to resume
	check(13, "interrupted") // spared
}

// CreateEnumerating must insert a row in the 'enumerating' state with no file
// list and zero totals, so the scan is visible the moment it starts but is
// reconciled as cancelled (not resumed) if the process dies mid-walk.
func TestScanRepository_CreateEnumerating(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id, err := repo.CreateEnumerating(ctx, CreateScanParams{
		Path: "/media/tv", PathID: 1,
		DetectionConfigJSON: `{"method":"ffprobe"}`,
		AutoRemediate:       true,
	})
	if err != nil {
		t.Fatalf("CreateEnumerating: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected a positive id, got %d", id)
	}

	var status, fileList string
	var total, idx int
	if err := db.QueryRow(
		`SELECT status, total_files, current_file_index, file_list FROM scans WHERE id=?`, id,
	).Scan(&status, &total, &idx, &fileList); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != "enumerating" {
		t.Errorf("status = %q, want enumerating", status)
	}
	if total != 0 || idx != 0 {
		t.Errorf("total=%d idx=%d, want 0/0 (nothing enumerated yet)", total, idx)
	}
	if fileList != "[]" {
		t.Errorf("file_list = %q, want []", fileList)
	}
}

// FinishEnumeration must flip an 'enumerating' row to 'scanning' and persist
// the discovered file list + total so the per-file loop and resume logic have
// the work set.
func TestScanRepository_FinishEnumeration(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id, err := repo.CreateEnumerating(ctx, CreateScanParams{Path: "/media/tv", PathID: 1})
	if err != nil {
		t.Fatalf("CreateEnumerating: %v", err)
	}

	fileList := `["/media/tv/a.mkv","/media/tv/b.mkv","/media/tv/c.mkv"]`
	if err := repo.FinishEnumeration(ctx, id, 3, fileList); err != nil {
		t.Fatalf("FinishEnumeration: %v", err)
	}

	var status, gotList string
	var total int
	if err := db.QueryRow(
		`SELECT status, total_files, file_list FROM scans WHERE id=?`, id,
	).Scan(&status, &total, &gotList); err != nil {
		t.Fatalf("read row: %v", err)
	}
	if status != "scanning" {
		t.Errorf("status = %q, want scanning", status)
	}
	if total != 3 {
		t.Errorf("total_files = %d, want 3", total)
	}
	if gotList != fileList {
		t.Errorf("file_list = %q, want %q", gotList, fileList)
	}
}

// An 'enumerating' orphan (process died mid-walk: status='enumerating',
// current_file_index=0, file_list='[]') must reconcile to 'cancelled', never
// 'interrupted' — there is no resumable work set.
func TestScanRepository_ReconcileOrphans_EnumeratingGoesCancelled(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	id, err := repo.CreateEnumerating(ctx, CreateScanParams{Path: "/media/tv", PathID: 1})
	if err != nil {
		t.Fatalf("CreateEnumerating: %v", err)
	}

	out, err := repo.ReconcileOrphans(ctx)
	if err != nil {
		t.Fatalf("ReconcileOrphans: %v", err)
	}
	if out.Interrupted != 0 {
		t.Errorf("Interrupted = %d, want 0 (enumeration is not resumable)", out.Interrupted)
	}
	if out.Cancelled != 1 {
		t.Errorf("Cancelled = %d, want 1", out.Cancelled)
	}

	var status string
	_ = db.QueryRow(`SELECT status FROM scans WHERE id=?`, id).Scan(&status)
	if status != "cancelled" {
		t.Errorf("status = %q, want cancelled", status)
	}
}

// MarkInterrupted must not resurrect terminal scans: Shutdown calls it for
// everything still in activeScans, and flipping a just-aborted scan to
// 'interrupted' would resume it on next startup against a possibly
// still-dead mount.
func TestScanRepository_MarkInterrupted_DoesNotResurrectTerminal(t *testing.T) {
	t.Parallel()
	db := newScanTestDB(t)
	repo := NewScanRepository(db)
	ctx := context.Background()

	for _, status := range []string{"aborted", "cancelled", "completed"} {
		id := seedScan(t, db, "/t-"+status, status, 10, 0, "")
		if err := repo.MarkInterrupted(ctx, id, 5); err != nil {
			t.Fatalf("MarkInterrupted(%s): %v", status, err)
		}
		var got string
		_ = db.QueryRow(`SELECT status FROM scans WHERE id=?`, id).Scan(&got)
		if got != status {
			t.Errorf("terminal %q row flipped to %q by MarkInterrupted", status, got)
		}
	}

	// And an active scan IS marked interrupted (the normal shutdown path).
	id := seedScan(t, db, "/active", "running", 10, 0, "")
	if err := repo.MarkInterrupted(ctx, id, 5); err != nil {
		t.Fatalf("MarkInterrupted(running): %v", err)
	}
	var got string
	_ = db.QueryRow(`SELECT status FROM scans WHERE id=?`, id).Scan(&got)
	if got != "interrupted" {
		t.Errorf("running row = %q, want interrupted", got)
	}
}
