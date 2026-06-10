package db

import (
	"context"
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
	"github.com/mescon/Healarr/internal/safego"
)

// MaxRetries is the number of times to retry a database operation on SQLITE_BUSY
const MaxRetries = 5

// RetryDelay is the base delay between retries (increases exponentially).
// A var (not a const) so tests exercising full retry exhaustion can shrink
// it to avoid paying the real 100+200+400+800ms backoff; production keeps
// the 100ms default.
var RetryDelay = 100 * time.Millisecond

//go:embed migrations/*.sql
var migrationsFS embed.FS

// DBMetricsRecorder is an interface for recording database metrics.
// This avoids circular imports with the metrics package.
type DBMetricsRecorder interface {
	RecordDBQuery(operation string, duration float64)
	RecordDBPoolStats(openConns, inUse, idle int)
}

// Repository provides database access methods for the application.
type Repository struct {
	DB      *sql.DB
	metrics DBMetricsRecorder // Optional metrics recorder
}

// NewRepository creates a new Repository with the database at the given path.
func NewRepository(dbPath string) (*Repository, error) {
	// Ensure directory exists with restricted permissions (owner only)
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Apply connection-level pragmas through the DSN so EVERY pooled
	// connection gets them. Setting them later with db.Exec only configures
	// the single connection that happens to serve that call; the other pool
	// connections kept busy_timeout=0 (immediate SQLITE_BUSY instead of
	// waiting), foreign_keys=OFF and synchronous=FULL. The busy_timeout gap
	// is what surfaced as "database is locked" on the parallel scan's
	// watermark writer (#321): whichever of the 4 connections it landed on
	// usually wasn't the one configured. modernc.org/sqlite runs each
	// _pragma on connection open. journal_mode is database-level (persists in
	// the file) but harmless to assert here too.
	db, err := sql.Open("sqlite", dbPath+pragmaDSNSuffix)
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

// pragmaDSNSuffix carries the connection-level pragmas appended to the DSN so
// modernc.org/sqlite applies them on EVERY pooled connection, not just the one
// a later db.Exec("PRAGMA ...") happens to land on. Each pragma:
//   - busy_timeout(30000): wait up to 30s for a lock instead of returning
//     SQLITE_BUSY immediately. This is the #321 fix — without it on every
//     connection, the parallel scan's watermark writer saw "database is
//     locked".
//   - journal_mode(WAL): concurrent readers + one writer. Database-level and
//     persistent, but asserted here so a fresh file gets it on first open.
//   - foreign_keys(1): enforce FK constraints (off by default per connection).
//   - synchronous(NORMAL): WAL-safe, avoids the per-commit fsync FULL pays on
//     the write-heavy scan path; only risks the last txn on OS crash/power
//     loss, recoverable since scans re-run and the event store replays.
//   - temp_store(MEMORY) / cache_size(-8000): per-connection performance knobs.
const pragmaDSNSuffix = "?_pragma=busy_timeout(30000)" +
	"&_pragma=journal_mode(WAL)" +
	"&_pragma=foreign_keys(1)" +
	"&_pragma=synchronous(NORMAL)" +
	"&_pragma=temp_store(MEMORY)" +
	"&_pragma=cache_size(-8000)"

// configureSQLite sets the database-level pragmas that aren't carried in the
// DSN. Connection-level pragmas (busy_timeout, journal_mode, foreign_keys,
// synchronous, temp_store, cache_size) are applied to every pooled connection
// via pragmaDSNSuffix instead — see #321.
//
// Its one job is converting the database to INCREMENTAL auto_vacuum.
// auto_vacuum is a database-level property that only takes effect on an empty
// database or after a VACUUM. It was historically set with a plain db.Exec
// after the schema already existed, so it silently stayed at NONE (0) — and
// the daily `PRAGMA incremental_vacuum` maintenance was a no-op, meaning the
// file never reclaimed pages freed by retention pruning and only ever grew.
//
// We convert once: set the mode and VACUUM to apply it. The check makes it
// idempotent (a no-op once the DB is already incremental). On a fresh DB the
// VACUUM is trivial and the mode persists as the schema is migrated in
// afterward; on an existing DB it is a one-time rewrite that also reclaims
// the accumulated free space. Both pragma and VACUUM run on one dedicated
// connection so the mode-change definitely applies to the VACUUM.
func configureSQLite(db *sql.DB) error {
	ctx := context.Background()
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("acquire connection for auto_vacuum config: %w", err)
	}
	defer func() { _ = conn.Close() }()

	var mode int
	if err := conn.QueryRowContext(ctx, "PRAGMA auto_vacuum").Scan(&mode); err != nil {
		return fmt.Errorf("read auto_vacuum: %w", err)
	}
	if mode == 2 { // 2 == INCREMENTAL: already converted
		return nil
	}

	logger.Infof("Converting database to incremental auto-vacuum (one-time; reclaims freed space going forward)...")
	if _, err := conn.ExecContext(ctx, "PRAGMA auto_vacuum=INCREMENTAL"); err != nil {
		return fmt.Errorf("set auto_vacuum=INCREMENTAL: %w", err)
	}
	if _, err := conn.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("vacuum to apply auto_vacuum: %w", err)
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

// SetMetrics configures the metrics recorder for database query tracking.
// This should be called after NewRepository to enable metrics collection.
func (r *Repository) SetMetrics(m DBMetricsRecorder) {
	r.metrics = m
}

// extractQueryType determines the SQL operation type from a query string.
// Returns "select", "insert", "update", "delete", or "other".
func extractQueryType(query string) string {
	// Trim whitespace and convert to lowercase for comparison
	trimmed := strings.TrimSpace(strings.ToLower(query))
	switch {
	case strings.HasPrefix(trimmed, "select"):
		return "select"
	case strings.HasPrefix(trimmed, "insert"):
		return "insert"
	case strings.HasPrefix(trimmed, "update"):
		return "update"
	case strings.HasPrefix(trimmed, "delete"):
		return "delete"
	default:
		return "other"
	}
}

// recordQueryMetrics records timing metrics for a database query if metrics are enabled.
func (r *Repository) recordQueryMetrics(query string, start time.Time) {
	if r.metrics == nil {
		return
	}
	duration := time.Since(start).Seconds()
	operation := extractQueryType(query)
	r.metrics.RecordDBQuery(operation, duration)
}

// QueryRowContext executes a query that returns at most one row with metrics tracking.
func (r *Repository) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	start := time.Now()
	row := r.DB.QueryRowContext(ctx, query, args...)
	r.recordQueryMetrics(query, start)
	return row
}

// QueryContext executes a query that returns rows with metrics tracking.
func (r *Repository) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	start := time.Now()
	rows, err := r.DB.QueryContext(ctx, query, args...)
	r.recordQueryMetrics(query, start)
	return rows, err
}

// ExecContext executes a query that doesn't return rows with metrics tracking.
func (r *Repository) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	start := time.Now()
	result, err := r.DB.ExecContext(ctx, query, args...)
	r.recordQueryMetrics(query, start)
	return result, err
}

// StartPeriodicPoolStats starts a background goroutine that reports connection pool
// statistics at the specified interval. Returns a stop function.
func (r *Repository) StartPeriodicPoolStats(interval time.Duration) func() {
	if r.metrics == nil {
		// Return no-op stop function when metrics not configured.
		// This allows callers to always call the returned function without nil checks.
		return func() { /* intentionally empty - no cleanup needed when metrics disabled */ }
	}

	stopCh := make(chan struct{})

	safego.Run("db-stats-collector", func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				stats := r.DB.Stats()
				r.metrics.RecordDBPoolStats(stats.OpenConnections, stats.InUse, stats.Idle)
			}
		}
	})

	return func() {
		close(stopCh)
	}
}

// Close closes the database connection.
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

	safego.Run("db-periodic-checkpoint", func() {
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
	})

	return func() {
		close(stopCh)
	}
}

// createViewsWithSummaryTable creates optimized views backed by the corruption_summary table.
// corruption_status remains as a thin VIEW wrapper for backwards compatibility with older queries.
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

// recreateViews drops and recreates database views to ensure they match the latest schema.
// SQLite views are not automatically updated when the underlying schema changes.
func (r *Repository) recreateViews() error {
	// Drop existing views
	// Security: view names are hardcoded in this slice, not from user input
	views := []string{"corruption_status", "dashboard_stats"}
	for _, view := range views {
		if _, err := r.DB.Exec("DROP VIEW IF EXISTS " + view); err != nil { // NOSONAR - view name from hardcoded slice
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

// pruneOperation represents a data pruning operation with query and logging format.
type pruneOperation struct {
	name   string
	query  string
	args   []interface{}
	format string
}

// executePruneOperation executes a pruning query and logs the result.
func (r *Repository) executePruneOperation(op pruneOperation) {
	result, err := r.DB.Exec(op.query, op.args...)
	if err != nil {
		logger.Errorf("Failed to %s: %v", op.name, err)
		return
	}
	if deleted, _ := result.RowsAffected(); deleted > 0 {
		logger.Infof(op.format, deleted)
	}
}

// executeMaintenanceCommand executes a maintenance SQL command and logs the result.
func (r *Repository) executeMaintenanceCommand(name, sql string, warnOnError bool) {
	if _, err := r.DB.Exec(sql); err != nil {
		if warnOnError {
			logger.Errorf("Failed to run %s: %v", name, err)
		} else {
			logger.Debugf("%s failed (might not be applicable): %v", name, err)
		}
		return
	}
	logger.Debugf("%s completed", name)
}

// RunMaintenance performs database maintenance tasks:
// - Incremental vacuum to reclaim space
// - Prune old data (events, scan history older than retention period)
// - Optimize indexes
// Call this periodically (e.g., daily or weekly)
func (r *Repository) RunMaintenance(retentionDays int) error {
	logger.Infof("Starting database maintenance...")

	if retentionDays > 0 {
		cutoff := time.Now().AddDate(0, 0, -retentionDays).Format(time.RFC3339)
		pruneOps := []pruneOperation{
			{
				name:   "prune old events",
				query:  "DELETE FROM events WHERE created_at < ?",
				args:   []interface{}{cutoff},
				format: "Pruned %d old events",
			},
			{
				name: "prune old scans",
				// aborted/interrupted are included: an aborted scan is
				// terminal, and an interrupted one older than the retention
				// window will never be resumed. COALESCE covers rows whose
				// terminal write never stamped completed_at (MarkAborted /
				// MarkInterrupted) — they previously never matched and the
				// table only grew.
				query:  "DELETE FROM scans WHERE status IN ('completed', 'cancelled', 'error', 'aborted', 'interrupted') AND COALESCE(completed_at, started_at) < ?",
				args:   []interface{}{cutoff},
				format: "Pruned %d old scan records",
			},
			{
				name:   "prune orphaned scan_files",
				query:  "DELETE FROM scan_files WHERE scan_id NOT IN (SELECT id FROM scans)",
				args:   nil,
				format: "Pruned %d orphaned scan_files records",
			},
		}
		for _, op := range pruneOps {
			r.executePruneOperation(op)
		}
	}

	maintenanceOps := []struct {
		name        string
		sql         string
		warnOnError bool
	}{
		{"incremental vacuum", "PRAGMA incremental_vacuum", true},
		{"database analysis", "ANALYZE", true},
		{"WAL checkpoint", "PRAGMA wal_checkpoint(TRUNCATE)", false},
	}
	for _, op := range maintenanceOps {
		r.executeMaintenanceCommand(op.name, op.sql, op.warnOnError)
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
	// Security: table names are hardcoded in this slice, not from user input
	tables := []string{"scans", "corruptions", "events", "arr_instances", "path_mappings"}
	tableCounts := make(map[string]int64)
	for _, table := range tables {
		var count int64
		// Table might not exist yet, so we don't fail on error here
		if err := r.DB.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count); err == nil { // NOSONAR - table name from hardcoded slice
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

// createMigrationsTable ensures the schema_migrations table exists.
func (r *Repository) createMigrationsTable() error {
	_, err := r.DB.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (version INTEGER PRIMARY KEY, applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP)`)
	if err != nil {
		return fmt.Errorf("failed to create schema_migrations table: %w", err)
	}
	return nil
}

// getCurrentMigrationVersion returns the highest applied migration version.
func (r *Repository) getCurrentMigrationVersion() (int, error) {
	var version int
	err := r.DB.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("failed to get current migration version: %w", err)
	}
	return version, nil
}

// getMigrationFiles returns sorted SQL migration files from the embedded filesystem.
func getMigrationFiles() ([]string, error) {
	entries, err := migrationsFS.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded migrations: %w", err)
	}

	var files []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			files = append(files, entry.Name())
		}
	}
	sort.Strings(files)
	return files, nil
}

// parseMigrationVersion extracts the version number from a migration filename.
func parseMigrationVersion(file string) (int, bool) {
	var version int
	if _, err := fmt.Sscanf(file, "%d_", &version); err != nil {
		return 0, false
	}
	return version, true
}

// applyMigration executes a single migration file within a transaction.
func (r *Repository) applyMigration(file string, version int) error {
	content, err := migrationsFS.ReadFile("migrations/" + file)
	if err != nil {
		return fmt.Errorf("failed to read migration file %s: %w", file, err)
	}

	tx, err := r.DB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if tx == nil {
			return
		}
		// A rollback error during migration means the schema may be in an
		// inconsistent state. The actual failure (the original migration
		// error) is returned to the caller; log the rollback outcome at
		// debug so it shows up when diagnosing a half-applied migration.
		if err := tx.Rollback(); err != nil {
			logger.Debugf("Migration rollback returned: %v", err)
		}
	}()

	if _, err := tx.Exec(string(content)); err != nil {
		return fmt.Errorf("failed to execute migration %s: %w", file, err)
	}

	if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
		return fmt.Errorf("failed to record migration version %s: %w", file, err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration %s: %w", file, err)
	}
	tx = nil // prevent deferred rollback after successful commit
	return nil
}

func (r *Repository) runMigrations() error {
	if err := r.createMigrationsTable(); err != nil {
		return err
	}

	currentVersion, err := r.getCurrentMigrationVersion()
	if err != nil {
		return err
	}

	migrationFiles, err := getMigrationFiles()
	if err != nil {
		return err
	}
	logger.Debugf("Found %d embedded migration files", len(migrationFiles))

	for _, file := range migrationFiles {
		version, ok := parseMigrationVersion(file)
		if !ok {
			logger.Errorf("Skipping invalid migration file: %s", file)
			continue
		}

		if version <= currentVersion {
			continue
		}

		logger.Infof("Applying migration: %s", file)
		if err := r.applyMigration(file, version); err != nil {
			return err
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
	// Security: backupPath is server-generated from config + timestamp, not user input
	_, err := r.DB.Exec(fmt.Sprintf("VACUUM INTO '%s'", backupPath)) // NOSONAR - path is server-generated
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
		// Security: Use filepath.Base to ensure we only use the filename portion,
		// preventing any potential path traversal even though os.ReadDir
		// should only return base names. Defense-in-depth approach.
		safeName := filepath.Base(backups[i].name)
		if safeName == "." || safeName == ".." || safeName != backups[i].name {
			logger.Warnf("Skipping suspicious backup filename: %s", backups[i].name)
			continue
		}
		path := filepath.Join(backupDir, safeName)
		if err := os.Remove(path); err != nil {
			logger.Errorf("Failed to remove old backup %s: %v", path, err)
		} else {
			logger.Infof("Removed old backup: %s", safeName)
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
