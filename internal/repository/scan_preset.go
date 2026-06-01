package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ScanPreset is a row from the scan_presets table (see migration 009).
// It bundles the set of scan-related field values that the UI applies to
// a scan path with one click.
//
// The fields are a strict mirror of the per-path scan configuration on
// scan_paths so that "apply preset to path" is a column-for-column copy
// with no translation. detection_args is the JSON encoding of []string;
// the nullable Thorough/Hwaccel columns follow the same NULL-means-
// inherit semantics as the equivalent columns on scan_paths (added in
// migration 008).
type ScanPreset struct {
	ID                      int64
	Name                    string
	Description             string
	DetectionMethod         string
	DetectionMode           string
	DetectionArgs           sql.NullString // JSON-encoded []string, or NULL
	ThoroughDurationSeconds sql.NullInt64
	ThoroughTimeoutSeconds  sql.NullInt64
	Hwaccel                 sql.NullString
	IsBuiltin               bool
}

// ScanPresetFields is the writable subset for Create/Update. IsBuiltin is
// not present here: built-ins are seeded by migration 009 and never
// authored through this API. The handler layer rejects any attempt to
// flip an existing row's is_builtin flag.
type ScanPresetFields struct {
	Name                    string
	Description             string
	DetectionMethod         string
	DetectionMode           string
	DetectionArgsJSON       string
	ThoroughDurationSeconds sql.NullInt64
	ThoroughTimeoutSeconds  sql.NullInt64
	Hwaccel                 sql.NullString
}

// ScanPresetRepository wraps the scan_presets table.
type ScanPresetRepository struct {
	db *sql.DB
}

// NewScanPresetRepository returns a repository backed by db.
func NewScanPresetRepository(db *sql.DB) *ScanPresetRepository {
	return &ScanPresetRepository{db: db}
}

const scanPresetColumns = `id, name, description, detection_method, detection_mode,
	detection_args, thorough_duration_seconds, thorough_timeout_seconds, hwaccel, is_builtin`

func scanScanPresetRow(scanner interface {
	Scan(dest ...interface{}) error
}, p *ScanPreset) error {
	var isBuiltin int
	if err := scanner.Scan(
		&p.ID, &p.Name, &p.Description, &p.DetectionMethod, &p.DetectionMode,
		&p.DetectionArgs, &p.ThoroughDurationSeconds, &p.ThoroughTimeoutSeconds,
		&p.Hwaccel, &isBuiltin,
	); err != nil {
		return err
	}
	p.IsBuiltin = isBuiltin != 0
	return nil
}

// ListAll returns every preset, with built-ins first (ordered by id so
// the migration-defined order is preserved) followed by customs in name
// order. This is the order the UI dropdown renders.
func (r *ScanPresetRepository) ListAll(ctx context.Context) ([]ScanPreset, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT `+scanPresetColumns+`
		 FROM scan_presets
		 ORDER BY is_builtin DESC, CASE WHEN is_builtin = 1 THEN id END ASC, name ASC`)
	if err != nil {
		return nil, fmt.Errorf("query scan_presets: %w", err)
	}
	defer rows.Close()

	var presets []ScanPreset
	for rows.Next() {
		var p ScanPreset
		if err := scanScanPresetRow(rows, &p); err != nil {
			return nil, fmt.Errorf("scan scan_presets row: %w", err)
		}
		presets = append(presets, p)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scan_presets: %w", err)
	}
	return presets, nil
}

// GetByID returns the row matching id, or ErrNotFound.
func (r *ScanPresetRepository) GetByID(ctx context.Context, id int64) (ScanPreset, error) {
	var p ScanPreset
	err := scanScanPresetRow(
		r.db.QueryRowContext(ctx, `SELECT `+scanPresetColumns+` FROM scan_presets WHERE id = ?`, id),
		&p,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ScanPreset{}, ErrNotFound
	}
	if err != nil {
		return ScanPreset{}, fmt.Errorf("get scan_preset: %w", err)
	}
	return p, nil
}

// Create inserts a new custom preset and returns its id. is_builtin is
// always 0 for rows created via this path; built-ins come from
// migration 009 only.
func (r *ScanPresetRepository) Create(ctx context.Context, f ScanPresetFields) (int64, error) {
	var detectionArgs interface{}
	if f.DetectionArgsJSON != "" {
		detectionArgs = f.DetectionArgsJSON
	}
	res, err := r.db.ExecContext(ctx, `INSERT INTO scan_presets
		(name, description, detection_method, detection_mode, detection_args,
		 thorough_duration_seconds, thorough_timeout_seconds, hwaccel, is_builtin)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, 0)`,
		f.Name, f.Description, f.DetectionMethod, f.DetectionMode, detectionArgs,
		f.ThoroughDurationSeconds, f.ThoroughTimeoutSeconds, f.Hwaccel,
	)
	if err != nil {
		return 0, fmt.Errorf("insert scan_preset: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// Update replaces all editable columns of the row matching id. Callers
// MUST refuse the request earlier if the row is built-in; the repo
// trusts the handler to have made that check (consistent with how the
// rest of the repos here treat permission gates).
func (r *ScanPresetRepository) Update(ctx context.Context, id int64, f ScanPresetFields) error {
	var detectionArgs interface{}
	if f.DetectionArgsJSON != "" {
		detectionArgs = f.DetectionArgsJSON
	}
	_, err := r.db.ExecContext(ctx, `UPDATE scan_presets SET
		name = ?, description = ?, detection_method = ?, detection_mode = ?,
		detection_args = ?, thorough_duration_seconds = ?,
		thorough_timeout_seconds = ?, hwaccel = ?
		WHERE id = ?`,
		f.Name, f.Description, f.DetectionMethod, f.DetectionMode, detectionArgs,
		f.ThoroughDurationSeconds, f.ThoroughTimeoutSeconds, f.Hwaccel, id,
	)
	if err != nil {
		return fmt.Errorf("update scan_preset: %w", err)
	}
	return nil
}

// Delete removes the row matching id. Like Update, the handler is
// responsible for refusing the request on built-in rows.
func (r *ScanPresetRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM scan_presets WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete scan_preset: %w", err)
	}
	return nil
}
