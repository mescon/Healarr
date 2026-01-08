package services

import (
	"database/sql"
	"testing"

	_ "modernc.org/sqlite" // Register pure-Go SQLite driver for database/sql

	"github.com/mescon/Healarr/internal/testutil"
)

// =============================================================================
// NewSchedulerService tests
// =============================================================================

func TestNewSchedulerService(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1,
			FOREIGN KEY (scan_path_id) REFERENCES scan_paths(id)
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	s := NewSchedulerService(db, nil)

	if s == nil {
		t.Fatal("NewSchedulerService should not return nil")
	}

	if s.db != db {
		t.Error("db not set correctly")
	}
	if s.cron == nil {
		t.Error("cron should be initialized")
	}
	if s.jobs == nil {
		t.Error("jobs map should be initialized")
	}
}

// =============================================================================
// LoadSchedules tests
// =============================================================================

func TestSchedulerService_LoadSchedules_EmptyDB(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	s := NewSchedulerService(db, nil)

	err = s.LoadSchedules()
	if err != nil {
		t.Errorf("LoadSchedules() error = %v", err)
	}

	if len(s.jobs) != 0 {
		t.Errorf("Expected 0 jobs, got %d", len(s.jobs))
	}
}

func TestSchedulerService_LoadSchedules_DisabledSchedules(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	// Add a disabled schedule
	_, err = db.Exec("INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled) VALUES (1, '0 * * * *', 0)")
	if err != nil {
		t.Fatalf("Failed to insert schedule: %v", err)
	}

	s := NewSchedulerService(db, nil)

	err = s.LoadSchedules()
	if err != nil {
		t.Errorf("LoadSchedules() error = %v", err)
	}

	// Disabled schedules should not be loaded
	if len(s.jobs) != 0 {
		t.Errorf("Expected 0 jobs (disabled), got %d", len(s.jobs))
	}
}

func TestSchedulerService_LoadSchedules_WithValidSchedule(t *testing.T) {
	// Use file-based temp db with shared cache to avoid SQLite in-memory isolation issues
	// when LoadSchedules does nested queries (Query + QueryRow in addJob)
	tmpFile := t.TempDir() + "/test.db"
	db, err := sql.Open("sqlite", tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Create required tables
	_, err = db.Exec(`
		CREATE TABLE scan_paths (
			id INTEGER PRIMARY KEY,
			local_path TEXT NOT NULL,
			arr_path TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		);
		CREATE TABLE scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create tables: %v", err)
	}

	// Add a scan path for the schedule to reference
	_, err = db.Exec(`
		INSERT INTO scan_paths (id, local_path, arr_path, enabled)
		VALUES (1, '/media/tv', '/data/tv', 1)
	`)
	if err != nil {
		t.Fatalf("Failed to insert scan path: %v", err)
	}

	// Add an enabled schedule with valid cron
	_, err = db.Exec("INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled) VALUES (1, '0 * * * *', 1)")
	if err != nil {
		t.Fatalf("Failed to insert schedule: %v", err)
	}

	s := NewSchedulerService(db, nil)

	err = s.LoadSchedules()
	if err != nil {
		t.Errorf("LoadSchedules() error = %v", err)
	}

	// Should have loaded the schedule
	if len(s.jobs) != 1 {
		t.Errorf("Expected 1 job, got %d", len(s.jobs))
	}
}

// =============================================================================
// AddSchedule tests
// =============================================================================

func TestSchedulerService_AddSchedule_InvalidCron(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	s := NewSchedulerService(db, nil)

	_, err = s.AddSchedule(1, "invalid cron")
	if err == nil {
		t.Error("AddSchedule should fail for invalid cron expression")
	}
}

func TestSchedulerService_AddSchedule_ValidCron(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	// Add a scan path
	err = testutil.SeedScanPath(db, 1, "/media/tv", "/data/tv", false, false)
	if err != nil {
		t.Fatalf("Failed to seed scan path: %v", err)
	}

	s := NewSchedulerService(db, nil)

	id, err := s.AddSchedule(1, "0 0 * * *") // Daily at midnight
	if err != nil {
		t.Errorf("AddSchedule() error = %v", err)
	}

	if id <= 0 {
		t.Error("AddSchedule should return positive ID")
	}

	// Verify it was saved to DB
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM scan_schedules WHERE id = ?", id).Scan(&count)
	if err != nil || count != 1 {
		t.Error("Schedule should be saved to database")
	}

	// Verify job was added
	if len(s.jobs) != 1 {
		t.Errorf("Expected 1 job, got %d", len(s.jobs))
	}
}

func TestSchedulerService_AddSchedule_NonExistentPath(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	s := NewSchedulerService(db, nil)

	// Try to add schedule for non-existent path
	id, err := s.AddSchedule(999, "0 0 * * *")

	// Should succeed in saving to DB but fail in addJob
	// The returned id is valid, but error indicates scheduling failed
	if id > 0 && err != nil {
		// Expected: saved to DB but failed to schedule
		t.Logf("Expected behavior: saved to DB (id=%d) but scheduling failed: %v", id, err)
	}
}

// =============================================================================
// DeleteSchedule tests
// =============================================================================

func TestSchedulerService_DeleteSchedule(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	// Add a scan path and schedule
	err = testutil.SeedScanPath(db, 1, "/media/tv", "/data/tv", false, false)
	if err != nil {
		t.Fatalf("Failed to seed scan path: %v", err)
	}

	s := NewSchedulerService(db, nil)

	id, err := s.AddSchedule(1, "0 0 * * *")
	if err != nil {
		t.Fatalf("AddSchedule() error = %v", err)
	}

	// Delete the schedule
	err = s.DeleteSchedule(int(id))
	if err != nil {
		t.Errorf("DeleteSchedule() error = %v", err)
	}

	// Verify deleted from DB
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM scan_schedules WHERE id = ?", id).Scan(&count)
	if err != nil || count != 0 {
		t.Error("Schedule should be deleted from database")
	}

	// Verify job was removed
	if len(s.jobs) != 0 {
		t.Errorf("Expected 0 jobs after delete, got %d", len(s.jobs))
	}
}

func TestSchedulerService_DeleteSchedule_NotFound(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	s := NewSchedulerService(db, nil)

	// Delete non-existent schedule - should not error
	err = s.DeleteSchedule(999)
	if err != nil {
		t.Errorf("DeleteSchedule() for non-existent ID error = %v", err)
	}
}

// =============================================================================
// UpdateSchedule tests
// =============================================================================

func TestSchedulerService_UpdateSchedule_InvalidCron(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	s := NewSchedulerService(db, nil)

	err = s.UpdateSchedule(1, "invalid cron", true)
	if err == nil {
		t.Error("UpdateSchedule should fail for invalid cron expression")
	}
}

func TestSchedulerService_UpdateSchedule_DisableSchedule(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	// Add a scan path and schedule
	err = testutil.SeedScanPath(db, 1, "/media/tv", "/data/tv", false, false)
	if err != nil {
		t.Fatalf("Failed to seed scan path: %v", err)
	}

	s := NewSchedulerService(db, nil)

	id, err := s.AddSchedule(1, "0 0 * * *")
	if err != nil {
		t.Fatalf("AddSchedule() error = %v", err)
	}

	initialJobs := len(s.jobs)
	if initialJobs != 1 {
		t.Fatalf("Expected 1 job initially, got %d", initialJobs)
	}

	// Disable the schedule
	err = s.UpdateSchedule(int(id), "", false)
	if err != nil {
		t.Errorf("UpdateSchedule() error = %v", err)
	}

	// Job should be removed
	if len(s.jobs) != 0 {
		t.Errorf("Expected 0 jobs after disable, got %d", len(s.jobs))
	}
}

func TestSchedulerService_UpdateSchedule_ChangeCron(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	// Add a scan path and schedule
	err = testutil.SeedScanPath(db, 1, "/media/tv", "/data/tv", false, false)
	if err != nil {
		t.Fatalf("Failed to seed scan path: %v", err)
	}

	s := NewSchedulerService(db, nil)

	id, err := s.AddSchedule(1, "0 0 * * *")
	if err != nil {
		t.Fatalf("AddSchedule() error = %v", err)
	}

	// Change cron expression
	err = s.UpdateSchedule(int(id), "0 */2 * * *", true)
	if err != nil {
		t.Errorf("UpdateSchedule() error = %v", err)
	}

	// Verify cron was updated in DB
	var cronExpr string
	err = db.QueryRow("SELECT cron_expression FROM scan_schedules WHERE id = ?", id).Scan(&cronExpr)
	if err != nil {
		t.Fatalf("Failed to query schedule: %v", err)
	}
	if cronExpr != "0 */2 * * *" {
		t.Errorf("Cron expression not updated, got %s", cronExpr)
	}
}

// =============================================================================
// Start/Stop tests
// =============================================================================

func TestSchedulerService_StartStop(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	s := NewSchedulerService(db, nil)

	// Should not panic
	s.Start()
	s.Stop()
}

// =============================================================================
// Cron expression validation tests
// =============================================================================

func TestSchedulerService_CronExpressionValidation(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	// Add a scan path
	err = testutil.SeedScanPath(db, 1, "/media/tv", "/data/tv", false, false)
	if err != nil {
		t.Fatalf("Failed to seed scan path: %v", err)
	}

	s := NewSchedulerService(db, nil)

	tests := []struct {
		name    string
		cron    string
		wantErr bool
	}{
		{"standard five-field", "0 0 * * *", false},   // Daily at midnight
		{"every hour", "0 * * * *", false},            // Every hour
		{"every minute", "* * * * *", false},          // Every minute
		{"specific time", "30 14 * * *", false},       // 2:30 PM daily
		{"weekdays only", "0 9 * * 1-5", false},       // 9 AM Mon-Fri
		{"with ranges", "0 0-6 * * *", false},         // Every hour 0-6
		{"with step", "*/15 * * * *", false},          // Every 15 minutes
		{"invalid - empty", "", true},                 // Empty
		{"invalid - bad format", "invalid", true},     // Garbage
		{"invalid - six fields", "0 0 0 * * *", true}, // Six fields (uses standard, not extended)
		{"invalid - bad range", "0 0 32 * *", true},   // Day 32
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := s.AddSchedule(1, tt.cron)
			if (err != nil) != tt.wantErr {
				t.Errorf("AddSchedule(%q) error = %v, wantErr %v", tt.cron, err, tt.wantErr)
			}
		})
	}
}

// =============================================================================
// CleanupOrphanedSchedules tests
// =============================================================================

func setupSchedulerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}

	// Add scan_schedules table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL,
			cron_expression TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1,
			FOREIGN KEY (scan_path_id) REFERENCES scan_paths(id)
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan_schedules table: %v", err)
	}

	return db
}

func TestSchedulerService_CleanupOrphanedSchedules(t *testing.T) {
	db := setupSchedulerTestDB(t)
	defer db.Close()

	s := NewSchedulerService(db, nil)

	// Create multiple scan paths
	_, err := db.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id) VALUES (1, '/valid/path', '/arr/path', 1)`)
	if err != nil {
		t.Fatalf("Failed to insert scan path 1: %v", err)
	}
	_, err = db.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id) VALUES (2, '/path/two', '/arr/two', 1)`)
	if err != nil {
		t.Fatalf("Failed to insert scan path 2: %v", err)
	}
	_, err = db.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id) VALUES (3, '/path/three', '/arr/three', 1)`)
	if err != nil {
		t.Fatalf("Failed to insert scan path 3: %v", err)
	}

	// Create schedules for all paths
	_, err = db.Exec(`INSERT INTO scan_schedules (id, scan_path_id, cron_expression, enabled) VALUES (1, 1, '0 * * * *', 1)`)
	if err != nil {
		t.Fatalf("Failed to insert schedule 1: %v", err)
	}
	_, err = db.Exec(`INSERT INTO scan_schedules (id, scan_path_id, cron_expression, enabled) VALUES (2, 2, '0 0 * * *', 1)`)
	if err != nil {
		t.Fatalf("Failed to insert schedule 2: %v", err)
	}
	_, err = db.Exec(`INSERT INTO scan_schedules (id, scan_path_id, cron_expression, enabled) VALUES (3, 3, '0 12 * * *', 0)`)
	if err != nil {
		t.Fatalf("Failed to insert schedule 3: %v", err)
	}

	// Disable foreign keys temporarily and delete scan paths to create orphaned schedules
	// This simulates what happens when FK constraints weren't enforced in older versions
	_, err = db.Exec("PRAGMA foreign_keys = OFF")
	if err != nil {
		t.Fatalf("Failed to disable foreign keys: %v", err)
	}

	// Delete scan paths 2 and 3, creating orphaned schedules
	_, err = db.Exec("DELETE FROM scan_paths WHERE id IN (2, 3)")
	if err != nil {
		t.Fatalf("Failed to delete scan paths: %v", err)
	}

	// Re-enable foreign keys
	_, err = db.Exec("PRAGMA foreign_keys = ON")
	if err != nil {
		t.Fatalf("Failed to re-enable foreign keys: %v", err)
	}

	// Verify we have 3 schedules
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM scan_schedules").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count schedules: %v", err)
	}
	if count != 3 {
		t.Fatalf("Expected 3 schedules, got %d", count)
	}

	// Run cleanup
	cleaned, err := s.CleanupOrphanedSchedules()
	if err != nil {
		t.Fatalf("CleanupOrphanedSchedules failed: %v", err)
	}

	if cleaned != 2 {
		t.Errorf("Expected 2 orphaned schedules cleaned up, got %d", cleaned)
	}

	// Verify only the valid schedule remains
	err = db.QueryRow("SELECT COUNT(*) FROM scan_schedules").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count schedules after cleanup: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 schedule remaining, got %d", count)
	}

	// Verify the remaining schedule is the valid one
	var remainingID int
	err = db.QueryRow("SELECT id FROM scan_schedules").Scan(&remainingID)
	if err != nil {
		t.Fatalf("Failed to get remaining schedule: %v", err)
	}
	if remainingID != 1 {
		t.Errorf("Expected remaining schedule to have id=1, got %d", remainingID)
	}
}

func TestSchedulerService_CleanupOrphanedSchedules_NoOrphans(t *testing.T) {
	db := setupSchedulerTestDB(t)
	defer db.Close()

	s := NewSchedulerService(db, nil)

	// Create a valid scan path and schedule
	_, err := db.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id) VALUES (1, '/valid/path', '/arr/path', 1)`)
	if err != nil {
		t.Fatalf("Failed to insert scan path: %v", err)
	}

	_, err = db.Exec(`INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled) VALUES (1, '0 * * * *', 1)`)
	if err != nil {
		t.Fatalf("Failed to insert schedule: %v", err)
	}

	// Run cleanup - should find nothing to clean
	cleaned, err := s.CleanupOrphanedSchedules()
	if err != nil {
		t.Fatalf("CleanupOrphanedSchedules failed: %v", err)
	}

	if cleaned != 0 {
		t.Errorf("Expected 0 orphaned schedules cleaned up, got %d", cleaned)
	}
}

func TestSchedulerService_CleanupOrphanedSchedules_EmptyDB(t *testing.T) {
	db := setupSchedulerTestDB(t)
	defer db.Close()

	s := NewSchedulerService(db, nil)

	// Run cleanup on empty database
	cleaned, err := s.CleanupOrphanedSchedules()
	if err != nil {
		t.Fatalf("CleanupOrphanedSchedules failed: %v", err)
	}

	if cleaned != 0 {
		t.Errorf("Expected 0 orphaned schedules cleaned up, got %d", cleaned)
	}
}
