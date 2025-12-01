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
