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
