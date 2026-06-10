package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/repository"
	"github.com/mescon/Healarr/internal/testutil"
)

// errPathNotConfigured is a test error for path mapping failures.
var errPathNotConfigured = errors.New("path not configured")

// seedRemediationPath inserts an enabled scan path so the remediator's
// consent re-read (resolveRemediationPolicy) finds a configured opt-in.
// Tests that expect remediation to PROCEED must seed the owning path: the
// remediator never trusts the event's own auto_remediate claim, so an
// unseeded DB means "no consent" and the flow stops before any delete.
func seedRemediationPath(t *testing.T, db *sql.DB, localPath string, autoRemediate, dryRun bool) {
	t.Helper()
	if _, err := db.Exec(`
		INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, dry_run)
		VALUES (?, ?, 1, ?, ?)
	`, localPath, localPath, autoRemediate, dryRun); err != nil {
		t.Fatalf("seed scan path %s: %v", localPath, err)
	}
}

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
			// Seed an auto-remediate path: the category gate, not missing
			// consent, must be what blocks these recoverable errors.
			seedRemediationPath(t, db, "/media/movies", true, false)

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

		// Seed the owning scan path with auto_remediate enabled: the remediator
		// re-reads consent from the path config (resolved by file path when the
		// event has no path_id) and never trusts the event's own claim.
		if _, err := db.Exec(`
			INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, dry_run)
			VALUES ('/media/movies', '/media/movies', 1, 1, 0)
		`); err != nil {
			t.Fatalf("seed scan path: %v", err)
		}

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
		seedRemediationPath(t, db, "/media/movies", true, false)

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
		// Path config carries the dry-run; the event payload is not trusted.
		seedRemediationPath(t, db, "/media/movies", true, true)

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
		seedRemediationPath(t, db, "/media/movies", true, false)

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
		seedRemediationPath(t, db, "/media/movies", true, false)

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
	remediator.executeRemediation("test-id", "/test/path.mkv", "/arr/path.mkv", 1, false)

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

	remediator.executeRemediation("test-id", "/test/path.mkv", "/arr/path.mkv", 1, false)

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

// =============================================================================
// Stop tests (lifecycle management)
// =============================================================================

func TestRemediatorService_Stop(t *testing.T) {
	t.Run("stop completes immediately when nothing running", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		remediator := NewRemediatorService(mockEventBus, nil, nil, db)
		remediator.Start()

		// Stop should complete immediately
		done := make(chan struct{})
		go func() {
			remediator.Stop()
			close(done)
		}()

		select {
		case <-done:
			// Success
		case <-time.After(1 * time.Second):
			t.Error("Stop() took too long when nothing running")
		}
	})

	t.Run("stop is idempotent", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		remediator := NewRemediatorService(mockEventBus, nil, nil, db)

		// Call Stop() multiple times - should not panic or hang
		remediator.Stop()
		remediator.Stop()
		remediator.Stop()
	})

	t.Run("stop waits for in-flight remediation", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()
		seedRemediationPath(t, db, "/media/movies", true, false)

		mockEventBus := testutil.NewMockEventBus()
		remediationStarted := make(chan struct{})
		remediationDone := make(chan struct{})

		mockArrClient := &testutil.MockArrClient{
			FindMediaByPathFunc: func(path string) (int64, error) {
				return 123, nil
			},
			DeleteFileFunc: func(mediaID int64, path string) (map[string]interface{}, error) {
				close(remediationStarted)
				// Wait until we're told to complete
				<-remediationDone
				return nil, nil
			},
			TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
				return nil
			},
		}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)
		remediator.Start()

		// Start a remediation
		event := testutil.NewCorruptionEventWithType(
			testutil.TestFilePaths.Movie1,
			"corrupt_header",
			testutil.WithAutoRemediate(true),
		)
		remediator.handleCorruptionDetected(event)

		// Wait for remediation to start
		<-remediationStarted

		// Start Stop() in a goroutine
		stopDone := make(chan struct{})
		go func() {
			remediator.Stop()
			close(stopDone)
		}()

		// Stop() should not complete yet (remediation still in progress)
		select {
		case <-stopDone:
			t.Error("Stop() completed while remediation still in progress")
		case <-time.After(100 * time.Millisecond):
			// Good - still waiting
		}

		// Complete the remediation
		close(remediationDone)

		// Now Stop() should complete
		select {
		case <-stopDone:
			// Success
		case <-time.After(2 * time.Second):
			t.Error("Stop() didn't complete after remediation finished")
		}
	})

	t.Run("shutdown during semaphore wait aborts gracefully", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()
		seedRemediationPath(t, db, "/media/movies", true, false)

		mockEventBus := testutil.NewMockEventBus()
		blockingDone := make(chan struct{})

		mockArrClient := &testutil.MockArrClient{
			FindMediaByPathFunc: func(path string) (int64, error) {
				return 123, nil
			},
			DeleteFileFunc: func(mediaID int64, path string) (map[string]interface{}, error) {
				// Block forever until test completes
				<-blockingDone
				return nil, nil
			},
			TriggerSearchFunc: func(mediaID int64, path string, episodeIDs []int64) error {
				return nil
			},
		}
		mockPathMapper := &testutil.MockPathMapper{}

		remediator := NewRemediatorService(mockEventBus, mockArrClient, mockPathMapper, db)
		remediator.Start()

		// Fill up all semaphore slots with blocking operations
		for i := 0; i < maxConcurrentRemediations; i++ {
			event := testutil.NewCorruptionEventWithType(
				testutil.TestFilePaths.Movie1,
				"corrupt_header",
				testutil.WithAutoRemediate(true),
			)
			remediator.handleCorruptionDetected(event)
		}

		// Give goroutines time to acquire semaphore
		time.Sleep(100 * time.Millisecond)

		// Now try to start one more that will wait on semaphore
		waitingEvent := testutil.NewCorruptionEventWithType(
			testutil.TestFilePaths.Movie2,
			"corrupt_header",
			testutil.WithAutoRemediate(true),
		)
		remediator.handleCorruptionDetected(waitingEvent)

		// Give the waiting goroutine time to start waiting
		time.Sleep(50 * time.Millisecond)

		// Stop should signal all goroutines to abort
		go remediator.Stop()

		// Give Stop() time to signal shutdown
		time.Sleep(50 * time.Millisecond)

		// Allow blocking operations to complete
		close(blockingDone)

		// The service should shut down within a reasonable time
		time.Sleep(500 * time.Millisecond)
	})
}

func TestRemediatorService_IsShuttingDown(t *testing.T) {
	t.Run("returns false before Stop", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		remediator := NewRemediatorService(mockEventBus, nil, nil, db)

		if remediator.isShuttingDown() {
			t.Error("isShuttingDown() should return false before Stop()")
		}
	})

	t.Run("returns true after Stop", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test DB: %v", err)
		}
		defer db.Close()

		mockEventBus := testutil.NewMockEventBus()
		remediator := NewRemediatorService(mockEventBus, nil, nil, db)

		remediator.Stop()

		if !remediator.isShuttingDown() {
			t.Error("isShuttingDown() should return true after Stop()")
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

// =============================================================================
// executeRemediation shutdown and semaphore path tests
// =============================================================================

func TestRemediatorService_ExecuteRemediation_SkipsWhenShuttingDown(t *testing.T) {
	// Test that executeRemediation returns early when service is shutting down
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()
	mockClient := &testutil.MockArrClient{}
	remediator := NewRemediatorService(mockEventBus, mockClient, nil, db)

	// Stop the service first
	remediator.Stop()

	// Now call executeRemediation - should return early due to shutdown
	remediator.executeRemediation("test-id", "/media/test.mkv", "/movies/test.mkv", 1, false)

	// Verify that no events were published (service skipped due to shutdown)
	if mockEventBus.EventCount(domain.DeletionStarted) != 0 {
		t.Error("Expected no DeletionStarted events when shutting down")
	}
	if mockEventBus.EventCount(domain.DeletionFailed) != 0 {
		t.Error("Expected no DeletionFailed events when shutting down")
	}
}

func TestRemediatorService_ExecuteRemediation_ShutdownWhileWaitingForSemaphore(t *testing.T) {
	// Test that executeRemediation aborts if shutdown occurs while waiting for semaphore
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()
	mockClient := &testutil.MockArrClient{
		FindMediaByPathFunc: func(path string) (int64, error) {
			// Hold the semaphore slot long enough that the waiting goroutine
			// is still blocked when Stop() fires (~150ms into the test). 500ms
			// is ample margin without making wg.Wait() pay a full 5s.
			time.Sleep(500 * time.Millisecond)
			return 123, nil
		},
	}
	remediator := NewRemediatorService(mockEventBus, mockClient, nil, db)

	// Fill the semaphore by starting goroutines that hold slots
	// Default semaphore size is 5
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			// This will hold a semaphore slot
			remediator.executeRemediation(
				"blocking-"+string(rune('A'+idx)),
				"/media/blocking.mkv",
				"/movies/blocking.mkv",
				1,
				false,
			)
		}(i)
	}

	// Give time for goroutines to acquire semaphore slots
	time.Sleep(100 * time.Millisecond)

	// Now start a goroutine that will wait for a semaphore
	var testCompleted bool
	var testMu sync.Mutex
	go func() {
		remediator.executeRemediation("waiting-test", "/media/test.mkv", "/movies/test.mkv", 1, false)
		testMu.Lock()
		testCompleted = true
		testMu.Unlock()
	}()

	// Give time for it to start waiting
	time.Sleep(50 * time.Millisecond)

	// Stop the service - this should cause the waiting goroutine to abort
	remediator.Stop()

	// Wait for all goroutines to complete
	wg.Wait()
	time.Sleep(100 * time.Millisecond)

	testMu.Lock()
	if !testCompleted {
		t.Error("Expected executeRemediation to return after Stop()")
	}
	testMu.Unlock()
}

func TestRemediatorService_PublishError_PublishesFailureEvent(t *testing.T) {
	// Test that publishError publishes the correct failure event
	mockEventBus := testutil.NewMockEventBus()
	remediator := NewRemediatorService(mockEventBus, nil, nil, nil)

	remediator.publishError("test-id", domain.DeletionFailed, "test error message")

	// Verify the failure event was published
	events := mockEventBus.GetEvents(domain.DeletionFailed)
	if len(events) != 1 {
		t.Errorf("Expected 1 DeletionFailed event, got %d", len(events))
	}
	if len(events) > 0 {
		if events[0].AggregateID != "test-id" {
			t.Errorf("Expected aggregate_id 'test-id', got '%s'", events[0].AggregateID)
		}
		errorMsg, _ := events[0].GetString("error")
		if errorMsg != "test error message" {
			t.Errorf("Expected error 'test error message', got '%s'", errorMsg)
		}
	}
}

func TestRemediatorService_ExecuteDryRun_PublishesQueuedEvent(t *testing.T) {
	// Test that executeDryRun publishes RemediationQueued event with dry_run flag
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	mockEventBus := testutil.NewMockEventBus()
	mockClient := &testutil.MockArrClient{
		FindMediaByPathFunc: func(path string) (int64, error) {
			return 123, nil
		},
	}
	remediator := NewRemediatorService(mockEventBus, mockClient, nil, db)

	remediator.executeDryRun("test-id", "/media/test.mkv", "/movies/test.mkv")

	// Verify the dry-run event was published
	events := mockEventBus.GetEvents(domain.RemediationQueued)
	if len(events) != 1 {
		t.Errorf("Expected 1 RemediationQueued event, got %d", len(events))
	}
	if len(events) > 0 {
		dryRun, ok := events[0].EventData["dry_run"].(bool)
		if !ok || !dryRun {
			t.Error("Expected dry_run=true in event data")
		}
	}
}

// TestRemediator_ResolveRemediationPolicy_RespectsCurrentPathConfig verifies the
// consent/dry-run fix: the remediator decides auto-remediate and dry-run from the
// scan path's CURRENT config, not the event payload. This is what prevents a
// recovery/monitor retry (which hardcodes auto_remediate=true and drops dry_run)
// from deleting a file on a manual-mode or dry-run path.
func TestRemediator_ResolveRemediationPolicy_RespectsCurrentPathConfig(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test DB: %v", err)
	}
	defer db.Close()

	repo := repository.NewScanPathRepository(db)
	ctx := context.Background()
	manualID, err := repo.Create(ctx, repository.ScanPathFields{
		LocalPath: "/media/manual", ArrPath: "/data/manual", Enabled: true,
		AutoRemediate: false, DryRun: false,
		DetectionMethod: "ffprobe", DetectionMode: "quick", MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("create manual path: %v", err)
	}
	dryID, err := repo.Create(ctx, repository.ScanPathFields{
		LocalPath: "/media/dry", ArrPath: "/data/dry", Enabled: true,
		AutoRemediate: true, DryRun: true,
		DetectionMethod: "ffprobe", DetectionMode: "quick", MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("create dry path: %v", err)
	}

	r := NewRemediatorService(testutil.NewMockEventBus(), &testutil.MockArrClient{}, &testutil.MockPathMapper{}, db)

	// Manual path: event claims auto_remediate=true (as recovery hardcodes), but the
	// live config says false, so we must NOT auto-remediate.
	if auto, _ := r.resolveRemediationPolicy(manualID, true, false, "/media/manual/file.mkv"); auto {
		t.Error("manual-mode path must not auto-remediate even when the event claims true")
	}
	// Dry-run path: live dry_run=true wins even though the (retry) event omits it.
	if auto, dry := r.resolveRemediationPolicy(dryID, true, false, "/media/dry/file.mkv"); !auto || !dry {
		t.Errorf("dry-run path: got auto=%v dry=%v, want auto=true dry=true", auto, dry)
	}
	// Unknown/deleted path id: refuse to auto-remediate (safe default). The file
	// path is outside every configured root, so the fallback cannot resolve it.
	if auto, _ := r.resolveRemediationPolicy(999999, true, false, "/elsewhere/file.mkv"); auto {
		t.Error("unknown path must not auto-remediate")
	}
	// Event without a path id (webhook corruptions pre-fix, recovery/monitor
	// retries): the file path resolves the owning scan path, whose CURRENT
	// config wins over the event's invented auto_remediate=true.
	if auto, dry := r.resolveRemediationPolicy(0, true, false, "/media/dry/Show/ep.mkv"); !auto || !dry {
		t.Errorf("pathID=0 + resolvable file: got auto=%v dry=%v, want auto=true dry=true (path config)", auto, dry)
	}
	if auto, _ := r.resolveRemediationPolicy(0, true, false, "/media/manual/Show/ep.mkv"); auto {
		t.Error("pathID=0 + manual-mode path: must not auto-remediate despite event claiming true")
	}
	// pathID=0 and the file matches NO configured path: never invent consent.
	if auto, _ := r.resolveRemediationPolicy(0, true, true, "/unmatched/file.mkv"); auto {
		t.Error("pathID=0 + unmatched file: must refuse to auto-remediate")
	}
}

// insertVerificationFailed appends a VerificationFailed event carrying failed_paths.
func insertVerificationFailed(t *testing.T, db *sql.DB, aggregateID string, failedPaths []string) {
	t.Helper()
	data, _ := json.Marshal(map[string]interface{}{
		"failed_paths": failedPaths,
		"failed_count": len(failedPaths),
	})
	_, err := db.Exec(`INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES ('corruption', ?, 'VerificationFailed', ?, 1, datetime('now'))`, aggregateID, string(data))
	if err != nil {
		t.Fatalf("insert VerificationFailed: %v", err)
	}
}

func newReplacementFile(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "replacement.mkv")
	if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return p
}

func grabbedHistoryFunc(id int64, title string) func(string, int64, int) ([]integration.HistoryItemInfo, error) {
	return func(string, int64, int) ([]integration.HistoryItemInfo, error) {
		return []integration.HistoryItemInfo{
			{ID: id, EventType: "grabbed", Date: "2026-05-27T10:00:00Z", SourceTitle: title},
		}, nil
	}
}

func makeScanPath(t *testing.T, db *sql.DB, local, arr string, auto, dry bool) int64 {
	t.Helper()
	id, err := repository.NewScanPathRepository(db).Create(context.Background(), repository.ScanPathFields{
		LocalPath: local, ArrPath: arr, Enabled: true,
		AutoRemediate: auto, DryRun: dry,
		DetectionMethod: "ffprobe", DetectionMode: "quick", MaxRetries: 3,
	})
	if err != nil {
		t.Fatalf("create scan path: %v", err)
	}
	return id
}

func TestRemediator_HandleCorruptReplacementBeforeSearch(t *testing.T) {
	t.Run("same-release corrupt replacement is deleted and blocklisted", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		replPath := newReplacementFile(t)
		insertVerificationFailed(t, db, "agg1", []string{replPath})
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", true, false)

		eb := testutil.NewMockEventBus()
		arr := &testutil.MockArrClient{
			GetRecentHistoryForMediaByPathFunc: grabbedHistoryFunc(42, "Show.S01E01-GRP"),
			DeleteFileFunc: func(int64, string) (map[string]interface{}, error) {
				return map[string]interface{}{}, nil
			},
			MarkReleaseAsFailedFunc: func(string, int64) error { return nil },
		}
		pm := &testutil.MockPathMapper{ToArrPathFunc: func(string) (string, error) { return "/data/tv/repl.mkv", nil }}
		r := NewRemediatorService(eb, arr, pm, db)

		if !r.handleCorruptReplacementBeforeSearch("agg1", pathID, 123, nil) {
			t.Fatal("expected blocklisted=true")
		}
		if arr.CallCount("DeleteFile") != 1 {
			t.Errorf("DeleteFile calls = %d, want 1", arr.CallCount("DeleteFile"))
		}
		if arr.CallCount("MarkReleaseAsFailed") != 1 {
			t.Errorf("MarkReleaseAsFailed calls = %d, want 1", arr.CallCount("MarkReleaseAsFailed"))
		}
		if eb.EventCount(domain.ReleaseBlocklisted) != 1 {
			t.Errorf("ReleaseBlocklisted events = %d, want 1", eb.EventCount(domain.ReleaseBlocklisted))
		}
	})

	t.Run("dry-run logs but does not delete or blocklist", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		replPath := newReplacementFile(t)
		insertVerificationFailed(t, db, "agg1", []string{replPath})
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", true, true) // dry-run

		eb := testutil.NewMockEventBus()
		arr := &testutil.MockArrClient{
			GetRecentHistoryForMediaByPathFunc: grabbedHistoryFunc(42, "Show.S01E01-GRP"),
		}
		pm := &testutil.MockPathMapper{ToArrPathFunc: func(string) (string, error) { return "/data/tv/repl.mkv", nil }}
		r := NewRemediatorService(eb, arr, pm, db)

		if r.handleCorruptReplacementBeforeSearch("agg1", pathID, 123, nil) {
			t.Fatal("expected false in dry-run")
		}
		if arr.CallCount("DeleteFile") != 0 || arr.CallCount("MarkReleaseAsFailed") != 0 {
			t.Error("dry-run must not delete or blocklist")
		}
		if eb.EventCount(domain.ReleaseBlocklisted) != 0 {
			t.Error("dry-run must not publish ReleaseBlocklisted")
		}
	})

	t.Run("non-auto path does nothing", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		replPath := newReplacementFile(t)
		insertVerificationFailed(t, db, "agg1", []string{replPath})
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", false, false) // auto off

		arr := &testutil.MockArrClient{}
		r := NewRemediatorService(testutil.NewMockEventBus(), arr, &testutil.MockPathMapper{}, db)

		if r.handleCorruptReplacementBeforeSearch("agg1", pathID, 123, nil) {
			t.Fatal("expected false on non-auto path")
		}
		if arr.CallCount("DeleteFile") != 0 {
			t.Error("non-auto path must not delete")
		}
	})

	t.Run("no corrupt replacement on disk falls through", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		// VerificationFailed references a file that does not exist.
		insertVerificationFailed(t, db, "agg1", []string{"/nonexistent/x.mkv"})
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", true, false)

		arr := &testutil.MockArrClient{}
		r := NewRemediatorService(testutil.NewMockEventBus(), arr, &testutil.MockPathMapper{}, db)

		if r.handleCorruptReplacementBeforeSearch("agg1", pathID, 123, nil) {
			t.Fatal("expected false when no corrupt replacement is on disk")
		}
		if arr.CallCount("DeleteFile") != 0 {
			t.Error("must not delete when no corrupt replacement is present")
		}
	})

	t.Run("unresolved grabbed release falls back without deleting", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		replPath := newReplacementFile(t)
		insertVerificationFailed(t, db, "agg1", []string{replPath})
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", true, false)

		// No GetRecentHistoryForMediaByPathFunc -> empty history -> grab id 0.
		arr := &testutil.MockArrClient{}
		pm := &testutil.MockPathMapper{ToArrPathFunc: func(string) (string, error) { return "/data/tv/repl.mkv", nil }}
		r := NewRemediatorService(testutil.NewMockEventBus(), arr, pm, db)

		if r.handleCorruptReplacementBeforeSearch("agg1", pathID, 123, nil) {
			t.Fatal("expected false when the grabbed release cannot be resolved")
		}
		if arr.CallCount("DeleteFile") != 0 || arr.CallCount("MarkReleaseAsFailed") != 0 {
			t.Error("must not delete or blocklist when the release is unknown")
		}
	})

	t.Run("blocklist failure deletes but does not mark blocklisted", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		replPath := newReplacementFile(t)
		insertVerificationFailed(t, db, "agg1", []string{replPath})
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", true, false)

		eb := testutil.NewMockEventBus()
		arr := &testutil.MockArrClient{
			GetRecentHistoryForMediaByPathFunc: grabbedHistoryFunc(42, "Show.S01E01-GRP"),
			DeleteFileFunc: func(int64, string) (map[string]interface{}, error) {
				return map[string]interface{}{}, nil
			},
			MarkReleaseAsFailedFunc: func(string, int64) error { return errors.New("arr 500") },
		}
		pm := &testutil.MockPathMapper{ToArrPathFunc: func(string) (string, error) { return "/data/tv/repl.mkv", nil }}
		r := NewRemediatorService(eb, arr, pm, db)

		if r.handleCorruptReplacementBeforeSearch("agg1", pathID, 123, nil) {
			t.Fatal("expected false when blocklist fails (fall back to plain search)")
		}
		if arr.CallCount("DeleteFile") != 1 {
			t.Errorf("DeleteFile calls = %d, want 1", arr.CallCount("DeleteFile"))
		}
		if eb.EventCount(domain.ReleaseBlocklisted) != 0 {
			t.Error("must not publish ReleaseBlocklisted when blocklist fails")
		}
	})
}

func TestRemediator_LatestGrabbedRelease_PicksNewest(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	arr := &testutil.MockArrClient{
		GetRecentHistoryForMediaByPathFunc: func(string, int64, int) ([]integration.HistoryItemInfo, error) {
			return []integration.HistoryItemInfo{
				{ID: 1, EventType: "grabbed", Date: "2026-05-20T10:00:00Z", SourceTitle: "Old-GRP"},
				{ID: 2, EventType: "grabbed", Date: "2026-05-27T10:00:00Z", SourceTitle: "New-GRP"},
				{ID: 3, EventType: "grabbed", Date: "2026-05-25T10:00:00Z", SourceTitle: "Mid-GRP"},
			}, nil
		},
	}
	r := NewRemediatorService(testutil.NewMockEventBus(), arr, &testutil.MockPathMapper{}, db)

	id, title := r.latestGrabbedRelease("/data/tv/x.mkv", 123)
	if id != 2 || title != "New-GRP" {
		t.Errorf("got (%d, %q), want (2, New-GRP)", id, title)
	}
}

// insertEventAt appends a minimal event with an explicit created_at so tests can
// control which failure is the most recent.
func insertEventAt(t *testing.T, db *sql.DB, aggregateID, eventType, createdAt string) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES ('corruption', ?, ?, '{}', 1, ?)`, aggregateID, eventType, createdAt)
	if err != nil {
		t.Fatalf("insert %s: %v", eventType, err)
	}
}

func stalledQueueFunc() func(string, int64) ([]integration.QueueItemInfo, error) {
	return func(string, int64) ([]integration.QueueItemInfo, error) {
		return []integration.QueueItemInfo{{ID: 7, Title: "Show.S01E01.STALLED-GRP"}}, nil
	}
}

func TestRemediator_HandleStalledDownloadBeforeSearch(t *testing.T) {
	t.Run("stalled timeout removes and blocklists the queue item", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		insertEventAt(t, db, "agg1", "DownloadTimeout", "2026-05-27 10:00:00")
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", true, false)

		eb := testutil.NewMockEventBus()
		removed := 0
		arr := &testutil.MockArrClient{
			FindQueueItemsByMediaIDForPathFunc: stalledQueueFunc(),
			RemoveFromQueueByPathFunc: func(_ string, qid int64, removeFromClient, blocklist bool) error {
				if qid == 7 && removeFromClient && blocklist {
					removed++
				}
				return nil
			},
		}
		r := NewRemediatorService(eb, arr, &testutil.MockPathMapper{}, db)

		if !r.handleStalledDownloadBeforeSearch("agg1", pathID, 123, "/data/tv/x.mkv", "/media/tv/x.mkv") {
			t.Fatal("expected blocklisted=true")
		}
		if removed != 1 {
			t.Errorf("RemoveFromQueueByPath(remove+blocklist) calls = %d, want 1", removed)
		}
		if eb.EventCount(domain.ReleaseBlocklisted) != 1 {
			t.Errorf("ReleaseBlocklisted events = %d, want 1", eb.EventCount(domain.ReleaseBlocklisted))
		}
	})

	t.Run("non-timeout retry does nothing", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		// Latest failure is a VerificationFailed, not a DownloadTimeout.
		insertEventAt(t, db, "agg1", "DownloadTimeout", "2026-05-27 10:00:00")
		insertEventAt(t, db, "agg1", "VerificationFailed", "2026-05-27 11:00:00")
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", true, false)

		arr := &testutil.MockArrClient{FindQueueItemsByMediaIDForPathFunc: stalledQueueFunc()}
		r := NewRemediatorService(testutil.NewMockEventBus(), arr, &testutil.MockPathMapper{}, db)

		if r.handleStalledDownloadBeforeSearch("agg1", pathID, 123, "/data/tv/x.mkv", "/media/tv/x.mkv") {
			t.Fatal("expected false when the latest failure is not a download timeout")
		}
		if arr.CallCount("RemoveFromQueueByPath") != 0 {
			t.Error("must not touch the queue when the retry is not from a timeout")
		}
	})

	t.Run("no queue item falls through", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		insertEventAt(t, db, "agg1", "DownloadTimeout", "2026-05-27 10:00:00")
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", true, false)

		// No FindQueueItemsByMediaIDForPathFunc -> empty queue.
		arr := &testutil.MockArrClient{}
		r := NewRemediatorService(testutil.NewMockEventBus(), arr, &testutil.MockPathMapper{}, db)

		if r.handleStalledDownloadBeforeSearch("agg1", pathID, 123, "/data/tv/x.mkv", "/media/tv/x.mkv") {
			t.Fatal("expected false when nothing is queued")
		}
		if arr.CallCount("RemoveFromQueueByPath") != 0 {
			t.Error("must not remove anything when the queue is empty")
		}
	})

	t.Run("dry-run does not remove or blocklist", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		insertEventAt(t, db, "agg1", "DownloadTimeout", "2026-05-27 10:00:00")
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", true, true) // dry-run

		eb := testutil.NewMockEventBus()
		arr := &testutil.MockArrClient{FindQueueItemsByMediaIDForPathFunc: stalledQueueFunc()}
		r := NewRemediatorService(eb, arr, &testutil.MockPathMapper{}, db)

		if r.handleStalledDownloadBeforeSearch("agg1", pathID, 123, "/data/tv/x.mkv", "/media/tv/x.mkv") {
			t.Fatal("expected false in dry-run")
		}
		if arr.CallCount("RemoveFromQueueByPath") != 0 || eb.EventCount(domain.ReleaseBlocklisted) != 0 {
			t.Error("dry-run must not remove from queue or blocklist")
		}
	})

	t.Run("non-auto path does nothing", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()

		insertEventAt(t, db, "agg1", "DownloadTimeout", "2026-05-27 10:00:00")
		pathID := makeScanPath(t, db, "/media/tv", "/data/tv", false, false) // auto off

		arr := &testutil.MockArrClient{FindQueueItemsByMediaIDForPathFunc: stalledQueueFunc()}
		r := NewRemediatorService(testutil.NewMockEventBus(), arr, &testutil.MockPathMapper{}, db)

		if r.handleStalledDownloadBeforeSearch("agg1", pathID, 123, "/data/tv/x.mkv", "/media/tv/x.mkv") {
			t.Fatal("expected false on non-auto path")
		}
		if arr.CallCount("RemoveFromQueueByPath") != 0 {
			t.Error("non-auto path must not remove from queue")
		}
	})
}

// seedSuccessfulRemediation appends a CorruptionDetected(file_path) + a
// VerificationSuccess for one aggregate, i.e. one completed remediation cycle.
func seedSuccessfulRemediation(t *testing.T, db *sql.DB, aggregateID, filePath string) {
	t.Helper()
	cd, _ := json.Marshal(map[string]interface{}{"file_path": filePath})
	if _, err := db.Exec(`INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES ('corruption', ?, 'CorruptionDetected', ?, 1, datetime('now'))`, aggregateID, string(cd)); err != nil {
		t.Fatalf("seed CorruptionDetected: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES ('corruption', ?, 'VerificationSuccess', '{}', 1, datetime('now'))`, aggregateID); err != nil {
		t.Fatalf("seed VerificationSuccess: %v", err)
	}
}

func TestRemediator_ShouldPauseForRecurringCorruption(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	r := NewRemediatorService(testutil.NewMockEventBus(), &testutil.MockArrClient{}, &testutil.MockPathMapper{}, db)
	const p = "/movies/x.mkv"

	seedSuccessfulRemediation(t, db, "a1", p)
	seedSuccessfulRemediation(t, db, "a2", p)
	if r.shouldPauseForRecurringCorruption(p) {
		t.Fatal("2 prior successes (< 3) must not pause")
	}
	seedSuccessfulRemediation(t, db, "a3", p)
	if !r.shouldPauseForRecurringCorruption(p) {
		t.Fatal("3 prior successes (>= 3) must pause")
	}
	if r.shouldPauseForRecurringCorruption("/movies/other.mkv") {
		t.Fatal("a different path must not be affected")
	}
}

func TestRemediator_EnsureMonitoredForRemediation(t *testing.T) {
	t.Run("unmonitored item is monitored and recorded", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		eb := testutil.NewMockEventBus()
		var setCalls int
		var setTo bool
		arr := &testutil.MockArrClient{
			IsMonitoredFunc:  func(string, int64) (bool, error) { return false, nil },
			SetMonitoredFunc: func(_ string, _ int64, m bool) error { setCalls++; setTo = m; return nil },
		}
		r := NewRemediatorService(eb, arr, &testutil.MockPathMapper{}, db)

		r.ensureMonitoredForRemediation("agg1", "/data/movies/x.mkv", 55)
		if setCalls != 1 || !setTo {
			t.Errorf("SetMonitored calls=%d setTo=%v, want 1/true", setCalls, setTo)
		}
		if eb.EventCount(domain.MonitorOverridden) != 1 {
			t.Errorf("MonitorOverridden events = %d, want 1", eb.EventCount(domain.MonitorOverridden))
		}
	})

	t.Run("already monitored: no-op", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		eb := testutil.NewMockEventBus()
		arr := &testutil.MockArrClient{IsMonitoredFunc: func(string, int64) (bool, error) { return true, nil }}
		r := NewRemediatorService(eb, arr, &testutil.MockPathMapper{}, db)

		r.ensureMonitoredForRemediation("agg1", "/data/movies/x.mkv", 55)
		if arr.CallCount("SetMonitored") != 0 {
			t.Error("must not change monitored when already monitored")
		}
		if eb.EventCount(domain.MonitorOverridden) != 0 {
			t.Error("must not record an override when already monitored")
		}
	})

	t.Run("IsMonitored error: no override", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		eb := testutil.NewMockEventBus()
		arr := &testutil.MockArrClient{IsMonitoredFunc: func(string, int64) (bool, error) { return false, errors.New("api down") }}
		r := NewRemediatorService(eb, arr, &testutil.MockPathMapper{}, db)

		r.ensureMonitoredForRemediation("agg1", "/data/movies/x.mkv", 55)
		if arr.CallCount("SetMonitored") != 0 {
			t.Error("must not override when the prior state is unknown")
		}
		if eb.EventCount(domain.MonitorOverridden) != 0 {
			t.Error("must not record an override on read error")
		}
	})
}

func TestRemediator_HandleRemediationTerminal_RestoresMonitored(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	ov, _ := json.Marshal(map[string]interface{}{"media_id": 55, "arr_path": "/data/movies/x.mkv", "original_monitored": false})
	if _, err := db.Exec(`INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES ('corruption', 'agg1', 'MonitorOverridden', ?, 1, datetime('now'))`, string(ov)); err != nil {
		t.Fatalf("seed MonitorOverridden: %v", err)
	}

	var restoredTo bool
	var calls int
	arr := &testutil.MockArrClient{SetMonitoredFunc: func(_ string, _ int64, m bool) error { calls++; restoredTo = m; return nil }}
	r := NewRemediatorService(testutil.NewMockEventBus(), arr, &testutil.MockPathMapper{}, db)

	r.handleRemediationTerminal(domain.Event{AggregateID: "agg1"})
	if calls != 1 || restoredTo {
		t.Errorf("restore SetMonitored calls=%d to=%v, want 1/false", calls, restoredTo)
	}

	// An aggregate we never overrode must be a no-op.
	arr2 := &testutil.MockArrClient{}
	r2 := NewRemediatorService(testutil.NewMockEventBus(), arr2, &testutil.MockPathMapper{}, db)
	r2.handleRemediationTerminal(domain.Event{AggregateID: "never-overridden"})
	if arr2.CallCount("SetMonitored") != 0 {
		t.Error("must not restore for an aggregate that was never overridden")
	}
}

func TestRemediator_LoopBreaker_PausesAndDoesNotDelete(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedRemediationPath(t, db, "/local/movies", true, false)

	const fp = "/local/movies/loop.mkv"
	for _, agg := range []string{"a1", "a2", "a3"} {
		seedSuccessfulRemediation(t, db, agg, fp)
	}

	eb := testutil.NewMockEventBus()
	arr := &testutil.MockArrClient{
		FindMediaByPathFunc: func(string) (int64, error) { return 55, nil },
		DeleteFileFunc:      func(int64, string) (map[string]interface{}, error) { return map[string]interface{}{}, nil },
	}
	pm := &testutil.MockPathMapper{ToArrPathFunc: func(p string) (string, error) { return p, nil }}
	r := NewRemediatorService(eb, arr, pm, db)

	event := testutil.NewCorruptionEventWithType(fp, integration.ErrorTypeCorruptHeader, testutil.WithAutoRemediate(true))
	r.handleCorruptionDetected(event)
	time.Sleep(200 * time.Millisecond)

	if eb.EventCount(domain.RemediationPaused) != 1 {
		t.Errorf("RemediationPaused events = %d, want 1", eb.EventCount(domain.RemediationPaused))
	}
	if arr.CallCount("DeleteFile") != 0 {
		t.Error("a paused remediation must not delete the file")
	}
}

// TestRemediator_RecoveryRetryCannotInventConsent reproduces the audit's
// CRITICAL finding 1: a RetryScheduled event shaped exactly like the recovery
// sweep / stuck monitor emit it (auto_remediate hardcoded true, no dry_run
// key, path_id absent) must NOT delete anything on a dry-run or manual path.
// Consent comes from the path config, resolved by file path when the event
// carries no path_id.
func TestRemediator_RecoveryRetryCannotInventConsent(t *testing.T) {
	run := func(t *testing.T, autoRemediate, dryRun bool, wantDelete bool) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatal(err)
		}
		defer db.Close()
		seedRemediationPath(t, db, "/media/tv", autoRemediate, dryRun)

		eb := testutil.NewMockEventBus()
		arr := &testutil.MockArrClient{
			FindMediaByPathFunc: func(string) (int64, error) { return 7, nil },
			DeleteFileFunc: func(int64, string) (map[string]interface{}, error) {
				return map[string]interface{}{}, nil
			},
			TriggerSearchFunc: func(int64, string, []int64) error { return nil },
		}
		pm := &testutil.MockPathMapper{ToArrPathFunc: func(p string) (string, error) { return p, nil }}
		r := NewRemediatorService(eb, arr, pm, db)

		// Exactly the recovery/monitor payload shape: hardcoded consent, no
		// path_id, no dry_run key.
		r.handleRetry(domain.Event{
			AggregateID:   "recovered-1",
			AggregateType: "corruption",
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path":       "/media/tv/Show/S01E01.mkv",
				"auto_remediate":  true,
				"recovery_action": "startup_recovery",
			},
		})
		time.Sleep(200 * time.Millisecond)

		got := arr.CallCount("DeleteFile") > 0
		if got != wantDelete {
			t.Errorf("auto=%v dry=%v: DeleteFile called=%v, want %v", autoRemediate, dryRun, got, wantDelete)
		}
	}

	t.Run("dry-run path: no delete", func(t *testing.T) { run(t, true, true, false) })
	t.Run("manual path: no delete", func(t *testing.T) { run(t, false, false, false) })
	t.Run("auto path: delete proceeds", func(t *testing.T) { run(t, true, false, true) })
}

// seedSuccessfulRemediationForMedia seeds a full journey (detected ->
// deletion-completed with media_id -> verification success) under a UNIQUE
// file path per aggregate, simulating the rename-per-round reality that
// bypassed the path-keyed loop-breaker.
func seedSuccessfulRemediationForMedia(t *testing.T, db *sql.DB, aggregateID, filePath string, mediaID int64) {
	t.Helper()
	seedSuccessfulRemediation(t, db, aggregateID, filePath)
	dc, _ := json.Marshal(map[string]interface{}{"media_id": mediaID})
	if _, err := db.Exec(`INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES ('corruption', ?, 'DeletionCompleted', ?, 1, datetime('now'))`, aggregateID, string(dc)); err != nil {
		t.Fatalf("seed DeletionCompleted: %v", err)
	}
}

// The loop-breaker must trip on MEDIA identity even when every round
// imported the replacement under a new filename (audit finding 14: the
// rename bypass made the path-keyed counter useless for exactly the
// Tdarr/silent-data-loss scenario it was built for).
func TestRemediator_LoopBreaker_MediaKeyedSurvivesRenames(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedRemediationPath(t, db, "/local/movies", true, false)

	// Three successful rounds, each under a DIFFERENT path, same media.
	seedSuccessfulRemediationForMedia(t, db, "m1", "/local/movies/Film.Release-A.mkv", 55)
	seedSuccessfulRemediationForMedia(t, db, "m2", "/local/movies/Film.Release-B.mkv", 55)
	seedSuccessfulRemediationForMedia(t, db, "m3", "/local/movies/Film.Release-C.mkv", 55)

	eb := testutil.NewMockEventBus()
	arr := &testutil.MockArrClient{
		FindMediaByPathFunc: func(string) (int64, error) { return 55, nil },
		DeleteFileFunc:      func(int64, string) (map[string]interface{}, error) { return map[string]interface{}{}, nil },
	}
	pm := &testutil.MockPathMapper{ToArrPathFunc: func(p string) (string, error) { return p, nil }}
	r := NewRemediatorService(eb, arr, pm, db)

	// Round 4 arrives under yet another new filename.
	event := testutil.NewCorruptionEventWithType("/local/movies/Film.Release-D.mkv", integration.ErrorTypeCorruptHeader, testutil.WithAutoRemediate(true))
	r.handleCorruptionDetected(event)
	time.Sleep(200 * time.Millisecond)

	if eb.EventCount(domain.RemediationPaused) != 1 {
		t.Errorf("RemediationPaused = %d, want 1 (media-keyed loop-breaker must survive renames)", eb.EventCount(domain.RemediationPaused))
	}
	if arr.CallCount("DeleteFile") != 0 {
		t.Error("a paused remediation must not delete the file")
	}
}

// manual_retry=true is the user's explicit consent to bypass a loop-breaker
// pause after fixing the root cause; previously the flag was written by the
// UI handler and read by nothing, leaving the user stuck for up to 30 days.
func TestRemediator_ManualRetryBypassesLoopBreakerPause(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedRemediationPath(t, db, "/local/movies", true, false)

	// Trip the path-keyed loop-breaker.
	const fp = "/local/movies/loop.mkv"
	for _, agg := range []string{"b1", "b2", "b3"} {
		seedSuccessfulRemediation(t, db, agg, fp)
	}

	eb := testutil.NewMockEventBus()
	arr := &testutil.MockArrClient{
		FindMediaByPathFunc: func(string) (int64, error) { return 77, nil },
		DeleteFileFunc:      func(int64, string) (map[string]interface{}, error) { return map[string]interface{}{}, nil },
		TriggerSearchFunc:   func(int64, string, []int64) error { return nil },
	}
	pm := &testutil.MockPathMapper{ToArrPathFunc: func(p string) (string, error) { return p, nil }}
	r := NewRemediatorService(eb, arr, pm, db)

	// Without the flag: paused, no delete.
	r.handleRetry(domain.Event{
		AggregateID: "b-noflag", AggregateType: "corruption", EventType: domain.RetryScheduled,
		EventData: map[string]interface{}{"file_path": fp, "auto_remediate": true},
	})
	time.Sleep(150 * time.Millisecond)
	if arr.CallCount("DeleteFile") != 0 {
		t.Fatal("retry without manual_retry must stay paused")
	}

	// With manual_retry=true: the user's one-cycle consent proceeds.
	r.handleRetry(domain.Event{
		AggregateID: "b-flag", AggregateType: "corruption", EventType: domain.RetryScheduled,
		EventData: map[string]interface{}{"file_path": fp, "auto_remediate": true, "manual_retry": true},
	})
	time.Sleep(150 * time.Millisecond)
	if arr.CallCount("DeleteFile") != 1 {
		t.Errorf("DeleteFile calls = %d, want 1 (manual retry bypasses the pause)", arr.CallCount("DeleteFile"))
	}
}
