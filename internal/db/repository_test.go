package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestDB creates a temporary database for testing
func setupTestDB(t *testing.T) (*Repository, func()) {
	t.Helper()

	// Create temp directory
	tmpDir, err := os.MkdirTemp("", "healarr-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to create repository: %v", err)
	}

	cleanup := func() {
		repo.Close()
		os.RemoveAll(tmpDir)
	}

	return repo, cleanup
}

func TestNewRepository(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	if repo == nil {
		t.Fatal("Repository should not be nil")
	}

	if repo.DB == nil {
		t.Fatal("Repository.DB should not be nil")
	}
}

func TestRepository_Ping(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	err := repo.DB.Ping()
	if err != nil {
		t.Errorf("Ping failed: %v", err)
	}
}

func TestRepository_WALMode(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	var journalMode string
	err := repo.DB.QueryRow("PRAGMA journal_mode").Scan(&journalMode)
	if err != nil {
		t.Fatalf("Failed to query journal mode: %v", err)
	}

	if journalMode != "wal" {
		t.Errorf("Expected WAL mode, got %s", journalMode)
	}
}

func TestRepository_ForeignKeysEnabled(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	var foreignKeys int
	err := repo.DB.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys)
	if err != nil {
		t.Fatalf("Failed to query foreign_keys: %v", err)
	}

	if foreignKeys != 1 {
		t.Errorf("Expected foreign_keys=1, got %d", foreignKeys)
	}
}

func TestRepository_TablesCreated(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	expectedTables := []string{
		"events",
		"scans",
		"scan_files",
		"scan_paths",
		"arr_instances",
		"settings",
		"notifications",
		"notification_log",
		"scan_schedules",
		"pending_rescans",
		"schema_migrations",
	}

	for _, table := range expectedTables {
		var name string
		err := repo.DB.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='table' AND name=?",
			table,
		).Scan(&name)

		if err == sql.ErrNoRows {
			t.Errorf("Table %s not found", table)
		} else if err != nil {
			t.Errorf("Error checking table %s: %v", table, err)
		}
	}
}

func TestRepository_ViewsCreated(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	expectedViews := []string{
		"corruption_status",
		"dashboard_stats",
	}

	for _, view := range expectedViews {
		var name string
		err := repo.DB.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='view' AND name=?",
			view,
		).Scan(&name)

		if err == sql.ErrNoRows {
			t.Errorf("View %s not found", view)
		} else if err != nil {
			t.Errorf("Error checking view %s: %v", view, err)
		}
	}
}

func TestRepository_IndexesCreated(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	expectedIndexes := []string{
		"idx_aggregate",
		"idx_event_type",
		"idx_created_at",
		"idx_events_aggregate_event",
		"idx_scans_status",
		"idx_scan_files_scan_id",
		"idx_scan_files_status",
	}

	for _, index := range expectedIndexes {
		var name string
		err := repo.DB.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='index' AND name=?",
			index,
		).Scan(&name)

		if err == sql.ErrNoRows {
			t.Errorf("Index %s not found", index)
		} else if err != nil {
			t.Errorf("Error checking index %s: %v", index, err)
		}
	}
}

func TestRepository_InsertAndQueryEvent(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert an event
	result, err := repo.DB.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
		VALUES (?, ?, ?, ?, ?)
	`, "corruption", "test-123", "CorruptionDetected", `{"file_path":"/test.mkv"}`, 1)

	if err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("Failed to get last insert ID: %v", err)
	}

	if id <= 0 {
		t.Error("Expected positive ID")
	}

	// Query it back
	var aggregateID, eventType string
	err = repo.DB.QueryRow(
		"SELECT aggregate_id, event_type FROM events WHERE id = ?",
		id,
	).Scan(&aggregateID, &eventType)

	if err != nil {
		t.Fatalf("Failed to query event: %v", err)
	}

	if aggregateID != "test-123" {
		t.Errorf("Expected aggregate_id 'test-123', got '%s'", aggregateID)
	}

	if eventType != "CorruptionDetected" {
		t.Errorf("Expected event_type 'CorruptionDetected', got '%s'", eventType)
	}
}

func TestRepository_InsertScanPath(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// First insert an arr_instance
	_, err := repo.DB.Exec(`
		INSERT INTO arr_instances (name, type, url, api_key, enabled)
		VALUES (?, ?, ?, ?, ?)
	`, "Test Sonarr", "sonarr", "http://localhost:8989", "test-api-key", 1)

	if err != nil {
		t.Fatalf("Failed to insert arr_instance: %v", err)
	}

	// Now insert a scan_path
	result, err := repo.DB.Exec(`
		INSERT INTO scan_paths (local_path, arr_path, arr_instance_id, enabled, auto_remediate)
		VALUES (?, ?, ?, ?, ?)
	`, "/media/tv", "/tv", 1, 1, 0)

	if err != nil {
		t.Fatalf("Failed to insert scan_path: %v", err)
	}

	id, _ := result.LastInsertId()
	if id <= 0 {
		t.Error("Expected positive ID for scan_path")
	}
}

func TestRepository_RunMaintenance(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert some old events
	oldTime := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	for i := 0; i < 5; i++ {
		_, err := repo.DB.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, "test", "old-event", "TestEvent", "{}", 1, oldTime)
		if err != nil {
			t.Fatalf("Failed to insert old event: %v", err)
		}
	}

	// Insert some recent events
	for i := 0; i < 3; i++ {
		_, err := repo.DB.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
			VALUES (?, ?, ?, ?, ?)
		`, "test", "new-event", "TestEvent", "{}", 1)
		if err != nil {
			t.Fatalf("Failed to insert new event: %v", err)
		}
	}

	// Run maintenance with 90-day retention
	err := repo.RunMaintenance(90)
	if err != nil {
		t.Errorf("RunMaintenance failed: %v", err)
	}

	// Check that old events were pruned
	var count int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = 'old-event'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count old events: %v", err)
	}

	if count != 0 {
		t.Errorf("Expected 0 old events after maintenance, got %d", count)
	}

	// Check that new events remain
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = 'new-event'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count new events: %v", err)
	}

	if count != 3 {
		t.Errorf("Expected 3 new events after maintenance, got %d", count)
	}
}

func TestRepository_GetDatabaseStats(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	stats, err := repo.GetDatabaseStats()
	if err != nil {
		t.Fatalf("GetDatabaseStats failed: %v", err)
	}

	// Check required fields
	if _, ok := stats["size_bytes"]; !ok {
		t.Error("Missing size_bytes in stats")
	}

	if _, ok := stats["page_count"]; !ok {
		t.Error("Missing page_count in stats")
	}

	if _, ok := stats["journal_mode"]; !ok {
		t.Error("Missing journal_mode in stats")
	}

	if stats["journal_mode"] != "wal" {
		t.Errorf("Expected journal_mode 'wal', got '%v'", stats["journal_mode"])
	}
}

func TestRepository_CheckIntegrity(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	err := repo.checkIntegrity()
	if err != nil {
		t.Errorf("checkIntegrity failed on fresh database: %v", err)
	}
}

func TestRepository_ConcurrentAccess(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Test concurrent inserts
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			_, err := repo.DB.Exec(`
				INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
				VALUES (?, ?, ?, ?, ?)
			`, "concurrent", "test", "ConcurrentEvent", "{}", 1)
			if err != nil {
				t.Errorf("Concurrent insert %d failed: %v", n, err)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}

	// Verify all inserts succeeded
	var count int
	err := repo.DB.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = 'ConcurrentEvent'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count events: %v", err)
	}

	if count != 10 {
		t.Errorf("Expected 10 concurrent events, got %d", count)
	}
}

func TestRepository_ConnectionPool(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	stats := repo.DB.Stats()

	// Verify connection pool settings
	if stats.MaxOpenConnections != 1 {
		t.Errorf("Expected MaxOpenConnections=1, got %d", stats.MaxOpenConnections)
	}
}

// Benchmark database operations
func BenchmarkInsertEvent(b *testing.B) {
	tmpDir, _ := os.MkdirTemp("", "healarr-bench-*")
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "bench.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		b.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := repo.DB.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
			VALUES (?, ?, ?, ?, ?)
		`, "benchmark", "bench-event", "BenchEvent", "{}", 1)
		if err != nil {
			b.Fatalf("Insert failed: %v", err)
		}
	}
}
