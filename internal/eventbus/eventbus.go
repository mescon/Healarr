package eventbus

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mescon/Healarr/internal/db"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/logger"
)

// Retry configuration for PublishWithRetry
const (
	publishMaxRetries = 3
	publishBaseDelay  = 100 * time.Millisecond
	publishMaxDelay   = 2 * time.Second
)

// Publisher defines the interface for publishing events.
// This interface enables testing with mock implementations.
type Publisher interface {
	Publish(event domain.Event) error
	PublishWithRetry(event domain.Event) error
	Subscribe(eventType domain.EventType, handler func(domain.Event))
}

// Ensure EventBus implements Publisher
var _ Publisher = (*EventBus)(nil)

type EventBus struct {
	db          *sql.DB
	subscribers map[domain.EventType][]chan domain.Event
	mu          sync.RWMutex
	stopChan    chan struct{}
	wg          sync.WaitGroup
}

func NewEventBus(db *sql.DB) *EventBus {
	return &EventBus{
		db:          db,
		subscribers: make(map[domain.EventType][]chan domain.Event),
		stopChan:    make(chan struct{}),
	}
}

func (eb *EventBus) Publish(event domain.Event) error {
	logger.Debugf("EventBus: Publishing event %s (ID: %d, AggregateID: %s)", event.EventType, event.ID, event.AggregateID)

	// 1. Store event in database (source of truth)
	eventDataJSON, err := json.Marshal(event.EventData)
	if err != nil {
		return fmt.Errorf("failed to marshal event data: %w", err)
	}

	// Set default values if missing
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC() // Use UTC for consistent SQLite date parsing
	}
	if event.EventVersion == 0 {
		event.EventVersion = 1
	}

	res, err := db.ExecWithRetry(eb.db, `
        INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at, user_id)
        VALUES (?, ?, ?, ?, ?, ?, ?)
    `, event.AggregateType, event.AggregateID, event.EventType, eventDataJSON, event.EventVersion, event.CreatedAt, event.UserID)

	if err != nil {
		return fmt.Errorf("failed to persist event: %w", err)
	}

	// Get the ID of the inserted event
	id, err := res.LastInsertId()
	if err == nil {
		event.ID = id
	}

	// 2. Publish to in-memory subscribers
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	if subscribers, ok := eb.subscribers[event.EventType]; ok {
		for _, ch := range subscribers {
			select {
			case ch <- event:
			default:
				// Non-blocking, drop if buffer full to prevent blocking the publisher
				// In a production system, we might want metrics here
			}
		}
	}

	return nil
}

// PublishWithRetry publishes an event with retry logic for transient failures.
// Use this for critical state-changing events where losing the event would cause
// inconsistent state (e.g., DeletionCompleted, SearchCompleted, VerificationSuccess).
// For informational events (DownloadProgress), use Publish() instead.
func (eb *EventBus) PublishWithRetry(event domain.Event) error {
	var lastErr error

	for attempt := 0; attempt <= publishMaxRetries; attempt++ {
		lastErr = eb.Publish(event)
		if lastErr == nil {
			return nil
		}

		// Don't retry marshal errors (data validation issue, not transient)
		if strings.Contains(lastErr.Error(), "marshal") {
			return lastErr
		}

		if attempt < publishMaxRetries {
			// Exponential backoff: 100ms, 200ms, 400ms
			delay := publishBaseDelay * time.Duration(1<<uint(attempt))
			if delay > publishMaxDelay {
				delay = publishMaxDelay
			}
			logger.Warnf("Event publish failed for %s (%s), retrying in %v (attempt %d/%d): %v",
				event.AggregateID, event.EventType, delay, attempt+1, publishMaxRetries, lastErr)
			time.Sleep(delay)
		}
	}

	return fmt.Errorf("failed to publish event %s after %d retries: %w", event.EventType, publishMaxRetries, lastErr)
}

func (eb *EventBus) Subscribe(eventType domain.EventType, handler func(domain.Event)) {
	ch := make(chan domain.Event, 100)

	eb.mu.Lock()
	eb.subscribers[eventType] = append(eb.subscribers[eventType], ch)
	eb.mu.Unlock()

	eb.wg.Add(1)
	go func() {
		defer eb.wg.Done()
		for {
			select {
			case event, ok := <-ch:
				if !ok {
					return // Channel closed
				}
				handler(event)
			case <-eb.stopChan:
				return // Shutdown signal received
			}
		}
	}()
}

// RepublishToSubscribers sends an already-persisted event to in-memory subscribers
// without re-persisting to the database. Used by the event replay service to
// deliver events that were persisted but not processed before a restart.
func (eb *EventBus) RepublishToSubscribers(event domain.Event) error {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	if subscribers, ok := eb.subscribers[event.EventType]; ok {
		for _, ch := range subscribers {
			select {
			case ch <- event:
			default:
				logger.Warnf("Subscriber buffer full for replayed event %s (%s)", event.AggregateID, event.EventType)
			}
		}
	}
	return nil
}

// Shutdown stops all subscriber goroutines and waits for them to finish
func (eb *EventBus) Shutdown() {
	close(eb.stopChan)
	eb.wg.Wait()
	logger.Infof("EventBus shutdown complete")
}
