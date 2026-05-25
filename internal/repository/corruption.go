package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CorruptionStatus is a row from the corruption_status view — the
// event-sourced projection that derives each corruption aggregate's
// current state from its append-only event stream.
type CorruptionStatus struct {
	CorruptionID   string
	CurrentState   string
	RetryCount     int
	FilePath       string
	PathID         sql.NullInt64
	LastError      sql.NullString
	CorruptionType sql.NullString
	DetectedAt     string
	LastUpdatedAt  string
}

// ResolvedCorruption is the trimmed shape the remediations list needs.
type ResolvedCorruption struct {
	CorruptionID  string
	FilePath      string
	LastUpdatedAt string
}

// CorruptionEvent is one row of an aggregate's event history.
type CorruptionEvent struct {
	EventType string
	EventData []byte // raw JSON; caller unmarshals
	CreatedAt string
}

// CorruptionRepository wraps reads against the corruption_status view and
// the corruption-aggregate rows of the events table, plus the event
// deletion used by the "delete corruption" action.
type CorruptionRepository struct {
	db *sql.DB
}

// NewCorruptionRepository returns a repository backed by db.
func NewCorruptionRepository(db *sql.DB) *CorruptionRepository {
	return &CorruptionRepository{db: db}
}

// corruptionStatusColumns is the SELECT list matching CorruptionStatus.
const corruptionStatusColumns = `corruption_id, current_state, retry_count, file_path, path_id, last_error, detected_at, last_updated_at, corruption_type`

func scanCorruptionStatusRow(scanner interface {
	Scan(dest ...interface{}) error
}, cs *CorruptionStatus) error {
	return scanner.Scan(
		&cs.CorruptionID, &cs.CurrentState, &cs.RetryCount, &cs.FilePath,
		&cs.PathID, &cs.LastError, &cs.DetectedAt, &cs.LastUpdatedAt, &cs.CorruptionType,
	)
}

// CountFiltered returns the number of corruption_status rows matching the
// given WHERE clause.
//
// IMPORTANT: whereClause is interpolated directly into the SQL. The caller
// MUST build it from fixed fragments + ? placeholders (the handler's
// statusFilterClauses map and a parameterized path_id predicate); user
// input belongs in args, never in whereClause. This mirrors
// ScanRepository.ListPaged's contract.
func (r *CorruptionRepository) CountFiltered(ctx context.Context, whereClause string, args ...interface{}) (int, error) {
	query := "SELECT COUNT(*) FROM corruption_status" + whereClause //nolint:gosec // whereClause is fixed fragments; values in args. See method doc.
	var n int
	if err := r.db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count corruptions: %w", err)
	}
	return n, nil
}

// ListFiltered returns corruption_status rows matching whereClause, sorted
// by orderByClause (allowlist-validated by the caller), paginated.
//
// The final two positional args are LIMIT and OFFSET; they are appended
// after the caller-supplied filter args. Same SQL-injection contract as
// CountFiltered + the orderByClause contract from ScanRepository.
func (r *CorruptionRepository) ListFiltered(ctx context.Context, whereClause, orderByClause string, limit, offset int, args ...interface{}) ([]CorruptionStatus, error) {
	query := fmt.Sprintf( //nolint:gosec // whereClause/orderByClause are caller-validated; values in args. See method doc.
		"SELECT %s FROM corruption_status%s %s LIMIT ? OFFSET ?",
		corruptionStatusColumns, whereClause, orderByClause,
	)
	args = append(args, limit, offset)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query corruptions: %w", err)
	}
	defer rows.Close()

	var out []CorruptionStatus
	for rows.Next() {
		var cs CorruptionStatus
		if err := scanCorruptionStatusRow(rows, &cs); err != nil {
			return nil, fmt.Errorf("scan corruption row: %w", err)
		}
		out = append(out, cs)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate corruptions: %w", err)
	}
	return out, nil
}

// CountByState returns the number of corruption_status rows in the given state.
func (r *CorruptionRepository) CountByState(ctx context.Context, state string) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM corruption_status WHERE current_state = ?`, state).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count corruptions by state: %w", err)
	}
	return n, nil
}

// ListByState returns the trimmed resolved-corruption shape for rows in the
// given state, ordered by last_updated_at DESC, paginated.
func (r *CorruptionRepository) ListByState(ctx context.Context, state string, limit, offset int) ([]ResolvedCorruption, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT corruption_id, file_path, last_updated_at FROM corruption_status
		 WHERE current_state = ? ORDER BY last_updated_at DESC LIMIT ? OFFSET ?`,
		state, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query corruptions by state: %w", err)
	}
	defer rows.Close()

	out := make([]ResolvedCorruption, 0)
	for rows.Next() {
		var rc ResolvedCorruption
		if err := rows.Scan(&rc.CorruptionID, &rc.FilePath, &rc.LastUpdatedAt); err != nil {
			return nil, fmt.Errorf("scan resolved corruption row: %w", err)
		}
		out = append(out, rc)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate resolved corruptions: %w", err)
	}
	return out, nil
}

// LatestEventData returns the raw event_data JSON for the most recent (or
// oldest, per order) event of the given type for an aggregate. order must
// be "ASC" or "DESC"; any other value defaults to "DESC". Returns
// ErrNotFound when no such event exists or its event_data is NULL.
func (r *CorruptionRepository) LatestEventData(ctx context.Context, aggregateID, eventType, order string) (string, error) {
	if order != "ASC" && order != "DESC" {
		order = "DESC"
	}
	query := fmt.Sprintf( //nolint:gosec // order is constrained to ASC/DESC above
		`SELECT event_data FROM events WHERE aggregate_id = ? AND event_type = ? ORDER BY created_at %s LIMIT 1`,
		order,
	)
	var data sql.NullString
	err := r.db.QueryRowContext(ctx, query, aggregateID, eventType).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("query latest event data: %w", err)
	}
	if !data.Valid {
		return "", ErrNotFound
	}
	return data.String, nil
}

// ListEvents returns the full event history for an aggregate, oldest first.
func (r *CorruptionRepository) ListEvents(ctx context.Context, aggregateID string) ([]CorruptionEvent, error) {
	rows, err := r.db.QueryContext(ctx,
		`SELECT event_type, event_data, created_at FROM events WHERE aggregate_id = ? ORDER BY created_at ASC`,
		aggregateID)
	if err != nil {
		return nil, fmt.Errorf("query event history: %w", err)
	}
	defer rows.Close()

	out := make([]CorruptionEvent, 0)
	for rows.Next() {
		var e CorruptionEvent
		if err := rows.Scan(&e.EventType, &e.EventData, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan event row: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate events: %w", err)
	}
	return out, nil
}

// CorruptionDetectedFileInfo extracts file_path and path_id from the
// aggregate's CorruptionDetected event. Returns ErrNotFound if there is
// no such event or it has no file_path.
func (r *CorruptionRepository) CorruptionDetectedFileInfo(ctx context.Context, aggregateID string) (filePath string, pathID sql.NullInt64, err error) {
	var fp sql.NullString
	err = r.db.QueryRowContext(ctx, `
		SELECT
			json_extract(event_data, '$.file_path'),
			json_extract(event_data, '$.path_id')
		FROM events
		WHERE aggregate_id = ? AND event_type = 'CorruptionDetected'
		LIMIT 1
	`, aggregateID).Scan(&fp, &pathID)
	if errors.Is(err, sql.ErrNoRows) {
		return "", sql.NullInt64{}, ErrNotFound
	}
	if err != nil {
		return "", sql.NullInt64{}, fmt.Errorf("query corruption file info: %w", err)
	}
	if !fp.Valid || fp.String == "" {
		return "", sql.NullInt64{}, ErrNotFound
	}
	return fp.String, pathID, nil
}

// DeleteEvents removes all events for an aggregate and returns the row count.
func (r *CorruptionRepository) DeleteEvents(ctx context.Context, aggregateID string) (int64, error) {
	res, err := r.db.ExecContext(ctx, `DELETE FROM events WHERE aggregate_id = ?`, aggregateID)
	if err != nil {
		return 0, fmt.Errorf("delete corruption events: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected: %w", err)
	}
	return n, nil
}
