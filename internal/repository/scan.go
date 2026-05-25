package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Scan is the read-shape of a row from the scans table. Only the columns
// used by handler-layer reads are populated; the scanner service uses
// raw SQL for its lifecycle mutations (its own follow-up PR).
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
