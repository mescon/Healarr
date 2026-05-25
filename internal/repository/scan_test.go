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
	repo := NewScanRepository(newScanTestDB(t))
	if _, err := repo.GetByID(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID = %v, want ErrNotFound", err)
	}
}

func TestScanRepository_Exists(t *testing.T) {
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
	repo := NewScanRepository(newScanTestDB(t))
	if _, err := repo.GetLastCompletedScan(context.Background()); !errors.Is(err, ErrNotFound) {
		t.Errorf("empty DB = %v, want ErrNotFound", err)
	}
}

func TestScanRepository_GetLastCompletedScanByPathID(t *testing.T) {
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
