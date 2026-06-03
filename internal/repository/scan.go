package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mescon/Healarr/internal/db"
)

// Scan is the read-shape of a row from the scans table. Only the columns
// used by handler-layer reads are populated.
type Scan struct {
	ID               int64
	Path             string
	PathID           sql.NullInt64
	Status           string
	FilesScanned     int
	CorruptionsFound int
	StartedAt        string
	CompletedAt      sql.NullString
}

// ScanStats is the aggregate the dashboard renders.
type ScanStats struct {
	ActiveScans       int
	TotalScans        int
	FilesScannedToday int
	FilesScannedWeek  int
}

// LastScan describes the most-recent completed scan (globally, or per path).
type LastScan struct {
	ID          sql.NullInt64
	CompletedAt sql.NullString
	Path        sql.NullString
}

// ScanRepository wraps read access to the scans table. Write access from
// the scanner service is intentionally not migrated here yet — those
// methods are state-machine transitions tied tightly to scan lifecycle
// and want a richer API than per-query Update methods.
type ScanRepository struct {
	db *sql.DB
}

// NewScanRepository returns a repository backed by db.
func NewScanRepository(db *sql.DB) *ScanRepository {
	return &ScanRepository{db: db}
}

// Count returns the total number of rows in the scans table.
func (r *ScanRepository) Count(ctx context.Context) (int, error) {
	var n int
	if err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scans`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count scans: %w", err)
	}
	return n, nil
}

// ListPaged returns a page of scans, ordered by the given orderByClause.
//
// IMPORTANT: orderByClause is interpolated directly into the SQL. The
// caller MUST pass an allowlist-validated clause (i.e. the output of
// api.SafeOrderByClause); this method does no further validation. The
// alternative — parsing the clause inside the repo — would duplicate
// the allowlist that the handler already enforces.
func (r *ScanRepository) ListPaged(ctx context.Context, orderByClause string, limit, offset int) ([]Scan, error) {
	query := fmt.Sprintf( //nolint:gosec // orderByClause is allowlist-validated by caller; see method doc
		`SELECT id, path, status, files_scanned, corruptions_found, started_at, completed_at FROM scans %s LIMIT ? OFFSET ?`,
		orderByClause,
	)
	rows, err := r.db.QueryContext(ctx, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query scans page: %w", err)
	}
	defer rows.Close()

	var out []Scan
	for rows.Next() {
		var s Scan
		if err := rows.Scan(&s.ID, &s.Path, &s.Status, &s.FilesScanned, &s.CorruptionsFound, &s.StartedAt, &s.CompletedAt); err != nil {
			return nil, fmt.Errorf("scan scans row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scans: %w", err)
	}
	return out, nil
}

// GetByID returns the row matching id, or ErrNotFound.
func (r *ScanRepository) GetByID(ctx context.Context, id int64) (Scan, error) {
	var s Scan
	err := r.db.QueryRowContext(ctx, `
		SELECT id, path, path_id, status, files_scanned, corruptions_found, started_at, completed_at
		FROM scans WHERE id = ?
	`, id).Scan(&s.ID, &s.Path, &s.PathID, &s.Status, &s.FilesScanned, &s.CorruptionsFound, &s.StartedAt, &s.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Scan{}, ErrNotFound
	}
	if err != nil {
		return Scan{}, fmt.Errorf("get scan: %w", err)
	}
	return s, nil
}

// Exists returns true if a scans row with the given id exists.
func (r *ScanRepository) Exists(ctx context.Context, id int64) (bool, error) {
	var found int64
	err := r.db.QueryRowContext(ctx, `SELECT id FROM scans WHERE id = ?`, id).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check scan exists: %w", err)
	}
	return true, nil
}

// GetScanStats returns the dashboard aggregates over the scans table —
// active count, total count, files scanned today and over the last week.
func (r *ScanRepository) GetScanStats(ctx context.Context) (ScanStats, error) {
	var s ScanStats
	err := r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(CASE WHEN status = 'running' THEN 1 END),
			COUNT(*),
			COALESCE(SUM(CASE WHEN substr(started_at, 1, 10) = date('now') THEN files_scanned END), 0),
			COALESCE(SUM(CASE WHEN substr(started_at, 1, 10) >= date('now', '-7 days') THEN files_scanned END), 0)
		FROM scans
	`).Scan(&s.ActiveScans, &s.TotalScans, &s.FilesScannedToday, &s.FilesScannedWeek)
	if err != nil {
		return ScanStats{}, fmt.Errorf("query scan stats: %w", err)
	}
	return s, nil
}

// GetLastCompletedScan returns the most recent completed scan globally.
// Returns ErrNotFound when no completed scan exists.
func (r *ScanRepository) GetLastCompletedScan(ctx context.Context) (LastScan, error) {
	var l LastScan
	err := r.db.QueryRowContext(ctx, `
		SELECT id, completed_at, path
		FROM scans
		WHERE status = 'completed' AND completed_at IS NOT NULL
		ORDER BY completed_at DESC
		LIMIT 1
	`).Scan(&l.ID, &l.CompletedAt, &l.Path)
	if errors.Is(err, sql.ErrNoRows) {
		return LastScan{}, ErrNotFound
	}
	if err != nil {
		return LastScan{}, fmt.Errorf("query last completed scan: %w", err)
	}
	return l, nil
}

// GetLastCompletedScanByPathID returns the most recent completed scan
// for a specific scan_path. Path is left zero-valued (the caller usually
// knows the path already). Returns ErrNotFound when none exists.
func (r *ScanRepository) GetLastCompletedScanByPathID(ctx context.Context, pathID int64) (LastScan, error) {
	var l LastScan
	err := r.db.QueryRowContext(ctx, `
		SELECT id, completed_at
		FROM scans
		WHERE path_id = ? AND status = 'completed' AND completed_at IS NOT NULL
		ORDER BY completed_at DESC
		LIMIT 1
	`, pathID).Scan(&l.ID, &l.CompletedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return LastScan{}, ErrNotFound
	}
	if err != nil {
		return LastScan{}, fmt.Errorf("query last scan by path: %w", err)
	}
	return l, nil
}

// =============================================================================
// Write path (scanner lifecycle)
//
// These methods own the scans state machine that the scanner service drives.
// IncrementCorruptions uses db.ExecWithRetry because it fires from parallel
// per-file scan goroutines and can hit SQLite BUSY; the rest run on the
// single scan-control path and use plain ExecContext.
// =============================================================================

// CreateScanParams is the input shape for starting a new scan record.
type CreateScanParams struct {
	Path                string
	PathID              int64
	TotalFiles          int
	FileListJSON        string
	DetectionConfigJSON string
	AutoRemediate       bool
	DryRun              bool
}

// InterruptedScan is the resume-shape for a scan that a prior shutdown left
// in the 'interrupted' state with a persisted file_list.
type InterruptedScan struct {
	ID                  int64
	PathID              sql.NullInt64
	Path                string
	TotalFiles          int
	CurrentFileIndex    int
	FileListJSON        string
	DetectionConfigJSON sql.NullString
	AutoRemediate       bool
	DryRun              bool
}

// Create inserts a new scan row in the 'running' state and returns its id.
func (r *ScanRepository) Create(ctx context.Context, p CreateScanParams) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		INSERT INTO scans (path, path_id, status, files_scanned, corruptions_found, total_files, current_file_index, file_list, detection_config, auto_remediate, dry_run, started_at)
		VALUES (?, ?, 'running', 0, 0, ?, 0, ?, ?, ?, ?, datetime('now'))
	`, p.Path, p.PathID, p.TotalFiles, p.FileListJSON, p.DetectionConfigJSON, p.AutoRemediate, p.DryRun)
	if err != nil {
		return 0, fmt.Errorf("insert scan: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// SetStatus updates only the status column.
func (r *ScanRepository) SetStatus(ctx context.Context, scanID int64, status string) error {
	if _, err := r.db.ExecContext(ctx, `UPDATE scans SET status = ? WHERE id = ?`, status, scanID); err != nil {
		return fmt.Errorf("set scan status: %w", err)
	}
	return nil
}

// Finalize sets the terminal status, the final files_scanned count, and
// stamps completed_at to now.
func (r *ScanRepository) Finalize(ctx context.Context, scanID int64, status string, filesScanned int) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE scans SET status = ?, files_scanned = ?, completed_at = datetime('now')
		WHERE id = ?
	`, status, filesScanned, scanID)
	if err != nil {
		return fmt.Errorf("finalize scan: %w", err)
	}
	return nil
}

// MarkInterrupted records a shutdown-time interruption, saving the file
// index reached so the scan can resume there.
func (r *ScanRepository) MarkInterrupted(ctx context.Context, scanID int64, currentFileIndex int) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE scans SET status = 'interrupted', current_file_index = ? WHERE id = ?
	`, currentFileIndex, scanID)
	if err != nil {
		return fmt.Errorf("mark scan interrupted: %w", err)
	}
	return nil
}

// MarkPaused records a pause, saving the file index reached.
func (r *ScanRepository) MarkPaused(ctx context.Context, scanID int64, currentFileIndex int) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE scans SET current_file_index = ?, status = 'paused' WHERE id = ?
	`, currentFileIndex, scanID)
	if err != nil {
		return fmt.Errorf("mark scan paused: %w", err)
	}
	return nil
}

// MarkAborted sets the 'aborted' status and an explanatory error_message
// (used when a mount/filesystem becomes inaccessible mid-scan).
func (r *ScanRepository) MarkAborted(ctx context.Context, scanID int64, errorMessage string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE scans SET status = 'aborted', error_message = ? WHERE id = ?
	`, errorMessage, scanID)
	if err != nil {
		return fmt.Errorf("mark scan aborted: %w", err)
	}
	return nil
}

// MarkCancelled records a user-initiated cancellation.
//
// The "status NOT IN ('cancelled', 'completed', 'aborted')" guard makes this
// a no-op for a scan that already reached a terminal state (benign race
// between cancel and completion / abort) so we never clobber it. It also
// catches inconsistent rows like status='running' + completed_at IS NOT NULL
// (the failure mode in #274 where an earlier-fixed bug left rows mid-state):
// the old "completed_at IS NULL" guard refused to touch those, leaving the
// row permanently zombified through every restart. The new guard cancels
// them.
//
// Returns true iff a row was updated, so callers can distinguish "no such
// scan" from "scan was already done" for error reporting.
func (r *ScanRepository) MarkCancelled(ctx context.Context, scanID int64, reason string) (bool, error) {
	res, err := r.db.ExecContext(ctx, `
		UPDATE scans SET status = 'cancelled', completed_at = datetime('now'), error_message = ?
		WHERE id = ? AND status NOT IN ('cancelled', 'completed', 'aborted')
	`, reason, scanID)
	if err != nil {
		return false, fmt.Errorf("mark scan cancelled: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("mark scan cancelled rows affected: %w", err)
	}
	return n > 0, nil
}

// ReconcileOrphansResult reports the outcome of the startup reconcile.
type ReconcileOrphansResult struct {
	// Interrupted is the count of rows that had real persisted progress
	// (current_file_index > 0 and a saved file_list) and were demoted to
	// 'interrupted' so ResumeInterruptedScans picks them up.
	Interrupted int64
	// Cancelled is the count of rows that had no resumable state
	// (enumerating, or zero progress) and were marked terminal.
	Cancelled int64
}

// MarkOrphansCancelled is the startup reconciliation: any row left in an
// active state ("running"/"enumerating"/"scanning") that does not belong to
// this process's in-memory activeScans is the residue of a hard restart
// (SIGKILL/OOM/crash) that prevented MarkInterrupted from running.
//
// Rows with real persisted progress (current_file_index > 0 AND
// total_files > 0 AND a saved file_list) are demoted to 'interrupted'
// so the next ResumeInterruptedScans sweep picks them up. Rows with no
// resumable state (enumerating mid-walk, or zero progress) are marked
// 'cancelled' so they leave the active UI.
//
// "paused" and "interrupted" are spared throughout: they are the
// legitimate resumable states that the user or graceful shutdown put
// the scan into.
//
// The legacy single-counter return is preserved for callers/tests: it
// is the sum of interrupted+cancelled. The structured outcome is also
// exposed via ReconcileOrphans for richer logging.
func (r *ScanRepository) MarkOrphansCancelled(ctx context.Context) (int64, error) {
	res, err := r.ReconcileOrphans(ctx)
	if err != nil {
		return 0, err
	}
	return res.Interrupted + res.Cancelled, nil
}

// ReconcileOrphans runs the two-step reconcile in a single transaction:
//  1. Resumable orphans -> 'interrupted'
//  2. Everything else still in an active status -> 'cancelled'
//
// The status filter (no "completed_at IS NULL") is intentional: an
// inconsistent row with an active status but completed_at already set
// (the #274 zombie pattern) IS exactly what this query is supposed to
// reap. activeScans is empty at startup, so any row in an active status
// is by definition an orphan.
func (r *ScanRepository) ReconcileOrphans(ctx context.Context) (ReconcileOrphansResult, error) {
	var out ReconcileOrphansResult

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return out, fmt.Errorf("reconcile orphans begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	resInt, err := tx.ExecContext(ctx, `
		UPDATE scans
		SET status = 'interrupted',
		    error_message = 'auto-marked interrupted on Healarr restart'
		WHERE status IN ('running', 'scanning')
		  AND current_file_index > 0
		  AND total_files > 0
		  AND file_list IS NOT NULL
	`)
	if err != nil {
		return out, fmt.Errorf("mark resumable orphans interrupted: %w", err)
	}
	out.Interrupted, _ = resInt.RowsAffected()

	resCan, err := tx.ExecContext(ctx, `
		UPDATE scans
		SET status = 'cancelled',
		    completed_at = datetime('now'),
		    error_message = 'abandoned on Healarr restart'
		WHERE status IN ('running', 'enumerating', 'scanning')
	`)
	if err != nil {
		return out, fmt.Errorf("mark orphan scans cancelled: %w", err)
	}
	out.Cancelled, _ = resCan.RowsAffected()

	if err := tx.Commit(); err != nil {
		return out, fmt.Errorf("reconcile orphans commit: %w", err)
	}
	return out, nil
}

// UpdateProgress updates the live current_file_index and files_scanned
// counters during a running scan.
func (r *ScanRepository) UpdateProgress(ctx context.Context, scanID int64, currentFileIndex, filesScanned int) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE scans SET current_file_index = ?, files_scanned = ? WHERE id = ?
	`, currentFileIndex, filesScanned, scanID)
	if err != nil {
		return fmt.Errorf("update scan progress: %w", err)
	}
	return nil
}

// IncrementCorruptions bumps corruptions_found by one. Uses db.ExecWithRetry
// because it fires from parallel per-file scan goroutines (SQLite BUSY
// contention); behavior matches the scanner's prior inline call.
func (r *ScanRepository) IncrementCorruptions(scanID int64) error {
	if _, err := db.ExecWithRetry(r.db, `UPDATE scans SET corruptions_found = corruptions_found + 1 WHERE id = ?`, scanID); err != nil {
		return fmt.Errorf("increment corruptions: %w", err)
	}
	return nil
}

// ListInterrupted returns scans left in the 'interrupted' state with a
// persisted file_list, newest first — the resume queue consumed at startup.
//
// The "completed_at IS NULL" filter prevents zombifying a previously-
// terminal scan: a row that was cancelled (completed_at set) and then
// later had its status overwritten to 'interrupted' by a graceful
// shutdown is NOT something we want to resume. The user already decided
// they were done with it. See #274 for the bug chain that surfaced this.
func (r *ScanRepository) ListInterrupted(ctx context.Context) ([]InterruptedScan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, path_id, path, total_files, current_file_index, file_list, detection_config, auto_remediate, COALESCE(dry_run, 0)
		FROM scans
		WHERE status = 'interrupted' AND file_list IS NOT NULL AND completed_at IS NULL
		ORDER BY started_at DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("query interrupted scans: %w", err)
	}
	defer rows.Close()

	var out []InterruptedScan
	for rows.Next() {
		var s InterruptedScan
		if err := rows.Scan(&s.ID, &s.PathID, &s.Path, &s.TotalFiles, &s.CurrentFileIndex,
			&s.FileListJSON, &s.DetectionConfigJSON, &s.AutoRemediate, &s.DryRun); err != nil {
			return nil, fmt.Errorf("scan interrupted scan row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate interrupted scans: %w", err)
	}
	return out, nil
}
