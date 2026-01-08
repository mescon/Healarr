package integration

import (
	"database/sql"
	"fmt"
	"testing"

	_ "github.com/mattn/go-sqlite3" // Register CGo SQLite driver for database/sql
)

// =============================================================================
// Test helpers (avoiding testutil import due to cycle)
// =============================================================================

// newTestDBForPathMapper creates an in-memory SQLite database with schema for tests.
func newTestDBForPathMapper() (*sql.DB, error) {
	db, err := sql.Open("sqlite3", ":memory:?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		return nil, err
	}

	// Create scan_paths table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_paths (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			local_path TEXT NOT NULL,
			arr_path TEXT NOT NULL,
			auto_remediate INTEGER NOT NULL DEFAULT 0,
			dry_run INTEGER NOT NULL DEFAULT 0,
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create scan_paths table: %w", err)
	}

	return db, nil
}

// seedScanPath inserts a scan path into the test database.
func seedScanPath(db *sql.DB, id int64, localPath, arrPath string, autoRemediate, dryRun bool) error {
	_, err := db.Exec(`
		INSERT INTO scan_paths (id, local_path, arr_path, auto_remediate, dry_run, enabled)
		VALUES (?, ?, ?, ?, ?, 1)
	`, id, localPath, arrPath, autoRemediate, dryRun)
	return err
}

// =============================================================================
// NewPathMapper tests
// =============================================================================

func TestNewPathMapper(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	if pm == nil {
		t.Fatal("NewPathMapper should not return nil")
	}

	if pm.db != db {
		t.Error("db not set correctly")
	}
}

func TestNewPathMapper_WithMappings(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add some scan paths
	seedScanPath(db, 1, "/mnt/media/tv", "/data/tv", false, false)
	seedScanPath(db, 2, "/mnt/media/movies", "/data/movies", false, false)

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	if len(pm.mappings) != 2 {
		t.Errorf("Expected 2 mappings, got %d", len(pm.mappings))
	}
}

// =============================================================================
// Reload tests
// =============================================================================

func TestPathMapper_Reload(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	if len(pm.mappings) != 0 {
		t.Errorf("Expected 0 mappings initially, got %d", len(pm.mappings))
	}

	// Add a mapping
	seedScanPath(db, 1, "/mnt/media/tv", "/data/tv", false, false)

	// Reload
	err = pm.Reload()
	if err != nil {
		t.Errorf("Reload() error = %v", err)
	}

	if len(pm.mappings) != 1 {
		t.Errorf("Expected 1 mapping after reload, got %d", len(pm.mappings))
	}
}

func TestPathMapper_Reload_DisabledPaths(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add an enabled path
	seedScanPath(db, 1, "/mnt/media/tv", "/data/tv", false, false)

	// Add a disabled path
	_, err = db.Exec("INSERT INTO scan_paths (id, local_path, arr_path, enabled) VALUES (2, '/mnt/media/movies', '/data/movies', 0)")
	if err != nil {
		t.Fatalf("Failed to insert disabled path: %v", err)
	}

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	// Should only have the enabled path
	if len(pm.mappings) != 1 {
		t.Errorf("Expected 1 mapping (disabled excluded), got %d", len(pm.mappings))
	}
}

func TestPathMapper_Reload_TrimsSlashes(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add paths with trailing slashes
	_, err = db.Exec("INSERT INTO scan_paths (id, local_path, arr_path, enabled) VALUES (1, '/mnt/media/tv/', '/data/tv/', 1)")
	if err != nil {
		t.Fatalf("Failed to insert path: %v", err)
	}

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	if len(pm.mappings) != 1 {
		t.Fatalf("Expected 1 mapping, got %d", len(pm.mappings))
	}

	// Trailing slashes should be removed
	if pm.mappings[0].LocalPath != "/mnt/media/tv" {
		t.Errorf("LocalPath = %s, want /mnt/media/tv (without trailing slash)", pm.mappings[0].LocalPath)
	}
	if pm.mappings[0].ArrPath != "/data/tv" {
		t.Errorf("ArrPath = %s, want /data/tv (without trailing slash)", pm.mappings[0].ArrPath)
	}
}

// =============================================================================
// ToArrPath tests
// =============================================================================

func TestPathMapper_ToArrPath_BasicMapping(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	seedScanPath(db, 1, "/mnt/media/tv", "/data/tv", false, false)

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	tests := []struct {
		name      string
		localPath string
		want      string
	}{
		{"exact match", "/mnt/media/tv", "/data/tv"},
		{"with subpath", "/mnt/media/tv/Show/Season 1/episode.mkv", "/data/tv/Show/Season 1/episode.mkv"},
		{"deep nested", "/mnt/media/tv/a/b/c/d/e.mkv", "/data/tv/a/b/c/d/e.mkv"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pm.ToArrPath(tt.localPath)
			if err != nil {
				t.Errorf("ToArrPath() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ToArrPath(%q) = %q, want %q", tt.localPath, got, tt.want)
			}
		})
	}
}

func TestPathMapper_ToArrPath_NoMapping(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	seedScanPath(db, 1, "/mnt/media/tv", "/data/tv", false, false)

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	_, err = pm.ToArrPath("/completely/different/path")
	if err == nil {
		t.Error("ToArrPath should return error for unmapped path")
	}
}

func TestPathMapper_ToArrPath_LongestPrefixMatching(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add two overlapping mappings - longer prefix should win
	seedScanPath(db, 1, "/mnt/media", "/data", false, false)
	_, err = db.Exec("INSERT INTO scan_paths (id, local_path, arr_path, enabled) VALUES (2, '/mnt/media/tv', '/data/television', 1)")
	if err != nil {
		t.Fatalf("Failed to insert path: %v", err)
	}

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	// This should match /mnt/media/tv (longer prefix), not /mnt/media
	got, err := pm.ToArrPath("/mnt/media/tv/Show/episode.mkv")
	if err != nil {
		t.Fatalf("ToArrPath() error = %v", err)
	}

	if got != "/data/television/Show/episode.mkv" {
		t.Errorf("ToArrPath() = %q, want /data/television/Show/episode.mkv (longest prefix)", got)
	}
}

func TestPathMapper_ToArrPath_PrefixBoundary(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	seedScanPath(db, 1, "/mnt/media/TV", "/data/TV", false, false)

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	// /mnt/media/TV2 should NOT match /mnt/media/TV (different directory)
	_, err = pm.ToArrPath("/mnt/media/TV2/Show/episode.mkv")
	if err == nil {
		t.Error("ToArrPath should not match /mnt/media/TV for path /mnt/media/TV2 (prefix boundary)")
	}
}

// =============================================================================
// ToLocalPath tests
// =============================================================================

func TestPathMapper_ToLocalPath_BasicMapping(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	seedScanPath(db, 1, "/mnt/media/tv", "/data/tv", false, false)

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	tests := []struct {
		name    string
		arrPath string
		want    string
	}{
		{"exact match", "/data/tv", "/mnt/media/tv"},
		{"with subpath", "/data/tv/Show/Season 1/episode.mkv", "/mnt/media/tv/Show/Season 1/episode.mkv"},
		{"deep nested", "/data/tv/a/b/c/d/e.mkv", "/mnt/media/tv/a/b/c/d/e.mkv"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pm.ToLocalPath(tt.arrPath)
			if err != nil {
				t.Errorf("ToLocalPath() error = %v", err)
			}
			if got != tt.want {
				t.Errorf("ToLocalPath(%q) = %q, want %q", tt.arrPath, got, tt.want)
			}
		})
	}
}

func TestPathMapper_ToLocalPath_NoMapping(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	seedScanPath(db, 1, "/mnt/media/tv", "/data/tv", false, false)

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	_, err = pm.ToLocalPath("/completely/different/path")
	if err == nil {
		t.Error("ToLocalPath should return error for unmapped path")
	}
}

func TestPathMapper_ToLocalPath_LongestPrefixMatching(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add two overlapping mappings
	seedScanPath(db, 1, "/mnt", "/data", false, false)
	_, err = db.Exec("INSERT INTO scan_paths (id, local_path, arr_path, enabled) VALUES (2, '/mnt/media/tv', '/data/media/tv', 1)")
	if err != nil {
		t.Fatalf("Failed to insert path: %v", err)
	}

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	// This should match /data/media/tv (longer arr prefix)
	got, err := pm.ToLocalPath("/data/media/tv/Show/episode.mkv")
	if err != nil {
		t.Fatalf("ToLocalPath() error = %v", err)
	}

	if got != "/mnt/media/tv/Show/episode.mkv" {
		t.Errorf("ToLocalPath() = %q, want /mnt/media/tv/Show/episode.mkv", got)
	}
}

func TestPathMapper_ToLocalPath_PrefixBoundary(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	seedScanPath(db, 1, "/mnt/media/TV", "/data/movies", false, false)

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	// /data/movies-archive should NOT match /data/movies (different directory)
	_, err = pm.ToLocalPath("/data/movies-archive/film.mkv")
	if err == nil {
		t.Error("ToLocalPath should not match /data/movies for path /data/movies-archive (prefix boundary)")
	}
}

// =============================================================================
// Roundtrip tests
// =============================================================================

func TestPathMapper_Roundtrip(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	seedScanPath(db, 1, "/mnt/media/tv", "/data/tv", false, false)
	seedScanPath(db, 2, "/mnt/media/movies", "/data/movies", false, false)

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	localPaths := []string{
		"/mnt/media/tv/Show/episode.mkv",
		"/mnt/media/movies/Film (2024)/film.mkv",
	}

	for _, localPath := range localPaths {
		// Local -> Arr
		arrPath, err := pm.ToArrPath(localPath)
		if err != nil {
			t.Errorf("ToArrPath(%q) error = %v", localPath, err)
			continue
		}

		// Arr -> Local (should get back original)
		gotLocal, err := pm.ToLocalPath(arrPath)
		if err != nil {
			t.Errorf("ToLocalPath(%q) error = %v", arrPath, err)
			continue
		}

		if gotLocal != localPath {
			t.Errorf("Roundtrip failed: %q -> %q -> %q", localPath, arrPath, gotLocal)
		}
	}
}

// =============================================================================
// Concurrent access tests
// =============================================================================

func TestPathMapper_ConcurrentAccess(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	seedScanPath(db, 1, "/mnt/media/tv", "/data/tv", false, false)

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	done := make(chan bool, 100)

	// Concurrent reads
	for i := 0; i < 50; i++ {
		go func() {
			_, _ = pm.ToArrPath("/mnt/media/tv/show.mkv")
			done <- true
		}()
	}

	// Concurrent reads in the other direction
	for i := 0; i < 50; i++ {
		go func() {
			_, _ = pm.ToLocalPath("/data/tv/show.mkv")
			done <- true
		}()
	}

	// Wait for all
	for i := 0; i < 100; i++ {
		<-done
	}
}

func TestPathMapper_ConcurrentReload(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	seedScanPath(db, 1, "/mnt/media/tv", "/data/tv", false, false)

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	done := make(chan bool, 30)

	// Mix of reloads and reads
	for i := 0; i < 10; i++ {
		go func() {
			_ = pm.Reload()
			done <- true
		}()
	}

	for i := 0; i < 20; i++ {
		go func() {
			_, _ = pm.ToArrPath("/mnt/media/tv/show.mkv")
			done <- true
		}()
	}

	// Wait for all
	for i := 0; i < 30; i++ {
		<-done
	}
}

// =============================================================================
// Empty database tests
// =============================================================================

func TestPathMapper_EmptyMappings(t *testing.T) {
	db, err := newTestDBForPathMapper()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	pm, err := NewPathMapper(db)
	if err != nil {
		t.Fatalf("NewPathMapper() error = %v", err)
	}

	if len(pm.mappings) != 0 {
		t.Errorf("Expected 0 mappings with empty db, got %d", len(pm.mappings))
	}

	_, err = pm.ToArrPath("/any/path")
	if err == nil {
		t.Error("ToArrPath should error with no mappings")
	}

	_, err = pm.ToLocalPath("/any/path")
	if err == nil {
		t.Error("ToLocalPath should error with no mappings")
	}
}
