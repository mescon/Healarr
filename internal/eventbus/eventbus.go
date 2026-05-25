package eventbus

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/repository"
	"github.com/mescon/Healarr/internal/safego"
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

// EventBus provides publish-subscribe messaging for domain events.
// Events are persisted to the database before being dispatched to subscribers.
type EventBus struct {
	db          *sql.DB
	events      *repository.EventRepository
	subscribers map[domain.EventType][]chan domain.Event
	mu          sync.RWMutex
	stopChan    chan struct{}
	wg          sync.WaitGroup
}

// NewEventBus creates a new EventBus with the given database connection.
func NewEventBus(db *sql.DB) *EventBus {
	return &EventBus{
		db:          db,
		events:      repository.NewEventRepository(db),
		subscribers: make(map[domain.EventType][]chan domain.Event),
		stopChan:    make(chan struct{}),
	}
}

func (eb *EventBus) Publish(event domain.Event) error {
	logger.Debugf("EventBus: Publishing event %s (ID: %d, AggregateID: %s)", event.EventType, event.ID, event.AggregateID)

	// Set default values if missing before persisting
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC() // Use UTC for consistent SQLite date parsing
	}
	if event.EventVersion == 0 {
		event.EventVersion = 1
	}

	// 1. Store event in database (source of truth)
	id, err := eb.events.Append(event)
	if err != nil {
		return fmt.Errorf("failed to persist event: %w", err)
	}
	event.ID = id

	// 2. Publish to in-memory subscribers. A dropped delivery here is not lost:
	// the event is persisted, and the EventReplayService catches unprocessed
	// CorruptionDetected events on restart (RecoveryService handles other stale items).
	eb.dispatch(event)

	return nil
}

// dispatch delivers an event to all in-memory subscribers for its type using a
// non-blocking send. If a subscriber's buffer is full the delivery is dropped
// (callers that persist first treat this as recoverable; volatile callers treat
// a dropped ephemeral event as benign). Does not touch the database.
func (eb *EventBus) dispatch(event domain.Event) {
	eb.mu.RLock()
	defer eb.mu.RUnlock()

	for _, ch := range eb.subscribers[event.EventType] {
		select {
		case ch <- event:
		default:
			logger.Warnf("EventBus: subscriber buffer full for %s (%s) - in-memory delivery skipped",
				event.AggregateID, event.EventType)
		}
	}
}

// PublishVolatile delivers a high-frequency, in-memory-only event to subscribers
// WITHOUT persisting it to the event store. Use it for ephemeral UI signals such
// as ScanProgress that are never replayed or queried back: persisting one per
// scanned file would add a synchronous, fsync'd INSERT to the scan hot path for
// no durable benefit. Durable scan state still lives in the scans table.
func (eb *EventBus) PublishVolatile(event domain.Event) {
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.EventVersion == 0 {
		event.EventVersion = 1
	}
	eb.dispatch(event)
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
	safego.Run("eventbus-subscriber", func() {
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
	})
}

// RepublishToSubscribers sends an already-persisted event to in-memory subscribers
// without re-persisting to the database. Used by the event replay service to
// deliver events that were persisted but not processed before a restart.
func (eb *EventBus) RepublishToSubscribers(event domain.Event) error {
	eb.dispatch(event)
	return nil
}

// Shutdown stops all subscriber goroutines and waits for them to finish
func (eb *EventBus) Shutdown() {
	close(eb.stopChan)
	eb.wg.Wait()
	logger.Infof("EventBus shutdown complete")
}
