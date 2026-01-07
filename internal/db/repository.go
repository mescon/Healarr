package db

import (
	"database/sql"
	"embed"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // Register pure-Go SQLite driver for database/sql

	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
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
	// Fewer connections reduces lock contention in SQLite
	db.SetMaxOpenConns(4)                  // 4 connections is optimal for WAL mode
	db.SetMaxIdleConns(2)                  // Keep 2 connections ready for reuse
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
	// Critical pragmas that must succeed for proper database operation
	criticalPragmas := []string{
		// WAL mode for better concurrency and crash recovery
		"PRAGMA journal_mode=WAL",
		// Enable foreign key constraints
		"PRAGMA foreign_keys=ON",
		// Busy timeout of 30 seconds to handle concurrent access during heavy scans
		"PRAGMA busy_timeout=30000",
	}

	for _, pragma := range criticalPragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("failed to set critical pragma %s: %w", pragma, err)
		}
	}

	// Non-critical pragmas - log failures but continue
	optionalPragmas := []string{
		// Synchronous FULL ensures durability even on power loss during checkpoint
		// Slightly slower than NORMAL but prevents corruption on unexpected shutdown
		"PRAGMA synchronous=FULL",
		// Auto-vacuum in incremental mode - reclaims space automatically
		"PRAGMA auto_vacuum=INCREMENTAL",
		// Store temp tables in memory for performance
		"PRAGMA temp_store=MEMORY",
		// Increase cache size (negative = KB, so -8000 = 8MB)
		"PRAGMA cache_size=-8000",
	}

	for _, pragma := range optionalPragmas {
		if _, err := db.Exec(pragma); err != nil {
			// Log but don't fail - some pragmas may not be supported
			logger.Debugf("Failed to set optional pragma %s: %v", pragma, err)
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

// GracefulClose performs a clean shutdown of the database:
// 1. Runs a WAL checkpoint to merge all WAL content into main database
// 2. Syncs to disk
// 3. Closes the database connection
// This should be called on application shutdown to ensure data integrity.
func (r *Repository) GracefulClose() error {
	logger.Infof("Database: initiating graceful shutdown...")

	// Run final checkpoint to merge WAL into main database
	if _, err := r.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		logger.Warnf("Shutdown WAL checkpoint failed: %v", err)
	} else {
		logger.Debugf("✓ WAL checkpoint completed")
	}

	// Close database
	if err := r.DB.Close(); err != nil {
		return fmt.Errorf("failed to close database: %w", err)
	}

	logger.Infof("✓ Database shutdown complete")
	return nil
}

// Checkpoint runs a passive WAL checkpoint (non-blocking).
// Call this periodically to prevent WAL file from growing too large.
func (r *Repository) Checkpoint() error {
	_, err := r.DB.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	if err != nil {
		return fmt.Errorf("checkpoint failed: %w", err)
	}
	return nil
}

// StartPeriodicCheckpoint starts a background goroutine that runs
// WAL checkpoints at the specified interval. Returns a stop function.
func (r *Repository) StartPeriodicCheckpoint(interval time.Duration) func() {
	stopCh := make(chan struct{})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				if err := r.Checkpoint(); err != nil {
					logger.Debugf("Periodic checkpoint failed: %v", err)
				}
			}
		}
	}()

	return func() {
		close(stopCh)
	}
}

// recreateViews drops and recreates database views to ensure they match the latest schema.
// This is necessary because SQLite views are not automatically updated when the schema changes.
// Note: corruption_status VIEW is now a thin wrapper over corruption_summary TABLE for backwards compatibility.
// createViewsWithSummaryTable creates optimized views using corruption_summary table
func (r *Repository) createViewsWithSummaryTable() error {
	// corruption_status VIEW is a thin wrapper for backwards compatibility
	_, err := r.DB.Exec(`
		CREATE VIEW corruption_status AS
		SELECT
			corruption_id,
			current_state,
			retry_count,
			file_path,
			path_id,
			last_error,
			corruption_type,
			detected_at,
			last_updated_at
		FROM corruption_summary
	`)
	if err != nil {
		return fmt.Errorf("failed to create corruption_status view: %w", err)
	}

	// dashboard_stats uses corruption_summary directly for maximum performance
	_, err = r.DB.Exec(`
		CREATE VIEW dashboard_stats AS
		SELECT
			COUNT(CASE
				WHEN current_state != 'VerificationSuccess'
				AND current_state != 'MaxRetriesReached'
				AND current_state != 'CorruptionIgnored'
				AND current_state != 'ImportBlocked'
				AND current_state != 'ManuallyRemoved'
				THEN 1 END) as active_corruptions,
			COUNT(CASE
				WHEN current_state = 'VerificationSuccess'
				THEN 1 END) as resolved_corruptions,
			COUNT(CASE
				WHEN current_state = 'MaxRetriesReached'
				THEN 1 END) as orphaned_corruptions,
			COUNT(CASE
				WHEN (current_state LIKE '%Started'
				OR current_state LIKE '%Queued'
				OR current_state LIKE '%Progress'
				OR current_state = 'SearchCompleted'
				OR current_state = 'DeletionCompleted'
				OR current_state = 'FileDetected')
				AND current_state != 'CorruptionIgnored'
				THEN 1 END) as in_progress,
			COUNT(CASE
				WHEN current_state = 'ImportBlocked'
				OR current_state = 'ManuallyRemoved'
				THEN 1 END) as manual_intervention_required
		FROM corruption_summary
		WHERE current_state != 'CorruptionIgnored'
	`)
	if err != nil {
		return fmt.Errorf("failed to create dashboard_stats view: %w", err)
	}

	return nil
}

// createViewsLegacy creates slower views using events table (pre-migration 004)
func (r *Repository) createViewsLegacy() error {
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

	return nil
}

func (r *Repository) recreateViews() error {
	// Drop existing views
	views := []string{"corruption_status", "dashboard_stats"}
	for _, view := range views {
		if _, err := r.DB.Exec("DROP VIEW IF EXISTS " + view); err != nil {
			return fmt.Errorf("failed to drop view %s: %w", view, err)
		}
	}

	// Check if corruption_summary table exists (migration 004)
	var tableExists int
	err := r.DB.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='corruption_summary'").Scan(&tableExists)
	if err != nil {
		return fmt.Errorf("failed to check for corruption_summary table: %w", err)
	}

	if tableExists > 0 {
		err = r.createViewsWithSummaryTable()
	} else {
		err = r.createViewsLegacy()
	}

	if err != nil {
		return err
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

// Backup creates a backup of the database file using VACUUM INTO for atomic, consistent backups.
// This method is safe to call while the database is in use - it handles locking properly.
// Returns the path to the backup file.
func (r *Repository) Backup(dbPath string) (string, error) {
	// Step 1: Verify source database integrity before backup
	// This prevents propagating corruption to backups
	if err := r.checkIntegrity(); err != nil {
		logger.Errorf("Pre-backup integrity check failed: %v", err)
		return "", fmt.Errorf("refusing to backup corrupted database: %w", err)
	}

	// Create backup directory if it doesn't exist
	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		return "", fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Generate backup filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	backupPath := filepath.Join(backupDir, fmt.Sprintf("healarr_%s.db", timestamp))

	// Step 2: Use VACUUM INTO for atomic backup
	// VACUUM INTO (SQLite 3.27+) creates a consistent point-in-time backup
	// that properly handles WAL mode and holds the necessary locks.
	// It also defragments and optimizes the backup file.
	_, err := r.DB.Exec(fmt.Sprintf("VACUUM INTO '%s'", backupPath))
	if err != nil {
		// Clean up any partial backup file
		_ = os.Remove(backupPath)
		return "", fmt.Errorf("backup failed: %w", err)
	}

	// Step 3: Verify backup integrity
	if err := verifyBackupIntegrity(backupPath); err != nil {
		logger.Errorf("Backup verification failed, removing corrupt backup: %v", err)
		_ = os.Remove(backupPath)
		return "", fmt.Errorf("backup verification failed: %w", err)
	}

	logger.Infof("✓ Database backup verified: %s", filepath.Base(backupPath))

	// Clean up old backups (keep last 5)
	r.cleanupOldBackups(backupDir, 5)

	return backupPath, nil
}

// verifyBackupIntegrity opens the backup file and runs an integrity check
func verifyBackupIntegrity(backupPath string) error {
	// Open backup database for verification
	backupDB, err := sql.Open("sqlite", backupPath)
	if err != nil {
		return fmt.Errorf("failed to open backup for verification: %w", err)
	}
	defer backupDB.Close()

	// Run quick integrity check
	var result string
	err = backupDB.QueryRow("PRAGMA quick_check").Scan(&result)
	if err != nil {
		return fmt.Errorf("backup integrity check query failed: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("backup integrity check failed: %s", result)
	}

	return nil
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
