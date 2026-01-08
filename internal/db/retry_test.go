package db

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	_ "modernc.org/sqlite" // Register pure-Go SQLite driver for database/sql
)

// testDBCounter ensures unique database names across parallel test runs
var testDBCounter atomic.Int64

// newTestDBForRetry creates an in-memory SQLite database for retry tests.
// This is a simplified version that doesn't use testutil to avoid import cycles.
// Each call creates a unique database to avoid test isolation issues in parallel runs.
func newTestDBForRetry() (*sql.DB, error) {
	// Use a unique database name per test to avoid interference between parallel tests.
	// The shared cache is still used for connection pooling within each test.
	dbName := fmt.Sprintf("file:retry_test_%d?mode=memory&cache=shared", testDBCounter.Add(1))
	db, err := sql.Open("sqlite", dbName)
	if err != nil {
		return nil, err
	}

	// Ensure single connection to prevent any remaining pooling issues
	db.SetMaxOpenConns(1)

	// Configure SQLite for testing
	pragmas := []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, err
		}
	}

	// Create a minimal scan_paths table for testing
	_, err = db.Exec(`
		CREATE TABLE scan_paths (
			id INTEGER PRIMARY KEY,
			local_path TEXT NOT NULL UNIQUE,
			arr_path TEXT NOT NULL,
			arr_instance_id INTEGER,
			enabled BOOLEAN DEFAULT 1,
			auto_remediate BOOLEAN DEFAULT 0,
			detection_method TEXT NOT NULL DEFAULT 'ffprobe',
			detection_args TEXT,
			detection_mode TEXT NOT NULL DEFAULT 'quick',
			max_retries INTEGER DEFAULT 3,
			verification_timeout_hours INTEGER DEFAULT NULL
		)
	`)
	if err != nil {
		_ = db.Close()
		return nil, err
	}

	return db, nil
}

// =============================================================================
// ExecWithRetry tests
// =============================================================================

func TestExecWithRetry_SuccessFirstAttempt(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Simple insert should succeed on first attempt
	result, err := ExecWithRetry(db, "INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"/test/path", "/arr/path", true, false, "ffprobe", "quick", 3)
	if err != nil {
		t.Fatalf("ExecWithRetry failed: %v", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("Failed to get rows affected: %v", err)
	}
	if rowsAffected != 1 {
		t.Errorf("Expected 1 row affected, got %d", rowsAffected)
	}
}

func TestExecWithRetry_LastInsertId(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	result, err := ExecWithRetry(db, "INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"/test/path2", "/arr/path2", true, false, "ffprobe", "quick", 3)
	if err != nil {
		t.Fatalf("ExecWithRetry failed: %v", err)
	}

	lastID, err := result.LastInsertId()
	if err != nil {
		t.Fatalf("Failed to get last insert id: %v", err)
	}
	if lastID <= 0 {
		t.Errorf("Expected positive last insert id, got %d", lastID)
	}
}

func TestExecWithRetry_UpdateOperation(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// First insert
	_, err = ExecWithRetry(db, "INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"/test/path", "/arr/path", true, false, "ffprobe", "quick", 3)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Then update
	result, err := ExecWithRetry(db, "UPDATE scan_paths SET enabled = ? WHERE local_path = ?", false, "/test/path")
	if err != nil {
		t.Fatalf("ExecWithRetry update failed: %v", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("Failed to get rows affected: %v", err)
	}
	if rowsAffected != 1 {
		t.Errorf("Expected 1 row affected, got %d", rowsAffected)
	}
}

func TestExecWithRetry_DeleteOperation(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// First insert
	_, err = ExecWithRetry(db, "INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"/test/delete", "/arr/delete", true, false, "ffprobe", "quick", 3)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Then delete
	result, err := ExecWithRetry(db, "DELETE FROM scan_paths WHERE local_path = ?", "/test/delete")
	if err != nil {
		t.Fatalf("ExecWithRetry delete failed: %v", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		t.Fatalf("Failed to get rows affected: %v", err)
	}
	if rowsAffected != 1 {
		t.Errorf("Expected 1 row affected, got %d", rowsAffected)
	}
}

func TestExecWithRetry_NonRetryableError(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Invalid SQL should fail immediately (not retry)
	_, err = ExecWithRetry(db, "INSERT INTO nonexistent_table (col) VALUES (?)", "value")
	if err == nil {
		t.Fatal("Expected error for non-existent table")
	}

	// Should not contain "database busy after" in the error
	if strings.Contains(err.Error(), "database busy after") {
		t.Error("Non-retryable error should not go through retry logic")
	}
}

func TestExecWithRetry_SyntaxError(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Syntax error should fail immediately
	_, err = ExecWithRetry(db, "INSER INTO scan_paths VALUES (?)", "value")
	if err == nil {
		t.Fatal("Expected error for syntax error")
	}

	// Error should be the SQL syntax error, not a retry exhaustion
	if strings.Contains(err.Error(), "database busy after") {
		t.Error("Syntax error should not go through retry logic")
	}
}

func TestExecWithRetry_ConstraintViolation(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Insert a row
	_, err = ExecWithRetry(db, "INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		999, "/test/path", "/arr/path", true, false, "ffprobe", "quick", 3)
	if err != nil {
		t.Fatalf("First insert failed: %v", err)
	}

	// Try to insert duplicate primary key
	_, err = ExecWithRetry(db, "INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		999, "/test/path2", "/arr/path2", true, false, "ffprobe", "quick", 3)
	if err == nil {
		t.Fatal("Expected error for duplicate primary key")
	}

	// Should not be a retry exhaustion error
	if strings.Contains(err.Error(), "database busy after") {
		t.Error("Constraint violation should not go through retry logic")
	}
}

// =============================================================================
// QueryWithRetry tests
// =============================================================================

func TestQueryWithRetry_SuccessFirstAttempt(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Insert test data
	_, err = db.Exec("INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"/test/query", "/arr/query", true, false, "ffprobe", "quick", 3)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Query should succeed on first attempt
	rows, err := QueryWithRetry(db, "SELECT id, local_path FROM scan_paths WHERE local_path = ?", "/test/query")
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("Expected at least one row")
	}

	var id int
	var localPath string
	if err := rows.Scan(&id, &localPath); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if localPath != "/test/query" {
		t.Errorf("Expected local_path=/test/query, got %s", localPath)
	}
}

func TestQueryWithRetry_EmptyResult(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Query for non-existent data should succeed (just return empty)
	rows, err := QueryWithRetry(db, "SELECT id FROM scan_paths WHERE local_path = ?", "/nonexistent")
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}
	defer rows.Close()

	if rows.Next() {
		t.Error("Expected no rows")
	}
}

func TestQueryWithRetry_MultipleRows(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Insert multiple rows with unique paths but same arr_path for filtering
	for i := 1; i <= 3; i++ {
		_, err = db.Exec("INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?)",
			"/test/multi/"+string(rune('0'+i)), "/arr/multi", true, false, "ffprobe", "quick", i)
		if err != nil {
			t.Fatalf("Insert %d failed: %v", i, err)
		}
	}

	rows, err := QueryWithRetry(db, "SELECT max_retries FROM scan_paths WHERE arr_path = ? ORDER BY max_retries", "/arr/multi")
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		count++
	}

	if count != 3 {
		t.Errorf("Expected 3 rows, got %d", count)
	}
}

func TestQueryWithRetry_NonRetryableError(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Invalid table name should fail immediately
	_, err = QueryWithRetry(db, "SELECT * FROM nonexistent_table")
	if err == nil {
		t.Fatal("Expected error for non-existent table")
	}

	// Should not be a retry exhaustion error
	if strings.Contains(err.Error(), "database busy after") {
		t.Error("Non-retryable error should not go through retry logic")
	}
}

func TestQueryWithRetry_SyntaxError(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Syntax error should fail immediately
	_, err = QueryWithRetry(db, "SELEC * FROM scan_paths")
	if err == nil {
		t.Fatal("Expected error for syntax error")
	}

	if strings.Contains(err.Error(), "database busy after") {
		t.Error("Syntax error should not go through retry logic")
	}
}

func TestQueryWithRetry_WithArguments(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Insert test data
	_, err = db.Exec("INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"/test/args", "/arr/args", true, false, "ffprobe", "quick", 5)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Query with multiple arguments
	rows, err := QueryWithRetry(db, "SELECT local_path FROM scan_paths WHERE enabled = ? AND max_retries >= ? AND detection_method = ?",
		true, 5, "ffprobe")
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("Expected at least one row")
	}
}

// =============================================================================
// Constants tests
// =============================================================================

func TestRetryConstants(t *testing.T) {
	// Verify the constants are set to expected values
	if MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", MaxRetries)
	}

	// RetryDelay should be 100ms
	expectedDelay := 100 * 1_000_000 // 100ms in nanoseconds
	if RetryDelay.Nanoseconds() != int64(expectedDelay) {
		t.Errorf("RetryDelay = %v, want 100ms", RetryDelay)
	}
}

// =============================================================================
// Error message format tests
// =============================================================================

func TestExecWithRetry_ErrorMessageFormat(t *testing.T) {
	// We can verify that non-busy errors are not wrapped as retry errors

	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	_, err = ExecWithRetry(db, "INSERT INTO nonexistent VALUES (?)", 1)
	if err == nil {
		t.Fatal("Expected error")
	}

	// The error should be the original SQLite error, not wrapped
	if strings.Contains(err.Error(), "database busy after") {
		t.Error("Non-busy error should not be wrapped as retry exhaustion")
	}
}

// =============================================================================
// Integration tests with real operations
// =============================================================================

func TestExecWithRetry_TransactionIntegration(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// ExecWithRetry should work for transaction-style operations
	_, err = ExecWithRetry(db, "BEGIN IMMEDIATE")
	if err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}

	_, err = ExecWithRetry(db, "INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"/test/tx", "/arr/tx", true, false, "ffprobe", "quick", 3)
	if err != nil {
		t.Fatalf("INSERT in tx failed: %v", err)
	}

	_, err = ExecWithRetry(db, "COMMIT")
	if err != nil {
		t.Fatalf("COMMIT failed: %v", err)
	}

	// Verify the insert
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM scan_paths WHERE local_path = ?", "/test/tx").Scan(&count)
	if err != nil {
		t.Fatalf("Verification query failed: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 row, got %d", count)
	}
}

func TestQueryWithRetry_ComplexQuery(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Insert test data
	for i := 1; i <= 5; i++ {
		enabled := i%2 == 0
		_, err = db.Exec("INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?)",
			"/test/complex/"+string(rune('0'+i)), "/arr/complex", enabled, false, "ffprobe", "quick", i)
		if err != nil {
			t.Fatalf("Insert %d failed: %v", i, err)
		}
	}

	// Complex query with aggregation
	rows, err := QueryWithRetry(db,
		"SELECT enabled, COUNT(*) as cnt, AVG(max_retries) as avg_retries FROM scan_paths WHERE local_path LIKE ? GROUP BY enabled",
		"/test/complex/%")
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var enabled bool
		var cnt int
		var avgRetries float64
		if err := rows.Scan(&enabled, &cnt, &avgRetries); err != nil {
			t.Fatalf("Scan failed: %v", err)
		}
		count++
	}

	if count != 2 { // One group for enabled=true, one for enabled=false
		t.Errorf("Expected 2 groups, got %d", count)
	}
}

// =============================================================================
// Error type verification
// =============================================================================

func TestExecWithRetry_ErrorUnwrapping(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	_, err = ExecWithRetry(db, "INSERT INTO nonexistent VALUES (?)", 1)
	if err == nil {
		t.Fatal("Expected error")
	}

	// The error should not be sql.ErrNoRows (that's a different error type)
	if errors.Is(err, sql.ErrNoRows) {
		t.Error("Table not found error should not be sql.ErrNoRows")
	}
}

// =============================================================================
// Rollback test
// =============================================================================

func TestExecWithRetry_RollbackOperation(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Start a transaction
	_, err = ExecWithRetry(db, "BEGIN IMMEDIATE")
	if err != nil {
		t.Fatalf("BEGIN failed: %v", err)
	}

	// Insert something
	_, err = ExecWithRetry(db, "INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?)",
		"/test/rollback", "/arr/rollback", true, false, "ffprobe", "quick", 3)
	if err != nil {
		t.Fatalf("INSERT failed: %v", err)
	}

	// Rollback
	_, err = ExecWithRetry(db, "ROLLBACK")
	if err != nil {
		t.Fatalf("ROLLBACK failed: %v", err)
	}

	// Verify the insert was rolled back
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM scan_paths WHERE local_path = ?", "/test/rollback").Scan(&count)
	if err != nil {
		t.Fatalf("Verification query failed: %v", err)
	}
	if count != 0 {
		t.Errorf("Expected 0 rows after rollback, got %d", count)
	}
}

// =============================================================================
// QueryRow equivalent pattern test
// =============================================================================

func TestQueryWithRetry_SingleRowPattern(t *testing.T) {
	db, err := newTestDBForRetry()
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}
	defer db.Close()

	// Insert test data
	_, err = db.Exec("INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, detection_method, detection_mode, max_retries) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		42, "/test/single", "/arr/single", true, false, "ffprobe", "quick", 3)
	if err != nil {
		t.Fatalf("Insert failed: %v", err)
	}

	// Use QueryWithRetry like QueryRow
	rows, err := QueryWithRetry(db, "SELECT id, local_path FROM scan_paths WHERE id = ?", 42)
	if err != nil {
		t.Fatalf("QueryWithRetry failed: %v", err)
	}
	defer rows.Close()

	if !rows.Next() {
		t.Fatal("Expected one row")
	}

	var id int
	var localPath string
	if err := rows.Scan(&id, &localPath); err != nil {
		t.Fatalf("Scan failed: %v", err)
	}

	if id != 42 {
		t.Errorf("Expected id=42, got %d", id)
	}
	if localPath != "/test/single" {
		t.Errorf("Expected local_path=/test/single, got %s", localPath)
	}

	// Should be no more rows
	if rows.Next() {
		t.Error("Expected only one row")
	}
}
