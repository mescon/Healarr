package services

import (
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/testutil"
)

func setupEventReplayTest(t *testing.T) (*EventReplayService, *eventbus.EventBus, *sql.DB, func()) {
	t.Helper()
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}

	eb := eventbus.NewEventBus(db)
	service := NewEventReplayService(db, eb)

	cleanup := func() {
		eb.Shutdown()
		db.Close()
	}

	return service, eb, db, cleanup
}

func TestNewEventReplayService(t *testing.T) {
	service, _, _, cleanup := setupEventReplayTest(t)
	defer cleanup()

	if service == nil {
		t.Fatal("Expected non-nil service")
	}
}

func TestReplayUnprocessedEvents_NoEvents(t *testing.T) {
	service, _, _, cleanup := setupEventReplayTest(t)
	defer cleanup()

	// Should not error when there are no events
	if err := service.ReplayUnprocessedEvents(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}
}

func TestReplayUnprocessedEvents_WithUnprocessedEvent(t *testing.T) {
	service, eb, _, cleanup := setupEventReplayTest(t)
	defer cleanup()

	// Track events received by subscriber
	received := make(chan domain.Event, 10)
	eb.Subscribe(domain.CorruptionDetected, func(event domain.Event) {
		received <- event
	})

	// Give subscriber time to start
	time.Sleep(50 * time.Millisecond)

	// Insert an unprocessed CorruptionDetected event directly into DB
	corruptionID := "test-corruption-id"
	eventData, _ := json.Marshal(domain.CorruptionEventData{
		FilePath:       "/test/file.mkv",
		CorruptionType: "corruption:video",
		Source:         "test",
	})

	_, err := service.db.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES (?, ?, ?, ?, 1, ?)
	`, "Corruption", corruptionID, domain.CorruptionDetected, eventData, time.Now().UTC())
	if err != nil {
		t.Fatalf("Failed to insert test event: %v", err)
	}

	// Run replay
	if err := service.ReplayUnprocessedEvents(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Check that event was replayed to subscriber
	select {
	case event := <-received:
		if event.AggregateID != corruptionID {
			t.Errorf("Expected aggregate ID %s, got %s", corruptionID, event.AggregateID)
		}
		if event.EventType != domain.CorruptionDetected {
			t.Errorf("Expected event type %s, got %s", domain.CorruptionDetected, event.EventType)
		}
	case <-time.After(time.Second):
		t.Error("Timed out waiting for replayed event")
	}
}

func TestReplayUnprocessedEvents_SkipsProcessedEvents(t *testing.T) {
	service, eb, _, cleanup := setupEventReplayTest(t)
	defer cleanup()

	// Track events received by subscriber
	received := make(chan domain.Event, 10)
	eb.Subscribe(domain.CorruptionDetected, func(event domain.Event) {
		received <- event
	})

	// Give subscriber time to start
	time.Sleep(50 * time.Millisecond)

	// Insert a CorruptionDetected event followed by a subsequent event
	corruptionID := "processed-corruption-id"
	eventData, _ := json.Marshal(domain.CorruptionEventData{
		FilePath:       "/test/processed-file.mkv",
		CorruptionType: "corruption:video",
		Source:         "test",
	})

	// First event: CorruptionDetected
	_, err := service.db.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES (?, ?, ?, ?, 1, ?)
	`, "Corruption", corruptionID, domain.CorruptionDetected, eventData, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Failed to insert first event: %v", err)
	}

	// Second event: RemediationQueued (indicates CorruptionDetected was processed)
	remediationData, _ := json.Marshal(map[string]string{"file_path": "/test/processed-file.mkv"})
	_, err = service.db.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES (?, ?, ?, ?, 2, ?)
	`, "Corruption", corruptionID, domain.RemediationQueued, remediationData, time.Now().UTC())
	if err != nil {
		t.Fatalf("Failed to insert second event: %v", err)
	}

	// Run replay
	if err := service.ReplayUnprocessedEvents(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should NOT receive the event since it was already processed
	select {
	case event := <-received:
		t.Errorf("Should not have received event for processed corruption, got: %s", event.AggregateID)
	case <-time.After(200 * time.Millisecond):
		// Expected - no event should be replayed
	}
}

func TestReplayUnprocessedEvents_MultipleUnprocessedEvents(t *testing.T) {
	service, eb, _, cleanup := setupEventReplayTest(t)
	defer cleanup()

	// Track events received by subscriber
	received := make(chan domain.Event, 10)
	eb.Subscribe(domain.CorruptionDetected, func(event domain.Event) {
		received <- event
	})

	// Give subscriber time to start
	time.Sleep(50 * time.Millisecond)

	// Insert multiple unprocessed events
	ids := []string{"unprocessed-1", "unprocessed-2", "unprocessed-3"}
	for i, id := range ids {
		eventData, _ := json.Marshal(domain.CorruptionEventData{
			FilePath:       "/test/file" + string(rune('1'+i)) + ".mkv",
			CorruptionType: "corruption:video",
			Source:         "test",
		})

		_, err := service.db.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
			VALUES (?, ?, ?, ?, 1, ?)
		`, "Corruption", id, domain.CorruptionDetected, eventData, time.Now().UTC().Add(time.Duration(i)*time.Second))
		if err != nil {
			t.Fatalf("Failed to insert event %d: %v", i, err)
		}
	}

	// Run replay
	if err := service.ReplayUnprocessedEvents(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should receive all three events
	receivedIDs := make(map[string]bool)
	for i := 0; i < 3; i++ {
		select {
		case event := <-received:
			receivedIDs[event.AggregateID] = true
		case <-time.After(time.Second):
			t.Error("Timed out waiting for replayed event")
		}
	}

	for _, id := range ids {
		if !receivedIDs[id] {
			t.Errorf("Expected to receive event for %s", id)
		}
	}
}

func TestReplayUnprocessedEvents_OnlyReplayCorruptionDetected(t *testing.T) {
	service, eb, _, cleanup := setupEventReplayTest(t)
	defer cleanup()

	// Track events received by subscribers
	corruptionReceived := make(chan domain.Event, 10)
	otherReceived := make(chan domain.Event, 10)

	eb.Subscribe(domain.CorruptionDetected, func(event domain.Event) {
		corruptionReceived <- event
	})
	eb.Subscribe(domain.RemediationQueued, func(event domain.Event) {
		otherReceived <- event
	})

	// Give subscriber time to start
	time.Sleep(50 * time.Millisecond)

	// Insert a CorruptionDetected event (should be replayed)
	corruptionData, _ := json.Marshal(domain.CorruptionEventData{
		FilePath: "/test/file.mkv",
	})
	_, err := service.db.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES (?, ?, ?, ?, 1, ?)
	`, "Corruption", "corruption-1", domain.CorruptionDetected, corruptionData, time.Now().UTC())
	if err != nil {
		t.Fatalf("Failed to insert corruption event: %v", err)
	}

	// Insert a RemediationQueued event for different aggregate (should NOT be replayed)
	remediationData, _ := json.Marshal(map[string]string{"file_path": "/test/file2.mkv"})
	_, err = service.db.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES (?, ?, ?, ?, 1, ?)
	`, "Corruption", "remediation-1", domain.RemediationQueued, remediationData, time.Now().UTC())
	if err != nil {
		t.Fatalf("Failed to insert remediation event: %v", err)
	}

	// Run replay
	if err := service.ReplayUnprocessedEvents(); err != nil {
		t.Fatalf("Expected no error, got: %v", err)
	}

	// Should receive the CorruptionDetected event
	select {
	case event := <-corruptionReceived:
		if event.AggregateID != "corruption-1" {
			t.Errorf("Expected aggregate ID corruption-1, got %s", event.AggregateID)
		}
	case <-time.After(time.Second):
		t.Error("Timed out waiting for corruption event")
	}

	// Should NOT receive the RemediationQueued event
	select {
	case event := <-otherReceived:
		t.Errorf("Should not have received RemediationQueued event, got: %s", event.AggregateID)
	case <-time.After(200 * time.Millisecond):
		// Expected - other events should not be replayed
	}
}
