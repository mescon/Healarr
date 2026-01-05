package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
	_ "modernc.org/sqlite"
)

// MaxRetries is the number of times to retry a database operation on SQLITE_BUSY
const MaxRetries = 5

// RetryDelay is the base delay between retries (increases exponentially)
const RetryDelay = 100 * time.Millisecond

//go:embed migrations/*.sql
var migrationsFS embed.FS

type Repository struct {
	DB *sql.DB
}

func NewRepository(dbPath string) (*Repository, error) {
	// Ensure directory exists with restricted permissions (owner only)
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool for SQLite with WAL mode
	// WAL mode allows multiple concurrent readers + 1 writer
	// Higher connection count enables parallel reads for better concurrency
	db.SetMaxOpenConns(10)                 // Allow concurrent readers (WAL mode safe)
	db.SetMaxIdleConns(5)                  // Keep connections ready for reuse
	db.SetConnMaxLifetime(0)               // Don't close connections due to age
	db.SetConnMaxIdleTime(5 * time.Minute) // Close idle connections after 5 minutes

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	// Configure SQLite for reliability and performance
	if err := configureSQLite(db); err != nil {
		return nil, fmt.Errorf("failed to configure database: %w", err)
	}

	repo := &Repository{DB: db}
	if err := repo.runMigrations(); err != nil {
		return nil, fmt.Errorf("failed to run migrations: %w", err)
	}

	// Recreate views to ensure they match latest schema
	if err := repo.recreateViews(); err != nil {
		logger.Errorf("Warning: failed to recreate views: %v", err)
		// Non-fatal - continue with startup
	}

	// Encrypt any unencrypted API keys (for backwards compatibility)
	if err := repo.migrateAPIKeyEncryption(); err != nil {
		logger.Errorf("Warning: failed to migrate API key encryption: %v", err)
		// Non-fatal - continue with startup
	}

	// Run integrity check on startup
	if err := repo.checkIntegrity(); err != nil {
		logger.Errorf("Warning: database integrity check failed: %v", err)
		// Non-fatal but logged - database may need attention
	}

	return repo, nil
}

// configureSQLite sets optimal SQLite pragmas for reliability and performance
func configureSQLite(db *sql.DB) error {
	pragmas := []string{
		// WAL mode for better concurrency and crash recovery
		"PRAGMA journal_mode=WAL",
		// Synchronous NORMAL is safe with WAL and faster than FULL
		"PRAGMA synchronous=NORMAL",
		// Auto-vacuum in incremental mode - reclaims space automatically
		"PRAGMA auto_vacuum=INCREMENTAL",
		// Store temp tables in memory for performance
		"PRAGMA temp_store=MEMORY",
		// Enable foreign key constraints
		"PRAGMA foreign_keys=ON",
		// Increase cache size (negative = KB, so -8000 = 8MB)
		"PRAGMA cache_size=-8000",
		// Busy timeout of 30 seconds to handle concurrent access during heavy scans
		"PRAGMA busy_timeout=30000",
	}

	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			// Log but don't fail - some pragmas may not be supported
			logger.Debugf("Failed to set %s: %v", pragma, err)
		}
	}

	return nil
}

// checkIntegrity runs a quick integrity check on the database
func (r *Repository) checkIntegrity() error {
	var result string
	err := r.DB.QueryRow("PRAGMA quick_check").Scan(&result)
	if err != nil {
		return fmt.Errorf("integrity check query failed: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("integrity check failed: %s", result)
	}
	logger.Infof("✓ Database integrity check passed")
	return nil
}

func (r *Repository) Close() error {
	return r.DB.Close()
}

// recreateViews drops and recreates database views to ensure they match the latest schema.
// This is necessary because SQLite views are not automatically updated when the schema changes.
func (r *Repository) recreateViews() error {
	// Drop existing views
	views := []string{"corruption_status", "dashboard_stats"}
	for _, view := range views {
		if _, err := r.DB.Exec("DROP VIEW IF EXISTS " + view); err != nil {
			return fmt.Errorf("failed to drop view %s: %w", view, err)
		}
	}

	// Recreate corruption_status view
	_, err := r.DB.Exec(`
		CREATE VIEW corruption_status AS
		SELECT
			aggregate_id as corruption_id,
			(SELECT event_type FROM events e2
			 WHERE e2.aggregate_id = e.aggregate_id
			 ORDER BY id DESC LIMIT 1) as current_state,
			(SELECT COUNT(*) FROM events e3
			 WHERE e3.aggregate_id = e.aggregate_id
			 AND e3.event_type LIKE '%Failed') as retry_count,
			(SELECT json_extract(event_data, '$.file_path') FROM events e4
			 WHERE e4.aggregate_id = e.aggregate_id
			 AND e4.event_type = 'CorruptionDetected'
			 LIMIT 1) as file_path,
			(SELECT json_extract(event_data, '$.path_id') FROM events e7
			 WHERE e7.aggregate_id = e.aggregate_id
			 AND e7.event_type = 'CorruptionDetected'
			 LIMIT 1) as path_id,
			(SELECT json_extract(event_data, '$.error') FROM events e5
			 WHERE e5.aggregate_id = e.aggregate_id
			 ORDER BY id DESC LIMIT 1) as last_error,
			(SELECT json_extract(event_data, '$.corruption_type') FROM events e6
			 WHERE e6.aggregate_id = e.aggregate_id
			 AND e6.event_type = 'CorruptionDetected'
			 LIMIT 1) as corruption_type,
			MIN(created_at) as detected_at,
			MAX(created_at) as last_updated_at
		FROM events e
		WHERE aggregate_type = 'corruption'
		GROUP BY aggregate_id
	`)
	if err != nil {
		return fmt.Errorf("failed to create corruption_status view: %w", err)
	}

	// Recreate dashboard_stats view with updated in_progress logic
	_, err = r.DB.Exec(`
		CREATE VIEW dashboard_stats AS
		SELECT
			COUNT(DISTINCT CASE
				WHEN current_state != 'VerificationSuccess'
				AND current_state != 'MaxRetriesReached'
				AND current_state != 'CorruptionIgnored'
				AND current_state != 'ImportBlocked'
				AND current_state != 'ManuallyRemoved'
				THEN corruption_id END) as active_corruptions,
			COUNT(DISTINCT CASE
				WHEN current_state = 'VerificationSuccess'
				THEN corruption_id END) as resolved_corruptions,
			COUNT(DISTINCT CASE
				WHEN current_state = 'MaxRetriesReached'
				THEN corruption_id END) as orphaned_corruptions,
			COUNT(DISTINCT CASE
				WHEN (current_state LIKE '%Started'
				OR current_state LIKE '%Queued'
				OR current_state LIKE '%Progress'
				OR current_state = 'SearchCompleted'
				OR current_state = 'DeletionCompleted'
				OR current_state = 'FileDetected')
				AND current_state != 'CorruptionIgnored'
				THEN corruption_id END) as in_progress,
			COUNT(DISTINCT CASE
				WHEN current_state = 'ImportBlocked'
				OR current_state = 'ManuallyRemoved'
				THEN corruption_id END) as manual_intervention_required
		FROM corruption_status
		WHERE current_state != 'CorruptionIgnored'
	`)
	if err != nil {
		return fmt.Errorf("failed to create dashboard_stats view: %w", err)
	}

	logger.Debugf("✓ Database views recreated")
	return nil
}

// RunMaintenance performs database maintenance tasks:
// - Incremental vacuum to reclaim space
// - Prune old data (events, scan history older than retention period)
// - Optimize indexes
// Call this periodically (e.g., daily or weekly)
func (r *Repository) RunMaintenance(retentionDays int) error {
	logger.Infof("Starting database maintenance...")

	// 1. Prune old events (keep last N days)
	if retentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -retentionDays).Format(time.RFC3339)

		// Delete old events
		result, err := r.DB.Exec("DELETE FROM events WHERE created_at < ?", cutoff)
		if err != nil {
			logger.Errorf("Failed to prune old events: %v", err)
		} else {
			deleted, _ := result.RowsAffected()
			if deleted > 0 {
				logger.Infof("Pruned %d old events (older than %d days)", deleted, retentionDays)
			}
		}

		// Delete old completed scans (keep scan records but clean up old ones)
		result, err = r.DB.Exec(`
			DELETE FROM scans
			WHERE status IN ('completed', 'cancelled', 'error')
			AND completed_at < ?
		`, cutoff)
		if err != nil {
			logger.Errorf("Failed to prune old scans: %v", err)
		} else {
			deleted, _ := result.RowsAffected()
			if deleted > 0 {
				logger.Infof("Pruned %d old scan records (older than %d days)", deleted, retentionDays)
			}
		}

		// Delete orphaned scan_files records (from deleted scans)
		result, err = r.DB.Exec(`
			DELETE FROM scan_files
			WHERE scan_id NOT IN (SELECT id FROM scans)
		`)
		if err != nil {
			logger.Errorf("Failed to prune orphaned scan_files: %v", err)
		} else {
			deleted, _ := result.RowsAffected()
			if deleted > 0 {
				logger.Infof("Pruned %d orphaned scan_files records", deleted)
			}
		}
	}

	// 2. Run incremental vacuum to reclaim space from deleted rows
	if _, err := r.DB.Exec("PRAGMA incremental_vacuum"); err != nil {
		logger.Errorf("Failed to run incremental vacuum: %v", err)
	} else {
		logger.Debugf("Incremental vacuum completed")
	}

	// 3. Analyze tables to update query planner statistics
	if _, err := r.DB.Exec("ANALYZE"); err != nil {
		logger.Errorf("Failed to analyze database: %v", err)
	} else {
		logger.Debugf("Database analysis completed")
	}

	// 4. WAL checkpoint to merge WAL into main database
	if _, err := r.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		logger.Debugf("WAL checkpoint failed (might not be in WAL mode): %v", err)
	}

	logger.Infof("✓ Database maintenance completed")
	return nil
}

// GetDatabaseStats returns statistics about the database
func (r *Repository) GetDatabaseStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Get page count and page size
	var pageCount, pageSize int64
	if err := r.DB.QueryRow("PRAGMA page_count").Scan(&pageCount); err != nil {
		return nil, fmt.Errorf("failed to get page_count: %w", err)
	}
	if err := r.DB.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
		return nil, fmt.Errorf("failed to get page_size: %w", err)
	}
	stats["size_bytes"] = pageCount * pageSize
	stats["page_count"] = pageCount
	stats["page_size"] = pageSize

	// Get freelist count (unused pages)
	var freelistCount int64
	if err := r.DB.QueryRow("PRAGMA freelist_count").Scan(&freelistCount); err != nil {
		return nil, fmt.Errorf("failed to get freelist_count: %w", err)
	}
	stats["freelist_pages"] = freelistCount
	stats["freelist_bytes"] = freelistCount * pageSize

	// Get table row counts
	tables := []string{"scans", "corruptions", "events", "arr_instances", "path_mappings"}
	tableCounts := make(map[string]int64)
	for _, table := range tables {
		var count int64
		// Table might not exist yet, so we don't fail on error here
		if err := r.DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count); err == nil {
			tableCounts[table] = count
		}
	}
	stats["table_counts"] = tableCounts

	// Get journal mode
	var journalMode string
	if err := r.DB.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		return nil, fmt.Errorf("failed to get journal_mode: %w", err)
	}
	stats["journal_mode"] = journalMode

	// Get auto_vacuum setting
	var autoVacuum int
	if err := r.DB.QueryRow("PRAGMA auto_vacuum").Scan(&autoVacuum); err != nil {
		return nil, fmt.Errorf("failed to get auto_vacuum: %w", err)
	}
	autoVacuumModes := map[int]string{0: "none", 1: "full", 2: "incremental"}
	stats["auto_vacuum"] = autoVacuumModes[autoVacuum]

	return stats, nil
}

func (r *Repository) runMigrations() error {
	// Create migrations table if not exists
	_, err := r.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}

	// Get current version
	var currentVersion int
	err = r.DB.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&currentVersion)
	if err != nil {
		return fmt.Errorf("failed to get current migration version: %w", err)
	}

	// Read migrations from embedded filesystem
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read embedded migrations: %w", err)
	}

	var migrationFiles []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			migrationFiles = append(migrationFiles, entry.Name())
		}
	}
	sort.Strings(migrationFiles)
	logger.Debugf("Found %d embedded migration files", len(migrationFiles))

	for _, file := range migrationFiles {
		var version int
		_, err := fmt.Sscanf(file, "%d_", &version)
		if err != nil {
			logger.Errorf("Skipping invalid migration file: %s", file)
			continue
		}

		if version > currentVersion {
			logger.Infof("Applying migration: %s", file)
			content, err := migrationsFS.ReadFile("migrations/" + file)
			if err != nil {
				return fmt.Errorf("failed to read migration file %s: %w", file, err)
			}

			tx, err := r.DB.Begin()
			if err != nil {
				return fmt.Errorf("failed to begin transaction: %w", err)
			}

			// Execute migration
			_, err = tx.Exec(string(content))
			if err != nil {
				if rbErr := tx.Rollback(); rbErr != nil {
					logger.Errorf("Failed to rollback transaction after migration error: %v", rbErr)
				}
				return fmt.Errorf("failed to execute migration %s: %w", file, err)
			}

			// Record version
			_, err = tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", version)
			if err != nil {
				if rbErr := tx.Rollback(); rbErr != nil {
					logger.Errorf("Failed to rollback transaction after version record error: %v", rbErr)
				}
				return fmt.Errorf("failed to record migration version %s: %w", file, err)
			}

			if err := tx.Commit(); err != nil {
				return fmt.Errorf("failed to commit migration %s: %w", file, err)
			}
		}
	}

	return nil
}

// Backup creates a backup of the database file
// Returns the path to the backup file
func (r *Repository) Backup(dbPath string) (string, error) {
	// Create backup directory if it doesn't exist
	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Generate backup filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("healarr_%s.db", timestamp))

	// Use SQLite's backup API via a checkpoint and file copy
	// First, force a WAL checkpoint to ensure all data is in the main database file
	_, err := r.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		logger.Debugf("WAL checkpoint failed (might not be in WAL mode): %v", err)
	}

	// Copy the database file
	srcFile, err := os.Open(dbPath)
	if err != nil {
		return "", fmt.Errorf("failed to open source database: %w", err)
	}
	defer func() {
		if closeErr := srcFile.Close(); closeErr != nil {
			logger.Warnf("Failed to close source database file: %v", closeErr)
		}
	}()

	dstFile, err := os.Create(backupPath)
	if err != nil {
		return "", fmt.Errorf("failed to create backup file: %w", err)
	}
	// Note: We handle dstFile.Close() explicitly below to catch errors

	_, err = io.Copy(dstFile, srcFile)
	if err != nil {
		_ = dstFile.Close()       // Ignore close error since we're already returning an error
		_ = os.Remove(backupPath) // Clean up partial backup, ignore error
		return "", fmt.Errorf("failed to copy database: %w", err)
	}

	// Sync to ensure backup is fully written to disk
	if err := dstFile.Sync(); err != nil {
		_ = dstFile.Close()
		_ = os.Remove(backupPath)
		return "", fmt.Errorf("failed to sync backup file: %w", err)
	}

	// Explicitly close dst file and check for errors - this ensures data is flushed
	if err := dstFile.Close(); err != nil {
		_ = os.Remove(backupPath)
		return "", fmt.Errorf("failed to close backup file: %w", err)
	}

	// Clean up old backups (keep last 5)
	r.cleanupOldBackups(backupDir, 5)

	return backupPath, nil
}

// cleanupOldBackups removes old backup files, keeping only the most recent 'keep' files
func (r *Repository) cleanupOldBackups(backupDir string, keep int) {
	entries, err := os.ReadDir(backupDir)
	if err != nil {
		logger.Errorf("Failed to read backup directory: %v", err)
		return
	}

	// Filter to only .db files and get file info
	type backupFile struct {
		name    string
		modTime time.Time
	}
	var backups []backupFile
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".db") {
			info, err := entry.Info()
			if err != nil {
				continue
			}
			backups = append(backups, backupFile{name: entry.Name(), modTime: info.ModTime()})
		}
	}

	// Sort by modification time (newest first)
	sort.Slice(backups, func(i, j int) bool {
		return backups[i].modTime.After(backups[j].modTime)
	})

	// Remove old backups
	for i := keep; i < len(backups); i++ {
		path := filepath.Join(backupDir, backups[i].name)
		if err := os.Remove(path); err != nil {
			logger.Errorf("Failed to remove old backup %s: %v", path, err)
		} else {
			logger.Infof("Removed old backup: %s", backups[i].name)
		}
	}
}

// migrateAPIKeyEncryption encrypts any unencrypted API key in the settings table.
// This ensures backwards compatibility with databases created before encryption was added.
func (r *Repository) migrateAPIKeyEncryption() error {
	// Check if there's an API key to migrate
	var apiKey string
	err := r.DB.QueryRow("SELECT value FROM settings WHERE key = 'api_key'").Scan(&apiKey)
	if err == sql.ErrNoRows {
		// No API key set yet, nothing to migrate
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to query API key: %w", err)
	}

	// Check if already encrypted
	if crypto.IsEncrypted(apiKey) {
		return nil // Already encrypted, nothing to do
	}

	// Check if encryption is enabled
	if !crypto.EncryptionEnabled() {
		logger.Infof("API key encryption: skipped (no encryption key configured)")
		return nil
	}

	// Encrypt and update
	encryptedKey, err := crypto.Encrypt(apiKey)
	if err != nil {
		return fmt.Errorf("failed to encrypt API key: %w", err)
	}

	_, err = r.DB.Exec("UPDATE settings SET value = ? WHERE key = 'api_key'", encryptedKey)
	if err != nil {
		return fmt.Errorf("failed to update encrypted API key: %w", err)
	}

	logger.Infof("✓ API key encrypted successfully")
	return nil
}
