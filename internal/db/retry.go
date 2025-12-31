package db

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mescon/Healarr/internal/logger"
)

// ExecWithRetry executes a SQL statement with retry logic for SQLITE_BUSY errors.
// This function works with any *sql.DB and is useful for high-concurrency scenarios
// where multiple goroutines may be writing to the database simultaneously.
func ExecWithRetry(db *sql.DB, query string, args ...interface{}) (sql.Result, error) {
	var result sql.Result
	var err error

	for attempt := 0; attempt < MaxRetries; attempt++ {
		result, err = db.Exec(query, args...)
		if err == nil {
			return result, nil
		}

		// Check if error is SQLITE_BUSY (database is locked)
		errStr := err.Error()
		if !strings.Contains(errStr, "SQLITE_BUSY") && !strings.Contains(errStr, "database is locked") {
			// Not a busy error, don't retry
			return nil, err
		}

		// Exponential backoff: 100ms, 200ms, 400ms, 800ms, 1600ms
		delay := RetryDelay * time.Duration(1<<attempt)
		if attempt < MaxRetries-1 {
			logger.Debugf("Database busy, retrying in %v (attempt %d/%d)", delay, attempt+1, MaxRetries)
			time.Sleep(delay)
		}
	}

	return nil, fmt.Errorf("database busy after %d retries: %w", MaxRetries, err)
}

// QueryWithRetry executes a query with retry logic for SQLITE_BUSY errors.
func QueryWithRetry(db *sql.DB, query string, args ...interface{}) (*sql.Rows, error) {
	var rows *sql.Rows
	var err error

	for attempt := 0; attempt < MaxRetries; attempt++ {
		rows, err = db.Query(query, args...)
		if err == nil {
			return rows, nil
		}

		// Check if error is SQLITE_BUSY
		errStr := err.Error()
		if !strings.Contains(errStr, "SQLITE_BUSY") && !strings.Contains(errStr, "database is locked") {
			return nil, err
		}

		delay := RetryDelay * time.Duration(1<<attempt)
		if attempt < MaxRetries-1 {
			logger.Debugf("Database busy on query, retrying in %v (attempt %d/%d)", delay, attempt+1, MaxRetries)
			time.Sleep(delay)
		}
	}

	return nil, fmt.Errorf("database busy after %d retries: %w", MaxRetries, err)
}
