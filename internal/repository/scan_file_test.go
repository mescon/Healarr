package repository

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

const scanFileSchema = `
CREATE TABLE scan_files (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	scan_id         INTEGER NOT NULL,
	file_path       TEXT NOT NULL,
	status          TEXT NOT NULL CHECK (status IN ('healthy', 'corrupt', 'error', 'inaccessible', 'skipped')),
	corruption_type TEXT,
	error_details   TEXT,
	file_size       INTEGER,
	scanned_at      TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
-- Mirrors migration 010_scan_files_unique_index.sql so the test DB
-- exercises the same ON CONFLICT(scan_id, file_path) path the production
-- repository relies on.
CREATE UNIQUE INDEX idx_scan_files_scan_id_file_path_unique
    ON scan_files(scan_id, file_path);
`

func newScanFileTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(scanFileSchema); err != nil {
		t.Fatalf("create scan_files schema: %v", err)
	}
	return db
}

func TestScanFileRepository_Record_healthyStoresNullOptionals(t *testing.T) {
	t.Parallel()
	db := newScanFileTestDB(t)
	repo := NewScanFileRepository(db)

	// Healthy file: no corruption_type / error_details → should be NULL.
	if err := repo.Record(context.Background(), 1, ScanFileRecord{
		FilePath: "/a.mkv", Status: "healthy", FileSize: 123,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var ct, ed sql.NullString
	var size sql.NullInt64
	if err := db.QueryRow(`SELECT corruption_type, error_details, file_size FROM scan_files WHERE scan_id = 1`).
		Scan(&ct, &ed, &size); err != nil {
		t.Fatalf("query: %v", err)
	}
	if ct.Valid || ed.Valid {
		t.Errorf("healthy record stored non-NULL optionals: ct=%+v ed=%+v", ct, ed)
	}
	if !size.Valid || size.Int64 != 123 {
		t.Errorf("file_size = %+v, want 123", size)
	}
}

func TestScanFileRepository_Record_corruptStoresDetails(t *testing.T) {
	t.Parallel()
	db := newScanFileTestDB(t)
	repo := NewScanFileRepository(db)

	if err := repo.Record(context.Background(), 1, ScanFileRecord{
		FilePath: "/b.mkv", Status: "corrupt", CorruptionType: "CorruptHeader",
		ErrorDetails: "bad ebml", FileSize: 999,
	}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var ct, ed string
	if err := db.QueryRow(`SELECT corruption_type, error_details FROM scan_files WHERE scan_id = 1`).
		Scan(&ct, &ed); err != nil {
		t.Fatalf("query: %v", err)
	}
	if ct != "CorruptHeader" || ed != "bad ebml" {
		t.Errorf("ct/ed = %q/%q, want CorruptHeader/bad ebml", ct, ed)
	}
}

func TestScanFileRepository_CountByStatus(t *testing.T) {
	t.Parallel()
	db := newScanFileTestDB(t)
	repo := NewScanFileRepository(db)
	ctx := context.Background()

	for _, rec := range []ScanFileRecord{
		{FilePath: "/1", Status: "healthy"},
		{FilePath: "/2", Status: "healthy"},
		{FilePath: "/3", Status: "corrupt", CorruptionType: "X"},
		{FilePath: "/4", Status: "skipped", CorruptionType: "Y"},
	} {
		if err := repo.Record(ctx, 1, rec); err != nil {
			t.Fatalf("Record: %v", err)
		}
	}
	// A different scan — must not bleed into scan 1's counts.
	_ = repo.Record(ctx, 2, ScanFileRecord{FilePath: "/other", Status: "corrupt", CorruptionType: "Z"})

	counts, err := repo.CountByStatus(ctx, 1)
	if err != nil {
		t.Fatalf("CountByStatus: %v", err)
	}
	if counts["healthy"] != 2 || counts["corrupt"] != 1 || counts["skipped"] != 1 {
		t.Errorf("counts = %+v, want healthy=2 corrupt=1 skipped=1", counts)
	}
}

func TestScanFileRepository_CountForScan_andListForScan(t *testing.T) {
	t.Parallel()
	db := newScanFileTestDB(t)
	repo := NewScanFileRepository(db)
	ctx := context.Background()
	for _, rec := range []ScanFileRecord{
		{FilePath: "/h1", Status: "healthy"},
		{FilePath: "/h2", Status: "healthy"},
		{FilePath: "/c1", Status: "corrupt", CorruptionType: "X"},
	} {
		_ = repo.Record(ctx, 1, rec)
	}

	// "all" filter.
	total, err := repo.CountForScan(ctx, 1, "all")
	if err != nil || total != 3 {
		t.Fatalf("CountForScan all = (%d, %v), want (3, nil)", total, err)
	}
	rows, err := repo.ListForScan(ctx, 1, "all", 10, 0)
	if err != nil || len(rows) != 3 {
		t.Fatalf("ListForScan all = (%d, %v), want (3, nil)", len(rows), err)
	}

	// Status filter.
	total, err = repo.CountForScan(ctx, 1, "corrupt")
	if err != nil || total != 1 {
		t.Fatalf("CountForScan corrupt = (%d, %v), want (1, nil)", total, err)
	}
	rows, err = repo.ListForScan(ctx, 1, "corrupt", 10, 0)
	if err != nil || len(rows) != 1 || rows[0].FilePath != "/c1" {
		t.Fatalf("ListForScan corrupt = %+v (err %v), want only /c1", rows, err)
	}
}

func TestScanFileRepository_ListForScan_pagination(t *testing.T) {
	t.Parallel()
	db := newScanFileTestDB(t)
	repo := NewScanFileRepository(db)
	ctx := context.Background()
	for _, p := range []string{"/a", "/b", "/c", "/d", "/e"} {
		_ = repo.Record(ctx, 1, ScanFileRecord{FilePath: p, Status: "healthy"})
	}

	page1, err := repo.ListForScan(ctx, 1, "all", 2, 0)
	if err != nil || len(page1) != 2 {
		t.Fatalf("page1 = (%d, %v), want (2, nil)", len(page1), err)
	}
	page3, err := repo.ListForScan(ctx, 1, "all", 2, 4)
	if err != nil || len(page3) != 1 {
		t.Fatalf("page3 = (%d, %v), want (1, nil)", len(page3), err)
	}
}

// Record must be idempotent on (scan_id, file_path) so the scanner's
// post-interruption replay window cannot produce duplicate rows. The
// underlying INSERT ... ON CONFLICT DO NOTHING (migration 010) is what
// guarantees this; this test pins the behavior.
func TestScanFileRepository_Record_IdempotentOnReplay(t *testing.T) {
	t.Parallel()
	db := newScanFileTestDB(t)
	repo := NewScanFileRepository(db)
	ctx := context.Background()

	rec := ScanFileRecord{FilePath: "/a.mkv", Status: "healthy", FileSize: 1024}

	// First insert succeeds.
	if err := repo.Record(ctx, 7, rec); err != nil {
		t.Fatalf("first Record: %v", err)
	}
	// Second insert with the same (scan_id, file_path) must NOT error.
	if err := repo.Record(ctx, 7, rec); err != nil {
		t.Fatalf("second Record (replay) should be a no-op, got error: %v", err)
	}

	// The table must still contain exactly one row for the pair.
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_files WHERE scan_id=7 AND file_path='/a.mkv'`).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 1 {
		t.Errorf("duplicate inserts produced %d rows, want 1", count)
	}

	// A different scan_id with the same file_path is a different row — the
	// uniqueness is scoped to (scan_id, file_path), not file_path alone.
	if err := repo.Record(ctx, 8, rec); err != nil {
		t.Fatalf("Record for different scan: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM scan_files WHERE file_path='/a.mkv'`).Scan(&count); err != nil {
		t.Fatalf("count query 2: %v", err)
	}
	if count != 2 {
		t.Errorf("got %d rows for /a.mkv across scans, want 2 (one per scan)", count)
	}
}

// Record must persist scanned_at at millisecond precision rather than the
// column's CURRENT_TIMESTAMP default (second precision). The chunked-burst
// timestamp clustering visible in the scan-detail UI before #290 came from
// many parallel workers committing within the same second; the new write
// path stamps each row with strftime('%f') so per-file order survives.
func TestScanFileRepository_Record_StoresMillisecondTimestamp(t *testing.T) {
	t.Parallel()
	db := newScanFileTestDB(t)
	repo := NewScanFileRepository(db)
	ctx := context.Background()

	if err := repo.Record(ctx, 1, ScanFileRecord{FilePath: "/a.mkv", Status: "healthy"}); err != nil {
		t.Fatalf("Record: %v", err)
	}

	var ts string
	if err := db.QueryRow(`SELECT scanned_at FROM scan_files WHERE scan_id=1 AND file_path='/a.mkv'`).Scan(&ts); err != nil {
		t.Fatalf("read scanned_at: %v", err)
	}
	// strftime('%Y-%m-%d %H:%M:%f', 'now') produces e.g. "2026-06-04 15:23:42.789".
	// CURRENT_TIMESTAMP produces "2026-06-04 15:23:42" (no fractional part).
	// We expect the dot-and-fractional-seconds tail.
	if len(ts) < 20 || ts[19] != '.' {
		t.Errorf("scanned_at=%q is missing fractional-seconds tail; expected millisecond-precision format", ts)
	}
}
