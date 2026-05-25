package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mescon/Healarr/internal/db"
	"github.com/mescon/Healarr/internal/domain"
)

// EventRepository owns the events table — the append-only event store that
// is the system's source of truth. It handles the persist (append) and the
// replay-on-startup read; higher-level read models (corruption_status,
// dashboard aggregates) live in their own repositories.
type EventRepository struct {
	db *sql.DB
}

// NewEventRepository returns a repository backed by sqlDB.
func NewEventRepository(sqlDB *sql.DB) *EventRepository {
	return &EventRepository{db: sqlDB}
}

// Append persists an event and returns its new row id.
//
// Uses db.ExecWithRetry rather than plain Exec: events are the event-
// sourcing source of truth, so a write dropped under transient SQLite BUSY
// would silently lose state history. This matches the retry the eventbus
// used inline before the repository extraction.
//
// Append marshals EventData to JSON and persists exactly the fields it is
// given — defaulting (CreatedAt, EventVersion) stays with the caller
// (eventbus), which owns publish policy.
func (r *EventRepository) Append(e domain.Event) (int64, error) {
	eventDataJSON, err := json.Marshal(e.EventData)
	if err != nil {
		return 0, fmt.Errorf("marshal event data: %w", err)
	}

	res, err := db.ExecWithRetry(r.db, `
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, e.AggregateType, e.AggregateID, e.EventType, eventDataJSON, e.EventVersion, e.CreatedAt, e.UserID)
	if err != nil {
		return 0, fmt.Errorf("persist event: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}
	return id, nil
}

// ListUnprocessed returns events of the given type that have no later event
// for the same aggregate — i.e. an aggregate whose stream ends at this
// event. Used by replay-on-startup to re-dispatch events that were
// persisted but never delivered to in-memory subscribers before a restart.
// Ordered oldest-first.
//
// Rows that fail to scan or whose event_data is unparseable JSON are
// skipped (not returned, not fatal): a corrupt persisted row can't be
// replayed regardless, and one bad row must not abort replay of the rest.
// The skipped count is returned so the caller can log/observe it. Only a
// query-level or iteration-level failure returns an error.
func (r *EventRepository) ListUnprocessed(ctx context.Context, eventType domain.EventType) (events []domain.Event, skipped int, err error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT e.id, e.aggregate_type, e.aggregate_id, e.event_type, e.event_data, e.event_version, e.created_at, e.user_id
		FROM events e
		WHERE e.event_type = ?
		AND NOT EXISTS (
			SELECT 1 FROM events e2
			WHERE e2.aggregate_id = e.aggregate_id
			AND e2.created_at > e.created_at
		)
		ORDER BY e.created_at ASC
	`, eventType)
	if err != nil {
		return nil, 0, fmt.Errorf("query unprocessed events: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		event, scanErr := scanEventRow(rows)
		if scanErr != nil {
			skipped++
			continue
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, skipped, fmt.Errorf("iterate events: %w", err)
	}
	return events, skipped, nil
}

// scanEventRow scans a full events row into a domain.Event, decoding the
// JSON event_data and the nullable user_id.
func scanEventRow(scanner interface {
	Scan(dest ...interface{}) error
}) (domain.Event, error) {
	var event domain.Event
	var userID sql.NullString
	var eventDataBytes []byte
	if err := scanner.Scan(
		&event.ID, &event.AggregateType, &event.AggregateID, &event.EventType,
		&eventDataBytes, &event.EventVersion, &event.CreatedAt, &userID,
	); err != nil {
		return domain.Event{}, fmt.Errorf("scan event row: %w", err)
	}
	if userID.Valid {
		event.UserID = userID.String
	}
	if len(eventDataBytes) > 0 {
		if err := json.Unmarshal(eventDataBytes, &event.EventData); err != nil {
			return domain.Event{}, fmt.Errorf("unmarshal event data for %s: %w", event.AggregateID, err)
		}
	}
	return event, nil
}

// FirstEventTime returns the created_at of the earliest event of the given
// type for an aggregate. Returns ErrNotFound when the aggregate has no such
// event. Used to measure elapsed time since a lifecycle milestone (e.g.
// "how long since CorruptionDetected").
func (r *EventRepository) FirstEventTime(ctx context.Context, aggregateID string, eventType domain.EventType) (time.Time, error) {
	var t time.Time
	err := r.db.QueryRowContext(ctx, `
		SELECT created_at FROM events
		WHERE aggregate_id = ? AND event_type = ?
		ORDER BY created_at ASC LIMIT 1
	`, aggregateID, eventType).Scan(&t)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, ErrNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("query first event time: %w", err)
	}
	return t, nil
}
