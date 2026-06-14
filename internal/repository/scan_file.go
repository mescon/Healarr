package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mescon/Healarr/internal/db"
)

// ScanFile is a row from the scan_files table (the per-file outcome of a scan).
type ScanFile struct {
	ID             int64
	FilePath       string
	Status         string
	CorruptionType sql.NullString
	ErrorDetails   sql.NullString
	FileSize       sql.NullInt64
	ScannedAt      string
	// CheckDetails is the scanner's JSON record of what was checked
	// (method, mode, hwaccel, durations, content-analysis outcome). NULL on
	// rows from before migration 013 and on stat-level skips that never
	// reached detection.
	CheckDetails sql.NullString
}

// ScanFileRecord is the write shape for a per-file scan result. Empty
// CorruptionType / ErrorDetails are stored as SQL NULL — that unifies the
// healthy-file insert (which sets neither) with the corrupt/skipped/
// inaccessible inserts (which set both) behind one statement.
type ScanFileRecord struct {
	FilePath       string
	Status         string
	CorruptionType string
	ErrorDetails   string
	FileSize       int64
	CheckDetails   string
}

// ScanFileRepository wraps the scan_files table.
type ScanFileRepository struct {
	db *sql.DB
}

// NewScanFileRepository returns a repository backed by sqlDB.
func NewScanFileRepository(sqlDB *sql.DB) *ScanFileRepository {
	return &ScanFileRepository{db: sqlDB}
}

// Record inserts a per-file scan result for the given scan.
//
// Uses db.ExecWithRetry because these inserts fire from parallel per-file
// scan goroutines and can hit SQLite BUSY under contention.
//
// Idempotent on (scan_id, file_path) via the UNIQUE index added in migration
// 010 and the ON CONFLICT DO NOTHING clause below: on resume from
// interruption, the scanner's watermark may lag behind the actual
// highest-completed index, so a re-detection of an already-recorded file
// must not produce a duplicate row. Callers that need to distinguish
// "fresh insert" from "already existed" can check the returned bool.
//
// scanned_at is set explicitly via strftime to millisecond precision
// instead of the column's CURRENT_TIMESTAMP default (second precision).
// The old precision clustered batch-completions into a single per-second
// timestamp; with the new out-of-order worker pool, real wall-clock order
// is visible in the scan-detail UI.
func (r *ScanFileRepository) Record(ctx context.Context, scanID int64, rec ScanFileRecord) error {
	_ = ctx // retry wrapper operates on the pool; ctx reserved for a future ExecContext variant
	var corruptionType, errorDetails, checkDetails interface{}
	if rec.CorruptionType != "" {
		corruptionType = rec.CorruptionType
	}
	if rec.ErrorDetails != "" {
		errorDetails = rec.ErrorDetails
	}
	if rec.CheckDetails != "" {
		checkDetails = rec.CheckDetails
	}
	_, err := db.ExecWithRetry(r.db, `
		INSERT INTO scan_files (scan_id, file_path, status, corruption_type, error_details, file_size, check_details, scanned_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, strftime('%Y-%m-%d %H:%M:%f', 'now'))
		ON CONFLICT(scan_id, file_path) DO NOTHING
	`, scanID, rec.FilePath, rec.Status, corruptionType, errorDetails, rec.FileSize, checkDetails)
	if err != nil {
		return fmt.Errorf("record scan file: %w", err)
	}
	return nil
}

// CountByStatus returns a status → count map for a scan (the scan-detail
// screen's healthy/corrupt/skipped/inaccessible breakdown).
func (r *ScanFileRepository) CountByStatus(ctx context.Context, scanID int64) (map[string]int, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT status, COUNT(*) FROM scan_files WHERE scan_id = ? GROUP BY status`, scanID)
	if err != nil {
		return nil, fmt.Errorf("count scan files by status: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var status string
		var count int
		if err := rows.Scan(&status, &count); err != nil {
			return nil, fmt.Errorf("scan status-count row: %w", err)
		}
		counts[status] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate status counts: %w", err)
	}
	return counts, nil
}

// CountForScan returns the number of scan_files rows for a scan, optionally
// filtered to a single status. A statusFilter of "" or "all" counts every row.
func (r *ScanFileRepository) CountForScan(ctx context.Context, scanID int64, statusFilter string) (int, error) {
	var n int
	var err error
	if statusFilter == "" || statusFilter == "all" {
		err = r.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM scan_files WHERE scan_id = ?`, scanID).Scan(&n)
	} else {
		err = r.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM scan_files WHERE scan_id = ? AND status = ?`, scanID, statusFilter).Scan(&n)
	}
	if err != nil {
		return 0, fmt.Errorf("count scan files: %w", err)
	}
	return n, nil
}

// ListForScan returns a page of scan_files for a scan, optionally filtered
// to a single status, ordered by status DESC then file_path ASC. A
// statusFilter of "" or "all" returns every status.
func (r *ScanFileRepository) ListForScan(ctx context.Context, scanID int64, statusFilter string, limit, offset int) ([]ScanFile, error) {
	var rows *sql.Rows
	var err error
	const tail = ` ORDER BY status DESC, file_path ASC LIMIT ? OFFSET ?`
	if statusFilter == "" || statusFilter == "all" {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, file_path, status, corruption_type, error_details, file_size, check_details, scanned_at
			 FROM scan_files WHERE scan_id = ?`+tail, scanID, limit, offset)
	} else {
		rows, err = r.db.QueryContext(ctx,
			`SELECT id, file_path, status, corruption_type, error_details, file_size, check_details, scanned_at
			 FROM scan_files WHERE scan_id = ? AND status = ?`+tail, scanID, statusFilter, limit, offset)
	}
	if err != nil {
		return nil, fmt.Errorf("query scan files: %w", err)
	}
	defer rows.Close()

	var out []ScanFile
	for rows.Next() {
		var f ScanFile
		if err := rows.Scan(&f.ID, &f.FilePath, &f.Status, &f.CorruptionType, &f.ErrorDetails, &f.FileSize, &f.CheckDetails, &f.ScannedAt); err != nil {
			return nil, fmt.Errorf("scan scan_files row: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scan files: %w", err)
	}
	return out, nil
}
