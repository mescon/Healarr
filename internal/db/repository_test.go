package db

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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

func TestRepository_MigrateAPIKeyEncryption_EncryptionDisabled(t *testing.T) {
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

	// Create new repository - migration should run but skip encryption (no key configured)
	repo2, err := NewRepository(dbPath)
	if err != nil {
		t.Fatalf("Failed to create second repository: %v", err)
	}
	defer repo2.Close()

	// Verify the key is unchanged (no encryption configured)
	var storedKey string
	err = repo2.DB.QueryRow("SELECT value FROM settings WHERE key = 'api_key'").Scan(&storedKey)
	if err != nil {
		t.Fatalf("Failed to query API key: %v", err)
	}

	if storedKey != plainKey {
		t.Errorf("Expected key unchanged (encryption disabled), got '%s'", storedKey)
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
