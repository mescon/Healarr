package services

import (
	"context"
	"database/sql"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/repository"
)

// EventReplayService replays unprocessed events on startup.
// This fixes a gap in event sourcing where events persisted to the database
// may not have been delivered to in-memory subscribers before a restart.
type EventReplayService struct {
	db       *sql.DB
	events   *repository.EventRepository
	eventBus *eventbus.EventBus
}

// NewEventReplayService creates a new event replay service.
func NewEventReplayService(db *sql.DB, eventBus *eventbus.EventBus) *EventReplayService {
	return &EventReplayService{db: db, events: repository.NewEventRepository(db), eventBus: eventBus}
}

// ReplayUnprocessedEvents finds CorruptionDetected events that have no subsequent
// state transitions and republishes them to the event bus.
// This should be called AFTER all services have subscribed to events but BEFORE
// the recovery service runs.
func (s *EventReplayService) ReplayUnprocessedEvents() error {
	// Find CorruptionDetected events with no subsequent events for the same
	// aggregate. These are events that were persisted but the remediator
	// never processed them (e.g., due to immediate restart after publishing).
	events, skipped, err := s.events.ListUnprocessed(context.Background(), domain.CorruptionDetected)
	if err != nil {
		return err
	}
	if skipped > 0 {
		logger.Warnf("Event replay: skipped %d unparseable event row(s)", skipped)
	}

	var replayed int
	for _, event := range events {
		// Republish to in-memory subscribers (skip DB persist since it already exists)
		if err := s.eventBus.RepublishToSubscribers(event); err != nil {
			logger.Warnf("Failed to replay event %s: %v", event.AggregateID, err)
			continue
		}

		replayed++
		logger.Infof("Replayed unprocessed event: %s (%s)", event.AggregateID, event.EventType)
	}

	if replayed > 0 {
		logger.Infof("Event replay complete: %d events replayed", replayed)
	}

	return nil
}
