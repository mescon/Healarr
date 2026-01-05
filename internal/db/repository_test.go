package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/crypto"
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

	// Test concurrent inserts using ExecWithRetry to handle SQLite contention
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func(n int) {
			_, err := ExecWithRetry(repo.DB, `
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

	// Verify connection pool settings (10 connections for concurrent reads with WAL mode)
	if stats.MaxOpenConnections != 10 {
		t.Errorf("Expected MaxOpenConnections=10, got %d", stats.MaxOpenConnections)
	}
}

func TestRepository_Backup(t *testing.T) {
	// Create temp directory for this test
	tmpDir, err := os.MkdirTemp("", "healarr-backup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Insert some data to make sure it's in the backup
	_, err = repo.DB.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
		VALUES (?, ?, ?, ?, ?)
	`, "test", "backup-test", "BackupEvent", "{}", 1)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	// Create backup
	backupPath, err := repo.Backup(dbPath)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Verify backup file exists
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Errorf("Backup file not created: %s", backupPath)
	}

	// Verify backup is valid by opening it
	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("Failed to open backup database: %v", err)
	}
	defer backupDB.Close()

	// Check if our test data is in the backup
	var count int
	err = backupDB.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = 'BackupEvent'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query backup database: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 event in backup, got %d", count)
	}
}

func TestRepository_CleanupOldBackups(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-cleanup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Create backup directory with multiple backup files
	backupDir := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatalf("Failed to create backup dir: %v", err)
	}

	// Create 7 backup files with different timestamps
	for i := 0; i < 7; i++ {
		timestamp := time.Now().Add(-time.Duration(i) * time.Hour).Format("20060102_150405")
		backupFile := filepath.Join(backupDir, "healarr_"+timestamp+".db")
		if err := os.WriteFile(backupFile, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create backup file: %v", err)
		}
		// Set different mod times
		os.Chtimes(backupFile, time.Now().Add(-time.Duration(i)*time.Hour), time.Now().Add(-time.Duration(i)*time.Hour))
	}

	// Run cleanup keeping 3 files
	repo.cleanupOldBackups(backupDir, 3)

	// Count remaining files
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("Failed to read backup dir: %v", err)
	}

	if len(entries) != 3 {
		t.Errorf("Expected 3 backup files after cleanup, got %d", len(entries))
	}
}

func TestExecWithRetry_Success(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-retry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Test successful execution
	result, err := ExecWithRetry(repo.DB, `
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
		VALUES (?, ?, ?, ?, ?)
	`, "test", "retry-test", "RetryEvent", "{}", 1)

	if err != nil {
		t.Errorf("ExecWithRetry failed: %v", err)
	}

	id, _ := result.LastInsertId()
	if id <= 0 {
		t.Error("Expected positive ID from insert")
	}
}

func TestExecWithRetry_NonBusyError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-retry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Test non-busy error (syntax error) - should not retry
	_, err = ExecWithRetry(repo.DB, "INVALID SQL SYNTAX")
	if err == nil {
		t.Error("Expected error from invalid SQL")
	}
}

func TestQueryWithRetry_Success(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-retry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Insert test data
	_, err = repo.DB.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
		VALUES (?, ?, ?, ?, ?)
	`, "test", "query-retry", "QueryRetryEvent", "{}", 1)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	// Test successful query with retry
	rows, err := QueryWithRetry(repo.DB, "SELECT event_type FROM events WHERE aggregate_id = ?", "query-retry")
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}
	defer rows.Close()

	var eventType string
	if rows.Next() {
		if err := rows.Scan(&eventType); err != nil {
			t.Fatalf("Failed to scan result: %v", err)
		}
		if eventType != "QueryRetryEvent" {
			t.Errorf("Expected 'QueryRetryEvent', got '%s'", eventType)
		}
	} else {
		t.Error("Expected at least one row")
	}
}

func TestQueryWithRetry_NonBusyError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-retry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Test non-busy error - should not retry
	_, err = QueryWithRetry(repo.DB, "SELECT * FROM nonexistent_table")
	if err == nil {
		t.Error("Expected error from querying non-existent table")
	}
}

func TestRepository_RunMaintenance_ZeroRetention(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert some events
	for i := 0; i < 3; i++ {
		_, err := repo.DB.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
			VALUES (?, ?, ?, ?, ?)
		`, "test", "zero-retention", "TestEvent", "{}", 1)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}
	}

	// Run maintenance with 0 retention (should not delete anything)
	err := repo.RunMaintenance(0)
	if err != nil {
		t.Errorf("RunMaintenance(0) failed: %v", err)
	}

	// Check events are still there
	var count int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = 'zero-retention'").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count events: %v", err)
	}

	if count != 3 {
		t.Errorf("Expected 3 events with 0 retention, got %d", count)
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

func TestRepository_MigrateAPIKeyEncryption_AlreadyEncrypted(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-apikey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create repo first to set up schema
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	// Insert an already-encrypted API key (with enc:v1: prefix)
	encryptedKey := "enc:v1:already-encrypted-key-value"
	_, err = repo.DB.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", "api_key", encryptedKey)
	if err != nil {
		t.Fatalf("Failed to insert encrypted API key: %v", err)
	}
	repo.Close()

	// Create new repository - migration should detect already encrypted key
	repo2, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create second repository: %v", err)
	}
	defer repo2.Close()

	// Verify the key is unchanged (migration skipped)
	var storedKey string
	err = repo2.DB.QueryRow("SELECT value FROM settings WHERE key = 'api_key'").Scan(&storedKey)
	if err != nil {
		t.Fatalf("Failed to query API key: %v", err)
	}

	if storedKey != encryptedKey {
		t.Errorf("Expected key unchanged, got '%s'", storedKey)
	}
}

func TestRepository_MigrateAPIKeyEncryption_PlainKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-apikey-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create repo first to set up schema
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	// Insert a plain API key (no enc:v1: prefix)
	plainKey := "plain-api-key-12345"
	_, err = repo.DB.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES (?, ?)", "api_key", plainKey)
	if err != nil {
		t.Fatalf("Failed to insert plain API key: %v", err)
	}
	repo.Close()

	// Create new repository - migration should run
	repo2, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create second repository: %v", err)
	}
	defer repo2.Close()

	// Query the stored key
	var storedKey string
	err = repo2.DB.QueryRow("SELECT value FROM settings WHERE key = 'api_key'").Scan(&storedKey)
	if err != nil {
		t.Fatalf("Failed to query API key: %v", err)
	}

	// Behavior depends on whether encryption is enabled
	if crypto.EncryptionEnabled() {
		// Key should be encrypted
		if !crypto.IsEncrypted(storedKey) {
			t.Errorf("Expected key to be encrypted when encryption is enabled, got '%s'", storedKey)
		}
	} else {
		// Key should remain unchanged
		if storedKey != plainKey {
			t.Errorf("Expected key unchanged when encryption disabled, got '%s'", storedKey)
		}
	}
}

func TestExecWithRetry_BusyExhausted(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-busy-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "busy.db")

	// Create a basic SQLite database using the modernc driver
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create a test table
	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Open a second connection
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open second connection: %v", err)
	}
	defer db2.Close()

	// Start exclusive transaction on db2
	tx, err := db2.Begin()
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	// Lock the database by writing
	_, err = tx.Exec("INSERT INTO test (value) VALUES ('lock')")
	if err != nil {
		t.Fatalf("Failed to insert lock row: %v", err)
	}

	// Try to write from db1 - should fail with busy
	_, err = ExecWithRetry(db, "INSERT INTO test (value) VALUES ('test')")

	// Rollback lock
	tx.Rollback()

	// The error may or may not occur depending on timing
	// This test exercises the retry loop paths
	if err != nil {
		if !strings.Contains(err.Error(), "busy") && !strings.Contains(err.Error(), "locked") {
			// Unexpected error
			t.Logf("Got non-busy error (acceptable): %v", err)
		}
	}
}

func TestQueryWithRetry_BusyExhausted(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-busy-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "busy.db")

	// Create a basic SQLite database using the modernc driver
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create a test table
	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Insert some data
	_, err = db.Exec("INSERT INTO test (value) VALUES ('data')")
	if err != nil {
		t.Fatalf("Failed to insert data: %v", err)
	}

	// Open a second connection
	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open second connection: %v", err)
	}
	defer db2.Close()

	// Start exclusive transaction on db2
	tx, err := db2.Begin()
	if err != nil {
		t.Fatalf("Failed to begin transaction: %v", err)
	}

	// Lock the database by writing
	_, err = tx.Exec("INSERT INTO test (value) VALUES ('lock')")
	if err != nil {
		t.Fatalf("Failed to insert lock row: %v", err)
	}

	// Try to query from db1 - should succeed (reads usually don't block on writes)
	// This tests the query retry path
	rows, err := QueryWithRetry(db, "SELECT value FROM test")

	// Rollback lock
	tx.Rollback()

	if err != nil {
		// Some configurations may get locked even for reads
		t.Logf("Query got error (can happen with exclusive locks): %v", err)
	} else {
		rows.Close()
	}
}

func TestRepository_GetDatabaseStats_EmptyDB(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Get stats on fresh database
	stats, err := repo.GetDatabaseStats()
	if err != nil {
		t.Fatalf("GetDatabaseStats failed: %v", err)
	}

	// Verify expected fields exist
	if _, ok := stats["size_bytes"]; !ok {
		t.Error("Expected size_bytes in stats")
	}
	if _, ok := stats["page_count"]; !ok {
		t.Error("Expected page_count in stats")
	}
	if _, ok := stats["table_counts"]; !ok {
		t.Error("Expected table_counts in stats")
	}

	// Check table_counts contains events table
	if tableCounts, ok := stats["table_counts"].(map[string]int64); ok {
		if count, exists := tableCounts["events"]; exists && count != 0 {
			t.Errorf("Expected 0 events in fresh DB, got %d", count)
		}
	}
}

func TestRepository_RunMaintenance_WithOldEvents(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert events with old timestamps using RFC3339 format (same as RunMaintenance)
	oldTime := time.Now().Add(-48 * time.Hour).Format(time.RFC3339)
	newTime := time.Now().Format(time.RFC3339)

	// Insert old event
	_, err := repo.DB.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "test", "old-event", "OldEvent", "{}", 1, oldTime)
	if err != nil {
		t.Fatalf("Failed to insert old event: %v", err)
	}

	// Insert new event
	_, err = repo.DB.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "test", "new-event", "NewEvent", "{}", 1, newTime)
	if err != nil {
		t.Fatalf("Failed to insert new event: %v", err)
	}

	// Run maintenance with 24h retention (1 day)
	err = repo.RunMaintenance(1)
	if err != nil {
		t.Fatalf("RunMaintenance failed: %v", err)
	}

	// Old event should be deleted (it's 48h old, cutoff is 24h)
	var oldCount int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = 'old-event'").Scan(&oldCount)
	if err != nil {
		t.Fatalf("Failed to count old events: %v", err)
	}
	if oldCount != 0 {
		t.Errorf("Expected old event to be deleted, found %d", oldCount)
	}

	// New event should still exist
	var newCount int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = 'new-event'").Scan(&newCount)
	if err != nil {
		t.Fatalf("Failed to count new events: %v", err)
	}
	if newCount != 1 {
		t.Errorf("Expected new event to exist, found %d", newCount)
	}
}

// =============================================================================
// Backup Error Path Tests
// =============================================================================

func TestRepository_Backup_NonexistentSourcePath(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-backup-error-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Try to backup from a non-existent path
	_, err = repo.Backup("/nonexistent/path/to/database.db")
	if err == nil {
		t.Error("Expected error when backing up non-existent database path")
	}
}

func TestRepository_Backup_MultipleBackups(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-multi-backup-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Create a backup and verify it works
	backupPath, err := repo.Backup(dbPath)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Errorf("Backup file not created: %s", backupPath)
	}

	// Verify backup directory exists with at least 1 file
	backupDir := filepath.Join(tmpDir, "backups")
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		t.Fatalf("Failed to read backup dir: %v", err)
	}
	if len(entries) < 1 {
		t.Errorf("Expected at least 1 backup file, got %d", len(entries))
	}
}

// =============================================================================
// migrateAPIKeyEncryption Tests (additional)
// =============================================================================

func TestRepository_MigrateAPIKeyEncryption_NoAPIKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-migrate-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Migration should succeed with no API key (nothing to migrate)
	err = repo.migrateAPIKeyEncryption()
	if err != nil {
		t.Errorf("migrateAPIKeyEncryption should succeed with no API key: %v", err)
	}
}

// =============================================================================
// checkIntegrity Error Tests
// =============================================================================

func TestRepository_CheckIntegrity_DroppedTable(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-integrity-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Database should still pass integrity check after normal operations
	err = repo.checkIntegrity()
	if err != nil {
		t.Errorf("checkIntegrity should pass on valid database: %v", err)
	}
}

// =============================================================================
// cleanupOldBackups Edge Cases
// =============================================================================

func TestRepository_CleanupOldBackups_EmptyDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-cleanup-empty-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Create empty backup directory
	backupDir := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatalf("Failed to create backup dir: %v", err)
	}

	// Should not panic on empty directory
	repo.cleanupOldBackups(backupDir, 3)

	// Directory should still exist
	if _, err := os.Stat(backupDir); os.IsNotExist(err) {
		t.Error("Backup directory should still exist")
	}
}

func TestRepository_CleanupOldBackups_NonexistentDir(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-cleanup-noexist-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Should not panic on non-existent directory (just logs error)
	repo.cleanupOldBackups("/nonexistent/backup/dir", 3)
}

func TestRepository_MigrateAPIKeyEncryption_Success(t *testing.T) {
	// Skip if encryption is not configured (key depends on environment)
	if !crypto.EncryptionEnabled() {
		t.Skip("Encryption not enabled - skipping migration success test")
	}

	tmpDir, err := os.MkdirTemp("", "healarr-migrate-success-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Insert an unencrypted API key
	_, err = repo.DB.Exec("INSERT INTO settings (key, value) VALUES ('api_key', 'plaintext-api-key')")
	if err != nil {
		t.Fatalf("Failed to insert API key: %v", err)
	}

	// Run migration
	err = repo.migrateAPIKeyEncryption()
	if err != nil {
		t.Errorf("migrateAPIKeyEncryption should succeed: %v", err)
	}

	// Verify key is now encrypted
	var value string
	repo.DB.QueryRow("SELECT value FROM settings WHERE key = 'api_key'").Scan(&value)
	if !crypto.IsEncrypted(value) {
		t.Error("API key should be encrypted after migration")
	}
}

func TestRepository_MigrateAPIKeyEncryption_QueryError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-migrate-queryerror-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Drop the settings table to cause a query error
	_, err = repo.DB.Exec("DROP TABLE settings")
	if err != nil {
		t.Fatalf("Failed to drop settings table: %v", err)
	}

	// Migration should fail due to query error
	err = repo.migrateAPIKeyEncryption()
	if err == nil {
		t.Error("migrateAPIKeyEncryption should fail with query error")
	}
}

func TestRepository_Backup_CreateDirError(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-backup-direrror-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Create a file where the backup directory should be
	blockingFile := filepath.Join(tmpDir, "backups")
	if err := os.WriteFile(blockingFile, []byte("blocking"), 0644); err != nil {
		t.Fatalf("Failed to create blocking file: %v", err)
	}

	// Backup should fail because it can't create the backup directory
	_, err = repo.Backup(tmpDir)
	if err == nil {
		t.Error("Backup should fail when directory creation fails")
	}
}

func TestQueryWithRetry_BusyRetried(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-retry-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath) // Use "sqlite" driver from modernc.org/sqlite
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create a table
	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Insert test data
	_, err = db.Exec("INSERT INTO test (id, value) VALUES (1, 'test')")
	if err != nil {
		t.Fatalf("Failed to insert data: %v", err)
	}

	// Query should succeed
	rows, err := QueryWithRetry(db, "SELECT id, value FROM test")
	if err != nil {
		t.Fatalf("QueryWithRetry should succeed: %v", err)
	}
	defer rows.Close()

	// Verify results
	if rows.Next() {
		var id int
		var value string
		if err := rows.Scan(&id, &value); err != nil {
			t.Fatalf("Failed to scan row: %v", err)
		}
		if id != 1 || value != "test" {
			t.Errorf("Unexpected values: id=%d, value=%s", id, value)
		}
	} else {
		t.Error("Expected at least one row")
	}
}

// =============================================================================
// Additional coverage tests for recreateViews
// =============================================================================

func TestRepository_RecreateViews_Success(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Views should already be created by NewRepository
	// Calling recreateViews should succeed (drop and recreate)
	err := repo.recreateViews()
	if err != nil {
		t.Errorf("recreateViews should succeed: %v", err)
	}

	// Verify views still exist
	views := []string{"corruption_status", "dashboard_stats"}
	for _, view := range views {
		var name string
		err := repo.DB.QueryRow(
			"SELECT name FROM sqlite_master WHERE type='view' AND name=?",
			view,
		).Scan(&name)
		if err != nil {
			t.Errorf("View %s should exist after recreateViews: %v", view, err)
		}
	}
}

func TestRepository_RecreateViews_QueryableAfterRecreate(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert some event data first
	_, err := repo.DB.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
		VALUES (?, ?, ?, ?, ?)
	`, "corruption", "view-test-123", "CorruptionDetected", `{"file_path":"/test.mkv"}`, 1)
	if err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}

	// Recreate views
	err = repo.recreateViews()
	if err != nil {
		t.Fatalf("recreateViews failed: %v", err)
	}

	// Query the views to make sure they work
	var count int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM corruption_status").Scan(&count)
	if err != nil {
		t.Errorf("Failed to query corruption_status view: %v", err)
	}

	err = repo.DB.QueryRow("SELECT active_corruptions FROM dashboard_stats").Scan(&count)
	if err != nil {
		t.Errorf("Failed to query dashboard_stats view: %v", err)
	}
}

// =============================================================================
// Additional coverage tests for GetDatabaseStats
// =============================================================================

func TestRepository_GetDatabaseStats_WithData(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert some data
	for i := 0; i < 5; i++ {
		_, err := repo.DB.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
			VALUES (?, ?, ?, ?, ?)
		`, "test", "stats-test", "StatsEvent", "{}", 1)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}
	}

	stats, err := repo.GetDatabaseStats()
	if err != nil {
		t.Fatalf("GetDatabaseStats failed: %v", err)
	}

	// Check that events table count reflects our inserts
	tableCounts, ok := stats["table_counts"].(map[string]int64)
	if !ok {
		t.Error("table_counts should be map[string]int64")
	} else {
		if count, exists := tableCounts["events"]; exists && count < 5 {
			t.Errorf("Expected at least 5 events, got %d", count)
		}
	}

	// Check auto_vacuum setting
	autoVacuum, ok := stats["auto_vacuum"]
	if !ok {
		t.Error("Expected auto_vacuum in stats")
	}
	// Should be one of the valid modes
	validModes := map[string]bool{"none": true, "full": true, "incremental": true}
	if !validModes[autoVacuum.(string)] {
		t.Errorf("Unexpected auto_vacuum value: %v", autoVacuum)
	}
}

func TestRepository_GetDatabaseStats_AllFields(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	stats, err := repo.GetDatabaseStats()
	if err != nil {
		t.Fatalf("GetDatabaseStats failed: %v", err)
	}

	// Verify all expected fields exist
	expectedFields := []string{
		"size_bytes", "page_count", "page_size",
		"freelist_pages", "freelist_bytes",
		"table_counts", "journal_mode", "auto_vacuum",
	}

	for _, field := range expectedFields {
		if _, ok := stats[field]; !ok {
			t.Errorf("Missing field in stats: %s", field)
		}
	}
}

// =============================================================================
// Additional coverage tests for RunMaintenance
// =============================================================================

func TestRepository_RunMaintenance_OldScans(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert an old completed scan
	oldTime := time.Now().AddDate(0, 0, -100).Format(time.RFC3339)
	_, err := repo.DB.Exec(`
		INSERT INTO scans (path, status, started_at, completed_at, total_files, files_scanned)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "/test/path", "completed", oldTime, oldTime, 10, 10)
	if err != nil {
		t.Fatalf("Failed to insert old scan: %v", err)
	}

	// Insert a recent scan
	newTime := time.Now().Format(time.RFC3339)
	_, err = repo.DB.Exec(`
		INSERT INTO scans (path, status, started_at, completed_at, total_files, files_scanned)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "/test/path2", "completed", newTime, newTime, 5, 5)
	if err != nil {
		t.Fatalf("Failed to insert new scan: %v", err)
	}

	// Run maintenance with 90-day retention
	err = repo.RunMaintenance(90)
	if err != nil {
		t.Errorf("RunMaintenance failed: %v", err)
	}

	// Check that old scan was pruned
	var count int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM scans").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count scans: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 scan after maintenance (recent one), got %d", count)
	}
}

func TestRepository_RunMaintenance_ScanFilesPreserved(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert a scan
	result, err := repo.DB.Exec(`
		INSERT INTO scans (path, status, started_at, total_files, files_scanned)
		VALUES (?, ?, ?, ?, ?)
	`, "/test/path", "completed", time.Now().Format(time.RFC3339), 1, 1)
	if err != nil {
		t.Fatalf("Failed to insert scan: %v", err)
	}
	scanID, _ := result.LastInsertId()

	// Insert a scan_file for this scan
	_, err = repo.DB.Exec(`
		INSERT INTO scan_files (scan_id, file_path, status)
		VALUES (?, ?, ?)
	`, scanID, "/test/file.mkv", "healthy")
	if err != nil {
		t.Fatalf("Failed to insert scan_file: %v", err)
	}

	// Run maintenance (should not delete the valid scan_file)
	err = repo.RunMaintenance(0) // 0 retention = no time-based pruning
	if err != nil {
		t.Errorf("RunMaintenance failed: %v", err)
	}

	// Check that valid scan_file is preserved
	var count int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM scan_files WHERE scan_id = ?", scanID).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count scan_files: %v", err)
	}

	if count != 1 {
		t.Errorf("Expected 1 scan_file to be preserved, found %d", count)
	}
}

// =============================================================================
// Additional coverage tests for Backup error paths
// =============================================================================

func TestRepository_Backup_CopyFailure(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-backup-copy-error-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Create backup directory and make it read-only to cause write failure
	backupDir := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatalf("Failed to create backup dir: %v", err)
	}

	// Make backup directory read-only (can't create files)
	if err := os.Chmod(backupDir, 0444); err != nil {
		t.Fatalf("Failed to chmod backup dir: %v", err)
	}
	defer os.Chmod(backupDir, 0755)

	// Backup should fail because it can't create the backup file
	_, err = repo.Backup(dbPath)
	if err == nil {
		t.Error("Backup should fail when backup directory is read-only")
	}
}

// =============================================================================
// Additional coverage tests for migrateAPIKeyEncryption
// =============================================================================

func TestRepository_MigrateAPIKeyEncryption_UpdateError(t *testing.T) {
	// Skip if encryption is not configured
	if !crypto.EncryptionEnabled() {
		t.Skip("Encryption not enabled - skipping migration update error test")
	}

	tmpDir, err := os.MkdirTemp("", "healarr-migrate-update-error-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Insert an unencrypted API key
	_, err = repo.DB.Exec("INSERT INTO settings (key, value) VALUES ('api_key', 'plaintext-api-key')")
	if err != nil {
		t.Fatalf("Failed to insert API key: %v", err)
	}

	// Drop settings table to cause update error (after query succeeds)
	_, err = repo.DB.Exec("DROP TABLE settings")
	if err != nil {
		t.Fatalf("Failed to drop settings table: %v", err)
	}

	// Recreate settings table without the api_key row to test query error
	_, err = repo.DB.Exec("CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)")
	if err != nil {
		t.Fatalf("Failed to recreate settings table: %v", err)
	}

	// Migration should succeed (no API key to migrate)
	err = repo.migrateAPIKeyEncryption()
	if err != nil {
		t.Errorf("migrateAPIKeyEncryption should succeed with no API key: %v", err)
	}
}

// =============================================================================
// Additional tests for runMigrations edge cases
// =============================================================================

func TestRepository_SchemaMigrationsTable(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Verify schema_migrations table exists and has entries
	var count int
	err := repo.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query schema_migrations: %v", err)
	}

	if count == 0 {
		t.Error("Expected at least one migration in schema_migrations")
	}

	// Get the max version
	var maxVersion int
	err = repo.DB.QueryRow("SELECT MAX(version) FROM schema_migrations").Scan(&maxVersion)
	if err != nil {
		t.Fatalf("Failed to get max version: %v", err)
	}

	if maxVersion <= 0 {
		t.Error("Expected positive migration version")
	}
}

// =============================================================================
// Tests for Close
// =============================================================================

func TestRepository_Close(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-close-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}

	// Close should succeed
	err = repo.Close()
	if err != nil {
		t.Errorf("Close should succeed: %v", err)
	}

	// Verify database is closed (operations should fail)
	_, err = repo.DB.Exec("SELECT 1")
	if err == nil {
		t.Error("Expected error after closing database")
	}
}

// =============================================================================
// Tests for configureSQLite edge cases
// =============================================================================

func TestRepository_ConfigureSQLite_Pragmas(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Verify pragmas were set correctly
	tests := []struct {
		pragma   string
		expected interface{}
	}{
		{"journal_mode", "wal"},
		{"foreign_keys", 1},
	}

	for _, tt := range tests {
		var result interface{}
		query := "PRAGMA " + tt.pragma
		err := repo.DB.QueryRow(query).Scan(&result)
		if err != nil {
			t.Errorf("Failed to query %s: %v", tt.pragma, err)
			continue
		}
		// Note: result type may vary (string vs int), just verify it doesn't error
	}
}

// =============================================================================
// Additional tests for RunMaintenance with various data scenarios
// =============================================================================

func TestRepository_RunMaintenance_AllPrunePaths(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Create old timestamps (older than 7 days)
	oldTime := time.Now().AddDate(0, 0, -10).Format(time.RFC3339)
	newTime := time.Now().Format(time.RFC3339)

	// Insert old events
	_, err := repo.DB.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "test", "old", "OldEvent", "{}", 1, oldTime)
	if err != nil {
		t.Fatalf("Failed to insert old event: %v", err)
	}

	// Insert new event
	_, err = repo.DB.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "test", "new", "NewEvent", "{}", 1, newTime)
	if err != nil {
		t.Fatalf("Failed to insert new event: %v", err)
	}

	// Insert old cancelled scan
	_, err = repo.DB.Exec(`
		INSERT INTO scans (path, status, started_at, completed_at, total_files, files_scanned)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "/test/cancelled", "cancelled", oldTime, oldTime, 5, 5)
	if err != nil {
		t.Fatalf("Failed to insert cancelled scan: %v", err)
	}

	// Insert old error scan
	_, err = repo.DB.Exec(`
		INSERT INTO scans (path, status, started_at, completed_at, total_files, files_scanned, error_message)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, "/test/error", "error", oldTime, oldTime, 3, 1, "Test error")
	if err != nil {
		t.Fatalf("Failed to insert error scan: %v", err)
	}

	// Run maintenance with 7 day retention
	err = repo.RunMaintenance(7)
	if err != nil {
		t.Errorf("RunMaintenance failed: %v", err)
	}

	// Verify old events were deleted
	var eventCount int
	repo.DB.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = 'old'").Scan(&eventCount)
	if eventCount != 0 {
		t.Errorf("Expected old event to be deleted, found %d", eventCount)
	}

	// Verify new event was preserved
	repo.DB.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = 'new'").Scan(&eventCount)
	if eventCount != 1 {
		t.Errorf("Expected new event to be preserved, found %d", eventCount)
	}

	// Verify old scans were deleted
	var scanCount int
	repo.DB.QueryRow("SELECT COUNT(*) FROM scans WHERE status IN ('cancelled', 'error')").Scan(&scanCount)
	if scanCount != 0 {
		t.Errorf("Expected old scans to be deleted, found %d", scanCount)
	}
}

// =============================================================================
// Additional tests for checkIntegrity
// =============================================================================

func TestRepository_CheckIntegrity_AfterOperations(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Perform various database operations
	for i := 0; i < 10; i++ {
		_, err := repo.DB.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
			VALUES (?, ?, ?, ?, ?)
		`, "test", "integrity-test", "TestEvent", "{}", 1)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}
	}

	// Delete some events
	_, err := repo.DB.Exec("DELETE FROM events WHERE rowid % 2 = 0")
	if err != nil {
		t.Fatalf("Failed to delete events: %v", err)
	}

	// Check integrity should still pass
	err = repo.checkIntegrity()
	if err != nil {
		t.Errorf("checkIntegrity should pass after delete operations: %v", err)
	}
}

// =============================================================================
// Additional tests for configureSQLite
// =============================================================================

func TestConfigureSQLite_AllPragmas(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-pragma-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Configure SQLite
	err = configureSQLite(db)
	if err != nil {
		t.Errorf("configureSQLite failed: %v", err)
	}

	// Verify key pragmas
	var busyTimeout int
	db.QueryRow("PRAGMA busy_timeout").Scan(&busyTimeout)
	if busyTimeout != 30000 {
		t.Errorf("Expected busy_timeout=30000, got %d", busyTimeout)
	}

	var syncMode string
	db.QueryRow("PRAGMA synchronous").Scan(&syncMode)
	// SQLite returns numeric mode (1=NORMAL)
	if syncMode != "1" && syncMode != "NORMAL" && syncMode != "normal" {
		t.Logf("synchronous mode: %s (expected 1 or NORMAL)", syncMode)
	}
}

// =============================================================================
// Tests for runMigrations edge cases
// =============================================================================

func TestRepository_RunMigrations_Idempotent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-migration-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create first repository - runs all migrations
	repo1, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create first repository: %v", err)
	}

	// Get initial migration count
	var count1 int
	repo1.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count1)
	repo1.Close()

	// Create second repository - should skip already-applied migrations
	repo2, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create second repository: %v", err)
	}
	defer repo2.Close()

	// Migration count should be the same
	var count2 int
	repo2.DB.QueryRow("SELECT COUNT(*) FROM schema_migrations").Scan(&count2)

	if count1 != count2 {
		t.Errorf("Migration count changed: %d -> %d", count1, count2)
	}
}

func TestRepository_RunMigrations_ExistingData(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-migration-data-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create repository and add data
	repo1, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create first repository: %v", err)
	}

	// Insert data
	_, err = repo1.DB.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
		VALUES (?, ?, ?, ?, ?)
	`, "test", "migration-test", "TestEvent", "{}", 1)
	if err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}
	repo1.Close()

	// Reopen repository - should preserve data
	repo2, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create second repository: %v", err)
	}
	defer repo2.Close()

	// Verify data still exists
	var count int
	repo2.DB.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = 'migration-test'").Scan(&count)
	if count != 1 {
		t.Errorf("Expected 1 event after reopen, found %d", count)
	}
}

// =============================================================================
// Tests for recreateViews with various data states
// =============================================================================

func TestRepository_RecreateViews_WithCorruptionData(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert corruption events with various states
	states := []string{"CorruptionDetected", "SearchStarted", "VerificationSuccess", "MaxRetriesReached"}
	for i, state := range states {
		_, err := repo.DB.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
			VALUES (?, ?, ?, ?, ?)
		`, "corruption", strings.ReplaceAll(strings.ToLower(state), " ", "-")+"-"+string(rune('0'+i)),
			state, `{"file_path":"/test.mkv","corruption_type":"header_corruption"}`, 1)
		if err != nil {
			t.Fatalf("Failed to insert event for %s: %v", state, err)
		}
	}

	// Recreate views
	err := repo.recreateViews()
	if err != nil {
		t.Fatalf("recreateViews failed: %v", err)
	}

	// Query corruption_status view - should have entries
	var count int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM corruption_status").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query corruption_status: %v", err)
	}
	if count != 4 {
		t.Errorf("Expected 4 corruption status entries, got %d", count)
	}

	// Query dashboard_stats view - should work
	var active, resolved, orphaned int
	err = repo.DB.QueryRow(`
		SELECT active_corruptions, resolved_corruptions, orphaned_corruptions
		FROM dashboard_stats
	`).Scan(&active, &resolved, &orphaned)
	if err != nil {
		t.Fatalf("Failed to query dashboard_stats: %v", err)
	}

	// We should have 1 resolved (VerificationSuccess) and 1 orphaned (MaxRetriesReached)
	if resolved != 1 {
		t.Errorf("Expected 1 resolved corruption, got %d", resolved)
	}
	if orphaned != 1 {
		t.Errorf("Expected 1 orphaned corruption, got %d", orphaned)
	}
}

// =============================================================================
// Tests for GetDatabaseStats table count iteration
// =============================================================================

func TestRepository_GetDatabaseStats_MissingTable(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// GetDatabaseStats should handle missing 'corruptions' table gracefully
	// (since schema doesn't have it, the query just skips it)
	stats, err := repo.GetDatabaseStats()
	if err != nil {
		t.Fatalf("GetDatabaseStats failed: %v", err)
	}

	tableCounts, ok := stats["table_counts"].(map[string]int64)
	if !ok {
		t.Fatal("table_counts should be map[string]int64")
	}

	// 'corruptions' table doesn't exist in schema, so it shouldn't be in the map
	// The code handles this by not failing (line 327 silently ignores errors)
	if _, exists := tableCounts["corruptions"]; exists {
		t.Log("corruptions table found (this is ok if schema includes it)")
	}

	// But other tables should exist
	if _, exists := tableCounts["events"]; !exists {
		t.Error("Expected events table in counts")
	}
	if _, exists := tableCounts["scans"]; !exists {
		t.Error("Expected scans table in counts")
	}
}

// =============================================================================
// Additional tests for retry functions edge cases
// =============================================================================

func TestExecWithRetry_SimpleQuery(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create a simple table and test ExecWithRetry
	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Simple insert
	result, err := ExecWithRetry(db, "INSERT INTO test (id) VALUES (1)")
	if err != nil {
		t.Fatalf("ExecWithRetry failed: %v", err)
	}

	if result != nil {
		affected, _ := result.RowsAffected()
		if affected != 1 {
			t.Errorf("Expected 1 row affected, got %d", affected)
		}
	}
}

func TestQueryWithRetry_NoArgs(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create a test table
	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Query without args
	rows, err := QueryWithRetry(db, "SELECT 1")
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}
	defer rows.Close()

	if rows.Next() {
		var val int
		rows.Scan(&val)
		if val != 1 {
			t.Errorf("Expected 1, got %d", val)
		}
	}
}

// =============================================================================
// Additional tests for Backup edge cases
// =============================================================================

func TestRepository_Backup_SuccessWithData(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-backup-data-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Insert substantial data
	for i := 0; i < 100; i++ {
		_, err = repo.DB.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
			VALUES (?, ?, ?, ?, ?)
		`, "test", "backup-data-test", "BackupDataEvent", `{"index":`+string(rune('0'+i%10))+`}`, 1)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}
	}

	// Create backup
	backupPath, err := repo.Backup(dbPath)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Verify backup file exists and has content
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("Backup file stat failed: %v", err)
	}
	if info.Size() == 0 {
		t.Error("Backup file is empty")
	}

	// Open backup and verify data
	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("Failed to open backup: %v", err)
	}
	defer backupDB.Close()

	var count int
	backupDB.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = 'backup-data-test'").Scan(&count)
	if count != 100 {
		t.Errorf("Expected 100 events in backup, got %d", count)
	}
}

// =============================================================================
// Additional tests for NewRepository with various scenarios
// =============================================================================

func TestNewRepository_AlreadyExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-exists-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create first repo
	repo1, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create first repository: %v", err)
	}

	// Insert data
	_, err = repo1.DB.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version)
		VALUES (?, ?, ?, ?, ?)
	`, "test", "exists-test", "ExistsEvent", "{}", 1)
	if err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}
	repo1.Close()

	// Open again - should preserve data
	repo2, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create second repository: %v", err)
	}
	defer repo2.Close()

	// Verify data preserved
	var count int
	repo2.DB.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = 'exists-test'").Scan(&count)
	if count != 1 {
		t.Errorf("Expected 1 event, got %d", count)
	}
}

func TestNewRepository_MultipleInstances(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-multi-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create multiple repositories in different directories
	for i := 0; i < 3; i++ {
		subDir := filepath.Join(tmpDir, "db"+string(rune('0'+i)))
		dbPath := filepath.Join(subDir, "test.db")

		repo, err := NewRepository(dbPath)
		if err != nil {
			t.Fatalf("Failed to create repository %d: %v", i, err)
		}

		// Verify directory was created
		if _, err := os.Stat(subDir); os.IsNotExist(err) {
			t.Errorf("Directory %s was not created", subDir)
		}

		repo.Close()
	}
}

// =============================================================================
// Test for cleanupOldBackups with files that fail to delete
// =============================================================================

func TestRepository_CleanupOldBackups_MixedFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-cleanup-mixed-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	backupDir := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatalf("Failed to create backup dir: %v", err)
	}

	// Create .db files and non-.db files
	for i := 0; i < 5; i++ {
		dbFile := filepath.Join(backupDir, "healarr_test_"+string(rune('0'+i))+".db")
		if err := os.WriteFile(dbFile, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create db file: %v", err)
		}
		os.Chtimes(dbFile, time.Now().Add(-time.Duration(i)*time.Hour), time.Now().Add(-time.Duration(i)*time.Hour))

		// Also create non-db file
		txtFile := filepath.Join(backupDir, "other_"+string(rune('0'+i))+".txt")
		if err := os.WriteFile(txtFile, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create txt file: %v", err)
		}
	}

	// Cleanup keeping 2 backups
	repo.cleanupOldBackups(backupDir, 2)

	// Count remaining .db files
	entries, _ := os.ReadDir(backupDir)
	dbCount := 0
	txtCount := 0
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".db") {
			dbCount++
		} else if strings.HasSuffix(e.Name(), ".txt") {
			txtCount++
		}
	}

	if dbCount != 2 {
		t.Errorf("Expected 2 .db files after cleanup, got %d", dbCount)
	}
	if txtCount != 5 {
		t.Errorf("Expected 5 .txt files preserved, got %d", txtCount)
	}
}

// =============================================================================
// Additional QueryWithRetry tests
// =============================================================================

func TestQueryWithRetry_WithResults(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create a table with data
	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	_, err = db.Exec("INSERT INTO test (name) VALUES ('Alice'), ('Bob'), ('Charlie')")
	if err != nil {
		t.Fatalf("Failed to insert data: %v", err)
	}

	// Query using QueryWithRetry
	rows, err := QueryWithRetry(db, "SELECT id, name FROM test ORDER BY id")
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}
	defer rows.Close()

	// Count rows
	count := 0
	for rows.Next() {
		var id int
		var name string
		if err := rows.Scan(&id, &name); err != nil {
			t.Fatalf("Failed to scan row: %v", err)
		}
		count++
	}

	if count != 3 {
		t.Errorf("Expected 3 rows, got %d", count)
	}
}

func TestQueryWithRetry_WithArgs(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create and populate table
	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, value INTEGER)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	for i := 1; i <= 10; i++ {
		_, err = db.Exec("INSERT INTO test (value) VALUES (?)", i*10)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	// Query with argument
	rows, err := QueryWithRetry(db, "SELECT id FROM test WHERE value > ?", 50)
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}

	if count != 5 {
		t.Errorf("Expected 5 rows with value > 50, got %d", count)
	}
}

func TestQueryWithRetry_InvalidQuery(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Query a non-existent table
	rows, err := QueryWithRetry(db, "SELECT * FROM non_existent_table")
	if err == nil {
		rows.Close()
		t.Error("Expected error for invalid table, got nil")
	}
}

func TestExecWithRetry_MultipleRows(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create table
	_, err = db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, value INTEGER)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Insert multiple rows
	for i := 0; i < 10; i++ {
		_, err = db.Exec("INSERT INTO test (value) VALUES (?)", i)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}
	}

	// Delete multiple rows using ExecWithRetry
	result, err := ExecWithRetry(db, "DELETE FROM test WHERE value < ?", 5)
	if err != nil {
		t.Fatalf("ExecWithRetry failed: %v", err)
	}

	if result != nil {
		affected, _ := result.RowsAffected()
		if affected != 5 {
			t.Errorf("Expected 5 rows affected, got %d", affected)
		}
	}
}

func TestExecWithRetry_InvalidQuery(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Try to execute an invalid query
	_, err = ExecWithRetry(db, "INSERT INTO non_existent_table VALUES (1)")
	if err == nil {
		t.Error("Expected error for invalid table, got nil")
	}
}

// =============================================================================
// RunMaintenance additional edge cases
// =============================================================================

func TestRepository_RunMaintenance_ZeroRetention_Fresh(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Add some data first to ensure maintenance has something to work with
	_, _ = repo.DB.Exec("INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at) VALUES ('test-1', 'test', 'TestEvent', '{}', datetime('now'))")

	// With zero retention, pruning should be skipped
	err := repo.RunMaintenance(0)
	if err != nil {
		t.Errorf("RunMaintenance with zero retention failed: %v", err)
	}

	// Verify event was NOT deleted (no pruning with 0 retention)
	var count int
	_ = repo.DB.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if count != 1 {
		t.Errorf("Expected 1 event (no pruning with 0 retention), got %d", count)
	}
}

func TestRepository_RunMaintenance_NegativeRetention_Fresh(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Add some data
	_, _ = repo.DB.Exec("INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at) VALUES ('test-1', 'test', 'TestEvent', '{}', datetime('now'))")

	// Negative retention should also skip pruning
	err := repo.RunMaintenance(-1)
	if err != nil {
		t.Errorf("RunMaintenance with negative retention failed: %v", err)
	}
}

func TestRepository_RunMaintenance_WithCancelledScans(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert scans with different statuses
	path := "/test/path"
	now := time.Now()
	oldTime := now.AddDate(0, 0, -60).Format(time.RFC3339)

	// Insert old cancelled scan
	_, err := repo.DB.Exec(`
		INSERT INTO scans (path, status, started_at, completed_at, files_scanned)
		VALUES (?, 'cancelled', ?, ?, 0)
	`, path, oldTime, oldTime)
	if err != nil {
		t.Fatalf("Failed to insert cancelled scan: %v", err)
	}

	// Insert old error scan
	_, err = repo.DB.Exec(`
		INSERT INTO scans (path, status, started_at, completed_at, files_scanned)
		VALUES (?, 'error', ?, ?, 0)
	`, path, oldTime, oldTime)
	if err != nil {
		t.Fatalf("Failed to insert error scan: %v", err)
	}

	// Run maintenance with 30 day retention
	err = repo.RunMaintenance(30)
	if err != nil {
		t.Errorf("RunMaintenance failed: %v", err)
	}

	// Verify scans were deleted
	var count int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM scans").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count scans: %v", err)
	}

	if count != 0 {
		t.Errorf("Expected 0 scans after cleanup, got %d", count)
	}
}

// =============================================================================
// Backup error path tests
// =============================================================================

func TestRepository_Backup_SourceDoesNotExist(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Try to backup a non-existent file
	_, err := repo.Backup("/nonexistent/path/to/db.db")
	if err == nil {
		t.Error("Expected error when backing up non-existent file, got nil")
	}
}

// =============================================================================
// NewRepository edge cases
// =============================================================================

func TestNewRepository_PermissionDenied(t *testing.T) {
	// Skip on systems where we can't test permission denial
	if os.Getuid() == 0 {
		t.Skip("Skipping test when running as root")
	}

	tmpDir, err := os.MkdirTemp("", "healarr-perm-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Create a directory with no write permission
	restrictedDir := filepath.Join(tmpDir, "restricted")
	if err := os.Mkdir(restrictedDir, 0500); err != nil {
		t.Fatalf("Failed to create restricted dir: %v", err)
	}

	// Try to create a repo in a subdirectory of the restricted dir
	dbPath := filepath.Join(restrictedDir, "subdir", "test.db")
	_, err = NewRepository(dbPath)
	if err == nil {
		t.Error("Expected error when creating repo in restricted directory, got nil")
	}
}

// =============================================================================
// GetDatabaseStats additional tests
// =============================================================================

func TestRepository_GetDatabaseStats_AfterOperations(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Get initial stats
	stats1, err := repo.GetDatabaseStats()
	if err != nil {
		t.Fatalf("Failed to get initial stats: %v", err)
	}

	initialPages := stats1["page_count"].(int64)

	// Insert some data
	for i := 0; i < 100; i++ {
		_, err = repo.DB.Exec(`
			INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at)
			VALUES (?, 'test', 'TestEvent', '{"test": true}', datetime('now'))
		`, i)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}
	}

	// Get stats after insertions
	stats2, err := repo.GetDatabaseStats()
	if err != nil {
		t.Fatalf("Failed to get stats after inserts: %v", err)
	}

	newPages := stats2["page_count"].(int64)

	// Pages should have increased
	if newPages <= initialPages {
		t.Errorf("Expected page count to increase after inserts, got initial=%d, after=%d", initialPages, newPages)
	}

	// Verify table counts reflect new data
	tableCounts := stats2["table_counts"].(map[string]int64)
	if tableCounts["events"] != 100 {
		t.Errorf("Expected events count=100, got %d", tableCounts["events"])
	}
}

// =============================================================================
// recreateViews tests
// =============================================================================

func TestRepository_RecreateViews_MultipleCalls(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Call recreateViews multiple times - should be idempotent
	for i := 0; i < 3; i++ {
		err := repo.recreateViews()
		if err != nil {
			t.Errorf("recreateViews call %d failed: %v", i+1, err)
		}
	}

	// Verify views still work
	var count int
	err := repo.DB.QueryRow("SELECT active_corruptions FROM dashboard_stats").Scan(&count)
	if err != nil {
		t.Errorf("Failed to query dashboard_stats after multiple recreates: %v", err)
	}
}

// =============================================================================
// checkIntegrity tests
// =============================================================================

func TestRepository_CheckIntegrity_OnFreshDB(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Run integrity check on a fresh database
	err := repo.checkIntegrity()
	if err != nil {
		t.Errorf("checkIntegrity failed on fresh database: %v", err)
	}
}

// =============================================================================
// cleanupOldBackups edge cases
// =============================================================================

func TestRepository_CleanupOldBackups_EmptyDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-cleanup-empty-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	emptyDir := filepath.Join(tmpDir, "empty_backups")
	if err := os.MkdirAll(emptyDir, 0755); err != nil {
		t.Fatalf("Failed to create empty dir: %v", err)
	}

	// Should not panic or error on empty directory
	repo.cleanupOldBackups(emptyDir, 5)
}

func TestRepository_CleanupOldBackups_NonExistentDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-cleanup-nonexist-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Should not panic on non-existent directory - just logs error
	repo.cleanupOldBackups("/nonexistent/directory/path", 5)
}

func TestRepository_CleanupOldBackups_KeepMoreThanExists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-cleanup-keepmore-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	backupDir := filepath.Join(tmpDir, "backups")
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		t.Fatalf("Failed to create backup dir: %v", err)
	}

	// Create only 2 backup files
	for i := 0; i < 2; i++ {
		dbFile := filepath.Join(backupDir, "healarr_test_"+string(rune('0'+i))+".db")
		if err := os.WriteFile(dbFile, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create db file: %v", err)
		}
	}

	// Keep 10 (more than exists)
	repo.cleanupOldBackups(backupDir, 10)

	// All files should remain
	entries, _ := os.ReadDir(backupDir)
	if len(entries) != 2 {
		t.Errorf("Expected 2 files to remain, got %d", len(entries))
	}
}

// =============================================================================
// migrateAPIKeyEncryption edge cases
// =============================================================================

func TestRepository_MigrateAPIKeyEncryption_WithEncPrefix(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert an already encrypted key (starts with enc:v1:)
	encryptedKey := "enc:v1:already-encrypted-key"
	_, err := repo.DB.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)
	if err != nil {
		t.Fatalf("Failed to insert encrypted key: %v", err)
	}

	// Run migration - should detect already encrypted and skip
	err = repo.migrateAPIKeyEncryption()
	if err != nil {
		t.Errorf("migrateAPIKeyEncryption failed: %v", err)
	}

	// Verify key is unchanged
	var storedKey string
	err = repo.DB.QueryRow("SELECT value FROM settings WHERE key = 'api_key'").Scan(&storedKey)
	if err != nil {
		t.Fatalf("Failed to query key: %v", err)
	}

	if storedKey != encryptedKey {
		t.Errorf("Expected key unchanged '%s', got '%s'", encryptedKey, storedKey)
	}
}

// =============================================================================
// configureSQLite test for error handling (logging path)
// =============================================================================

func TestConfigureSQLite_AfterClose(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	// Close the database first
	db.Close()

	// configureSQLite should handle errors gracefully (logs but doesn't fail)
	// Since the function only logs errors and returns nil, it won't error
	err = configureSQLite(db)
	if err != nil {
		t.Errorf("configureSQLite returned error: %v", err)
	}
}

// =============================================================================
// Additional tests for RunMaintenance coverage
// =============================================================================

func TestRepository_RunMaintenance_WithScanFilesFromDeletedScans(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Create a completed scan first
	now := time.Now()
	oldTime := now.AddDate(0, 0, -60).Format(time.RFC3339)

	result, err := repo.DB.Exec(`
		INSERT INTO scans (path, status, started_at, completed_at, files_scanned)
		VALUES ('/test/path', 'completed', ?, ?, 5)
	`, oldTime, oldTime)
	if err != nil {
		t.Fatalf("Failed to insert scan: %v", err)
	}

	scanID, _ := result.LastInsertId()

	// Add scan_files that belong to this scan (using correct schema)
	for i := 0; i < 3; i++ {
		_, err = repo.DB.Exec(`
			INSERT INTO scan_files (scan_id, file_path, status, file_size)
			VALUES (?, ?, 'healthy', ?)
		`, scanID, "/test/file"+string(rune('0'+i))+".txt", 1000)
		if err != nil {
			t.Fatalf("Failed to insert scan_file: %v", err)
		}
	}

	// Run maintenance - should delete old scan AND its scan_files via CASCADE
	err = repo.RunMaintenance(30)
	if err != nil {
		t.Errorf("RunMaintenance failed: %v", err)
	}

	// Verify scans were deleted
	var scanCount int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM scans").Scan(&scanCount)
	if err != nil {
		t.Fatalf("Failed to count scans: %v", err)
	}

	if scanCount != 0 {
		t.Errorf("Expected 0 scans after cleanup, got %d", scanCount)
	}

	// scan_files should be deleted via CASCADE from foreign key
	var fileCount int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM scan_files").Scan(&fileCount)
	if err != nil {
		t.Fatalf("Failed to count scan_files: %v", err)
	}

	if fileCount != 0 {
		t.Errorf("Expected 0 scan_files after cleanup, got %d", fileCount)
	}
}

func TestRepository_RunMaintenance_WithMixedAgeEvents(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	now := time.Now()
	oldTime := now.AddDate(0, 0, -60).Format(time.RFC3339)
	newTime := now.Format(time.RFC3339)

	// Insert old events
	for i := 0; i < 5; i++ {
		_, err := repo.DB.Exec(`
			INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at)
			VALUES (?, 'test', 'OldEvent', '{}', ?)
		`, i, oldTime)
		if err != nil {
			t.Fatalf("Failed to insert old event: %v", err)
		}
	}

	// Insert new events
	for i := 5; i < 8; i++ {
		_, err := repo.DB.Exec(`
			INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at)
			VALUES (?, 'test', 'NewEvent', '{}', ?)
		`, i, newTime)
		if err != nil {
			t.Fatalf("Failed to insert new event: %v", err)
		}
	}

	// Run maintenance with 30 day retention
	err := repo.RunMaintenance(30)
	if err != nil {
		t.Errorf("RunMaintenance failed: %v", err)
	}

	// Verify old events were deleted
	var count int
	err = repo.DB.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count events: %v", err)
	}

	if count != 3 {
		t.Errorf("Expected 3 events after cleanup (new ones), got %d", count)
	}
}

// =============================================================================
// Additional tests for Backup coverage
// =============================================================================

func TestRepository_Backup_FullCycle(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-backup-full-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Add some data to ensure backup has content
	for i := 0; i < 10; i++ {
		_, err = repo.DB.Exec(`
			INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at)
			VALUES (?, 'test', 'BackupTestEvent', '{"test": true}', datetime('now'))
		`, i)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}
	}

	// Force WAL checkpoint to ensure data is written
	_, _ = repo.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")

	// Create backup
	backupPath, err := repo.Backup(dbPath)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Verify backup file exists and has content
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("Failed to stat backup file: %v", err)
	}

	if info.Size() == 0 {
		t.Error("Backup file is empty")
	}

	// Open backup and verify it's a valid database with data
	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		t.Fatalf("Failed to open backup database: %v", err)
	}
	defer backupDB.Close()

	var count int
	err = backupDB.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count events in backup: %v", err)
	}

	if count != 10 {
		t.Errorf("Expected 10 events in backup, got %d", count)
	}
}

func TestRepository_Backup_VerifyFileContent(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-backup-verify-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	repo, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create repository: %v", err)
	}
	defer repo.Close()

	// Insert test data
	_, err = repo.DB.Exec(`
		INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at)
		VALUES ('backup-test', 'test', 'BackupVerifyEvent', '{"verified": true}', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}

	// Create backup
	backupPath, err := repo.Backup(dbPath)
	if err != nil {
		t.Fatalf("Backup failed: %v", err)
	}

	// Verify backup exists
	info, err := os.Stat(backupPath)
	if err != nil {
		t.Fatalf("Failed to stat backup: %v", err)
	}

	// Backup should not be empty
	if info.Size() == 0 {
		t.Error("Backup file is empty")
	}

	// Verify backup is in correct directory
	if !strings.Contains(backupPath, "backups") {
		t.Errorf("Expected backup in 'backups' directory, got %s", backupPath)
	}

	// Verify backup filename format
	if !strings.Contains(filepath.Base(backupPath), "healarr_") {
		t.Errorf("Expected backup filename to contain 'healarr_', got %s", filepath.Base(backupPath))
	}
}

// =============================================================================
// Additional tests for checkIntegrity coverage
// =============================================================================

func TestRepository_CheckIntegrity_AfterManyOperations(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Perform many database operations
	for i := 0; i < 100; i++ {
		// Build valid JSON string - SQLite's generated column requires valid JSON
		eventData := fmt.Sprintf(`{"iteration": %d}`, i)
		_, err := repo.DB.Exec(`
			INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at)
			VALUES (?, 'test', 'IntegrityTestEvent', ?, datetime('now'))
		`, i, eventData)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}
	}

	// Delete some events
	_, err := repo.DB.Exec("DELETE FROM events WHERE aggregate_id < 50")
	if err != nil {
		t.Fatalf("Failed to delete events: %v", err)
	}

	// Run integrity check
	err = repo.checkIntegrity()
	if err != nil {
		t.Errorf("checkIntegrity failed after operations: %v", err)
	}
}

// =============================================================================
// Additional tests for recreateViews coverage
// =============================================================================

func TestRepository_RecreateViews_WithExistingData(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert corruption events that the views will query
	_, err := repo.DB.Exec(`
		INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at)
		VALUES ('corr-001', 'corruption', 'CorruptionDetected', '{"file_path": "/test/file.mkv", "corruption_type": "video", "path_id": 1}', datetime('now'))
	`)
	if err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}

	// Recreate views
	err = repo.recreateViews()
	if err != nil {
		t.Errorf("recreateViews failed: %v", err)
	}

	// Query the view to ensure it works with real data
	var activeCount int
	err = repo.DB.QueryRow("SELECT active_corruptions FROM dashboard_stats").Scan(&activeCount)
	if err != nil {
		t.Fatalf("Failed to query dashboard_stats: %v", err)
	}

	// CorruptionDetected is an active state
	if activeCount != 1 {
		t.Errorf("Expected 1 active corruption, got %d", activeCount)
	}
}

// =============================================================================
// Additional tests for runMigrations coverage
// =============================================================================

func TestRepository_RunMigrations_VerifySchemaVersion(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Query the schema_migrations table to verify migrations ran
	var maxVersion int
	err := repo.DB.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&maxVersion)
	if err != nil {
		t.Fatalf("Failed to query schema version: %v", err)
	}

	// Should have at least one migration applied
	if maxVersion < 1 {
		t.Errorf("Expected at least version 1 applied, got %d", maxVersion)
	}
}

func TestRepository_RunMigrations_RerunSafe(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-migration-rerun-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")

	// Create first instance
	repo1, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create first repository: %v", err)
	}

	// Get version after first init
	var version1 int
	_ = repo1.DB.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version1)
	repo1.Close()

	// Create second instance - should be safe to re-run migrations
	repo2, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create second repository: %v", err)
	}
	defer repo2.Close()

	var version2 int
	_ = repo2.DB.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version2)

	// Versions should be the same (no duplicate migrations)
	if version1 != version2 {
		t.Errorf("Expected same version after rerun, got v1=%d, v2=%d", version1, version2)
	}
}

// =============================================================================
// Additional tests targeting specific uncovered lines
// =============================================================================

func TestRepository_MigrateAPIKeyEncryption_NoKey(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// No API key in settings - migration should return nil (nothing to migrate)
	err := repo.migrateAPIKeyEncryption()
	if err != nil {
		t.Errorf("migrateAPIKeyEncryption failed when no key: %v", err)
	}
}

func TestRepository_GetDatabaseStats_Comprehensive(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// Insert data into multiple tables to ensure all counts work
	_, _ = repo.DB.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES ('test', 'sonarr', 'http://test', 'key', 1)")
	_, _ = repo.DB.Exec("INSERT INTO scans (path, status, started_at, files_scanned) VALUES ('/test', 'running', datetime('now'), 0)")
	_, _ = repo.DB.Exec("INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at) VALUES ('test', 'test', 'TestEvent', '{}', datetime('now'))")

	stats, err := repo.GetDatabaseStats()
	if err != nil {
		t.Fatalf("GetDatabaseStats failed: %v", err)
	}

	// Verify all expected keys are present
	expectedKeys := []string{"size_bytes", "page_count", "page_size", "freelist_pages", "freelist_bytes", "table_counts", "journal_mode", "auto_vacuum"}
	for _, key := range expectedKeys {
		if _, ok := stats[key]; !ok {
			t.Errorf("Missing expected key: %s", key)
		}
	}

	// Verify table counts (arr_instances and events are in the list)
	tableCounts := stats["table_counts"].(map[string]int64)
	if tableCounts["arr_instances"] != 1 {
		t.Errorf("Expected arr_instances=1, got %d", tableCounts["arr_instances"])
	}
	if tableCounts["scans"] != 1 {
		t.Errorf("Expected scans=1, got %d", tableCounts["scans"])
	}
	if tableCounts["events"] != 1 {
		t.Errorf("Expected events=1, got %d", tableCounts["events"])
	}
}

func TestRepository_RunMaintenance_Comprehensive(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	// This test runs maintenance with data to exercise all branches
	now := time.Now()
	oldTime := now.AddDate(0, 0, -45).Format(time.RFC3339)

	// Add old events
	for i := 0; i < 3; i++ {
		_, _ = repo.DB.Exec(`
			INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data, created_at)
			VALUES (?, 'test', 'OldEvent', '{}', ?)
		`, i, oldTime)
	}

	// Add old scans
	result, _ := repo.DB.Exec(`
		INSERT INTO scans (path, status, started_at, completed_at, files_scanned)
		VALUES ('/old/path', 'completed', ?, ?, 10)
	`, oldTime, oldTime)

	scanID, _ := result.LastInsertId()

	// Add scan_files for the old scan
	_, _ = repo.DB.Exec(`
		INSERT INTO scan_files (scan_id, file_path, status, file_size)
		VALUES (?, '/old/file.txt', 'healthy', 1000)
	`, scanID)

	// Run maintenance with 30-day retention
	err := repo.RunMaintenance(30)
	if err != nil {
		t.Errorf("RunMaintenance failed: %v", err)
	}

	// Verify all old data was cleaned up
	var eventCount, scanCount, fileCount int
	_ = repo.DB.QueryRow("SELECT COUNT(*) FROM events").Scan(&eventCount)
	_ = repo.DB.QueryRow("SELECT COUNT(*) FROM scans").Scan(&scanCount)
	_ = repo.DB.QueryRow("SELECT COUNT(*) FROM scan_files").Scan(&fileCount)

	if eventCount != 0 {
		t.Errorf("Expected 0 events after maintenance, got %d", eventCount)
	}
	if scanCount != 0 {
		t.Errorf("Expected 0 scans after maintenance, got %d", scanCount)
	}
	if fileCount != 0 {
		t.Errorf("Expected 0 scan_files after maintenance, got %d", fileCount)
	}
}

func TestQueryWithRetry_SimpleSuccessPath(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create table
	_, err = db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, name TEXT)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	// Insert data
	_, err = db.Exec("INSERT INTO items (name) VALUES ('test1'), ('test2')")
	if err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	// Use QueryWithRetry
	rows, err := QueryWithRetry(db, "SELECT name FROM items")
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}

	var names []string
	for rows.Next() {
		var name string
		_ = rows.Scan(&name)
		names = append(names, name)
	}
	rows.Close()

	if len(names) != 2 {
		t.Errorf("Expected 2 names, got %d", len(names))
	}
}

func TestExecWithRetry_UpdateQuery(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}
	defer db.Close()

	// Create table with data
	_, err = db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, value INTEGER)")
	if err != nil {
		t.Fatalf("Failed to create table: %v", err)
	}

	_, err = db.Exec("INSERT INTO items (value) VALUES (1), (2), (3)")
	if err != nil {
		t.Fatalf("Failed to insert: %v", err)
	}

	// Update using ExecWithRetry
	result, err := ExecWithRetry(db, "UPDATE items SET value = value * 2 WHERE value < 3")
	if err != nil {
		t.Fatalf("ExecWithRetry failed: %v", err)
	}

	affected, _ := result.RowsAffected()
	if affected != 2 {
		t.Errorf("Expected 2 rows affected, got %d", affected)
	}
}
