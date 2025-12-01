package eventbus

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/mescon/Healarr/internal/db"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/logger"
)

// Publisher defines the interface for publishing events.
// This interface enables testing with mock implementations.
type Publisher interface {
	Publish(event domain.Event) error
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

// Shutdown stops all subscriber goroutines and waits for them to finish
func (eb *EventBus) Shutdown() {
	close(eb.stopChan)
	eb.wg.Wait()
	logger.Infof("EventBus shutdown complete")
}
