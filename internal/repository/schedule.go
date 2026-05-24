package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Schedule is a row from the scan_schedules table.
type Schedule struct {
	ID             int64
	ScanPathID     int64
	CronExpression string
	Enabled        bool
}

// ScheduleWithPath augments Schedule with the local_path of the joined
// scan_paths row — what the schedules list endpoint and the config-export
// path both need.
type ScheduleWithPath struct {
	Schedule
	LocalPath string
}

// ScheduleRepository wraps the scan_schedules table.
type ScheduleRepository struct {
	db *sql.DB
}

// NewScheduleRepository returns a repository backed by db.
func NewScheduleRepository(db *sql.DB) *ScheduleRepository {
	return &ScheduleRepository{db: db}
}

// ListEnabled returns rows where enabled = 1. Used by the scheduler at
// startup to (re)register cron jobs.
func (r *ScheduleRepository) ListEnabled(ctx context.Context) ([]Schedule, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT id, scan_path_id, cron_expression, enabled FROM scan_schedules WHERE enabled = 1`)
	if err != nil {
		return nil, fmt.Errorf("query enabled schedules: %w", err)
	}
	defer rows.Close()

	var out []Schedule
	for rows.Next() {
		var s Schedule
		if err := rows.Scan(&s.ID, &s.ScanPathID, &s.CronExpression, &s.Enabled); err != nil {
			return nil, fmt.Errorf("scan schedule row: %w", err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schedules: %w", err)
	}
	return out, nil
}

// ListWithPaths returns every schedule joined with its scan_path's
// local_path. Skips rows whose scan_path has been deleted (INNER JOIN).
func (r *ScheduleRepository) ListWithPaths(ctx context.Context) ([]ScheduleWithPath, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT s.id, s.scan_path_id, p.local_path, s.cron_expression, s.enabled
		FROM scan_schedules s
		JOIN scan_paths p ON s.scan_path_id = p.id
	`)
	if err != nil {
		return nil, fmt.Errorf("query schedules with paths: %w", err)
	}
	defer rows.Close()

	var out []ScheduleWithPath
	for rows.Next() {
		var sp ScheduleWithPath
		if err := rows.Scan(&sp.ID, &sp.ScanPathID, &sp.LocalPath, &sp.CronExpression, &sp.Enabled); err != nil {
			return nil, fmt.Errorf("scan schedule-with-path row: %w", err)
		}
		out = append(out, sp)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate schedules with paths: %w", err)
	}
	return out, nil
}

// GetByID returns the row matching id, or ErrNotFound.
func (r *ScheduleRepository) GetByID(ctx context.Context, id int64) (Schedule, error) {
	var s Schedule
	err := r.db.QueryRowContext(ctx,
		`SELECT id, scan_path_id, cron_expression, enabled FROM scan_schedules WHERE id = ?`, id,
	).Scan(&s.ID, &s.ScanPathID, &s.CronExpression, &s.Enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return Schedule{}, ErrNotFound
	}
	if err != nil {
		return Schedule{}, fmt.Errorf("get schedule: %w", err)
	}
	return s, nil
}

// FindIDByPathAndCron returns the id of the row with the given
// scan_path_id and cron_expression, or ErrNotFound. Used by config-import
// dedup so the same (path, cron) tuple isn't inserted twice.
func (r *ScheduleRepository) FindIDByPathAndCron(ctx context.Context, scanPathID int64, cronExpr string) (int64, error) {
	var id int64
	err := r.db.QueryRowContext(ctx,
		`SELECT id FROM scan_schedules WHERE scan_path_id = ? AND cron_expression = ?`,
		scanPathID, cronExpr).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("find schedule by (path, cron): %w", err)
	}
	return id, nil
}

// Create inserts a new schedule and returns its id.
func (r *ScheduleRepository) Create(ctx context.Context, scanPathID int64, cronExpr string, enabled bool) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled) VALUES (?, ?, ?)`,
		scanPathID, cronExpr, enabled)
	if err != nil {
		return 0, fmt.Errorf("insert schedule: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// Update updates the row's enabled flag and, if cronExpr is non-empty,
// the cron_expression. The "keep existing cron when string is empty"
// shape matches the existing PATCH semantics where omitting
// cron_expression means "leave it alone."
func (r *ScheduleRepository) Update(ctx context.Context, id int64, cronExpr string, enabled bool) error {
	var err error
	if cronExpr == "" {
		_, err = r.db.ExecContext(ctx,
			`UPDATE scan_schedules SET enabled = ? WHERE id = ?`, enabled, id)
	} else {
		_, err = r.db.ExecContext(ctx,
			`UPDATE scan_schedules SET enabled = ?, cron_expression = ? WHERE id = ?`,
			enabled, cronExpr, id)
	}
	if err != nil {
		return fmt.Errorf("update schedule: %w", err)
	}
	return nil
}

// Delete removes the row matching id. Returns nil if no row matched.
func (r *ScheduleRepository) Delete(ctx context.Context, id int64) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM scan_schedules WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete schedule: %w", err)
	}
	return nil
}

// DeleteOrphaned removes schedules whose scan_path_id no longer exists.
// Returns the count of removed rows.
func (r *ScheduleRepository) DeleteOrphaned(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx, `
		DELETE FROM scan_schedules
		WHERE scan_path_id NOT IN (SELECT id FROM scan_paths)
	`)
	if err != nil {
		return 0, fmt.Errorf("delete orphaned schedules: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}
