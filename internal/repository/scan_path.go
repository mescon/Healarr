package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ScanPath is a row from the scan_paths table — one configured filesystem
// directory the scanner walks for corruption.
//
// DetectionMethod / DetectionMode are stored as plain strings rather than
// the typed enums from internal/integration to keep this package
// integration-free (avoids an import cycle once arr_client.go's SQL also
// migrates here). Callers convert via integration.ParseDetectionMethod /
// ParseDetectionMode at the HTTP boundary.
type ScanPath struct {
	ID                       int64
	LocalPath                string
	ArrPath                  string
	ArrInstanceID            sql.NullInt64
	Enabled                  bool
	AutoRemediate            bool
	DryRun                   bool
	DetectionMethod          string
	DetectionArgs            sql.NullString // JSON-encoded []string, or NULL
	DetectionMode            string
	MaxRetries               int
	VerificationTimeoutHours sql.NullInt64
}

// ScanPathFields is the input shape shared by Create and Update — all the
// editable columns of scan_paths, in one struct.
//
// DetectionArgsJSON should be the JSON encoding of []string (or empty
// string for NULL). The encoding belongs to the HTTP handler that owns
// the request shape; the repo just stores the bytes.
type ScanPathFields struct {
	LocalPath                string
	ArrPath                  string
	ArrInstanceID            sql.NullInt64
	Enabled                  bool
	AutoRemediate            bool
	DryRun                   bool
	DetectionMethod          string
	DetectionArgsJSON        string
	DetectionMode            string
	MaxRetries               int
	VerificationTimeoutHours sql.NullInt64
}

// ScanPathRepository wraps the scan_paths table.
type ScanPathRepository struct {
	db *sql.DB
}

// NewScanPathRepository returns a repository backed by db.
func NewScanPathRepository(db *sql.DB) *ScanPathRepository {
	return &ScanPathRepository{db: db}
}

const scanPathColumns = `id, local_path, arr_path, arr_instance_id, enabled,
	auto_remediate, dry_run, detection_method, detection_args, detection_mode,
	max_retries, verification_timeout_hours`

func scanScanPathRow(scanner interface {
	Scan(dest ...interface{}) error
}, p *ScanPath) error {
	return scanner.Scan(
		&p.ID, &p.LocalPath, &p.ArrPath, &p.ArrInstanceID, &p.Enabled,
		&p.AutoRemediate, &p.DryRun, &p.DetectionMethod, &p.DetectionArgs,
		&p.DetectionMode, &p.MaxRetries, &p.VerificationTimeoutHours,
	)
}

// Count returns the total number of rows.
func (r *ScanPathRepository) Count(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scan_paths`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count scan_paths: %w", err)
	}
	return n, nil
}

// ListAll returns every row, ordered by id ASC.
func (r *ScanPathRepository) ListAll(ctx context.Context) ([]ScanPath, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+scanPathColumns+` FROM scan_paths ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query scan_paths: %w", err)
	}
	defer rows.Close()

	var paths []ScanPath
	for rows.Next() {
		var p ScanPath
		if err := scanScanPathRow(rows, &p); err != nil {
			return nil, fmt.Errorf("scan scan_paths row: %w", err)
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scan_paths: %w", err)
	}
	return paths, nil
}

// ListEnabled returns rows where enabled = 1, ordered by id ASC.
func (r *ScanPathRepository) ListEnabled(ctx context.Context) ([]ScanPath, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+scanPathColumns+` FROM scan_paths WHERE enabled = 1 ORDER BY id ASC`)
	if err != nil {
		return nil, fmt.Errorf("query enabled scan_paths: %w", err)
	}
	defer rows.Close()

	var paths []ScanPath
	for rows.Next() {
		var p ScanPath
		if err := scanScanPathRow(rows, &p); err != nil {
			return nil, fmt.Errorf("scan scan_paths row: %w", err)
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scan_paths: %w", err)
	}
	return paths, nil
}

// ListOrderedByLocalPath returns every row, ordered alphabetically by
// local_path (used by the path-health stats endpoint to present a stable
// UI ordering independent of insertion order).
func (r *ScanPathRepository) ListOrderedByLocalPath(ctx context.Context) ([]ScanPath, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+scanPathColumns+` FROM scan_paths ORDER BY local_path`)
	if err != nil {
		return nil, fmt.Errorf("query scan_paths ordered: %w", err)
	}
	defer rows.Close()

	var paths []ScanPath
	for rows.Next() {
		var p ScanPath
		if err := scanScanPathRow(rows, &p); err != nil {
			return nil, fmt.Errorf("scan scan_paths row: %w", err)
		}
		paths = append(paths, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scan_paths: %w", err)
	}
	return paths, nil
}

// GetByID returns the row matching id, or ErrNotFound.
func (r *ScanPathRepository) GetByID(ctx context.Context, id int64) (ScanPath, error) {
	var p ScanPath
	err := scanScanPathRow(
		r.db.QueryRowContext(ctx, `SELECT `+scanPathColumns+` FROM scan_paths WHERE id = ?`, id),
		&p,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ScanPath{}, ErrNotFound
	}
	if err != nil {
		return ScanPath{}, fmt.Errorf("get scan_path: %w", err)
	}
	return p, nil
}

// FindIDByLocalPath returns the id of the row whose local_path matches,
// or ErrNotFound if none exists. Used by config-import dedup.
func (r *ScanPathRepository) FindIDByLocalPath(ctx context.Context, localPath string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`SELECT id FROM scan_paths WHERE local_path = ?`, localPath).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("find scan_path by local_path: %w", err)
	}
	return id, nil
}

// FindEnabledIDByLocalPath returns the id of an *enabled* row whose
// local_path matches, or ErrNotFound. Used by the rescan path to refuse
// scans against disabled or unknown paths.
func (r *ScanPathRepository) FindEnabledIDByLocalPath(ctx context.Context, localPath string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`SELECT id FROM scan_paths WHERE local_path = ? AND enabled = 1`, localPath).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("find enabled scan_path by local_path: %w", err)
	}
	return id, nil
}

// Create inserts a new row and returns its id.
func (r *ScanPathRepository) Create(ctx context.Context, f ScanPathFields) (int64, error) {
	var detectionArgs interface{}
	if f.DetectionArgsJSON != "" {
		detectionArgs = f.DetectionArgsJSON
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO scan_paths
		(local_path, arr_path, arr_instance_id, enabled, auto_remediate, dry_run,
		 detection_method, detection_args, detection_mode, max_retries, verification_timeout_hours)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		f.LocalPath, f.ArrPath, f.ArrInstanceID, f.Enabled, f.AutoRemediate, f.DryRun,
		f.DetectionMethod, detectionArgs, f.DetectionMode, f.MaxRetries, f.VerificationTimeoutHours,
	)
	if err != nil {
		return 0, fmt.Errorf("insert scan_path: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// Update replaces all editable columns of the row matching id.
func (r *ScanPathRepository) Update(ctx context.Context, id int64, f ScanPathFields) error {
	var detectionArgs interface{}
	if f.DetectionArgsJSON != "" {
		detectionArgs = f.DetectionArgsJSON
	}
	_, err := r.db.ExecContext(ctx, `UPDATE scan_paths SET
		local_path = ?, arr_path = ?, arr_instance_id = ?, enabled = ?,
		auto_remediate = ?, dry_run = ?, detection_method = ?, detection_args = ?,
		detection_mode = ?, max_retries = ?, verification_timeout_hours = ?
		WHERE id = ?`,
		f.LocalPath, f.ArrPath, f.ArrInstanceID, f.Enabled,
		f.AutoRemediate, f.DryRun, f.DetectionMethod, detectionArgs,
		f.DetectionMode, f.MaxRetries, f.VerificationTimeoutHours, id,
	)
	if err != nil {
		return fmt.Errorf("update scan_path: %w", err)
	}
	return nil
}

// Delete removes the row matching id. Returns nil if no row matched.
func (r *ScanPathRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM scan_paths WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete scan_path: %w", err)
	}
	return nil
}
