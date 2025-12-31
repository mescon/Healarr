package eventbus

import (
	"database/sql"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/domain"
	_ "modernc.org/sqlite"
)

// newTestDB creates an in-memory SQLite database with the events table.
// This is a local helper to avoid import cycles with testutil.
// Uses shared cache mode for consistency across goroutines.
func newTestDB(t *testing.T) *sql.DB {
	t.Helper()
	// Use shared cache mode for in-memory database to work correctly with concurrent access
	db, err := sql.Open("sqlite", "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("Failed to open in-memory database: %v", err)
	}
	// Set max open connections to 1 for in-memory databases to avoid issues
	db.SetMaxOpenConns(1)

	// Configure SQLite
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		t.Fatalf("Failed to set pragma: %v", err)
	}
	if _, err := db.Exec("PRAGMA busy_timeout=5000"); err != nil {
		t.Fatalf("Failed to set pragma: %v", err)
	}

	// Create events table
	_, err = db.Exec(`
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			aggregate_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			event_data JSON NOT NULL,
			event_version INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_id TEXT
		)
	`)
	if err != nil {
		t.Fatalf("Failed to create events table: %v", err)
	}

	return db
}

// getEventsByAggregate retrieves all events for a given aggregate ID.
func getEventsByAggregate(t *testing.T, db *sql.DB, aggregateID string) []domain.Event {
	t.Helper()
	rows, err := db.Query(`
		SELECT id, aggregate_type, aggregate_id, event_type, event_data, event_version, created_at, user_id
		FROM events WHERE aggregate_id = ? ORDER BY id ASC
	`, aggregateID)
	if err != nil {
		t.Fatalf("Failed to query events: %v", err)
	}
	defer rows.Close()

	var events []domain.Event
	for rows.Next() {
		var e domain.Event
		var eventDataJSON string
		var userID sql.NullString
		if err := rows.Scan(&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType, &eventDataJSON, &e.EventVersion, &e.CreatedAt, &userID); err != nil {
			t.Fatalf("Failed to scan event: %v", err)
		}
		if err := json.Unmarshal([]byte(eventDataJSON), &e.EventData); err != nil {
			t.Fatalf("Failed to unmarshal event data: %v", err)
		}
		if userID.Valid {
			e.UserID = userID.String
		}
		events = append(events, e)
	}
	return events
}

// countEventsByType counts events of a given type.
func countEventsByType(t *testing.T, db *sql.DB, eventType domain.EventType) int {
	t.Helper()
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", eventType).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to count events: %v", err)
	}
	return count
}

// TestEventBus_PublishAndSubscribe tests that events are delivered to subscribers.
func TestEventBus_PublishAndSubscribe(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	// Track received events
	var received []domain.Event
	var mu sync.Mutex

	// Subscribe to corruption events
	eb.Subscribe(domain.CorruptionDetected, func(event domain.Event) {
		mu.Lock()
		received = append(received, event)
		mu.Unlock()
	})

	// Publish an event
	event := domain.Event{
		AggregateType: "corruption",
		AggregateID:   "test-123",
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/media/movies/test.mkv",
		},
	}

	err := eb.Publish(event)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Wait for async delivery
	time.Sleep(100 * time.Millisecond)

	// Verify event was received
	mu.Lock()
	if len(received) != 1 {
		t.Errorf("Expected 1 event, got %d", len(received))
	}
	if len(received) > 0 {
		if filePath, _ := received[0].GetString("file_path"); filePath != "/media/movies/test.mkv" {
			t.Errorf("Received event has wrong file_path: %q", filePath)
		}
	}
	mu.Unlock()
}

// TestEventBus_PublishPersistsToDatabase tests that events are stored in the database.
func TestEventBus_PublishPersistsToDatabase(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	event := domain.Event{
		AggregateType: "corruption",
		AggregateID:   "persist-test-456",
		EventType:     domain.DeletionCompleted,
		EventData: map[string]interface{}{
			"media_id": float64(789),
		},
	}

	err := eb.Publish(event)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Query the database to verify persistence
	events := getEventsByAggregate(t, db, "persist-test-456")

	if len(events) != 1 {
		t.Errorf("Expected 1 event in database, got %d", len(events))
	}

	if len(events) > 0 {
		if events[0].EventType != domain.DeletionCompleted {
			t.Errorf("Event type = %v, want %v", events[0].EventType, domain.DeletionCompleted)
		}
		if events[0].AggregateID != "persist-test-456" {
			t.Errorf("AggregateID = %q, want %q", events[0].AggregateID, "persist-test-456")
		}
	}
}

// TestEventBus_MultipleSubscribers tests that multiple subscribers receive the same event.
func TestEventBus_MultipleSubscribers(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	var count1, count2, count3 int
	var mu sync.Mutex

	// Three different subscribers for the same event type
	eb.Subscribe(domain.SearchCompleted, func(event domain.Event) {
		mu.Lock()
		count1++
		mu.Unlock()
	})
	eb.Subscribe(domain.SearchCompleted, func(event domain.Event) {
		mu.Lock()
		count2++
		mu.Unlock()
	})
	eb.Subscribe(domain.SearchCompleted, func(event domain.Event) {
		mu.Lock()
		count3++
		mu.Unlock()
	})

	// Publish an event
	event := domain.Event{
		AggregateType: "corruption",
		AggregateID:   "multi-sub-test",
		EventType:     domain.SearchCompleted,
		EventData:     map[string]interface{}{},
	}

	err := eb.Publish(event)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if count1 != 1 || count2 != 1 || count3 != 1 {
		t.Errorf("Expected all subscribers to receive 1 event, got counts: %d, %d, %d", count1, count2, count3)
	}
	mu.Unlock()
}

// TestEventBus_UnsubscribedEventType tests that events are not delivered to unrelated subscribers.
func TestEventBus_UnsubscribedEventType(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	var corruptionCount, searchCount int
	var mu sync.Mutex

	eb.Subscribe(domain.CorruptionDetected, func(event domain.Event) {
		mu.Lock()
		corruptionCount++
		mu.Unlock()
	})
	eb.Subscribe(domain.SearchCompleted, func(event domain.Event) {
		mu.Lock()
		searchCount++
		mu.Unlock()
	})

	// Publish only corruption event
	err := eb.Publish(domain.Event{
		AggregateType: "corruption",
		AggregateID:   "filter-test",
		EventType:     domain.CorruptionDetected,
		EventData:     map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	mu.Lock()
	if corruptionCount != 1 {
		t.Errorf("Expected 1 corruption event, got %d", corruptionCount)
	}
	if searchCount != 0 {
		t.Errorf("Expected 0 search events, got %d", searchCount)
	}
	mu.Unlock()
}

// TestEventBus_DefaultValues tests that default values are set on events.
func TestEventBus_DefaultValues(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	// Publish event with missing CreatedAt and EventVersion
	event := domain.Event{
		AggregateType: "corruption",
		AggregateID:   "default-values-test",
		EventType:     domain.CorruptionDetected,
		EventData:     map[string]interface{}{},
		// CreatedAt and EventVersion intentionally not set
	}

	beforePublish := time.Now()
	err := eb.Publish(event)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Query the database to verify defaults were set
	events := getEventsByAggregate(t, db, "default-values-test")

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	// EventVersion should default to 1
	if events[0].EventVersion != 1 {
		t.Errorf("EventVersion = %d, want 1", events[0].EventVersion)
	}

	// CreatedAt should be set to approximately now
	if events[0].CreatedAt.Before(beforePublish) {
		t.Errorf("CreatedAt (%v) should not be before publish time (%v)", events[0].CreatedAt, beforePublish)
	}
}

// TestEventBus_ConcurrentPublish tests thread-safety of concurrent publishes.
func TestEventBus_ConcurrentPublish(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	var receivedCount int
	var mu sync.Mutex

	eb.Subscribe(domain.ScanProgress, func(event domain.Event) {
		mu.Lock()
		receivedCount++
		mu.Unlock()
	})

	// Publish 50 events concurrently
	const numEvents = 50
	var wg sync.WaitGroup
	wg.Add(numEvents)

	for i := 0; i < numEvents; i++ {
		go func(n int) {
			defer wg.Done()
			event := domain.Event{
				AggregateType: "scan",
				AggregateID:   "concurrent-test",
				EventType:     domain.ScanProgress,
				EventData: map[string]interface{}{
					"progress": float64(n),
				},
			}
			if err := eb.Publish(event); err != nil {
				t.Errorf("Publish failed: %v", err)
			}
		}(i)
	}

	wg.Wait()
	time.Sleep(200 * time.Millisecond)

	// Check database has all events
	count := countEventsByType(t, db, domain.ScanProgress)
	if count != numEvents {
		t.Errorf("Expected %d events in database, got %d", numEvents, count)
	}

	// Subscriber should have received all events (unless buffer was full)
	mu.Lock()
	if receivedCount < numEvents/2 { // Allow some tolerance for dropped events
		t.Errorf("Expected at least %d received events, got %d", numEvents/2, receivedCount)
	}
	mu.Unlock()
}

// TestEventBus_Shutdown tests that Shutdown properly stops subscribers.
func TestEventBus_Shutdown(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)

	eb.Subscribe(domain.CorruptionDetected, func(event domain.Event) {
		// Subscriber handler
	})

	// Shutdown should complete without hanging
	done := make(chan struct{})
	go func() {
		eb.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// Shutdown completed successfully
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown timed out")
	}
}

// TestEventBus_PublishSetsEventID tests that the event ID is set after publish.
func TestEventBus_PublishSetsEventID(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	event := domain.Event{
		AggregateType: "corruption",
		AggregateID:   "id-test",
		EventType:     domain.CorruptionDetected,
		EventData:     map[string]interface{}{},
	}

	// Before publish, ID should be 0
	if event.ID != 0 {
		t.Errorf("Event ID before publish = %d, want 0", event.ID)
	}

	err := eb.Publish(event)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Note: The event ID is set on the local event variable in Publish,
	// but since Go passes by value, the original event won't have the ID.
	// This test verifies the database-assigned ID was retrieved.
	events := getEventsByAggregate(t, db, "id-test")
	if len(events) > 0 && events[0].ID == 0 {
		t.Error("Event in database should have non-zero ID")
	}
}

// TestPublisher_Interface verifies that EventBus implements Publisher interface.
func TestPublisher_Interface(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	// This compiles only if EventBus implements Publisher
	var publisher Publisher = NewEventBus(db)

	// Verify we can use interface methods
	_ = publisher.Publish(domain.Event{
		AggregateType: "test",
		AggregateID:   "interface-test",
		EventType:     domain.CorruptionDetected,
		EventData:     map[string]interface{}{},
	})
	publisher.Subscribe(domain.CorruptionDetected, func(event domain.Event) {})

	// Shutdown via type assertion
	if eb, ok := publisher.(*EventBus); ok {
		eb.Shutdown()
	}
}

// TestEventBus_Publish_MarshalError tests that Publish returns an error when EventData cannot be marshaled.
func TestEventBus_Publish_MarshalError(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	// Create an event with a value that cannot be JSON marshaled.
	// Functions and channels cannot be marshaled.
	event := domain.Event{
		AggregateType: "test",
		AggregateID:   "marshal-error-test",
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"unmarshalable": func() {}, // Functions cannot be JSON marshaled
		},
	}

	err := eb.Publish(event)
	if err == nil {
		t.Error("Expected error when EventData contains unmarshalable value")
	}

	// Verify the error message mentions marshaling
	if err != nil && !containsString(err.Error(), "marshal") {
		t.Errorf("Expected error about marshaling, got: %v", err)
	}
}

// TestEventBus_Publish_DatabaseError tests that Publish returns an error on database failure.
func TestEventBus_Publish_DatabaseError(t *testing.T) {
	db := newTestDB(t)

	eb := NewEventBus(db)
	defer eb.Shutdown()

	// Close the database to simulate a failure
	db.Close()

	event := domain.Event{
		AggregateType: "test",
		AggregateID:   "db-error-test",
		EventType:     domain.CorruptionDetected,
		EventData:     map[string]interface{}{},
	}

	err := eb.Publish(event)
	if err == nil {
		t.Error("Expected error when database is closed")
	}

	// Verify the error message mentions persistence
	if err != nil && !containsString(err.Error(), "persist") {
		t.Errorf("Expected error about persisting event, got: %v", err)
	}
}

// TestEventBus_Subscribe_ChannelClosed tests the channel closed path in subscriber goroutine.
func TestEventBus_Subscribe_ChannelClosed(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)

	var receivedCount int
	var mu sync.Mutex

	// Subscribe - this creates a channel and starts a goroutine
	eb.Subscribe(domain.CorruptionDetected, func(event domain.Event) {
		mu.Lock()
		receivedCount++
		mu.Unlock()
	})

	// Publish an event - should be received
	err := eb.Publish(domain.Event{
		AggregateType: "test",
		AggregateID:   "channel-close-test",
		EventType:     domain.CorruptionDetected,
		EventData:     map[string]interface{}{},
	})
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if receivedCount != 1 {
		t.Errorf("Expected 1 event before shutdown, got %d", receivedCount)
	}
	mu.Unlock()

	// Now shutdown - this closes stopChan, causing the subscriber to exit
	eb.Shutdown()

	// After shutdown, the subscriber goroutine should have exited
	// We can't directly test if the goroutine exited, but shutdown should complete
}

// TestEventBus_BufferFull_DropsEvent tests that events are dropped when subscriber buffer is full.
func TestEventBus_BufferFull_DropsEvent(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	// Create a subscriber that blocks forever (until test cleanup)
	blocker := make(chan struct{})
	defer close(blocker)

	var startedBlocking sync.WaitGroup
	startedBlocking.Add(1)
	var firstCall bool

	eb.Subscribe(domain.CorruptionDetected, func(event domain.Event) {
		if !firstCall {
			firstCall = true
			startedBlocking.Done()
		}
		// Block indefinitely (until test ends)
		<-blocker
	})

	// Publish one event to trigger the blocking handler
	err := eb.Publish(domain.Event{
		AggregateType: "test",
		AggregateID:   "buffer-test-trigger",
		EventType:     domain.CorruptionDetected,
		EventData:     map[string]interface{}{"idx": 0},
	})
	if err != nil {
		t.Fatalf("First publish failed: %v", err)
	}

	// Wait for the handler to start blocking
	startedBlocking.Wait()

	// Now publish more events than the buffer can hold (buffer is 100).
	// Since the handler is blocked, these should fill the buffer then be dropped.
	for i := 1; i <= 150; i++ {
		_ = eb.Publish(domain.Event{
			AggregateType: "test",
			AggregateID:   "buffer-test",
			EventType:     domain.CorruptionDetected,
			EventData:     map[string]interface{}{"idx": i},
		})
	}

	// Events should still be in database despite subscriber buffer overflow
	count := countEventsByType(t, db, domain.CorruptionDetected)
	if count < 150 {
		t.Errorf("Expected at least 150 events in database, got %d", count)
	}
}

// TestEventBus_NoSubscribers tests publishing when there are no subscribers for the event type.
func TestEventBus_NoSubscribers(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	// Subscribe to a different event type
	var receivedCount int
	var mu sync.Mutex
	eb.Subscribe(domain.SearchCompleted, func(event domain.Event) {
		mu.Lock()
		receivedCount++
		mu.Unlock()
	})

	// Publish an event type with no subscribers - should still persist to DB
	event := domain.Event{
		AggregateType: "test",
		AggregateID:   "no-subscribers-test",
		EventType:     domain.DeletionFailed, // No subscribers for this type
		EventData:     map[string]interface{}{},
	}

	err := eb.Publish(event)
	if err != nil {
		t.Fatalf("Publish should succeed even with no subscribers: %v", err)
	}

	// Verify event was persisted
	events := getEventsByAggregate(t, db, "no-subscribers-test")
	if len(events) != 1 {
		t.Errorf("Expected 1 event in database, got %d", len(events))
	}

	// SearchCompleted subscriber should not have received anything
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	if receivedCount != 0 {
		t.Errorf("Expected 0 events for wrong subscriber, got %d", receivedCount)
	}
	mu.Unlock()
}

// TestEventBus_PresetCreatedAt tests that a preset CreatedAt is preserved.
func TestEventBus_PresetCreatedAt(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	// Set a specific CreatedAt time
	presetTime := time.Date(2023, 1, 15, 10, 30, 0, 0, time.UTC)

	event := domain.Event{
		AggregateType: "test",
		AggregateID:   "preset-time-test",
		EventType:     domain.CorruptionDetected,
		EventData:     map[string]interface{}{},
		CreatedAt:     presetTime,
		EventVersion:  5, // Also preset version
	}

	err := eb.Publish(event)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Query the database to verify preset values were preserved
	events := getEventsByAggregate(t, db, "preset-time-test")

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	// EventVersion should be preserved
	if events[0].EventVersion != 5 {
		t.Errorf("EventVersion = %d, want 5", events[0].EventVersion)
	}

	// CreatedAt should be the preset time (allow 1 second tolerance for DB storage)
	if events[0].CreatedAt.Sub(presetTime).Abs() > time.Second {
		t.Errorf("CreatedAt = %v, want approximately %v", events[0].CreatedAt, presetTime)
	}
}

// TestEventBus_UserID tests that UserID is persisted correctly.
func TestEventBus_UserID(t *testing.T) {
	db := newTestDB(t)
	defer db.Close()

	eb := NewEventBus(db)
	defer eb.Shutdown()

	event := domain.Event{
		AggregateType: "test",
		AggregateID:   "userid-test",
		EventType:     domain.CorruptionDetected,
		EventData:     map[string]interface{}{},
		UserID:        "admin-user-123",
	}

	err := eb.Publish(event)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}

	// Query the database to verify UserID was preserved
	events := getEventsByAggregate(t, db, "userid-test")

	if len(events) != 1 {
		t.Fatalf("Expected 1 event, got %d", len(events))
	}

	if events[0].UserID != "admin-user-123" {
		t.Errorf("UserID = %q, want %q", events[0].UserID, "admin-user-123")
	}
}

// containsString is a helper to check if a string contains a substring.
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 ||
		(len(s) > 0 && len(substr) > 0 && findSubstring(s, substr)))
}

func findSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
