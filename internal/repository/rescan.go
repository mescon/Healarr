package repository

import (
	"context"
	"database/sql"
	"fmt"
)

// PendingRescan is a row from the pending_rescans retry queue that's
// ready to be retried.
type PendingRescan struct {
	ID         int64
	FilePath   string
	PathID     sql.NullInt64
	RetryCount int
	MaxRetries int
}

// RescanStats is the pending/abandoned/resolved breakdown of the queue.
type RescanStats struct {
	Pending   int
	Abandoned int
	Resolved  int
}

// RescanRepository wraps the pending_rescans table — the queue of files
// that hit a recoverable (infrastructure) error during scanning and are
// awaiting a backoff-scheduled retry.
type RescanRepository struct {
	db *sql.DB
}

// NewRescanRepository returns a repository backed by db.
func NewRescanRepository(db *sql.DB) *RescanRepository {
	return &RescanRepository{db: db}
}

// Queue inserts (or, on file_path conflict, re-queues) a file for rescan.
//
// The next_retry_at expression encodes exponential backoff directly in
// SQL: 5 minutes initially, doubling each retry via a bit-shift, capped
// at 5 * 2^5 = 160 minutes. That schedule is persistence policy, so it
// lives here rather than being computed in Go and passed in.
func (r *RescanRepository) Queue(ctx context.Context, filePath string, pathID int64, errorType, errorMessage string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO pending_rescans (file_path, path_id, error_type, error_message, next_retry_at)
		VALUES (?, ?, ?, ?, datetime('now', '+5 minutes'))
		ON CONFLICT(file_path) DO UPDATE SET
			retry_count = retry_count + 1,
			last_attempt_at = CURRENT_TIMESTAMP,
			error_type = excluded.error_type,
			error_message = excluded.error_message,
			next_retry_at = datetime('now', '+' || (5 * (1 << MIN(retry_count, 5))) || ' minutes')
	`, filePath, pathID, errorType, errorMessage)
	if err != nil {
		return fmt.Errorf("queue rescan: %w", err)
	}
	return nil
}

// ListReady returns up to limit pending rescans whose next_retry_at has
// passed and that haven't exhausted their retries, oldest-due first.
func (r *RescanRepository) ListReady(ctx context.Context, limit int) ([]PendingRescan, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, file_path, path_id, retry_count, max_retries
		FROM pending_rescans
		WHERE status = 'pending'
		AND next_retry_at <= datetime('now')
		AND retry_count < max_retries
		ORDER BY next_retry_at ASC
		LIMIT ?
	`, limit)
	if err != nil {
		return nil, fmt.Errorf("query ready rescans: %w", err)
	}
	defer rows.Close()

	var out []PendingRescan
	for rows.Next() {
		var f PendingRescan
		if err := rows.Scan(&f.ID, &f.FilePath, &f.PathID, &f.RetryCount, &f.MaxRetries); err != nil {
			return nil, fmt.Errorf("scan pending rescan row: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate pending rescans: %w", err)
	}
	return out, nil
}

// MarkResolved closes a queue entry with the given status ("resolved" or
// "abandoned") and resolution detail.
func (r *RescanRepository) MarkResolved(ctx context.Context, id int64, status, resolution string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE pending_rescans
		SET status = ?, resolved_at = CURRENT_TIMESTAMP, resolution = ?
		WHERE id = ?
	`, status, resolution, id)
	if err != nil {
		return fmt.Errorf("mark rescan resolved: %w", err)
	}
	return nil
}

// BumpRetry increments the retry counter and reschedules the next attempt
// using the same exponential-backoff schedule as Queue (note the +1 in the
// shift: this row's retry_count is about to be incremented).
func (r *RescanRepository) BumpRetry(ctx context.Context, id int64, errorType, errorMessage string) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE pending_rescans
		SET retry_count = retry_count + 1,
		    last_attempt_at = CURRENT_TIMESTAMP,
		    error_type = ?,
		    error_message = ?,
		    next_retry_at = datetime('now', '+' || (5 * (1 << MIN(retry_count + 1, 5))) || ' minutes')
		WHERE id = ?
	`, errorType, errorMessage, id)
	if err != nil {
		return fmt.Errorf("bump rescan retry: %w", err)
	}
	return nil
}

// Stats returns the pending/abandoned/resolved counts for the queue.
func (r *RescanRepository) Stats(ctx context.Context) (RescanStats, error) {
	var s RescanStats
	err := r.db.QueryRowContext(ctx, `
		SELECT
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'abandoned' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'resolved' THEN 1 ELSE 0 END), 0)
		FROM pending_rescans
	`).Scan(&s.Pending, &s.Abandoned, &s.Resolved)
	if err != nil {
		return RescanStats{}, fmt.Errorf("query rescan stats: %w", err)
	}
	return s, nil
}
