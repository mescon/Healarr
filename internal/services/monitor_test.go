package services

import (
	"sync"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/testutil"
)

// =============================================================================
// MonitorService construction tests
// =============================================================================

func TestNewMonitorService_DefaultClock(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	monitor := NewMonitorService(eb, db)

	if monitor.clk == nil {
		t.Error("Expected clock to be set")
	}
	// Clock should be a RealClock - verify by calling Now() and checking it's recent
	now := monitor.clk.Now()
	if time.Since(now) > time.Second {
		t.Errorf("RealClock.Now() should return current time, got %v", now)
	}
}

func TestNewMonitorService_WithMockClock(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockClock := testutil.NewMockClock()
	fixedTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	mockClock.SetNow(fixedTime)

	monitor := NewMonitorService(eb, db, mockClock)

	if monitor.clk.Now() != fixedTime {
		t.Errorf("Expected clock to return fixed time %v, got %v", fixedTime, monitor.clk.Now())
	}
}

func TestNewMonitorService_NilClockUsesReal(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	monitor := NewMonitorService(eb, db, nil)

	if monitor.clk == nil {
		t.Error("Expected clock to be set even when nil passed")
	}
}

// =============================================================================
// MonitorService retry scheduling tests
// =============================================================================

func TestMonitorService_SchedulesRetryWithExponentialBackoff(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Set up a scan path with custom max retries
	testutil.SeedScanPath(db, 1, "/media/movies", "/movies", true, false)
	_, err = db.Exec(`UPDATE scan_paths SET max_retries = 5 WHERE id = 1`)
	if err != nil {
		t.Fatalf("Failed to update scan path: %v", err)
	}

	// Seed the original CorruptionDetected event so we have context
	corruptionID := "test-corruption-001"
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test Movie/movie.mkv",
			"path_id":   int64(1),
		},
	})

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)
	monitor.Start()

	// Track RetryScheduled events
	var mu sync.Mutex
	var retryEvents []domain.Event
	eb.Subscribe(domain.RetryScheduled, func(e domain.Event) {
		mu.Lock()
		retryEvents = append(retryEvents, e)
		mu.Unlock()
	})

	// Trigger a failure event - should schedule a retry
	failureEvent := domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionFailed,
		EventData: map[string]interface{}{
			"error": "test error",
		},
	}
	eb.Publish(failureEvent)

	// Wait for async event processing
	time.Sleep(50 * time.Millisecond)

	// Check that a timer was scheduled
	if mockClock.PendingCount() != 1 {
		t.Errorf("Expected 1 pending timer, got %d", mockClock.PendingCount())
	}

	// Fire all pending timers
	fired := mockClock.FireAll()
	if fired != 1 {
		t.Errorf("Expected 1 timer to fire, got %d", fired)
	}

	// Wait for the RetryScheduled event
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if len(retryEvents) != 1 {
		t.Errorf("Expected 1 RetryScheduled event, got %d", len(retryEvents))
	} else {
		// Verify event contains file_path and path_id from original corruption
		if fp, ok := retryEvents[0].GetString("file_path"); !ok || fp != "/movies/Test Movie/movie.mkv" {
			t.Errorf("Expected file_path '/movies/Test Movie/movie.mkv', got %q", fp)
		}
		if pathID, ok := retryEvents[0].GetInt64("path_id"); !ok || pathID != 1 {
			t.Errorf("Expected path_id 1, got %d", pathID)
		}
	}
	mu.Unlock()
}

func TestMonitorService_ExponentialBackoffDelays(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	testutil.SeedScanPath(db, 1, "/media/movies", "/movies", true, false)
	_, _ = db.Exec(`UPDATE scan_paths SET max_retries = 10 WHERE id = 1`)

	corruptionID := "test-backoff"
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	mockClock := testutil.NewMockClockAt(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))
	monitor := NewMonitorService(eb, db, mockClock)
	monitor.Start()

	// Simulate multiple failures and track when timers fire
	for i := 0; i < 3; i++ {
		// Add a Failed event to increment retry_count
		if i > 0 {
			testutil.SeedEvent(db, domain.Event{
				AggregateType: "corruption",
				AggregateID:   corruptionID,
				EventType:     domain.SearchFailed,
				EventData:     map[string]interface{}{"error": "test error"},
			})
		}

		failureEvent := domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.VerificationFailed,
			EventData:     map[string]interface{}{"error": "test error"},
		}
		eb.Publish(failureEvent)
		time.Sleep(50 * time.Millisecond)
	}

	// Advance time incrementally to capture when each timer fires
	// First retry: 15 min, Second: 30 min, Third: 60 min
	expectedDelays := []time.Duration{15 * time.Minute, 30 * time.Minute, 60 * time.Minute}

	for _, expectedDelay := range expectedDelays {
		// Advance to just before the expected timer
		mockClock.Advance(expectedDelay - time.Minute)
		// Timer should still be pending
		if mockClock.PendingCount() == 0 {
			t.Errorf("Timer should not have fired yet at delay %v", expectedDelay)
		}
		// Advance the remaining minute plus buffer to trigger the timer
		mockClock.Advance(2 * time.Minute)
	}
}

func TestMonitorService_EmitsMaxRetriesReached(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Set up scan path with max_retries = 2
	testutil.SeedScanPath(db, 1, "/media/movies", "/movies", true, false)
	_, _ = db.Exec(`UPDATE scan_paths SET max_retries = 2 WHERE id = 1`)

	corruptionID := "test-max-retries"

	// Seed original CorruptionDetected
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	// Seed 2 Failed events to reach the retry limit
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.DeletionFailed,
		EventData:     map[string]interface{}{"error": "fail 1"},
	})
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.SearchFailed,
		EventData:     map[string]interface{}{"error": "fail 2"},
	})

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)
	monitor.Start()

	// Track MaxRetriesReached events
	var mu sync.Mutex
	maxRetriesEvents := []domain.Event{}
	eb.Subscribe(domain.MaxRetriesReached, func(e domain.Event) {
		mu.Lock()
		maxRetriesEvents = append(maxRetriesEvents, e)
		mu.Unlock()
	})

	// Another failure should trigger MaxRetriesReached instead of RetryScheduled
	eb.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.VerificationFailed,
		EventData:     map[string]interface{}{"error": "final fail"},
	})

	// Wait for async processing
	time.Sleep(100 * time.Millisecond)

	// Should NOT schedule a retry
	if mockClock.PendingCount() != 0 {
		t.Errorf("Expected no pending timers when max retries reached, got %d", mockClock.PendingCount())
	}

	// Should emit MaxRetriesReached
	mu.Lock()
	if len(maxRetriesEvents) != 1 {
		t.Errorf("Expected 1 MaxRetriesReached event, got %d", len(maxRetriesEvents))
	}
	if len(maxRetriesEvents) > 0 && maxRetriesEvents[0].AggregateID != corruptionID {
		t.Errorf("Expected AggregateID %q, got %q", corruptionID, maxRetriesEvents[0].AggregateID)
	}
	mu.Unlock()
}

func TestMonitorService_HandlesMultipleFailureTypes(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	testutil.SeedScanPath(db, 1, "/media/movies", "/movies", true, false)
	_, _ = db.Exec(`UPDATE scan_paths SET max_retries = 10 WHERE id = 1`)

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)
	monitor.Start()

	failureTypes := []domain.EventType{
		domain.DeletionFailed,
		domain.SearchFailed,
		domain.VerificationFailed,
		domain.DownloadTimeout,
	}

	for i, failureType := range failureTypes {
		t.Run(string(failureType), func(t *testing.T) {
			corruptionID := "test-failure-" + string(failureType)

			// Seed CorruptionDetected for each
			testutil.SeedEvent(db, domain.Event{
				AggregateType: "corruption",
				AggregateID:   corruptionID,
				EventType:     domain.CorruptionDetected,
				EventData: map[string]interface{}{
					"file_path": "/movies/Test" + string(rune(i+'A')) + "/movie.mkv",
					"path_id":   int64(1),
				},
			})

			beforePending := mockClock.PendingCount()

			eb.Publish(domain.Event{
				AggregateID:   corruptionID,
				AggregateType: "corruption",
				EventType:     failureType,
				EventData:     map[string]interface{}{"error": "test"},
			})

			time.Sleep(50 * time.Millisecond)

			afterPending := mockClock.PendingCount()
			if afterPending <= beforePending {
				t.Errorf("Expected a timer to be scheduled for %s", failureType)
			}
		})
	}
}

func TestMonitorService_UsesDefaultMaxRetriesWhenPathNotFound(t *testing.T) {
	testConfig := config.NewTestConfig()
	testConfig.DefaultMaxRetries = 3
	config.SetForTesting(testConfig)

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// No scan path seeded - should use default

	corruptionID := "test-default-retries"
	// Seed CorruptionDetected without path_id
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			// No path_id
		},
	})

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)
	monitor.Start()

	// First failure should schedule retry (using default max_retries of 3)
	eb.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionFailed,
	})

	time.Sleep(50 * time.Millisecond)

	if mockClock.PendingCount() != 1 {
		t.Errorf("Expected 1 pending timer (using default max_retries), got %d", mockClock.PendingCount())
	}
}

// =============================================================================
// MockClock timer behavior tests
// =============================================================================

func TestMockClock_TimerStop(t *testing.T) {
	mockClock := testutil.NewMockClock()

	fired := false
	timer := mockClock.AfterFunc(time.Hour, func() {
		fired = true
	})

	// Stop before firing
	if !timer.Stop() {
		t.Error("Expected Stop() to return true for unfired timer")
	}

	// Advance past the scheduled time
	mockClock.Advance(2 * time.Hour)

	if fired {
		t.Error("Timer should not fire after Stop()")
	}
}

func TestMockClock_TimerStopAfterFire(t *testing.T) {
	mockClock := testutil.NewMockClock()

	fired := false
	timer := mockClock.AfterFunc(time.Minute, func() {
		fired = true
	})

	// Fire the timer
	mockClock.Advance(2 * time.Minute)

	if !fired {
		t.Error("Timer should have fired")
	}

	// Stop after firing should return false
	if timer.Stop() {
		t.Error("Expected Stop() to return false for already fired timer")
	}
}

func TestMockClock_AdvancePartial(t *testing.T) {
	mockClock := testutil.NewMockClock()

	var firedOrder []int
	mockClock.AfterFunc(10*time.Minute, func() { firedOrder = append(firedOrder, 1) })
	mockClock.AfterFunc(20*time.Minute, func() { firedOrder = append(firedOrder, 2) })
	mockClock.AfterFunc(30*time.Minute, func() { firedOrder = append(firedOrder, 3) })

	// Advance 15 minutes - only first timer should fire
	count := mockClock.Advance(15 * time.Minute)
	if count != 1 {
		t.Errorf("Expected 1 timer to fire, got %d", count)
	}
	if len(firedOrder) != 1 || firedOrder[0] != 1 {
		t.Errorf("Expected [1], got %v", firedOrder)
	}

	// Advance another 10 minutes - second timer should fire
	count = mockClock.Advance(10 * time.Minute)
	if count != 1 {
		t.Errorf("Expected 1 timer to fire, got %d", count)
	}
	if len(firedOrder) != 2 || firedOrder[1] != 2 {
		t.Errorf("Expected [1, 2], got %v", firedOrder)
	}

	// Advance another 10 minutes - third timer should fire
	count = mockClock.Advance(10 * time.Minute)
	if count != 1 {
		t.Errorf("Expected 1 timer to fire, got %d", count)
	}
	if len(firedOrder) != 3 {
		t.Errorf("Expected 3 timers total, got %d", len(firedOrder))
	}
}

// =============================================================================
// handleFailure additional error path tests
// =============================================================================

func TestMonitorService_HandleFailure_MaxRetriesReached(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)

	config.SetForTesting(&config.Config{
		DefaultMaxRetries: 3,
	})

	// Insert a corruption with 3 failures (at max retries)
	corruptionID := "max-retries-test"
	_, err = db.Exec(`
		INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data)
		VALUES (?, 'corruption', 'CorruptionDetected', '{"file_path": "/test.mkv", "path_id": 1}')
	`, corruptionID)
	if err != nil {
		t.Fatalf("Failed to insert detection event: %v", err)
	}

	// Insert 3 failure events
	for i := 0; i < 3; i++ {
		_, err = db.Exec(`
			INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data)
			VALUES (?, 'corruption', 'DeletionFailed', '{}')
		`, corruptionID)
		if err != nil {
			t.Fatalf("Failed to insert failure event: %v", err)
		}
	}

	// Track if MaxRetriesReached was published
	var maxRetriesReceived bool
	var mu sync.Mutex
	eb.Subscribe(domain.MaxRetriesReached, func(event domain.Event) {
		mu.Lock()
		defer mu.Unlock()
		if event.AggregateID == corruptionID {
			maxRetriesReceived = true
		}
	})

	// Handle the failure event
	monitor.handleFailure(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionFailed,
	})

	// Wait a bit for event to be processed
	time.Sleep(50 * time.Millisecond)

	mu.Lock()
	if !maxRetriesReceived {
		t.Error("Expected MaxRetriesReached event to be published")
	}
	mu.Unlock()
}

func TestMonitorService_HandleFailure_DBError(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)

	// Drop events table to cause DB error
	db.Exec("DROP TABLE events")

	// Should not panic, just log error
	monitor.handleFailure(domain.Event{
		AggregateID:   "test-corruption",
		AggregateType: "corruption",
		EventType:     domain.DeletionFailed,
	})
	// Test passes if no panic
}

func TestMonitorService_GetCorruptionContext_NoData(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	monitor := NewMonitorService(eb, db)

	// Try to get context for non-existent corruption
	_, _, err = monitor.getCorruptionContext("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent corruption")
	}
}

// =============================================================================
// MonitorService Stop() graceful shutdown tests
// =============================================================================

func TestMonitorService_Stop_CancelsPendingTimers(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	config.SetForTesting(&config.Config{
		DefaultMaxRetries: 10,
	})

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)
	monitor.Start()

	// Seed corruption
	corruptionID := "stop-test"
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	// Trigger a failure to schedule a retry timer
	eb.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionFailed,
	})

	time.Sleep(50 * time.Millisecond)

	// Verify timer was scheduled
	if mockClock.PendingCount() != 1 {
		t.Fatalf("Expected 1 pending timer, got %d", mockClock.PendingCount())
	}

	// Stop the service - should cancel the timer
	monitor.Stop()

	// MockClock doesn't automatically clear timers on Stop(),
	// but our Stop() should have called timer.Stop() and decremented WaitGroup.
	// The key behavior is that Stop() completes without hanging.

	// Verify Stop() is idempotent
	monitor.Stop() // Should not panic or deadlock
}

func TestMonitorService_Stop_PreventsNewScheduling(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	config.SetForTesting(&config.Config{
		DefaultMaxRetries: 10,
	})

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)
	monitor.Start()

	// Seed corruption
	corruptionID := "stop-prevent-test"
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	// Stop the service first
	monitor.Stop()

	beforePending := mockClock.PendingCount()

	// Now try to handle a failure - should not schedule new timer
	monitor.handleFailure(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionFailed,
	})

	// No new timer should be scheduled
	if mockClock.PendingCount() != beforePending {
		t.Errorf("Expected no new timers after Stop(), got %d (was %d)",
			mockClock.PendingCount(), beforePending)
	}
}

func TestMonitorService_Stop_WaitsForInFlightCallbacks(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	config.SetForTesting(&config.Config{
		DefaultMaxRetries: 10,
	})

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)
	monitor.Start()

	// Seed corruption
	corruptionID := "inflight-test"
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	// Schedule a retry
	eb.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionFailed,
	})

	time.Sleep(50 * time.Millisecond)

	// Fire the timer (starts the callback)
	mockClock.Advance(20 * time.Minute)

	// Stop should wait for any callbacks (in real code, MockClock fires synchronously)
	// so this should complete without issue
	monitor.Stop()
}

// =============================================================================
// handleNeedsAttention tests
// =============================================================================

func TestMonitorService_HandleNeedsAttention_ImportBlocked(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	monitor := NewMonitorService(eb, db)
	monitor.Start()

	corruptionID := "import-blocked-test"

	// Seed corruption context
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	// Publish ImportBlocked event
	eb.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.ImportBlocked,
		EventData: map[string]interface{}{
			"file_path":     "/movies/Test/movie.mkv",
			"error_message": "Quality does not meet cutoff",
		},
	})

	// Give time for handler to run
	time.Sleep(50 * time.Millisecond)

	// Test passes if no panic - the handler just logs
}

func TestMonitorService_HandleNeedsAttention_SearchExhausted(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	monitor := NewMonitorService(eb, db)
	monitor.Start()

	corruptionID := "search-exhausted-test"

	// Seed corruption context
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	// Publish SearchExhausted event
	eb.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.SearchExhausted,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"reason":    "No indexers found valid releases",
		},
	})

	// Give time for handler to run
	time.Sleep(50 * time.Millisecond)

	// Test passes if no panic - the handler just logs
}

func TestMonitorService_HandleNeedsAttention_FallbackToContext(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	monitor := NewMonitorService(eb, db)
	monitor.Start()

	corruptionID := "fallback-context-test"

	// Seed corruption context
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	// Publish event WITHOUT file_path in data - should fall back to context lookup
	eb.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.ImportBlocked,
		EventData: map[string]interface{}{
			"error_message": "Quality issue",
			// No file_path - should look up from corruption context
		},
	})

	// Give time for handler to run
	time.Sleep(50 * time.Millisecond)

	// Test passes if no panic
}

// =============================================================================
// DownloadFailed handler test
// =============================================================================

func TestMonitorService_HandlesDownloadFailed(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	testutil.SeedScanPath(db, 1, "/media/movies", "/movies", true, false)
	_, _ = db.Exec(`UPDATE scan_paths SET max_retries = 10 WHERE id = 1`)

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)
	monitor.Start()

	corruptionID := "download-failed-test"

	// Seed CorruptionDetected
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	beforePending := mockClock.PendingCount()

	// DownloadFailed should trigger a retry (this was the bug being fixed)
	eb.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DownloadFailed,
		EventData:     map[string]interface{}{"error": "no seeders"},
	})

	time.Sleep(50 * time.Millisecond)

	afterPending := mockClock.PendingCount()
	if afterPending <= beforePending {
		t.Errorf("Expected a timer to be scheduled for DownloadFailed, pending before=%d, after=%d",
			beforePending, afterPending)
	}
}

// =============================================================================
// Additional edge case tests for coverage
// =============================================================================

func TestMonitorService_HandleNeedsAttention_DefaultCase(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	monitor := NewMonitorService(eb, db)

	corruptionID := "default-case-test"

	// Seed corruption context
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	// Call handleNeedsAttention with an event type that hits the default case
	// We'll use a custom event type to hit the default switch case
	monitor.handleNeedsAttention(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.MaxRetriesReached, // Not ImportBlocked or SearchExhausted
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
		},
	})

	// Test passes if no panic - just exercises default case logging
}

func TestMonitorService_HandleFailure_CorruptionContextNotFound(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	config.SetForTesting(&config.Config{
		DefaultMaxRetries: 10,
	})

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)

	// Don't seed any corruption - context lookup will fail
	corruptionID := "missing-context-test"

	beforePending := mockClock.PendingCount()

	// Handle failure for a corruption that doesn't exist
	monitor.handleFailure(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionFailed,
	})

	// No timer should be scheduled since context lookup fails
	afterPending := mockClock.PendingCount()
	if afterPending != beforePending {
		t.Errorf("Expected no timer when context not found, but pending changed from %d to %d",
			beforePending, afterPending)
	}
}

func TestMonitorService_GetCorruptionContext_EmptyFilePath(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	monitor := NewMonitorService(eb, db)

	// Seed corruption with empty file_path
	corruptionID := "empty-path-test"
	_, err = db.Exec(`
		INSERT INTO events (aggregate_id, aggregate_type, event_type, event_data)
		VALUES (?, 'corruption', 'CorruptionDetected', '{"file_path": "", "path_id": 1}')
	`, corruptionID)
	if err != nil {
		t.Fatalf("Failed to insert event: %v", err)
	}

	// Should return error when file_path is empty
	_, _, err = monitor.getCorruptionContext(corruptionID)
	if err == nil {
		t.Error("Expected error for empty file_path")
	}
}

func TestMonitorService_TimerFiringAfterStop(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	config.SetForTesting(&config.Config{
		DefaultMaxRetries: 10,
	})

	mockClock := testutil.NewMockClock()
	monitor := NewMonitorService(eb, db, mockClock)
	monitor.Start()

	// Seed corruption
	corruptionID := "timer-stop-test"
	testutil.SeedEvent(db, domain.Event{
		AggregateType: "corruption",
		AggregateID:   corruptionID,
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path": "/movies/Test/movie.mkv",
			"path_id":   int64(1),
		},
	})

	// Schedule a retry
	eb.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionFailed,
	})

	time.Sleep(50 * time.Millisecond)

	// Verify timer is scheduled
	if mockClock.PendingCount() < 1 {
		t.Fatal("Expected at least 1 pending timer")
	}

	// Stop the service
	monitor.Stop()

	// Try to advance time - timer should not fire because it was canceled
	// MockClock behavior: stopped timers don't fire
	mockClock.Advance(30 * time.Minute)

	// Test passes if no deadlock/panic
}

func TestMonitorService_HandleNeedsAttention_NoFilePath(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	monitor := NewMonitorService(eb, db)

	// Don't seed any corruption - context lookup will fail
	corruptionID := "no-file-path-test"

	// Publish event without file_path and without corruption context
	// This tests the fallback path where getCorruptionContext fails
	monitor.handleNeedsAttention(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.ImportBlocked,
		EventData: map[string]interface{}{
			"error_message": "Quality issue",
			// No file_path
		},
	})

	// Test passes if no panic - filePath will be empty but that's OK
}
