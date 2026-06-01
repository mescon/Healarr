package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const scanPathSchema = `
CREATE TABLE scan_paths (
	id                         INTEGER PRIMARY KEY AUTOINCREMENT,
	local_path                 TEXT NOT NULL UNIQUE,
	arr_path                   TEXT NOT NULL,
	arr_instance_id            INTEGER,
	enabled                    BOOLEAN DEFAULT 1,
	auto_remediate             BOOLEAN DEFAULT 0,
	dry_run                    BOOLEAN DEFAULT 0,
	detection_method           TEXT NOT NULL DEFAULT 'ffprobe',
	detection_args             TEXT,
	detection_mode             TEXT NOT NULL DEFAULT 'quick',
	max_retries                INTEGER DEFAULT 3,
	verification_timeout_hours INTEGER,
	thorough_duration_seconds  INTEGER,
	thorough_timeout_seconds   INTEGER,
	hwaccel                    TEXT,
	created_at                 TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`

func newScanPathTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(scanPathSchema); err != nil {
		t.Fatalf("create scan_paths schema: %v", err)
	}
	return db
}

func defaultFields(localPath string) ScanPathFields {
	return ScanPathFields{
		LocalPath:       localPath,
		ArrPath:         "/arr" + localPath,
		Enabled:         true,
		DetectionMethod: "ffprobe",
		DetectionMode:   "quick",
		MaxRetries:      3,
	}
}

func TestScanPathRepository_Create_andGetByID(t *testing.T) {
	t.Parallel()
	repo := NewScanPathRepository(newScanPathTestDB(t))
	ctx := context.Background()

	f := defaultFields("/movies")
	f.ArrInstanceID = sql.NullInt64{Int64: 7, Valid: true}
	f.DetectionArgsJSON = `["-v","quiet"]`
	f.VerificationTimeoutHours = sql.NullInt64{Int64: 24, Valid: true}

	id, err := repo.Create(ctx, f)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == 0 {
		t.Error("Create returned id=0")
	}

	got, err := repo.GetByID(ctx, id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.LocalPath != "/movies" || got.ArrPath != "/arr/movies" || !got.Enabled {
		t.Errorf("unexpected row: %+v", got)
	}
	if !got.ArrInstanceID.Valid || got.ArrInstanceID.Int64 != 7 {
		t.Errorf("ArrInstanceID = %+v, want valid 7", got.ArrInstanceID)
	}
	if got.DetectionArgs.String != `["-v","quiet"]` {
		t.Errorf("DetectionArgs = %q, want JSON array", got.DetectionArgs.String)
	}
	if !got.VerificationTimeoutHours.Valid || got.VerificationTimeoutHours.Int64 != 24 {
		t.Errorf("VerificationTimeoutHours = %+v, want valid 24", got.VerificationTimeoutHours)
	}
}

func TestScanPathRepository_Create_emptyDetectionArgsStoresNull(t *testing.T) {
	t.Parallel()
	repo := NewScanPathRepository(newScanPathTestDB(t))
	id, err := repo.Create(context.Background(), defaultFields("/p"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), id)
	if got.DetectionArgs.Valid {
		t.Errorf("expected DetectionArgs NULL, got %q", got.DetectionArgs.String)
	}
}

func TestScanPathRepository_GetByID_notFound(t *testing.T) {
	t.Parallel()
	repo := NewScanPathRepository(newScanPathTestDB(t))
	if _, err := repo.GetByID(context.Background(), 99); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID = %v, want ErrNotFound", err)
	}
}

func TestScanPathRepository_FindIDByLocalPath(t *testing.T) {
	t.Parallel()
	repo := NewScanPathRepository(newScanPathTestDB(t))
	ctx := context.Background()
	id, _ := repo.Create(ctx, defaultFields("/movies"))

	got, err := repo.FindIDByLocalPath(ctx, "/movies")
	if err != nil || got != id {
		t.Errorf("FindIDByLocalPath = (%d, %v), want (%d, nil)", got, err, id)
	}

	if _, err := repo.FindIDByLocalPath(ctx, "/nowhere"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing = %v, want ErrNotFound", err)
	}
}

func TestScanPathRepository_FindEnabledIDByLocalPath_skipsDisabled(t *testing.T) {
	t.Parallel()
	repo := NewScanPathRepository(newScanPathTestDB(t))
	ctx := context.Background()
	f := defaultFields("/movies")
	f.Enabled = false
	_, _ = repo.Create(ctx, f)

	if _, err := repo.FindEnabledIDByLocalPath(ctx, "/movies"); !errors.Is(err, ErrNotFound) {
		t.Errorf("disabled-row lookup = %v, want ErrNotFound", err)
	}
}

func TestScanPathRepository_ListAll_andListEnabled(t *testing.T) {
	t.Parallel()
	repo := NewScanPathRepository(newScanPathTestDB(t))
	ctx := context.Background()

	f1 := defaultFields("/a")
	f2 := defaultFields("/b")
	f2.Enabled = false
	f3 := defaultFields("/c")
	for _, f := range []ScanPathFields{f1, f2, f3} {
		if _, err := repo.Create(ctx, f); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	all, err := repo.ListAll(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListAll = (%d, %v), want (3, nil)", len(all), err)
	}

	enabled, err := repo.ListEnabled(ctx)
	if err != nil || len(enabled) != 2 {
		t.Fatalf("ListEnabled = (%d, %v), want (2, nil)", len(enabled), err)
	}
	for _, p := range enabled {
		if !p.Enabled {
			t.Errorf("ListEnabled returned disabled row: %+v", p)
		}
	}
}

func TestScanPathRepository_ListOrderedByLocalPath(t *testing.T) {
	t.Parallel()
	repo := NewScanPathRepository(newScanPathTestDB(t))
	ctx := context.Background()
	// Insert in non-alphabetical order.
	for _, path := range []string{"/zulu", "/alpha", "/mike"} {
		if _, err := repo.Create(ctx, defaultFields(path)); err != nil {
			t.Fatalf("Create %s: %v", path, err)
		}
	}

	ordered, err := repo.ListOrderedByLocalPath(ctx)
	if err != nil {
		t.Fatalf("ListOrderedByLocalPath: %v", err)
	}
	if len(ordered) != 3 || ordered[0].LocalPath != "/alpha" ||
		ordered[1].LocalPath != "/mike" || ordered[2].LocalPath != "/zulu" {
		t.Errorf("unexpected order: %+v", ordered)
	}
}

func TestScanPathRepository_Update(t *testing.T) {
	t.Parallel()
	repo := NewScanPathRepository(newScanPathTestDB(t))
	ctx := context.Background()
	id, _ := repo.Create(ctx, defaultFields("/old"))

	updated := defaultFields("/new")
	updated.Enabled = false
	updated.MaxRetries = 7
	if err := repo.Update(ctx, id, updated); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := repo.GetByID(ctx, id)
	if got.LocalPath != "/new" || got.Enabled || got.MaxRetries != 7 {
		t.Errorf("Update did not apply: %+v", got)
	}
}

func TestScanPathRepository_Delete(t *testing.T) {
	t.Parallel()
	repo := NewScanPathRepository(newScanPathTestDB(t))
	ctx := context.Background()
	id, _ := repo.Create(ctx, defaultFields("/x"))

	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("After Delete: GetByID = %v, want ErrNotFound", err)
	}

	if err := repo.Delete(ctx, 9999); err != nil {
		t.Errorf("Delete missing id = %v, want nil (idempotent)", err)
	}
}

func TestScanPathRepository_Count(t *testing.T) {
	t.Parallel()
	repo := NewScanPathRepository(newScanPathTestDB(t))
	ctx := context.Background()

	if n, _ := repo.Count(ctx); n != 0 {
		t.Errorf("Count empty = %d, want 0", n)
	}
	_, _ = repo.Create(ctx, defaultFields("/a"))
	_, _ = repo.Create(ctx, defaultFields("/b"))
	if n, _ := repo.Count(ctx); n != 2 {
		t.Errorf("Count = %d, want 2", n)
	}
}
