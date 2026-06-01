package repository

import (
	"context"
	"database/sql"
	"testing"

	_ "modernc.org/sqlite"
)

// newScanPresetTestDB returns an in-memory SQLite seeded with the
// scan_presets schema and the same five built-in rows migration 009 ships
// with. Test cases use this to exercise the repo without invoking the
// full migration runner.
func newScanPresetTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	schema := `
		CREATE TABLE scan_presets (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			detection_method TEXT NOT NULL DEFAULT 'ffprobe',
			detection_mode TEXT NOT NULL DEFAULT 'quick',
			detection_args TEXT,
			thorough_duration_seconds INTEGER,
			thorough_timeout_seconds INTEGER,
			hwaccel TEXT,
			is_builtin INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO scan_presets (name, detection_method, detection_mode, is_builtin) VALUES
			('Zero-byte only', 'zero_byte', 'quick', 1),
			('Quick',          'ffprobe',   'quick', 1),
			('Fast triage',    'ffprobe',   'thorough', 1),
			('Deep scan',      'ffprobe',   'thorough', 1),
			('Paranoid',       'handbrake', 'thorough', 1);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("seed schema: %v", err)
	}
	return db
}

func TestScanPresetRepository_ListAll_BuiltinsFirst(t *testing.T) {
	db := newScanPresetTestDB(t)
	repo := NewScanPresetRepository(db)

	// Add a custom preset that, alphabetically, sorts before "Zero-byte only".
	if _, err := repo.Create(context.Background(), ScanPresetFields{
		Name:            "AAA Custom",
		Description:     "x",
		DetectionMethod: "ffprobe",
		DetectionMode:   "quick",
	}); err != nil {
		t.Fatalf("create custom: %v", err)
	}

	got, err := repo.ListAll(context.Background())
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Built-ins (5) come first in migration order, custom rows after.
	if len(got) != 6 {
		t.Fatalf("len = %d, want 6", len(got))
	}
	builtinNames := []string{"Zero-byte only", "Quick", "Fast triage", "Deep scan", "Paranoid"}
	for i, name := range builtinNames {
		if got[i].Name != name {
			t.Errorf("position %d: name = %q, want %q", i, got[i].Name, name)
		}
		if !got[i].IsBuiltin {
			t.Errorf("position %d (%s): IsBuiltin = false, want true", i, name)
		}
	}
	if got[5].Name != "AAA Custom" || got[5].IsBuiltin {
		t.Errorf("custom row: got %+v, want {Name:AAA Custom, IsBuiltin:false}", got[5])
	}
}

func TestScanPresetRepository_Create_AlwaysSetsBuiltinZero(t *testing.T) {
	db := newScanPresetTestDB(t)
	repo := NewScanPresetRepository(db)

	id, err := repo.Create(context.Background(), ScanPresetFields{
		Name:                    "Test Preset",
		Description:             "test",
		DetectionMethod:         "ffprobe",
		DetectionMode:           "thorough",
		ThoroughDurationSeconds: sql.NullInt64{Int64: 60, Valid: true},
		Hwaccel:                 sql.NullString{String: "cuda", Valid: true},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	got, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.IsBuiltin {
		t.Error("custom preset created with is_builtin=true; Create() must always insert is_builtin=0")
	}
	if got.ThoroughDurationSeconds.Int64 != 60 {
		t.Errorf("duration = %d, want 60", got.ThoroughDurationSeconds.Int64)
	}
	if got.Hwaccel.String != "cuda" {
		t.Errorf("hwaccel = %q, want cuda", got.Hwaccel.String)
	}
}

func TestScanPresetRepository_UpdateAndDelete_RoundTrip(t *testing.T) {
	db := newScanPresetTestDB(t)
	repo := NewScanPresetRepository(db)

	id, err := repo.Create(context.Background(), ScanPresetFields{
		Name:            "Updatable",
		DetectionMethod: "ffprobe",
		DetectionMode:   "quick",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if err := repo.Update(context.Background(), id, ScanPresetFields{
		Name:                   "Updatable",
		Description:            "now-updated",
		DetectionMethod:        "ffprobe",
		DetectionMode:          "thorough",
		ThoroughTimeoutSeconds: sql.NullInt64{Int64: 600, Valid: true},
	}); err != nil {
		t.Fatalf("update: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), id)
	if got.Description != "now-updated" || got.DetectionMode != "thorough" || got.ThoroughTimeoutSeconds.Int64 != 600 {
		t.Errorf("after update: got %+v", got)
	}

	if err := repo.Delete(context.Background(), id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.GetByID(context.Background(), id); err == nil {
		t.Error("GetByID after Delete: want ErrNotFound, got nil")
	}
}

func TestScanPresetRepository_UniqueName(t *testing.T) {
	db := newScanPresetTestDB(t)
	repo := NewScanPresetRepository(db)

	_, err := repo.Create(context.Background(), ScanPresetFields{
		Name:            "Quick", // already taken by built-in
		DetectionMethod: "ffprobe",
		DetectionMode:   "quick",
	})
	if err == nil {
		t.Error("Create with duplicate name: want UNIQUE-constraint error, got nil")
	}
}
