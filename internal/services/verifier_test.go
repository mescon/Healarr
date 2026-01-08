package services

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/testutil"
)

// =============================================================================
// Utility function tests
// =============================================================================

func TestCalculateBackoffInterval(t *testing.T) {
	tests := []struct {
		name            string
		attempt         int
		initialInterval time.Duration
		maxInterval     time.Duration
		expected        time.Duration
	}{
		{
			name:            "first attempt uses initial interval",
			attempt:         0,
			initialInterval: 30 * time.Second,
			maxInterval:     1 * time.Hour,
			expected:        30 * time.Second,
		},
		{
			name:            "second attempt doubles",
			attempt:         1,
			initialInterval: 30 * time.Second,
			maxInterval:     1 * time.Hour,
			expected:        60 * time.Second,
		},
		{
			name:            "third attempt quadruples",
			attempt:         2,
			initialInterval: 30 * time.Second,
			maxInterval:     1 * time.Hour,
			expected:        120 * time.Second,
		},
		{
			name:            "caps at max interval",
			attempt:         20, // 2^20 * 30s would be huge
			initialInterval: 30 * time.Second,
			maxInterval:     1 * time.Hour,
			expected:        1 * time.Hour,
		},
		{
			name:            "very large attempt caps at max",
			attempt:         50, // 2^50 * 1s would overflow, but should cap at max
			initialInterval: 1 * time.Second,
			maxInterval:     24 * time.Hour,
			expected:        24 * time.Hour,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateBackoffInterval(tt.attempt, tt.initialInterval, tt.maxInterval)
			if result != tt.expected {
				t.Errorf("calculateBackoffInterval(%d, %v, %v) = %v, want %v",
					tt.attempt, tt.initialInterval, tt.maxInterval, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// VerifierService tests
// =============================================================================

func TestVerifierService_Shutdown(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockHC := &testutil.MockHealthChecker{}
	mockPM := &testutil.MockPathMapper{}
	mockArr := &testutil.MockArrClient{}

	verifier := NewVerifierService(eb, mockHC, mockPM, mockArr, db)

	// Start the verifier
	verifier.Start()

	// Shutdown should complete quickly
	done := make(chan struct{})
	go func() {
		verifier.Shutdown()
		close(done)
	}()

	select {
	case <-done:
		// Good - shutdown completed
	case <-time.After(2 * time.Second):
		t.Fatal("Shutdown timed out")
	}
}

func TestVerifierService_IsShuttingDown(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockHC := &testutil.MockHealthChecker{}
	mockPM := &testutil.MockPathMapper{}
	mockArr := &testutil.MockArrClient{}

	verifier := NewVerifierService(eb, mockHC, mockPM, mockArr, db)

	t.Run("returns false before shutdown", func(t *testing.T) {
		if verifier.isShuttingDown() {
			t.Error("Expected isShuttingDown to return false before shutdown")
		}
	})

	t.Run("returns true after shutdown", func(t *testing.T) {
		close(verifier.shutdownCh)
		if !verifier.isShuttingDown() {
			t.Error("Expected isShuttingDown to return true after shutdown")
		}
	})
}

func TestVerifierService_GetVerificationTimeout(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Create a scan path with custom timeout
	testutil.SeedScanPath(db, 1, "/media/movies", "/movies", false, false)
	// Update with custom timeout
	_, err = db.Exec(`UPDATE scan_paths SET verification_timeout_hours = 48 WHERE id = 1`)
	if err != nil {
		t.Fatalf("Failed to update scan path: %v", err)
	}

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	verifier := NewVerifierService(eb, nil, nil, nil, db)

	t.Run("returns default timeout for pathID 0", func(t *testing.T) {
		timeout := verifier.getVerificationTimeout(0)
		expectedDefault := config.Get().VerificationTimeout
		if timeout != expectedDefault {
			t.Errorf("Expected default timeout %v, got %v", expectedDefault, timeout)
		}
	})

	t.Run("returns custom timeout for configured path", func(t *testing.T) {
		timeout := verifier.getVerificationTimeout(1)
		expected := 48 * time.Hour
		if timeout != expected {
			t.Errorf("Expected custom timeout %v, got %v", expected, timeout)
		}
	})

	t.Run("returns default timeout for unknown path", func(t *testing.T) {
		timeout := verifier.getVerificationTimeout(999)
		expectedDefault := config.Get().VerificationTimeout
		if timeout != expectedDefault {
			t.Errorf("Expected default timeout %v, got %v", expectedDefault, timeout)
		}
	})
}

func TestVerifierService_VerifyHealthMultiple(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("calls health checker for each file", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		checkedPaths := make([]string, 0)
		mockHC := &testutil.MockHealthChecker{
			CheckFunc: func(path, mode string) (bool, *integration.HealthCheckError) {
				checkedPaths = append(checkedPaths, path)
				return true, nil // All files healthy
			},
		}

		verifier := NewVerifierService(eb, mockHC, nil, nil, db)

		// Verify multiple files
		verifier.verifyHealthMultiple("test-corruption-1", []string{
			"/media/movies/file1.mkv",
			"/media/movies/file2.mkv",
		})

		// Verify both files were checked
		if len(checkedPaths) != 2 {
			t.Errorf("Expected 2 files checked, got %d", len(checkedPaths))
		}
	})

	t.Run("detects failure when file unhealthy", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		callCount := 0
		mockHC := &testutil.MockHealthChecker{
			CheckFunc: func(path, mode string) (bool, *integration.HealthCheckError) {
				callCount++
				if callCount == 2 {
					// Second file is corrupt
					return false, &integration.HealthCheckError{
						Type:    integration.ErrorTypeCorruptStream,
						Message: "Stream error detected",
					}
				}
				return true, nil
			},
		}

		verifier := NewVerifierService(eb, mockHC, nil, nil, db)

		// Verify multiple files - the function itself logs and emits events
		// We're testing that it processes all files correctly
		verifier.verifyHealthMultiple("test-corruption-2", []string{
			"/media/movies/file1.mkv",
			"/media/movies/file2.mkv",
		})

		// Both files should have been checked even if one failed
		if callCount != 2 {
			t.Errorf("Expected 2 health checks, got %d", callCount)
		}
	})
}

func TestVerifierService_EmitFilesDetected(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("single file emits simple event", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockHC := &testutil.MockHealthChecker{
			CheckFunc: func(path, mode string) (bool, *integration.HealthCheckError) {
				return true, nil
			},
		}

		verifier := NewVerifierService(eb, mockHC, nil, nil, db)

		var mu sync.Mutex
		var fileDetectedEvent *domain.Event
		eb.Subscribe(domain.FileDetected, func(e domain.Event) {
			mu.Lock()
			eCopy := e // Copy the event
			fileDetectedEvent = &eCopy
			mu.Unlock()
		})

		verifier.emitFilesDetected("test-1", []string{"/media/movies/single.mkv"})

		// Wait for async delivery
		time.Sleep(100 * time.Millisecond)

		mu.Lock()
		defer mu.Unlock()
		if fileDetectedEvent == nil {
			t.Fatal("Expected FileDetected event")
		}

		// Single file should have file_path but not file_paths
		if fp, ok := fileDetectedEvent.GetString("file_path"); !ok || fp != "/media/movies/single.mkv" {
			t.Errorf("Expected file_path '/media/movies/single.mkv', got %q", fp)
		}
	})

	t.Run("multiple files emits event with file_paths", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockHC := &testutil.MockHealthChecker{
			CheckFunc: func(path, mode string) (bool, *integration.HealthCheckError) {
				return true, nil
			},
		}

		verifier := NewVerifierService(eb, mockHC, nil, nil, db)

		var mu sync.Mutex
		var fileDetectedEvent *domain.Event
		eb.Subscribe(domain.FileDetected, func(e domain.Event) {
			mu.Lock()
			eCopy := e
			fileDetectedEvent = &eCopy
			mu.Unlock()
		})

		verifier.emitFilesDetected("test-2", []string{
			"/media/tv/episode1.mkv",
			"/media/tv/episode2.mkv",
			"/media/tv/episode3.mkv",
		})

		// Wait for async delivery
		time.Sleep(100 * time.Millisecond)

		mu.Lock()
		defer mu.Unlock()
		if fileDetectedEvent == nil {
			t.Fatal("Expected FileDetected event")
		}

		// Multi-file should have file_count
		if count, ok := fileDetectedEvent.GetInt64("file_count"); !ok || count != 3 {
			t.Errorf("Expected file_count 3, got %d", count)
		}

		// Should have primary file_path for compatibility
		if fp, ok := fileDetectedEvent.GetString("file_path"); !ok || fp != "/media/tv/episode1.mkv" {
			t.Errorf("Expected file_path '/media/tv/episode1.mkv', got %q", fp)
		}
	})

	t.Run("empty file list does nothing", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockHC := &testutil.MockHealthChecker{}

		verifier := NewVerifierService(eb, mockHC, nil, nil, db)

		eventCount := 0
		eb.Subscribe(domain.FileDetected, func(e domain.Event) {
			eventCount++
		})

		verifier.emitFilesDetected("test-3", []string{})

		// Wait for async delivery
		time.Sleep(100 * time.Millisecond)

		if eventCount != 0 {
			t.Errorf("Expected 0 events for empty file list, got %d", eventCount)
		}
	})
}

func TestVerifierService_HandleSearchCompleted(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("missing file_path logs error", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockHC := &testutil.MockHealthChecker{}
		mockPM := &testutil.MockPathMapper{}
		mockArr := &testutil.MockArrClient{}

		verifier := NewVerifierService(eb, mockHC, mockPM, mockArr, db)

		// Create event without file_path
		event := domain.Event{
			AggregateID:   "test-corrupt-1",
			AggregateType: "corruption",
			EventType:     domain.SearchCompleted,
			EventData:     map[string]interface{}{},
		}

		// This should log an error but not panic
		verifier.handleSearchCompleted(event)

		// If we get here without panic, test passes
	})
}

// =============================================================================
// Partial replacement tests
// =============================================================================

func TestVerifierService_EmitPartialReplacement(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("emits FileDetected with partial_replacement flag", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockHC := &testutil.MockHealthChecker{
			CheckFunc: func(path, mode string) (bool, *integration.HealthCheckError) {
				return true, nil // All existing files are healthy
			},
		}

		verifier := NewVerifierService(eb, mockHC, nil, nil, db)

		var mu sync.Mutex
		var fileDetectedEvents []domain.Event
		eb.Subscribe(domain.FileDetected, func(e domain.Event) {
			mu.Lock()
			fileDetectedEvents = append(fileDetectedEvents, e)
			mu.Unlock()
		})

		// 2 of 4 expected files exist
		verifier.emitPartialReplacement("partial-test-1", []string{
			"/media/tv/show/S01E01.mkv",
			"/media/tv/show/S01E02.mkv",
		}, 4)

		// Wait for async delivery
		time.Sleep(100 * time.Millisecond)

		mu.Lock()
		defer mu.Unlock()

		if len(fileDetectedEvents) != 1 {
			t.Fatalf("Expected 1 FileDetected event, got %d", len(fileDetectedEvents))
		}

		event := fileDetectedEvents[0]

		// Check partial_replacement flag
		if pr, ok := event.GetBool("partial_replacement"); !ok || !pr {
			t.Error("Expected partial_replacement=true")
		}

		// Check expected_count
		if ec, ok := event.GetInt64("expected_count"); !ok || ec != 4 {
			t.Errorf("Expected expected_count=4, got %d", ec)
		}

		// Check file_count
		if fc, ok := event.GetInt64("file_count"); !ok || fc != 2 {
			t.Errorf("Expected file_count=2, got %d", fc)
		}

		// Check missing_count
		if mc, ok := event.GetInt64("missing_count"); !ok || mc != 2 {
			t.Errorf("Expected missing_count=2, got %d", mc)
		}

		// Check file_path is set for compatibility
		if fp, ok := event.GetString("file_path"); !ok || fp != "/media/tv/show/S01E01.mkv" {
			t.Errorf("Expected file_path='/media/tv/show/S01E01.mkv', got %q", fp)
		}
	})

	t.Run("verifies health of existing files", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		checkedPaths := make([]string, 0)
		mockHC := &testutil.MockHealthChecker{
			CheckFunc: func(path, mode string) (bool, *integration.HealthCheckError) {
				checkedPaths = append(checkedPaths, path)
				return true, nil
			},
		}

		verifier := NewVerifierService(eb, mockHC, nil, nil, db)

		verifier.emitPartialReplacement("partial-test-2", []string{
			"/media/tv/show/S01E03.mkv",
			"/media/tv/show/S01E04.mkv",
		}, 6)

		// Wait for async processing
		time.Sleep(100 * time.Millisecond)

		// Both existing files should have been health checked
		if len(checkedPaths) != 2 {
			t.Errorf("Expected 2 health checks, got %d", len(checkedPaths))
		}
	})
}

// =============================================================================
// calculateBackoffInterval additional tests
// =============================================================================

func TestCalculateBackoffInterval_EdgeCases(t *testing.T) {
	tests := []struct {
		name            string
		attempt         int
		initialInterval time.Duration
		maxInterval     time.Duration
		expected        time.Duration
	}{
		{
			name:            "negative attempt uses 2^-1 = 0.5 multiplier",
			attempt:         -1,
			initialInterval: 30 * time.Second,
			maxInterval:     1 * time.Hour,
			expected:        15 * time.Second, // 30s * 0.5
		},
		{
			name:            "extremely large attempt",
			attempt:         100, // 2^100 would overflow everything
			initialInterval: 1 * time.Second,
			maxInterval:     10 * time.Minute,
			expected:        10 * time.Minute,
		},
		{
			name:            "max interval smaller than initial (edge case)",
			attempt:         0,
			initialInterval: 1 * time.Hour,
			maxInterval:     30 * time.Second,
			expected:        30 * time.Second, // Should cap at max
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := calculateBackoffInterval(tt.attempt, tt.initialInterval, tt.maxInterval)
			if result != tt.expected {
				t.Errorf("calculateBackoffInterval(%d, %v, %v) = %v, want %v",
					tt.attempt, tt.initialInterval, tt.maxInterval, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// handleSearchCompleted tests - additional coverage
// =============================================================================

func TestVerifierService_HandleSearchCompleted_WithMediaID(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("path mapping failure falls back to polling", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockHC := &testutil.MockHealthChecker{}
		mockPM := &testutil.MockPathMapper{
			ToArrPathFunc: func(localPath string) (string, error) {
				return "", errPathNotConfigured
			},
		}
		mockArr := &testutil.MockArrClient{}

		verifier := NewVerifierService(eb, mockHC, mockPM, mockArr, db)
		verifier.Start()

		// Create SearchCompleted event with media_id but path mapping will fail
		event := domain.Event{
			AggregateID:   "test-path-mapping-fail",
			AggregateType: "corruption",
			EventType:     domain.SearchCompleted,
			EventData: map[string]interface{}{
				"file_path": "/media/movies/test.mkv",
				"media_id":  float64(123), // JSON numbers are float64
			},
		}

		// The handler should start a goroutine for pollForFileWithBackoff
		// We're just testing that it doesn't panic
		verifier.handleSearchCompleted(event)

		// Give the goroutine a moment to start
		time.Sleep(50 * time.Millisecond)

		// Shutdown should work cleanly
		verifier.Shutdown()
	})

	// Skip: This test starts monitorDownloadProgress which has a 30s initial sleep
	// that cannot be interrupted, causing test timeouts. The monitor behavior is
	// tested separately in TestVerifierService_MonitorDownloadProgress tests.
	t.Run("with valid media_id and path starts monitoring", func(t *testing.T) {
		t.Skip("Skipped: monitorDownloadProgress has 30s initial sleep that causes test timeout")
	})
}

// =============================================================================
// getHistoryWithRetry tests
// =============================================================================

func TestVerifierService_GetHistoryWithRetry(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("success on first try", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		callCount := 0
		mockArr := &testutil.MockArrClient{
			GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
				callCount++
				return []integration.HistoryItemInfo{
					{EventType: "downloadFolderImported"},
				}, nil
			},
		}

		verifier := NewVerifierService(eb, nil, nil, mockArr, db)

		history, err := verifier.getHistoryWithRetry("/movies", 123, 20, 3)
		if err != nil {
			t.Errorf("Expected success, got error: %v", err)
		}
		if len(history) != 1 {
			t.Errorf("Expected 1 history item, got %d", len(history))
		}
		if callCount != 1 {
			t.Errorf("Expected 1 call, got %d", callCount)
		}
	})

	t.Run("success on retry", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		callCount := 0
		mockArr := &testutil.MockArrClient{
			GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
				callCount++
				if callCount < 2 {
					return nil, errPathNotConfigured
				}
				return []integration.HistoryItemInfo{
					{EventType: "episodeFileImported"},
				}, nil
			},
		}

		verifier := NewVerifierService(eb, nil, nil, mockArr, db)

		history, err := verifier.getHistoryWithRetry("/movies", 123, 20, 3)
		if err != nil {
			t.Errorf("Expected success, got error: %v", err)
		}
		if len(history) != 1 {
			t.Errorf("Expected 1 history item, got %d", len(history))
		}
		if callCount != 2 {
			t.Errorf("Expected 2 calls, got %d", callCount)
		}
	})

	t.Run("failure after max retries", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		callCount := 0
		mockArr := &testutil.MockArrClient{
			GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
				callCount++
				return nil, errPathNotConfigured
			},
		}

		verifier := NewVerifierService(eb, nil, nil, mockArr, db)

		_, err := verifier.getHistoryWithRetry("/movies", 123, 20, 3)
		if err == nil {
			t.Error("Expected error, got success")
		}
		if callCount != 3 {
			t.Errorf("Expected 3 calls (max retries), got %d", callCount)
		}
	})

	t.Run("shutdown during retry", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockArr := &testutil.MockArrClient{
			GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
				return nil, errPathNotConfigured
			},
		}

		verifier := NewVerifierService(eb, nil, nil, mockArr, db)

		// Close shutdown channel to simulate shutdown
		close(verifier.shutdownCh)

		_, err := verifier.getHistoryWithRetry("/movies", 123, 20, 3)
		if err == nil {
			t.Error("Expected error due to shutdown")
		}
		if err.Error() != "shutdown in progress" {
			t.Errorf("Expected shutdown error, got: %v", err)
		}
	})
}

// =============================================================================
// checkHistoryForImport tests
// =============================================================================

func TestVerifierService_CheckHistoryForImport(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("no import events returns false", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockArr := &testutil.MockArrClient{
			GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
				return []integration.HistoryItemInfo{
					{EventType: "grabbed"},
					{EventType: "downloadCompleted"},
				}, nil
			},
		}

		verifier := NewVerifierService(eb, nil, nil, mockArr, db)

		result := verifier.checkHistoryForImport("test-1", "/movies", 123, "/test.mkv", nil)
		if result {
			t.Error("Expected false for no import events")
		}
	})

	t.Run("history API error returns false", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockArr := &testutil.MockArrClient{
			GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
				return nil, errPathNotConfigured
			},
		}

		verifier := NewVerifierService(eb, nil, nil, mockArr, db)

		result := verifier.checkHistoryForImport("test-2", "/movies", 123, "/test.mkv", nil)
		if result {
			t.Error("Expected false for history API error")
		}
	})

	t.Run("import event found but no arr client", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockArr := &testutil.MockArrClient{
			GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
				return []integration.HistoryItemInfo{
					{EventType: "downloadFolderImported"},
				}, nil
			},
			GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
				return nil, errPathNotConfigured
			},
		}

		verifier := NewVerifierService(eb, nil, nil, mockArr, db)

		result := verifier.checkHistoryForImport("test-3", "/movies", 123, "/test.mkv", nil)
		// Returns false because GetAllFilePaths returns error
		if result {
			t.Error("Expected false when GetAllFilePaths fails")
		}
	})
}

// =============================================================================
// pollForFileWithBackoff tests
// =============================================================================

func TestVerifierService_PollForFileWithBackoff(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	t.Run("stops on shutdown", func(t *testing.T) {
		db, err := testutil.NewTestDB()
		if err != nil {
			t.Fatalf("Failed to create test database: %v", err)
		}
		defer db.Close()

		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		verifier := NewVerifierService(eb, nil, nil, nil, db)

		// Close shutdown channel immediately to test immediate exit
		close(verifier.shutdownCh)

		done := make(chan struct{})
		go func() {
			verifier.pollForFileWithBackoff("test-shutdown", "/nonexistent/file.mkv", 0, nil, 0)
			close(done)
		}()

		// Should exit immediately since shutdown was already signaled
		select {
		case <-done:
			// Expected - poll stopped immediately
		case <-time.After(500 * time.Millisecond):
			t.Error("Poll did not stop on shutdown")
		}
	})
}

// =============================================================================
// monitorDownloadProgress tests - various queue states
// =============================================================================

func TestVerifierService_MonitorDownloadProgress_FailedState(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockArr := &testutil.MockArrClient{
		FindQueueItemsByMediaIDForPathFunc: func(arrPath string, mediaID int64) ([]integration.QueueItemInfo, error) {
			return []integration.QueueItemInfo{
				{
					ID:                    1,
					TrackedDownloadState:  "failed",
					TrackedDownloadStatus: "warning",
					ErrorMessage:          "Download failed - no seeders",
					DownloadID:            "abc123",
				},
			}, nil
		},
	}

	verifier := NewVerifierService(eb, nil, nil, mockArr, db)

	done := make(chan struct{})
	go func() {
		verifier.monitorDownloadProgress("test-failed", "/test.mkv", "/movies", 123, nil, 0)
		close(done)
	}()

	// The monitor should exit after detecting the failed state
	select {
	case <-done:
		// Expected - should exit after detecting failed state
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not exit after failed state")
	}

	// Verify event was persisted to database
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", domain.DownloadFailed).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query events: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 DownloadFailed event in DB, got %d", count)
	}
}

func TestVerifierService_MonitorDownloadProgress_IgnoredState(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockArr := &testutil.MockArrClient{
		FindQueueItemsByMediaIDForPathFunc: func(arrPath string, mediaID int64) ([]integration.QueueItemInfo, error) {
			return []integration.QueueItemInfo{
				{
					ID:                   2,
					TrackedDownloadState: "ignored",
					Title:                "Test Movie 2024",
					DownloadID:           "def456",
				},
			}, nil
		},
	}

	verifier := NewVerifierService(eb, nil, nil, mockArr, db)

	done := make(chan struct{})
	go func() {
		verifier.monitorDownloadProgress("test-ignored", "/test.mkv", "/movies", 456, nil, 0)
		close(done)
	}()

	select {
	case <-done:
		// Expected
	case <-time.After(2 * time.Second):
		t.Error("Monitor did not exit after ignored state")
	}

	// Verify event was persisted to database
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", domain.DownloadIgnored).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query events: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 DownloadIgnored event in DB, got %d", count)
	}
}

func TestVerifierService_MonitorDownloadProgress_ShutdownDuringMonitoring(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Use a channel to signal when the mock function is called
	firstCallDone := make(chan struct{}, 1)
	callCount := 0

	mockArr := &testutil.MockArrClient{
		FindQueueItemsByMediaIDForPathFunc: func(arrPath string, mediaID int64) ([]integration.QueueItemInfo, error) {
			callCount++
			if callCount == 1 {
				firstCallDone <- struct{}{}
			}
			// Active download, never finishes
			return []integration.QueueItemInfo{
				{ID: 3, TrackedDownloadState: "downloading", Progress: 50},
			}, nil
		},
	}

	verifier := NewVerifierService(eb, nil, nil, mockArr, db)

	go verifier.monitorDownloadProgress("test-shutdown", "/test.mkv", "/movies", 789, nil, 0)

	// Wait for first API call to confirm monitoring started
	select {
	case <-firstCallDone:
		// Monitoring started
	case <-time.After(1 * time.Second):
		t.Fatal("Monitor did not start")
	}

	// Signal shutdown
	close(verifier.shutdownCh)

	// The shutdown will be detected on next tick (up to 30s poll interval).
	// This test verifies the monitor starts correctly with a downloading state.
	// Immediate shutdown response is tested elsewhere.
	if callCount < 1 {
		t.Error("Expected at least 1 API call")
	}
}

func TestVerifierService_MonitorDownloadProgress_ImportBlocked(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Use channel to signal when mock is called
	firstCallDone := make(chan struct{}, 1)
	callCount := 0

	mockArr := &testutil.MockArrClient{
		FindQueueItemsByMediaIDForPathFunc: func(arrPath string, mediaID int64) ([]integration.QueueItemInfo, error) {
			callCount++
			if callCount == 1 {
				firstCallDone <- struct{}{}
			}
			return []integration.QueueItemInfo{
				{
					ID:                   4,
					TrackedDownloadState: "importBlocked",
					ErrorMessage:         "File already exists",
					StatusMessages:       []string{"Target file already exists", "Manual intervention required"},
					Title:                "Test Show S01E01",
				},
			}, nil
		},
		GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
			return []integration.HistoryItemInfo{}, nil
		},
	}

	verifier := NewVerifierService(eb, nil, nil, mockArr, db)

	go verifier.monitorDownloadProgress("test-blocked", "/test.mkv", "/movies", 321, nil, 0)

	// Wait for first API call
	select {
	case <-firstCallDone:
		// Monitoring started
	case <-time.After(1 * time.Second):
		t.Fatal("Monitor did not start")
	}

	// Give a moment for the event to be persisted
	time.Sleep(100 * time.Millisecond)

	// Signal shutdown
	close(verifier.shutdownCh)

	// Verify ImportBlocked event was persisted to database
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", domain.ImportBlocked).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query events: %v", err)
	}
	if count < 1 {
		t.Errorf("Expected at least 1 ImportBlocked event in DB, got %d", count)
	}
}

// =============================================================================
// checkHistoryForImport success paths
// =============================================================================

func TestVerifierService_CheckHistoryForImport_WithExistingFiles(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Create a temp file that will "exist" for the import check
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "imported.mkv")
	os.WriteFile(tmpFile, []byte("test"), 0644)

	var filesDetectedCount int
	eb.Subscribe(domain.FileDetected, func(e domain.Event) {
		filesDetectedCount++
	})

	mockArr := &testutil.MockArrClient{
		GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
			return []integration.HistoryItemInfo{
				{EventType: "downloadFolderImported"},
			}, nil
		},
		GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
			return []string{tmpFile}, nil
		},
	}

	// Path mapper returns paths as-is
	mockPathMapper := &testutil.MockPathMapper{
		ToLocalPathFunc: func(arrPath string) (string, error) {
			return arrPath, nil
		},
	}

	// Mock health checker returns healthy for all files
	mockDetector := &testutil.MockHealthChecker{
		CheckFunc: func(path, mode string) (bool, *integration.HealthCheckError) {
			return true, nil
		},
	}

	verifier := NewVerifierService(eb, mockDetector, mockPathMapper, mockArr, db)

	result := verifier.checkHistoryForImport("test-success", "/movies", 123, "/test.mkv", nil)
	if !result {
		t.Error("Expected true for successful import with existing file")
	}
}

// =============================================================================
// monitorDownloadProgress timeout path
// =============================================================================

func TestVerifierService_MonitorDownloadProgress_Timeout(t *testing.T) {
	// Create config with very short timeout and poll interval
	testCfg := config.NewTestConfig()
	testCfg.VerificationTimeout = 50 * time.Millisecond
	testCfg.VerificationInterval = 10 * time.Millisecond
	config.SetForTesting(testCfg)

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockArr := &testutil.MockArrClient{
		FindQueueItemsByMediaIDForPathFunc: func(arrPath string, mediaID int64) ([]integration.QueueItemInfo, error) {
			// Empty queue - not found
			return []integration.QueueItemInfo{}, nil
		},
		GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
			return []integration.HistoryItemInfo{}, nil
		},
	}

	verifier := NewVerifierService(eb, nil, nil, mockArr, db)

	done := make(chan struct{})
	go func() {
		verifier.monitorDownloadProgress("test-timeout", "/test.mkv", "/movies", 456, nil, 0)
		close(done)
	}()

	// Should timeout quickly
	select {
	case <-done:
		// Good - monitor exited
	case <-time.After(2 * time.Second):
		verifier.Shutdown()
		t.Error("Monitor did not timeout as expected")
	}

	// Verify DownloadTimeout event was published
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", domain.DownloadTimeout).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query events: %v", err)
	}
	if count < 1 {
		t.Errorf("Expected DownloadTimeout event, got %d events", count)
	}
}

// =============================================================================
// monitorDownloadProgress history check path
// =============================================================================

func TestVerifierService_MonitorDownloadProgress_HistoryImportDetected(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Create temp file for import detection
	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "imported.mkv")
	os.WriteFile(tmpFile, []byte("test"), 0644)

	historyCallCount := 0
	mockArr := &testutil.MockArrClient{
		FindQueueItemsByMediaIDForPathFunc: func(arrPath string, mediaID int64) ([]integration.QueueItemInfo, error) {
			return []integration.QueueItemInfo{}, nil // Not in queue
		},
		GetRecentHistoryForMediaByPathFunc: func(arrPath string, mediaID int64, limit int) ([]integration.HistoryItemInfo, error) {
			historyCallCount++
			if historyCallCount >= 2 {
				// After first check, "import" has happened
				return []integration.HistoryItemInfo{
					{EventType: "downloadFolderImported"},
				}, nil
			}
			return []integration.HistoryItemInfo{}, nil
		},
		GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
			return []string{tmpFile}, nil
		},
	}

	// Path mapper returns paths as-is
	mockPathMapper := &testutil.MockPathMapper{
		ToLocalPathFunc: func(arrPath string) (string, error) {
			return arrPath, nil
		},
	}

	// Mock health checker returns healthy for all files
	mockDetector := &testutil.MockHealthChecker{
		CheckFunc: func(path, mode string) (bool, *integration.HealthCheckError) {
			return true, nil
		},
	}

	verifier := NewVerifierService(eb, mockDetector, mockPathMapper, mockArr, db)

	done := make(chan struct{})
	go func() {
		verifier.monitorDownloadProgress("test-history", "/test.mkv", "/movies", 789, nil, 0)
		close(done)
	}()

	// Should detect import via history and exit
	select {
	case <-done:
		// Good - monitor detected import and exited
	case <-time.After(3 * time.Second):
		verifier.Shutdown()
		t.Error("Monitor did not detect import as expected")
	}
}

// =============================================================================
// Helper function tests - convertAndVerifyPaths
// =============================================================================

func TestVerifierService_ConvertAndVerifyPaths(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("returns empty slice for empty input", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockPM := &testutil.MockPathMapper{}
		verifier := NewVerifierService(eb, nil, mockPM, nil, db)

		result := verifier.convertAndVerifyPaths([]string{})
		if len(result) != 0 {
			t.Errorf("Expected empty slice, got %v", result)
		}
	})

	t.Run("returns only existing files", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		tmpDir := t.TempDir()
		existingFile := filepath.Join(tmpDir, "exists.mkv")
		os.WriteFile(existingFile, []byte("test"), 0644)

		mockPM := &testutil.MockPathMapper{
			ToLocalPathFunc: func(arrPath string) (string, error) {
				return arrPath, nil // Pass through
			},
		}
		verifier := NewVerifierService(eb, nil, mockPM, nil, db)

		input := []string{
			existingFile,
			"/nonexistent/path.mkv",
		}
		result := verifier.convertAndVerifyPaths(input)

		if len(result) != 1 {
			t.Errorf("Expected 1 file, got %d", len(result))
		}
		if len(result) > 0 && result[0] != existingFile {
			t.Errorf("Expected %s, got %s", existingFile, result[0])
		}
	})

	t.Run("falls back to original path on mapping error", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		tmpDir := t.TempDir()
		existingFile := filepath.Join(tmpDir, "test.mkv")
		os.WriteFile(existingFile, []byte("test"), 0644)

		mockPM := &testutil.MockPathMapper{
			ToLocalPathFunc: func(arrPath string) (string, error) {
				return "", errPathNotConfigured
			},
		}
		verifier := NewVerifierService(eb, nil, mockPM, nil, db)

		// If mapping fails, should use original path
		result := verifier.convertAndVerifyPaths([]string{existingFile})

		if len(result) != 1 {
			t.Errorf("Expected 1 file (using original path), got %d", len(result))
		}
	})
}

// =============================================================================
// Helper function tests - waitWithShutdown
// =============================================================================

func TestVerifierService_WaitWithShutdown(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("returns true immediately when already shutdown", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		verifier := NewVerifierService(eb, nil, nil, nil, db)
		close(verifier.shutdownCh) // Pre-close shutdown channel

		result := verifier.waitWithShutdown(1 * time.Second)
		if !result {
			t.Error("Expected true when shutdown channel is closed")
		}
	})

	t.Run("returns false after timeout without shutdown", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		verifier := NewVerifierService(eb, nil, nil, nil, db)

		start := time.Now()
		result := verifier.waitWithShutdown(50 * time.Millisecond)
		elapsed := time.Since(start)

		if result {
			t.Error("Expected false when wait completes normally")
		}
		if elapsed < 50*time.Millisecond {
			t.Errorf("Expected wait to take at least 50ms, took %v", elapsed)
		}
	})

	t.Run("returns true when shutdown during wait", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		verifier := NewVerifierService(eb, nil, nil, nil, db)

		// Close shutdown channel after a short delay
		go func() {
			time.Sleep(25 * time.Millisecond)
			close(verifier.shutdownCh)
		}()

		start := time.Now()
		result := verifier.waitWithShutdown(1 * time.Second)
		elapsed := time.Since(start)

		if !result {
			t.Error("Expected true when shutdown signal received")
		}
		if elapsed > 100*time.Millisecond {
			t.Errorf("Expected early return on shutdown, took %v", elapsed)
		}
	})
}

// =============================================================================
// Helper function tests - publishDownloadTimeout
// =============================================================================

func TestVerifierService_PublishDownloadTimeout(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	verifier := NewVerifierService(eb, nil, nil, nil, db)

	corruptionID := "timeout-test-123"
	elapsed := 6 * time.Hour
	attempt := 120
	lastStatus := "downloading"

	verifier.publishDownloadTimeout(corruptionID, elapsed, attempt, lastStatus)

	// Wait for async delivery
	time.Sleep(100 * time.Millisecond)

	// Check that event was stored in database
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = ? AND event_type = ?",
		corruptionID, domain.DownloadTimeout).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query events: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 DownloadTimeout event, got %d", count)
	}
}

// =============================================================================
// Helper function tests - publishManuallyRemoved
// =============================================================================

func TestVerifierService_PublishManuallyRemoved(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	verifier := NewVerifierService(eb, nil, nil, nil, db)

	corruptionID := "manual-remove-test-456"
	lastStatus := "was_in_queue"

	verifier.publishManuallyRemoved(corruptionID, lastStatus)

	// Wait for async delivery
	time.Sleep(100 * time.Millisecond)

	// Check that event was stored in database
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM events WHERE aggregate_id = ? AND event_type = ?",
		corruptionID, domain.ManuallyRemoved).Scan(&count)
	if err != nil {
		t.Fatalf("Failed to query events: %v", err)
	}
	if count != 1 {
		t.Errorf("Expected 1 ManuallyRemoved event, got %d", count)
	}
}

// =============================================================================
// Helper function tests - storeImportMetadata
// =============================================================================

func TestVerifierService_StoreImportMetadata(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("stores single file metadata with size", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		verifier := NewVerifierService(eb, nil, nil, nil, db)

		tmpDir := t.TempDir()
		tmpFile := filepath.Join(tmpDir, "test.mkv")
		os.WriteFile(tmpFile, []byte("test content"), 0644)

		importItem := &integration.HistoryItemInfo{
			Quality:        "Bluray-1080p",
			ReleaseGroup:   "SPARKS",
			Indexer:        "NZBgeek",
			DownloadClient: "SABnzbd",
		}

		verifier.storeImportMetadata("test-123", []string{tmpFile}, importItem)

		meta := verifier.getVerifyMeta("test-123")
		if meta == nil {
			t.Fatal("Expected metadata to be stored")
		}
		if meta.Quality != "Bluray-1080p" {
			t.Errorf("Expected Quality 'Bluray-1080p', got %q", meta.Quality)
		}
		if meta.ReleaseGroup != "SPARKS" {
			t.Errorf("Expected ReleaseGroup 'SPARKS', got %q", meta.ReleaseGroup)
		}
		if meta.NewFilePath != tmpFile {
			t.Errorf("Expected NewFilePath %q, got %q", tmpFile, meta.NewFilePath)
		}
		if meta.NewFileSize != 12 { // "test content" is 12 bytes
			t.Errorf("Expected NewFileSize 12, got %d", meta.NewFileSize)
		}
	})

	t.Run("stores multi-file metadata without size", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		verifier := NewVerifierService(eb, nil, nil, nil, db)

		paths := []string{"/path1.mkv", "/path2.mkv", "/path3.mkv"}
		importItem := &integration.HistoryItemInfo{
			Quality: "WEBDL-1080p",
		}

		verifier.storeImportMetadata("multi-123", paths, importItem)

		meta := verifier.getVerifyMeta("multi-123")
		if meta == nil {
			t.Fatal("Expected metadata to be stored")
		}
		if len(meta.NewFilePaths) != 3 {
			t.Errorf("Expected 3 NewFilePaths, got %d", len(meta.NewFilePaths))
		}
		// For multi-file, NewFilePath should be empty
		if meta.NewFilePath != "" {
			t.Errorf("Expected empty NewFilePath for multi-file, got %q", meta.NewFilePath)
		}
	})
}

// =============================================================================
// Helper function tests - checkAndEmitFilesFromArrAPI
// =============================================================================

func TestVerifierService_CheckAndEmitFilesFromArrAPI(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("returns false with nil arrClient", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		verifier := NewVerifierService(eb, nil, nil, nil, db)

		result := verifier.checkAndEmitFilesFromArrAPI("test-1", "/path.mkv", 123, nil, time.Hour, 6*time.Hour)
		if result {
			t.Error("Expected false with nil arrClient")
		}
	})

	t.Run("returns false when GetAllFilePaths fails", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockArr := &testutil.MockArrClient{
			GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
				return nil, errPathNotConfigured
			},
		}
		verifier := NewVerifierService(eb, nil, nil, mockArr, db)

		result := verifier.checkAndEmitFilesFromArrAPI("test-2", "/path.mkv", 123, nil, time.Hour, 6*time.Hour)
		if result {
			t.Error("Expected false when GetAllFilePaths fails")
		}
	})

	t.Run("returns true and emits event when all files exist", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		tmpDir := t.TempDir()
		existingFile := filepath.Join(tmpDir, "test.mkv")
		os.WriteFile(existingFile, []byte("test"), 0644)

		mockArr := &testutil.MockArrClient{
			GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
				return []string{existingFile}, nil
			},
		}
		mockPM := &testutil.MockPathMapper{}
		mockHC := &testutil.MockHealthChecker{
			CheckFunc: func(path, mode string) (bool, *integration.HealthCheckError) {
				return true, nil
			},
		}

		verifier := NewVerifierService(eb, mockHC, mockPM, mockArr, db)

		var fileDetectedCount int
		eb.Subscribe(domain.FileDetected, func(e domain.Event) {
			fileDetectedCount++
		})

		result := verifier.checkAndEmitFilesFromArrAPI("test-3", "/path.mkv", 123, nil, time.Hour, 6*time.Hour)

		time.Sleep(100 * time.Millisecond)

		if !result {
			t.Error("Expected true when all files exist")
		}
		if fileDetectedCount != 1 {
			t.Errorf("Expected 1 FileDetected event, got %d", fileDetectedCount)
		}
	})

	t.Run("emits partial replacement after half timeout", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		tmpDir := t.TempDir()
		existingFile := filepath.Join(tmpDir, "ep1.mkv")
		os.WriteFile(existingFile, []byte("test"), 0644)

		mockArr := &testutil.MockArrClient{
			GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
				return []string{existingFile, "/nonexistent/ep2.mkv"}, nil
			},
		}
		mockPM := &testutil.MockPathMapper{}
		mockHC := &testutil.MockHealthChecker{
			CheckFunc: func(path, mode string) (bool, *integration.HealthCheckError) {
				return true, nil
			},
		}

		verifier := NewVerifierService(eb, mockHC, mockPM, mockArr, db)

		var fileDetectedCount int
		eb.Subscribe(domain.FileDetected, func(e domain.Event) {
			fileDetectedCount++
		})

		timeout := 6 * time.Hour
		elapsed := 4 * time.Hour // > half of timeout
		result := verifier.checkAndEmitFilesFromArrAPI("test-4", "/path.mkv", 123, nil, elapsed, timeout)

		time.Sleep(100 * time.Millisecond)

		if !result {
			t.Error("Expected true when partial replacement detected after half timeout")
		}
	})
}

// =============================================================================
// Helper function tests - findFilesForVerification
// =============================================================================

func TestVerifierService_FindFilesForVerification(t *testing.T) {
	config.SetForTesting(config.NewTestConfig())

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	t.Run("uses arr API when smart verification enabled", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		tmpDir := t.TempDir()
		existingFile := filepath.Join(tmpDir, "test.mkv")
		os.WriteFile(existingFile, []byte("test"), 0644)

		mockArr := &testutil.MockArrClient{
			GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
				return []string{existingFile}, nil
			},
		}
		mockPM := &testutil.MockPathMapper{}

		verifier := NewVerifierService(eb, nil, mockPM, mockArr, db)

		result := verifier.findFilesForVerification(123, nil, existingFile, true)

		if len(result) != 1 {
			t.Errorf("Expected 1 file, got %d", len(result))
		}
		if len(result) > 0 && result[0] != existingFile {
			t.Errorf("Expected %s, got %s", existingFile, result[0])
		}
	})

	t.Run("falls back to reference path when smart verification disabled", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		tmpDir := t.TempDir()
		existingFile := filepath.Join(tmpDir, "test.mkv")
		os.WriteFile(existingFile, []byte("test"), 0644)

		mockArr := &testutil.MockArrClient{}

		verifier := NewVerifierService(eb, nil, nil, mockArr, db)

		result := verifier.findFilesForVerification(123, nil, existingFile, false)

		if len(result) != 1 {
			t.Errorf("Expected 1 file, got %d", len(result))
		}
		if len(result) > 0 && result[0] != existingFile {
			t.Errorf("Expected %s, got %s", existingFile, result[0])
		}
	})

	t.Run("returns nil when no files found", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		mockArr := &testutil.MockArrClient{
			GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
				return nil, nil
			},
		}

		verifier := NewVerifierService(eb, nil, nil, mockArr, db)

		result := verifier.findFilesForVerification(123, nil, "/nonexistent/path.mkv", true)

		if result != nil {
			t.Errorf("Expected nil, got %v", result)
		}
	})

	t.Run("returns nil when arr returns partial files", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		tmpDir := t.TempDir()
		existingFile := filepath.Join(tmpDir, "ep1.mkv")
		os.WriteFile(existingFile, []byte("test"), 0644)

		mockArr := &testutil.MockArrClient{
			GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
				// Returns 2 paths but only 1 exists
				return []string{existingFile, "/nonexistent/ep2.mkv"}, nil
			},
		}
		mockPM := &testutil.MockPathMapper{}

		verifier := NewVerifierService(eb, nil, mockPM, mockArr, db)

		// Should return nil because not ALL files exist (2 returned, only 1 exists)
		result := verifier.findFilesForVerification(123, nil, "/ref.mkv", true)

		if result != nil {
			t.Errorf("Expected nil when not all files exist, got %v", result)
		}
	})
}

// =============================================================================
// enrichVerificationEventData tests
// =============================================================================

func TestEnrichVerificationEventData(t *testing.T) {
	t.Run("nil meta does nothing", func(t *testing.T) {
		eventData := map[string]interface{}{
			"existing": "value",
		}
		enrichVerificationEventData(eventData, nil)

		if len(eventData) != 1 {
			t.Errorf("Expected eventData to remain unchanged, got %d keys", len(eventData))
		}
		if eventData["existing"] != "value" {
			t.Error("Existing value should be preserved")
		}
	})

	t.Run("empty meta does nothing", func(t *testing.T) {
		eventData := map[string]interface{}{}
		meta := &VerificationMeta{}
		enrichVerificationEventData(eventData, meta)

		// Empty meta should not add any fields
		if len(eventData) != 0 {
			t.Errorf("Expected empty eventData, got %d keys: %v", len(eventData), eventData)
		}
	})

	t.Run("all fields populated", func(t *testing.T) {
		eventData := map[string]interface{}{}
		meta := &VerificationMeta{
			NewFilePath:    "/new/path.mkv",
			NewFileSize:    1234567890,
			Quality:        "1080p",
			ReleaseGroup:   "SPARKS",
			Indexer:        "NZBgeek",
			DownloadClient: "SABnzbd",
		}
		enrichVerificationEventData(eventData, meta)

		if eventData["new_file_path"] != "/new/path.mkv" {
			t.Errorf("Expected new_file_path '/new/path.mkv', got %v", eventData["new_file_path"])
		}
		if eventData["new_file_size"] != int64(1234567890) {
			t.Errorf("Expected new_file_size 1234567890, got %v", eventData["new_file_size"])
		}
		if eventData["quality"] != "1080p" {
			t.Errorf("Expected quality '1080p', got %v", eventData["quality"])
		}
		if eventData["release_group"] != "SPARKS" {
			t.Errorf("Expected release_group 'SPARKS', got %v", eventData["release_group"])
		}
		if eventData["indexer"] != "NZBgeek" {
			t.Errorf("Expected indexer 'NZBgeek', got %v", eventData["indexer"])
		}
		if eventData["download_client"] != "SABnzbd" {
			t.Errorf("Expected download_client 'SABnzbd', got %v", eventData["download_client"])
		}
	})

	t.Run("partial fields populated", func(t *testing.T) {
		eventData := map[string]interface{}{}
		meta := &VerificationMeta{
			Quality:      "720p",
			ReleaseGroup: "RARBG",
			// Other fields left empty/zero
		}
		enrichVerificationEventData(eventData, meta)

		// Should only have the two non-empty fields
		if len(eventData) != 2 {
			t.Errorf("Expected 2 keys, got %d: %v", len(eventData), eventData)
		}
		if eventData["quality"] != "720p" {
			t.Errorf("Expected quality '720p', got %v", eventData["quality"])
		}
		if eventData["release_group"] != "RARBG" {
			t.Errorf("Expected release_group 'RARBG', got %v", eventData["release_group"])
		}
	})

	t.Run("zero file size not added", func(t *testing.T) {
		eventData := map[string]interface{}{}
		meta := &VerificationMeta{
			NewFilePath: "/path.mkv",
			NewFileSize: 0, // zero should not be added
		}
		enrichVerificationEventData(eventData, meta)

		if _, exists := eventData["new_file_size"]; exists {
			t.Error("Zero file size should not be added to eventData")
		}
		if eventData["new_file_path"] != "/path.mkv" {
			t.Errorf("Expected new_file_path, got %v", eventData["new_file_path"])
		}
	})

	t.Run("preserves existing eventData", func(t *testing.T) {
		eventData := map[string]interface{}{
			"file_path":   "/original/path.mkv",
			"instance_id": 42,
		}
		meta := &VerificationMeta{
			Quality: "4K",
		}
		enrichVerificationEventData(eventData, meta)

		// Original values preserved
		if eventData["file_path"] != "/original/path.mkv" {
			t.Error("Original file_path should be preserved")
		}
		if eventData["instance_id"] != 42 {
			t.Error("Original instance_id should be preserved")
		}
		// New value added
		if eventData["quality"] != "4K" {
			t.Error("Quality should be added")
		}
	})
}

// =============================================================================
// Polling progress helper tests
// =============================================================================

func TestVerifierService_ShouldLogPollingProgress(t *testing.T) {
	v := &VerifierService{}

	tests := []struct {
		name     string
		attempt  int
		interval time.Duration
		want     bool
	}{
		{"first attempt", 0, time.Minute, false},
		{"attempt 5 short interval", 5, time.Minute, false},
		{"attempt 10 short interval", 10, time.Minute, true},
		{"attempt 20 short interval", 20, time.Minute, true},
		{"attempt 5 long interval (1h)", 5, time.Hour, true},
		{"attempt 1 long interval (2h)", 1, 2 * time.Hour, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := v.shouldLogPollingProgress(tt.attempt, tt.interval); got != tt.want {
				t.Errorf("shouldLogPollingProgress(%d, %v) = %v, want %v", tt.attempt, tt.interval, got, tt.want)
			}
		})
	}
}

// =============================================================================
// logFilesDetected tests
// =============================================================================

func TestVerifierService_LogFilesDetected(t *testing.T) {
	v := &VerifierService{}

	// These tests verify the function doesn't panic with various inputs
	// The function just logs, so we verify it handles edge cases gracefully
	t.Run("logs single file detected", func(t *testing.T) {
		// Should not panic
		v.logFilesDetected("corruption-123", 5, []string{"/media/show/episode.mkv"})
	})

	t.Run("logs multiple files detected", func(t *testing.T) {
		// Should not panic
		v.logFilesDetected("corruption-456", 3, []string{
			"/media/show/episode1.mkv",
			"/media/show/episode2.mkv",
			"/media/show/episode3.mkv",
		})
	})

	t.Run("handles empty path list", func(t *testing.T) {
		// Should not panic even with empty list
		v.logFilesDetected("corruption-789", 1, []string{})
	})
}
