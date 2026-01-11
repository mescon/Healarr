package services

import (
	"database/sql"
	"encoding/json"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/logger"
)

// EventReplayService replays unprocessed events on startup.
// This fixes a gap in event sourcing where events persisted to the database
// may not have been delivered to in-memory subscribers before a restart.
type EventReplayService struct {
	db       *sql.DB
	eventBus *eventbus.EventBus
}

// NewEventReplayService creates a new event replay service.
func NewEventReplayService(db *sql.DB, eventBus *eventbus.EventBus) *EventReplayService {
	return &EventReplayService{db: db, eventBus: eventBus}
}

// ReplayUnprocessedEvents finds CorruptionDetected events that have no subsequent
// state transitions and republishes them to the event bus.
// This should be called AFTER all services have subscribed to events but BEFORE
// the recovery service runs.
func (s *EventReplayService) ReplayUnprocessedEvents() error {
	// Find CorruptionDetected events with no subsequent events for the same aggregate.
	// These are events that were persisted but the remediator never processed them
	// (e.g., due to immediate restart after publishing).
	query := `
		SELECT e.id, e.aggregate_type, e.aggregate_id, e.event_type, e.event_data, e.event_version, e.created_at, e.user_id
		FROM events e
		WHERE e.event_type = ?
		AND NOT EXISTS (
			SELECT 1 FROM events e2
			WHERE e2.aggregate_id = e.aggregate_id
			AND e2.created_at > e.created_at
		)
		ORDER BY e.created_at ASC
	`

	rows, err := s.db.Query(query, domain.CorruptionDetected)
	if err != nil {
		return err
	}
	defer rows.Close()

	var replayed int
	for rows.Next() {
		var event domain.Event
		var userID sql.NullString
		var eventDataBytes []byte
		if err := rows.Scan(
			&event.ID,
			&event.AggregateType,
			&event.AggregateID,
			&event.EventType,
			&eventDataBytes,
			&event.EventVersion,
			&event.CreatedAt,
			&userID,
		); err != nil {
			logger.Warnf("Failed to scan event for replay: %v", err)
			continue
		}
		if userID.Valid {
			event.UserID = userID.String
		}

		// Unmarshal event data from JSON
		if len(eventDataBytes) > 0 {
			if err := json.Unmarshal(eventDataBytes, &event.EventData); err != nil {
				logger.Warnf("Failed to unmarshal event data for %s: %v", event.AggregateID, err)
				continue
			}
		}

		// Republish to in-memory subscribers (skip DB persist since it already exists)
		if err := s.eventBus.RepublishToSubscribers(event); err != nil {
			logger.Warnf("Failed to replay event %s: %v", event.AggregateID, err)
			continue
		}

		replayed++
		logger.Infof("Replayed unprocessed event: %s (%s)", event.AggregateID, event.EventType)
	}

	if err := rows.Err(); err != nil {
		return err
	}

	if replayed > 0 {
		logger.Infof("Event replay complete: %d events replayed", replayed)
	}

	return nil
}
