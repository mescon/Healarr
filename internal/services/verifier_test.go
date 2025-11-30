package services

import (
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
			CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
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
			CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
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
			CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
				return true, nil
			},
		}

		verifier := NewVerifierService(eb, mockHC, nil, nil, db)

		var fileDetectedEvent *domain.Event
		eb.Subscribe(domain.FileDetected, func(e domain.Event) {
			fileDetectedEvent = &e
		})

		verifier.emitFilesDetected("test-1", []string{"/media/movies/single.mkv"})

		// Wait for async delivery
		time.Sleep(100 * time.Millisecond)

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
			CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
				return true, nil
			},
		}

		verifier := NewVerifierService(eb, mockHC, nil, nil, db)

		var fileDetectedEvent *domain.Event
		eb.Subscribe(domain.FileDetected, func(e domain.Event) {
			fileDetectedEvent = &e
		})

		verifier.emitFilesDetected("test-2", []string{
			"/media/tv/episode1.mkv",
			"/media/tv/episode2.mkv",
			"/media/tv/episode3.mkv",
		})

		// Wait for async delivery
		time.Sleep(100 * time.Millisecond)

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
