package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
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

// CountSuccessfulRemediationsForPath counts how many times the file at filePath
// has been successfully remediated (reached VerificationSuccess) within the last
// windowDays days, across all corruption aggregates for that path. The
// remediation loop-breaker uses this: a file that keeps coming back corrupt even
// though we have already restored it to health several times is being
// re-corrupted by something we cannot fix by re-downloading (a transcode
// pipeline or failing storage), so auto-remediation should pause rather than
// thrash. The window lets a file that was healthy for a long time and then
// breaks again start fresh.
func (r *CorruptionRepository) CountSuccessfulRemediationsForPath(ctx context.Context, filePath string, windowDays int) (int, error) {
	var n int
	// windowDays is bound as a datetime modifier parameter, not interpolated.
	modifier := fmt.Sprintf("-%d days", windowDays)
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events vs
		WHERE vs.event_type = 'VerificationSuccess'
		  AND vs.created_at > datetime('now', ?)
		  AND vs.aggregate_id IN (
		      SELECT cd.aggregate_id FROM events cd
		      WHERE cd.event_type = 'CorruptionDetected'
		        AND json_extract(cd.event_data, '$.file_path') = ?
		  )
	`, modifier, filePath).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count successful remediations for path: %w", err)
	}
	return n, nil
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

// =============================================================================
// Dashboard / stats read-model aggregates
// =============================================================================

// CorruptionStateCounts is the dashboard breakdown of corruptions by lifecycle
// bucket, derived from the corruption_status view's current_state.
type CorruptionStateCounts struct {
	Resolved           int
	Orphaned           int
	InProgress         int
	ManualIntervention int
	Pending            int
	Failed             int
	Ignored            int
}

// DayCount is a (date, count) pair for time-series stats.
type DayCount struct {
	Date  string
	Count int
}

// TypeCount is a (corruption_type, count) pair.
type TypeCount struct {
	Type  string
	Count int
}

// Corruption lifecycle buckets: the ONE state-to-bucket mapping consumed by
// the dashboard StateCounts AND the /corruptions filter clauses, so the two
// views can never partition states differently again. Every lifecycle event
// type that can become corruption_summary.current_state must appear in
// exactly one bucket; the audit found nine states falling through (and two
// phantom types, SearchQueued/DownloadStarted, that no code publishes).
var (
	BucketResolved = []string{"VerificationSuccess"}
	BucketOrphaned = []string{"MaxRetriesReached"}
	// In flight: remediation machinery is actively working the item.
	BucketInProgress = []string{
		"RemediationQueued", "DeletionStarted", "DeletionCompleted",
		"SearchStarted", "SearchCompleted", "DownloadProgress",
		"FileDetected", "VerificationStarted", "RetryScheduled",
		"StuckRemediation", "ReleaseBlocklisted", "MonitorOverridden",
	}
	// Deliberately parked: the system stopped and a human decides.
	BucketManualIntervention = []string{
		"ImportBlocked", "ManuallyRemoved", "SearchExhausted",
		"RemediationPaused", "DownloadIgnored",
	}
	BucketPending = []string{"CorruptionDetected"}
	// Failed-but-retryable: counts toward the retry budget.
	BucketFailed = []string{
		"DeletionFailed", "SearchFailed", "VerificationFailed",
		"DownloadFailed", "DownloadTimeout",
	}
	BucketIgnored = []string{"CorruptionIgnored"}
)

// InClause renders a bucket as a SQL IN(...) literal. Safe by construction:
// the inputs are the hardcoded bucket constants above, never user input.
func InClause(states []string) string {
	quoted := make([]string, len(states))
	for i, st := range states {
		quoted[i] = "'" + st + "'"
	}
	return "(" + strings.Join(quoted, ", ") + ")"
}

// StateCounts returns the dashboard breakdown of corruptions by lifecycle
// bucket in a single pass over corruption_status. Built from the shared
// bucket constants so it cannot drift from the /corruptions filters.
func (r *CorruptionRepository) StateCounts(ctx context.Context) (CorruptionStateCounts, error) {
	var c CorruptionStateCounts
	query := `
		SELECT
			COUNT(DISTINCT CASE WHEN current_state IN ` + InClause(BucketResolved) + ` THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state IN ` + InClause(BucketOrphaned) + ` THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state IN ` + InClause(BucketInProgress) + ` THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state IN ` + InClause(BucketManualIntervention) + ` THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state IN ` + InClause(BucketPending) + ` THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state IN ` + InClause(BucketFailed) + ` THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state IN ` + InClause(BucketIgnored) + ` THEN corruption_id END)
		FROM corruption_status
	`
	err := r.db.QueryRowContext(ctx, query).Scan(&c.Resolved, &c.Orphaned, &c.InProgress, &c.ManualIntervention, &c.Pending, &c.Failed, &c.Ignored)
	if err != nil {
		return CorruptionStateCounts{}, fmt.Errorf("query corruption state counts: %w", err)
	}
	return c, nil
}

// CountDetectedToday returns the number of CorruptionDetected events created
// today, excluding aggregates the user has ignored.
func (r *CorruptionRepository) CountDetectedToday(ctx context.Context) (int, error) {
	var n int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events e
		WHERE e.event_type = 'CorruptionDetected'
		AND substr(e.created_at, 1, 10) = date('now')
		AND NOT EXISTS (
			SELECT 1 FROM corruption_status cs
			WHERE cs.corruption_id = e.aggregate_id
			AND cs.current_state = 'CorruptionIgnored'
		)
	`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count corruptions today: %w", err)
	}
	return n, nil
}

// CountDetectedByDay returns daily CorruptionDetected counts over the last
// `days` days, oldest day first.
func (r *CorruptionRepository) CountDetectedByDay(ctx context.Context, days int) ([]DayCount, error) {
	// The cutoff is a literal like '-30 days' that SQLite's date() understands.
	cutoff := fmt.Sprintf("-%d days", days)
	rows, err := r.db.QueryContext(ctx, `
		SELECT substr(created_at, 1, 10) as date, COUNT(*) as count
		FROM events
		WHERE event_type = 'CorruptionDetected'
		AND substr(created_at, 1, 10) >= date('now', ?)
		GROUP BY substr(created_at, 1, 10)
		ORDER BY date ASC
	`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("query corruptions by day: %w", err)
	}
	defer rows.Close()

	out := make([]DayCount, 0)
	for rows.Next() {
		var d DayCount
		if err := rows.Scan(&d.Date, &d.Count); err != nil {
			return nil, fmt.Errorf("scan day-count row: %w", err)
		}
		out = append(out, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate day counts: %w", err)
	}
	return out, nil
}

// CountByCorruptionType returns CorruptionDetected counts grouped by the
// corruption_type field of the event data. A NULL type is reported as "".
func (r *CorruptionRepository) CountByCorruptionType(ctx context.Context) ([]TypeCount, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT json_extract(event_data, '$.corruption_type') as type, COUNT(*) as count
		FROM events
		WHERE event_type = 'CorruptionDetected'
		GROUP BY type
	`)
	if err != nil {
		return nil, fmt.Errorf("query corruptions by type: %w", err)
	}
	defer rows.Close()

	out := make([]TypeCount, 0)
	for rows.Next() {
		var typeName sql.NullString
		var count int
		if err := rows.Scan(&typeName, &count); err != nil {
			return nil, fmt.Errorf("scan type-count row: %w", err)
		}
		out = append(out, TypeCount{Type: typeName.String, Count: count})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate type counts: %w", err)
	}
	return out, nil
}

// PathCorruptionStats returns active, total, and resolved corruption counts
// for a single scan path.
func (r *CorruptionRepository) PathCorruptionStats(ctx context.Context, pathID int64) (active, total, resolved int, err error) {
	err = r.db.QueryRowContext(ctx, `
		SELECT
			COUNT(DISTINCT CASE WHEN current_state NOT IN ('VerificationSuccess', 'MaxRetriesReached', 'CorruptionIgnored') THEN corruption_id END),
			COUNT(DISTINCT corruption_id),
			COUNT(DISTINCT CASE WHEN current_state = 'VerificationSuccess' THEN corruption_id END)
		FROM corruption_status
		WHERE path_id = ?
	`, pathID).Scan(&active, &total, &resolved)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("query path corruption stats: %w", err)
	}
	return active, total, resolved, nil
}

// =============================================================================
// Active-corruption lookups (scanner dedup)
// =============================================================================

// HasActive reports whether the given file has an unresolved CorruptionDetected
// event within the last 7 days — used by the scanner to skip files already
// being remediated. "Active" means a CorruptionDetected with no subsequent
// VerificationSuccess or MaxRetriesReached for the same aggregate.
func (r *CorruptionRepository) HasActive(ctx context.Context, filePath string) (bool, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events e1
		WHERE e1.event_type = 'CorruptionDetected'
		AND json_extract(e1.event_data, '$.file_path') = ?
		AND e1.created_at > datetime('now', '-7 days')
		AND NOT EXISTS (
			SELECT 1 FROM events e2
			WHERE e2.aggregate_id = e1.aggregate_id
			AND e2.event_type IN ('VerificationSuccess', 'MaxRetriesReached')
		)
	`, filePath).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("check active corruption: %w", err)
	}
	return count > 0, nil
}

// ListActiveFilePathsUnderRoot returns the set of file paths under rootPath
// that have an active corruption (same definition as HasActive). Returned as
// a set so the scanner can do O(1) lookups while walking a path — avoids the
// N+1 HasActive query per file.
func (r *CorruptionRepository) ListActiveFilePathsUnderRoot(ctx context.Context, rootPath string) (map[string]bool, error) {
	// Match only files genuinely under rootPath by requiring a path-separator
	// boundary ("rootPath/..."). A bare "rootPath%" prefix would also match
	// sibling roots (e.g. "/media/TV" matching "/media/TV-Archive/..."). Trim a
	// trailing slash first so "/media/TV/" and "/media/TV" behave identically.
	rootPath = strings.TrimRight(rootPath, "/")
	rows, err := r.db.QueryContext(ctx, `
		SELECT DISTINCT json_extract(e1.event_data, '$.file_path') as file_path
		FROM events e1
		WHERE e1.event_type = 'CorruptionDetected'
		AND json_extract(e1.event_data, '$.file_path') LIKE ? || '/%'
		AND e1.created_at > datetime('now', '-7 days')
		AND NOT EXISTS (
			SELECT 1 FROM events e2
			WHERE e2.aggregate_id = e1.aggregate_id
			AND e2.event_type IN ('VerificationSuccess', 'MaxRetriesReached')
		)
	`, rootPath)
	if err != nil {
		return nil, fmt.Errorf("query active corruptions under root: %w", err)
	}
	defer rows.Close()

	result := make(map[string]bool)
	for rows.Next() {
		var filePath string
		if err := rows.Scan(&filePath); err != nil {
			return nil, fmt.Errorf("scan active-corruption file_path: %w", err)
		}
		result[filePath] = true
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active corruptions under root: %w", err)
	}
	return result, nil
}

// CountSuccessfulRemediationsForMedia counts VerificationSuccess events in
// the window whose remediation journey handled the given *arr media id
// (identified via the journey's DeletionCompleted event). This is the
// rename-proof sibling of CountSuccessfulRemediationsForPath: each
// remediation round typically imports the replacement under a NEW release
// filename, so a per-path counter never accumulates for exactly the
// recurring-corruption scenario the loop-breaker exists for.
func (r *CorruptionRepository) CountSuccessfulRemediationsForMedia(ctx context.Context, mediaID int64, windowDays int) (int, error) {
	var n int
	modifier := fmt.Sprintf("-%d days", windowDays)
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events vs
		WHERE vs.event_type = 'VerificationSuccess'
		  AND vs.created_at > datetime('now', ?)
		  AND vs.aggregate_id IN (
		      SELECT dc.aggregate_id FROM events dc
		      WHERE dc.event_type = 'DeletionCompleted'
		        AND json_extract(dc.event_data, '$.media_id') = ?
		  )
	`, modifier, mediaID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count successful remediations for media: %w", err)
	}
	return n, nil
}

// CountExhaustedRemediationsForMedia counts MaxRetriesReached events in the
// window for journeys that handled the given media id. Covers the
// never-succeeds loop: when remediation never produces a verified
// replacement there are no VerificationSuccess events to count, but each
// exhausted journey burned deletes, searches, and timeouts on the same
// media — that recurrence must also trip the loop-breaker.
func (r *CorruptionRepository) CountExhaustedRemediationsForMedia(ctx context.Context, mediaID int64, windowDays int) (int, error) {
	var n int
	modifier := fmt.Sprintf("-%d days", windowDays)
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events mr
		WHERE mr.event_type = 'MaxRetriesReached'
		  AND mr.created_at > datetime('now', ?)
		  AND mr.aggregate_id IN (
		      SELECT dc.aggregate_id FROM events dc
		      WHERE dc.event_type = 'DeletionCompleted'
		        AND json_extract(dc.event_data, '$.media_id') = ?
		  )
	`, modifier, mediaID).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count exhausted remediations for media: %w", err)
	}
	return n, nil
}
