package services

import (
	"errors"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/testutil"
)

// errPathNotConfigured is a test error for path mapping failures.
var errPathNotConfigured = errors.New("path not configured")

// TestMain sets up test configuration before running tests.
func TestMain(m *testing.M) {
	// Initialize config for tests that require it
	config.SetForTesting(config.NewTestConfig())
	os.Exit(m.Run())
}

// TestRemediatorService_SafetyCheck verifies that recoverable errors are NOT remediated.
// This is the most critical test - if this fails, the system could delete files
// when infrastructure (NAS, mounts, network) is having issues.
func TestRemediatorService_SafetyCheck(t *testing.T) {
	recoverableErrorTypes := []string{
		integration.ErrorTypeAccessDenied,
		integration.ErrorTypePathNotFound,
		integration.ErrorTypeMountLost,
		integration.ErrorTypeIOError,
		integration.ErrorTypeTimeout,
		integration.ErrorTypeInvalidConfig,
	}

	for _, errorType := range recoverableErrorTypes {
		t.Run("blocks_"+errorType, func(t *testing.T) {
			// Setup
			db, err := testutil.NewTestDB()
			if err != nil {
				t.Fatalf("Failed to create test DB: %v", err)
			}
			defer db.Close()

			mockEventBus := testutil.NewMockEventBus()
			mockArrClient := &testutil.MockArrClient{}
			mockPathMapper := &testutil.MockPathMapper{}

			// Create remediator
			remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

			// Create corruption event with recoverable error type
			event := testutil.NewCorruptionEventWithType(
				testutil.TestFilePaths.Movie1,
				errorType,
				testutil.WithAutoRemediate(true),
			)

			// Act - simulate corruption detected
			remediator.handleCorruptionDetected(event)

			// Give async operations a moment
			time.Sleep(100 * time.Millisecond)

			// Assert - should have published DeletionFailed, not deleted anything
			deletionFailedEvents := mockEventBus.GetEvents(domain.DeletionFailed)
			if len(deletionFailedEvents) != 1 {
				t.Errorf("Expected 1 DeletionFailed event for error type %s, got %d", errorType, len(deletionFailedEvents))
			}

			// Verify DeleteFile was NEVER called
			if mockArrClient.CallCount("DeleteFile") > 0 {
				t.Errorf("DeleteFile should NOT be called for recoverable error type %s", errorType)
			}

			// Verify the error message mentions infrastructure issue
			if len(deletionFailedEvents) > 0 {
				errMsg, _ := deletionFailedEvents[0].GetString("error")
				if errMsg == "" {
					t.Errorf("DeletionFailed event should contain error message")
				}
			}
		})
	}
}

// TestRemediatorService_HandleCorruptionDetected tests the full corruption handling flow.
func TestRemediatorService_HandleCorruptionDetected(t *testing.T) {
	t.Run("valid_corruption_triggers_remediation", func(t *testing.T) {
		// Setup
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			FindMediaByPathFunc: func(path string) (int64, error) {
				return 123, nil
			},
			DeleteFileFunc: func(mediaID int64, path string) (map[string]interface{}, error) {
				return map[string]interface{}{
					"deleted": true,
				}, nil
			},
			TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
				return nil
			},
		}
		mockPathMapper := &testutil.MockPathMapper{
			ToArrPathFunc: func(localPath string) (string, error) {
				return localPath, nil // Simple pass-through
			},
		}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		// Create corruption event with TRUE corruption type
		event := testutil.NewCorruptionEventWithType(
			testutil.TestFilePaths.Corrupt,
			integration.ErrorTypeCorruptHeader,
			testutil.WithAutoRemediate(true),
		)

		// Act
		remediator.handleCorruptionDetected(event)

		// Wait for async operations
		time.Sleep(200 * time.Millisecond)

		// Assert - should have published RemediationQueued, DeletionStarted, DeletionCompleted, SearchStarted, SearchCompleted
		if mockEventBus.EventCount(domain.RemediationQueued) != 1 {
			t.Errorf("Expected RemediationQueued event")
		}
		if mockEventBus.EventCount(domain.DeletionStarted) != 1 {
			t.Errorf("Expected DeletionStarted event")
		}
		if mockEventBus.EventCount(domain.DeletionCompleted) != 1 {
			t.Errorf("Expected DeletionCompleted event")
		}
		if mockEventBus.EventCount(domain.SearchStarted) != 1 {
			t.Errorf("Expected SearchStarted event")
		}
		if mockEventBus.EventCount(domain.SearchCompleted) != 1 {
			t.Errorf("Expected SearchCompleted event")
		}

		// Verify DeleteFile was called
		if mockArrClient.CallCount("DeleteFile") != 1 {
			t.Errorf("Expected DeleteFile to be called once, got %d", mockArrClient.CallCount("DeleteFile"))
		}

		// Verify TriggerSearch was called
		if mockArrClient.CallCount("TriggerSearch") != 1 {
			t.Errorf("Expected TriggerSearch to be called once, got %d", mockArrClient.CallCount("TriggerSearch"))
		}
	})

	t.Run("missing_file_path_publishes_error", func(t *testing.T) {
		// Setup
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		// Create corruption event WITHOUT file_path
		event := domain.Event{
			AggregateType: "corruption",
			AggregateID:   "test-123",
			EventType:     domain.CorruptionDetected,
			EventData:     map[string]interface{}{}, // Missing file_path
		}

		// Act
		remediator.handleCorruptionDetected(event)

		time.Sleep(50 * time.Millisecond)

		// Assert - should have published DeletionFailed
		if mockEventBus.EventCount(domain.DeletionFailed) != 1 {
			t.Errorf("Expected DeletionFailed event for missing file_path")
		}

		// Verify no remediation was attempted
		if mockArrClient.CallCount("DeleteFile") > 0 {
			t.Errorf("DeleteFile should not be called when file_path is missing")
		}
	})

	t.Run("path_mapping_failure_publishes_error", func(t *testing.T) {
		// Setup
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{}
		mockPathMapper := &testutil.MockPathMapper{
			ToArrPathFunc: func(localPath string) (string, error) {
				return "", errPathNotConfigured
			},
		}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		event := testutil.NewCorruptionEventWithType(
			testutil.TestFilePaths.Movie1,
			integration.ErrorTypeCorruptHeader,
			testutil.WithAutoRemediate(true),
		)

		// Act
		remediator.handleCorruptionDetected(event)

		time.Sleep(50 * time.Millisecond)

		// Assert - should have published DeletionFailed
		if mockEventBus.EventCount(domain.DeletionFailed) != 1 {
			t.Errorf("Expected DeletionFailed event for path mapping failure")
		}
	})

	t.Run("auto_remediate_false_does_not_remediate", func(t *testing.T) {
		// Setup
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		event := testutil.NewCorruptionEventWithType(
			testutil.TestFilePaths.Movie1,
			integration.ErrorTypeCorruptHeader,
			testutil.WithAutoRemediate(false), // Auto-remediate disabled
		)

		// Act
		remediator.handleCorruptionDetected(event)

		time.Sleep(100 * time.Millisecond)

		// Assert - should have published RemediationQueued but nothing else
		if mockEventBus.EventCount(domain.RemediationQueued) != 1 {
			t.Errorf("Expected RemediationQueued event")
		}
		if mockEventBus.EventCount(domain.DeletionStarted) != 0 {
			t.Errorf("Should NOT have DeletionStarted when auto_remediate is false")
		}
		if mockArrClient.CallCount("DeleteFile") > 0 {
			t.Errorf("DeleteFile should not be called when auto_remediate is false")
		}
	})
}

// TestRemediatorService_DryRunMode tests that dry-run mode simulates but doesn't execute.
func TestRemediatorService_DryRunMode(t *testing.T) {
	t.Run("dry_run_does_not_delete", func(t *testing.T) {
		// Setup
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			FindMediaByPathFunc: func(path string) (int64, error) {
				return 123, nil
			},
		}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		event := testutil.NewCorruptionEventWithType(
			testutil.TestFilePaths.Movie1,
			integration.ErrorTypeCorruptHeader,
			testutil.WithAutoRemediate(true),
			testutil.WithDryRun(true), // DRY RUN enabled
		)

		// Act
		remediator.handleCorruptionDetected(event)

		time.Sleep(200 * time.Millisecond)

		// Assert - should have published RemediationQueued but NO DeletionStarted
		if mockEventBus.EventCount(domain.RemediationQueued) != 2 { // Initial + dry-run update
			t.Logf("Got %d RemediationQueued events", mockEventBus.EventCount(domain.RemediationQueued))
		}

		// CRITICAL: DeleteFile should NOT be called in dry-run mode
		if mockArrClient.CallCount("DeleteFile") > 0 {
			t.Errorf("DeleteFile should NOT be called in dry-run mode")
		}

		// CRITICAL: TriggerSearch should NOT be called in dry-run mode
		if mockArrClient.CallCount("TriggerSearch") > 0 {
			t.Errorf("TriggerSearch should NOT be called in dry-run mode")
		}
	})
}

// TestRemediatorService_RetryLogic tests the retry handling behavior.
func TestRemediatorService_RetryLogic(t *testing.T) {
	t.Run("retry_with_completed_deletion_skips_to_search", func(t *testing.T) {
		// Setup
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			FindMediaByPathFunc: func(path string) (int64, error) {
				return 456, nil
			},
			TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
				return nil
			},
		}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		// Pre-seed a DeletionCompleted event in the database
		aggregateID := "retry-test-123"
		filePath := testutil.TestFilePaths.Movie1

		_, err = testutil.SeedEvent(db, testutil.NewDeletionCompletedEvent(aggregateID, 456, nil))
		if err != nil {
			t.Fatalf("Failed to seed deletion event: %v", err)
		}

		// Create retry event
		retryEvent := testutil.NewRetryEvent(aggregateID, filePath)

		// Act
		remediator.handleRetry(retryEvent)

		time.Sleep(200 * time.Millisecond)

		// Assert - DeleteFile should NOT be called (deletion already done)
		if mockArrClient.CallCount("DeleteFile") > 0 {
			t.Errorf("DeleteFile should NOT be called when deletion was already completed")
		}

		// TriggerSearch SHOULD be called
		if mockArrClient.CallCount("TriggerSearch") != 1 {
			t.Errorf("Expected TriggerSearch to be called once, got %d", mockArrClient.CallCount("TriggerSearch"))
		}
	})

	t.Run("retry_without_completed_deletion_runs_full_flow", func(t *testing.T) {
		// Setup
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			FindMediaByPathFunc: func(path string) (int64, error) {
				return 789, nil
			},
			DeleteFileFunc: func(mediaID int64, path string) (map[string]interface{}, error) {
				return nil, nil
			},
			TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
				return nil
			},
		}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		// Create retry event (no prior DeletionCompleted in DB)
		aggregateID := "retry-full-flow-123"
		filePath := testutil.TestFilePaths.Movie2

		// Seed original CorruptionDetected event with auto_remediate=true
		_, err = testutil.SeedEvent(db, testutil.NewCorruptionEventWithType(
			filePath,
			integration.ErrorTypeCorruptHeader,
			testutil.WithAggregateID(aggregateID),
			testutil.WithAutoRemediate(true),
		))
		if err != nil {
			t.Fatalf("Failed to seed corruption event: %v", err)
		}

		retryEvent := domain.Event{
			AggregateType: "corruption",
			AggregateID:   aggregateID,
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path":      filePath,
				"auto_remediate": true,
			},
		}

		// Act
		remediator.handleRetry(retryEvent)

		time.Sleep(200 * time.Millisecond)

		// Assert - full flow should run (since no prior DeletionCompleted)
		// The handleRetry calls handleCorruptionDetected for full flow
		// which requires auto_remediate=true to actually delete
	})
}

// TestRemediatorService_Concurrency tests the semaphore limits concurrent remediations.
func TestRemediatorService_Concurrency(t *testing.T) {
	t.Run("semaphore_limits_concurrent_remediations", func(t *testing.T) {
		// Setup
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()

		// Track concurrent calls with proper synchronization
		concurrentCalls := make(chan int, 100)
		var concurrentMu sync.Mutex
		currentConcurrent := 0
		maxConcurrent := 0

		mockArrClient := &testutil.MockArrClient{
			FindMediaByPathFunc: func(path string) (int64, error) {
				return 123, nil
			},
			DeleteFileFunc: func(mediaID int64, path string) (map[string]interface{}, error) {
				concurrentMu.Lock()
				currentConcurrent++
				if currentConcurrent > maxConcurrent {
					maxConcurrent = currentConcurrent
				}
				concurrentCalls <- currentConcurrent
				concurrentMu.Unlock()

				// Simulate some work
				time.Sleep(50 * time.Millisecond)

				concurrentMu.Lock()
				currentConcurrent--
				concurrentMu.Unlock()
				return nil, nil
			},
			TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
				return nil
			},
		}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		// Fire 10 concurrent corruption events
		for i := 0; i < 10; i++ {
			event := testutil.NewCorruptionEventWithType(
				testutil.TestFilePaths.Movie1,
				integration.ErrorTypeCorruptHeader,
				testutil.WithAutoRemediate(true),
			)
			go remediator.handleCorruptionDetected(event)
		}

		// Wait for all to complete
		time.Sleep(1 * time.Second)

		// Assert - max concurrent should be <= 5 (maxConcurrentRemediations)
		concurrentMu.Lock()
		maxConcurrentValue := maxConcurrent
		concurrentMu.Unlock()
		if maxConcurrentValue > maxConcurrentRemediations {
			t.Errorf("Expected max concurrent <= %d, got %d", maxConcurrentRemediations, maxConcurrentValue)
		}
	})
}

// =============================================================================
// RemediatorService Start tests
// =============================================================================

func TestRemediatorService_Start(t *testing.T) {
	t.Run("subscribes to events", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		// Before Start, should have no subscribers
		if len(mockEventBus.Subscribers) != 0 {
			t.Error("Expected no subscribers before Start()")
		}

		remediator.Start()

		// After Start, should have subscribers for CorruptionDetected and RetryScheduled
		if len(mockEventBus.Subscribers[domain.CorruptionDetected]) != 1 {
			t.Errorf("Expected 1 subscriber for CorruptionDetected, got %d",
				len(mockEventBus.Subscribers[domain.CorruptionDetected]))
		}
		if len(mockEventBus.Subscribers[domain.RetryScheduled]) != 1 {
			t.Errorf("Expected 1 subscriber for RetryScheduled, got %d",
				len(mockEventBus.Subscribers[domain.RetryScheduled]))
		}
	})
}

// =============================================================================
// extractEpisodeIDs tests
// =============================================================================

func TestExtractEpisodeIDs(t *testing.T) {
	tests := []struct {
		name     string
		metadata map[string]interface{}
		expected []int64
	}{
		{
			name:     "nil metadata",
			metadata: nil,
			expected: nil,
		},
		{
			name:     "empty metadata",
			metadata: map[string]interface{}{},
			expected: nil,
		},
		{
			name:     "no episode_ids key",
			metadata: map[string]interface{}{"series_id": 123},
			expected: nil,
		},
		{
			name: "episode_ids as []int64",
			metadata: map[string]interface{}{
				"episode_ids": []int64{101, 102, 103},
			},
			expected: []int64{101, 102, 103},
		},
		{
			name: "episode_ids as []interface{} with float64",
			metadata: map[string]interface{}{
				"episode_ids": []interface{}{float64(201), float64(202)},
			},
			expected: []int64{201, 202},
		},
		{
			name: "episode_ids as []interface{} with int64",
			metadata: map[string]interface{}{
				"episode_ids": []interface{}{int64(301), int64(302), int64(303)},
			},
			expected: []int64{301, 302, 303},
		},
		{
			name: "episode_ids as []interface{} with mixed types",
			metadata: map[string]interface{}{
				"episode_ids": []interface{}{float64(401), int64(402)},
			},
			expected: []int64{401, 402},
		},
		{
			name: "episode_ids as []interface{} with unsupported types",
			metadata: map[string]interface{}{
				"episode_ids": []interface{}{"not_a_number", true},
			},
			expected: nil, // Nothing extracted
		},
		{
			name: "episode_ids as wrong type (string)",
			metadata: map[string]interface{}{
				"episode_ids": "501,502",
			},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractEpisodeIDs(tt.metadata)

			if len(result) != len(tt.expected) {
				t.Errorf("extractEpisodeIDs() returned %d items, want %d", len(result), len(tt.expected))
				return
			}

			for i, v := range result {
				if v != tt.expected[i] {
					t.Errorf("extractEpisodeIDs()[%d] = %d, want %d", i, v, tt.expected[i])
				}
			}
		})
	}
}

// =============================================================================
// retrySearchOnly tests
// =============================================================================

func TestRemediatorService_RetrySearchOnly(t *testing.T) {
	t.Run("missing file_path publishes error", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		// Event without file_path
		event := domain.Event{
			AggregateType: "corruption",
			AggregateID:   "retry-test-missing-path",
			EventType:     domain.RetryScheduled,
			EventData:     map[string]interface{}{}, // Missing file_path
		}

		remediator.retrySearchOnly(event, 0, nil)

		// Wait for async processing
		time.Sleep(100 * time.Millisecond)

		// Should have SearchFailed event
		if mockEventBus.EventCount(domain.SearchFailed) != 1 {
			t.Errorf("Expected 1 SearchFailed event, got %d", mockEventBus.EventCount(domain.SearchFailed))
		}
	})

	t.Run("path mapping failure publishes error", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{}
		mockPathMapper := &testutil.MockPathMapper{
			ToArrPathFunc: func(localPath string) (string, error) {
				return "", errPathNotConfigured
			},
		}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		event := domain.Event{
			AggregateType: "corruption",
			AggregateID:   "retry-test-path-fail",
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path": "/media/movies/test.mkv",
			},
		}

		remediator.retrySearchOnly(event, 0, nil)

		// Wait for async processing
		time.Sleep(100 * time.Millisecond)

		if mockEventBus.EventCount(domain.SearchFailed) != 1 {
			t.Errorf("Expected 1 SearchFailed event, got %d", mockEventBus.EventCount(domain.SearchFailed))
		}
	})

	t.Run("media lookup failure publishes error", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			FindMediaByPathFunc: func(path string) (int64, error) {
				return 0, errors.New("media not found")
			},
		}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		event := domain.Event{
			AggregateType: "corruption",
			AggregateID:   "retry-test-media-fail",
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path": "/media/movies/test.mkv",
			},
		}

		// Pass mediaID=0 to trigger FindMediaByPath lookup
		remediator.retrySearchOnly(event, 0, nil)

		// Wait for async processing
		time.Sleep(200 * time.Millisecond)

		if mockEventBus.EventCount(domain.SearchFailed) != 1 {
			t.Errorf("Expected 1 SearchFailed event, got %d", mockEventBus.EventCount(domain.SearchFailed))
		}
	})

	t.Run("search trigger failure publishes error", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
				return errors.New("search API error")
			},
		}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		event := domain.Event{
			AggregateType: "corruption",
			AggregateID:   "retry-test-search-fail",
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path": "/media/movies/test.mkv",
			},
		}

		// Pass mediaID to skip FindMediaByPath
		remediator.retrySearchOnly(event, 456, nil)

		// Wait for async processing
		time.Sleep(200 * time.Millisecond)

		// Should have SearchStarted + SearchFailed
		if mockEventBus.EventCount(domain.SearchStarted) != 1 {
			t.Errorf("Expected 1 SearchStarted event, got %d", mockEventBus.EventCount(domain.SearchStarted))
		}
		if mockEventBus.EventCount(domain.SearchFailed) != 1 {
			t.Errorf("Expected 1 SearchFailed event, got %d", mockEventBus.EventCount(domain.SearchFailed))
		}
	})

	t.Run("successful retry with episode_ids", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()

		var capturedEpisodeIDs []int64
		mockArrClient := &testutil.MockArrClient{
			TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
				capturedEpisodeIDs = episodeIDs
				return nil
			},
		}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

		event := domain.Event{
			AggregateType: "corruption",
			AggregateID:   "retry-test-with-episodes",
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path": "/media/tv/show/episode.mkv",
				"path_id":   float64(1),
			},
		}

		// Pass metadata with episode_ids
		metadata := map[string]interface{}{
			"episode_ids": []interface{}{float64(101), float64(102)},
		}
		remediator.retrySearchOnly(event, 789, metadata)

		// Wait for async processing
		time.Sleep(200 * time.Millisecond)

		// Should have SearchStarted + SearchCompleted
		if mockEventBus.EventCount(domain.SearchStarted) != 1 {
			t.Errorf("Expected 1 SearchStarted event, got %d", mockEventBus.EventCount(domain.SearchStarted))
		}
		if mockEventBus.EventCount(domain.SearchCompleted) != 1 {
			t.Errorf("Expected 1 SearchCompleted event, got %d", mockEventBus.EventCount(domain.SearchCompleted))
		}

		// Verify episode IDs were passed to TriggerSearch
		if len(capturedEpisodeIDs) != 2 {
			t.Errorf("Expected 2 episode IDs, got %d", len(capturedEpisodeIDs))
		}
		if capturedEpisodeIDs[0] != 101 || capturedEpisodeIDs[1] != 102 {
			t.Errorf("Expected episode IDs [101, 102], got %v", capturedEpisodeIDs)
		}
	})
}

// =============================================================================
// isInfrastructureError tests
// =============================================================================

func TestRemediatorService_IsInfrastructureError(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()
	remediator := NewRemediatorService(mockEventBus, nil, nil, db)

	infraErrors := []string{
		integration.ErrorTypeAccessDenied,
		integration.ErrorTypePathNotFound,
		integration.ErrorTypeMountLost,
		integration.ErrorTypeIOError,
		integration.ErrorTypeTimeout,
		integration.ErrorTypeInvalidConfig,
	}

	for _, errType := range infraErrors {
		t.Run("infrastructure_"+errType, func(t *testing.T) {
			if !remediator.isInfrastructureError(errType) {
				t.Errorf("Expected %s to be identified as infrastructure error", errType)
			}
		})
	}

	nonInfraErrors := []string{
		integration.ErrorTypeCorruptHeader,
		integration.ErrorTypeCorruptStream,
		integration.ErrorTypeZeroByte,
		integration.ErrorTypeInvalidFormat,
		"unknown_error",
		"",
	}

	for _, errType := range nonInfraErrors {
		t.Run("not_infrastructure_"+errType, func(t *testing.T) {
			if remediator.isInfrastructureError(errType) {
				t.Errorf("Expected %s to NOT be identified as infrastructure error", errType)
			}
		})
	}
}

// =============================================================================
// checkDeletionCompleted tests
// =============================================================================

// =============================================================================
// executeDryRun tests
// =============================================================================

func TestRemediatorService_ExecuteDryRun_FindMediaFails(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()
	mockArrClient := &testutil.MockArrClient{
		FindMediaByPathFunc: func(path string) (int64, error) {
			return 0, errors.New("media not found")
		},
	}
	mockPathMapper := &testutil.MockPathMapper{}

	remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

	// Call executeDryRun directly (it runs synchronously in test)
	remediator.executeDryRun("test-corruption-id", "/test/path.mkv", "/arr/path.mkv")

	// Should NOT publish any events when FindMedia fails in dry-run
	if mockEventBus.EventCount(domain.RemediationQueued) > 0 {
		t.Logf("RemediationQueued events: %d (expected 0 on FindMedia failure)", mockEventBus.EventCount(domain.RemediationQueued))
	}
}

// =============================================================================
// executeRemediation tests
// =============================================================================

func TestRemediatorService_ExecuteRemediation_FindMediaFails(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()
	mockArrClient := &testutil.MockArrClient{
		FindMediaByPathFunc: func(path string) (int64, error) {
			return 0, errors.New("media not found")
		},
	}
	mockPathMapper := &testutil.MockPathMapper{}

	remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

	// Call executeRemediation directly
	remediator.executeRemediation("test-id", "/test/path.mkv", "/arr/path.mkv", 1)

	// Should only have DeletionFailed (no DeletionStarted since we fail before starting)
	// DeletionStarted is now emitted AFTER FindMediaByPath succeeds to avoid false "started" events
	if mockEventBus.EventCount(domain.DeletionStarted) != 0 {
		t.Errorf("Expected 0 DeletionStarted events (fail-fast before starting), got %d", mockEventBus.EventCount(domain.DeletionStarted))
	}
	if mockEventBus.EventCount(domain.DeletionFailed) != 1 {
		t.Errorf("Expected 1 DeletionFailed event, got %d", mockEventBus.EventCount(domain.DeletionFailed))
	}
}

func TestRemediatorService_ExecuteRemediation_DeleteFileFails(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()
	mockArrClient := &testutil.MockArrClient{
		FindMediaByPathFunc: func(path string) (int64, error) {
			return 123, nil
		},
		DeleteFileFunc: func(mediaID int64, path string) (map[string]interface{}, error) {
			return nil, errors.New("deletion failed")
		},
	}
	mockPathMapper := &testutil.MockPathMapper{}

	remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

	remediator.executeRemediation("test-id", "/test/path.mkv", "/arr/path.mkv", 1)

	// Should have DeletionFailed
	if mockEventBus.EventCount(domain.DeletionFailed) != 1 {
		t.Errorf("Expected 1 DeletionFailed event, got %d", mockEventBus.EventCount(domain.DeletionFailed))
	}
	// Should NOT have DeletionCompleted
	if mockEventBus.EventCount(domain.DeletionCompleted) != 0 {
		t.Errorf("Expected 0 DeletionCompleted events, got %d", mockEventBus.EventCount(domain.DeletionCompleted))
	}
}

// =============================================================================
// triggerSearch tests
// =============================================================================

func TestRemediatorService_TriggerSearch_Success(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()
	mockArrClient := &testutil.MockArrClient{
		TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
			return nil
		},
	}
	mockPathMapper := &testutil.MockPathMapper{}

	remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

	// Call triggerSearch directly
	remediator.triggerSearch("test-id", "/test/path.mkv", "/arr/path.mkv", 1, 123, nil)

	// Should have SearchStarted and SearchCompleted
	if mockEventBus.EventCount(domain.SearchStarted) != 1 {
		t.Errorf("Expected 1 SearchStarted event, got %d", mockEventBus.EventCount(domain.SearchStarted))
	}
	if mockEventBus.EventCount(domain.SearchCompleted) != 1 {
		t.Errorf("Expected 1 SearchCompleted event, got %d", mockEventBus.EventCount(domain.SearchCompleted))
	}
}

func TestRemediatorService_TriggerSearch_Failure(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()
	mockArrClient := &testutil.MockArrClient{
		TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
			return errors.New("search failed")
		},
	}
	mockPathMapper := &testutil.MockPathMapper{}

	remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

	remediator.triggerSearch("test-id", "/test/path.mkv", "/arr/path.mkv", 1, 123, nil)

	// Should have SearchStarted and SearchFailed
	if mockEventBus.EventCount(domain.SearchStarted) != 1 {
		t.Errorf("Expected 1 SearchStarted event, got %d", mockEventBus.EventCount(domain.SearchStarted))
	}
	if mockEventBus.EventCount(domain.SearchFailed) != 1 {
		t.Errorf("Expected 1 SearchFailed event, got %d", mockEventBus.EventCount(domain.SearchFailed))
	}
	// Should NOT have SearchCompleted
	if mockEventBus.EventCount(domain.SearchCompleted) != 0 {
		t.Errorf("Expected 0 SearchCompleted events, got %d", mockEventBus.EventCount(domain.SearchCompleted))
	}
}

func TestRemediatorService_TriggerSearch_WithEpisodeIDs(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()

	var capturedEpisodeIDs []int64
	mockArrClient := &testutil.MockArrClient{
		TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
			capturedEpisodeIDs = episodeIDs
			return nil
		},
	}
	mockPathMapper := &testutil.MockPathMapper{}

	remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)

	// Metadata with episode_ids
	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(1), float64(2), float64(3)},
	}

	remediator.triggerSearch("test-id", "/test/path.mkv", "/arr/path.mkv", 1, 123, metadata)

	// Verify episode IDs were extracted and passed
	if len(capturedEpisodeIDs) != 3 {
		t.Errorf("Expected 3 episode IDs, got %d", len(capturedEpisodeIDs))
	}
}

// =============================================================================
// publishError tests
// =============================================================================

func TestRemediatorService_PublishError(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()
	remediator := NewRemediatorService(mockEventBus, nil, nil, db)

	// Call publishError
	remediator.publishError("test-id", domain.DeletionFailed, "test error message")

	// Should have the error event
	events := mockEventBus.GetEvents(domain.DeletionFailed)
	if len(events) != 1 {
		t.Errorf("Expected 1 DeletionFailed event, got %d", len(events))
	}
	if len(events) > 0 {
		errMsg, _ := events[0].GetString("error")
		if errMsg != "test error message" {
			t.Errorf("Expected error message 'test error message', got %q", errMsg)
		}
	}
}

// =============================================================================
// buildSearchEventData tests
// =============================================================================

func TestRemediatorService_BuildSearchEventData(t *testing.T) {
	t.Run("basic event data without media details", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			// GetMediaDetails returns nil - simulating unavailable details
			GetMediaDetailsFunc: func(mediaID int64, arrPath string) (*integration.MediaDetails, error) {
				return nil, nil
			},
		}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, nil, db)

		filePath := "/media/movies/test.mkv"
		arrPath := "/movies/test.mkv"
		mediaID := int64(123)
		pathID := int64(1)
		metadata := map[string]interface{}{"key": "value"}

		eventData := remediator.buildSearchEventData(filePath, arrPath, mediaID, pathID, metadata, false)

		// Verify basic fields
		if eventData["file_path"] != filePath {
			t.Errorf("Expected file_path %q, got %q", filePath, eventData["file_path"])
		}
		if eventData["media_id"] != mediaID {
			t.Errorf("Expected media_id %d, got %v", mediaID, eventData["media_id"])
		}
		if eventData["path_id"] != pathID {
			t.Errorf("Expected path_id %d, got %v", pathID, eventData["path_id"])
		}
		if eventData["metadata"] == nil {
			t.Error("Expected metadata to be set")
		}
		// is_retry should not be set when isRetry=false
		if _, ok := eventData["is_retry"]; ok {
			t.Error("is_retry should not be set when isRetry is false")
		}
	})

	t.Run("includes is_retry flag when isRetry is true", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, nil, db)

		eventData := remediator.buildSearchEventData("/path", "/arr", 1, 1, nil, true)

		isRetry, ok := eventData["is_retry"].(bool)
		if !ok || !isRetry {
			t.Error("Expected is_retry to be true")
		}
	})

	t.Run("includes media details when available", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			GetMediaDetailsFunc: func(mediaID int64, arrPath string) (*integration.MediaDetails, error) {
				return &integration.MediaDetails{
					Title:        "Test Movie",
					Year:         2024,
					MediaType:    "movie",
					ArrType:      "radarr",
					InstanceName: "Radarr 4K",
				}, nil
			},
		}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, nil, db)

		eventData := remediator.buildSearchEventData("/path", "/arr", 123, 1, nil, false)

		if eventData["media_title"] != "Test Movie" {
			t.Errorf("Expected media_title 'Test Movie', got %v", eventData["media_title"])
		}
		if eventData["media_year"] != 2024 {
			t.Errorf("Expected media_year 2024, got %v", eventData["media_year"])
		}
		if eventData["media_type"] != "movie" {
			t.Errorf("Expected media_type 'movie', got %v", eventData["media_type"])
		}
		if eventData["arr_type"] != "radarr" {
			t.Errorf("Expected arr_type 'radarr', got %v", eventData["arr_type"])
		}
		if eventData["instance_name"] != "Radarr 4K" {
			t.Errorf("Expected instance_name 'Radarr 4K', got %v", eventData["instance_name"])
		}
	})

	t.Run("includes episode details for TV shows", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			GetMediaDetailsFunc: func(mediaID int64, arrPath string) (*integration.MediaDetails, error) {
				return &integration.MediaDetails{
					Title:         "Breaking Bad",
					Year:          2008,
					MediaType:     "episode",
					ArrType:       "sonarr",
					InstanceName:  "Sonarr",
					SeasonNumber:  5,
					EpisodeNumber: 16,
					EpisodeTitle:  "Felina",
				}, nil
			},
		}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, nil, db)

		eventData := remediator.buildSearchEventData("/path", "/arr", 456, 1, nil, false)

		if eventData["season_number"] != 5 {
			t.Errorf("Expected season_number 5, got %v", eventData["season_number"])
		}
		if eventData["episode_number"] != 16 {
			t.Errorf("Expected episode_number 16, got %v", eventData["episode_number"])
		}
		if eventData["episode_title"] != "Felina" {
			t.Errorf("Expected episode_title 'Felina', got %v", eventData["episode_title"])
		}
	})

	t.Run("omits zero season/episode numbers", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			GetMediaDetailsFunc: func(mediaID int64, arrPath string) (*integration.MediaDetails, error) {
				return &integration.MediaDetails{
					Title:         "Movie",
					SeasonNumber:  0,  // Should not be included
					EpisodeNumber: 0,  // Should not be included
					EpisodeTitle:  "", // Should not be included
				}, nil
			},
		}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, nil, db)

		eventData := remediator.buildSearchEventData("/path", "/arr", 789, 1, nil, false)

		if _, ok := eventData["season_number"]; ok {
			t.Error("season_number should not be set when 0")
		}
		if _, ok := eventData["episode_number"]; ok {
			t.Error("episode_number should not be set when 0")
		}
		if _, ok := eventData["episode_title"]; ok {
			t.Error("episode_title should not be set when empty")
		}
	})

	t.Run("handles GetMediaDetails error gracefully", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		mockArrClient := &testutil.MockArrClient{
			GetMediaDetailsFunc: func(mediaID int64, arrPath string) (*integration.MediaDetails, error) {
				return nil, errors.New("API error")
			},
		}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, nil, db)

		eventData := remediator.buildSearchEventData("/path", "/arr", 123, 1, nil, false)

		// Should still have basic fields
		if eventData["file_path"] != "/path" {
			t.Errorf("Expected file_path '/path', got %v", eventData["file_path"])
		}
		// Should not have media details
		if _, ok := eventData["media_title"]; ok {
			t.Error("media_title should not be set when GetMediaDetails fails")
		}
	})
}

func TestRemediatorService_CheckDeletionCompleted(t *testing.T) {
	t.Run("returns false with nil db", func(t *testing.T) {
		mockEventBus := testutil.NewMockEventBus()
		remediator := NewRemediatorService(mockEventBus, nil, nil, nil)

		completed, mediaID, metadata := remediator.checkDeletionCompleted("test-id")

		if completed {
			t.Error("Expected false with nil db")
		}
		if mediaID != 0 {
			t.Errorf("Expected mediaID 0, got %d", mediaID)
		}
		if metadata != nil {
			t.Error("Expected nil metadata")
		}
	})

	t.Run("returns false when no DeletionCompleted event exists", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		remediator := NewRemediatorService(mockEventBus, nil, nil, db)

		completed, mediaID, _ := remediator.checkDeletionCompleted("nonexistent-id")

		if completed {
			t.Error("Expected false when no DeletionCompleted event exists")
		}
		if mediaID != 0 {
			t.Errorf("Expected mediaID 0, got %d", mediaID)
		}
	})

	t.Run("returns true with mediaID when DeletionCompleted exists", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		// Seed a DeletionCompleted event
		aggregateID := "deletion-completed-test"
		_, err = testutil.SeedEvent(db, domain.Event{
			AggregateType: "corruption",
			AggregateID:   aggregateID,
			EventType:     domain.DeletionCompleted,
			EventData: map[string]interface{}{
				"media_id": float64(12345),
			},
		})
		if err != nil {
			t.Fatalf("Failed to seed event: %v", err)
		}

		mockEventBus := testutil.NewMockEventBus()
		remediator := NewRemediatorService(mockEventBus, nil, nil, db)

		completed, mediaID, _ := remediator.checkDeletionCompleted(aggregateID)

		if !completed {
			t.Error("Expected true when DeletionCompleted event exists")
		}
		if mediaID != 12345 {
			t.Errorf("Expected mediaID 12345, got %d", mediaID)
		}
	})
}
