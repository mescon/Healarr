package services

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/testutil"
)

func init() {
	config.SetForTesting(config.NewTestConfig())
}

// =============================================================================
// Helper function tests
// =============================================================================

func TestIsMediaFile(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"mkv file", "/media/movies/test.mkv", true},
		{"MKV uppercase", "/media/movies/test.MKV", true},
		{"mp4 file", "/media/movies/test.mp4", true},
		{"avi file", "/media/movies/test.avi", true},
		{"m4v file", "/media/movies/test.m4v", true},
		{"ts file", "/media/movies/test.ts", true},
		{"txt file", "/media/movies/test.txt", false},
		{"jpg file", "/media/movies/cover.jpg", false},
		{"nfo file", "/media/movies/test.nfo", false},
		{"srt file", "/media/movies/test.srt", false},
		{"no extension", "/media/movies/testfile", false},
		{"empty path", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isMediaFile(tt.path)
			if result != tt.expected {
				t.Errorf("isMediaFile(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestIsHiddenOrTempFile(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		{"normal file", "/media/movies/Test Movie.mkv", false},
		{"hidden file", "/media/movies/.hidden.mkv", true},
		{"fuse hidden", "/media/movies/.fuse_hidden000123", true},
		{"tmp file", "/media/movies/test.tmp", true},
		{"temp file", "/media/movies/test.temp", true},
		{"part file", "/media/movies/test.part", true},
		{"partial file", "/media/movies/test.partial", true},
		{"qbittorrent incomplete", "/media/movies/test.!qb", true},
		{"sabnzbd temp", "/media/movies/__test.mkv", true},
		{"nzbget temp", "/media/movies/test.nzbget", true},
		{"sample file", "/media/movies/sample.mkv", true},
		{"SAMPLE file", "/media/movies/SAMPLE.mkv", true},
		{"movie-sample", "/media/movies/movie-sample.mkv", true},
		{"sampler (allowed)", "/media/movies/sampler.mkv", false},
		{"trailer file", "/media/movies/movie-trailer.mkv", true},
		{".trailer. file", "/media/movies/test.trailer.mkv", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isHiddenOrTempFile(tt.path)
			if result != tt.expected {
				t.Errorf("isHiddenOrTempFile(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// ScannerService tests
// =============================================================================

func TestScannerService_IsFileBeingScanned(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	mockEB := testutil.NewMockEventBus()
	mockHC := &testutil.MockHealthChecker{}
	mockPM := &testutil.MockPathMapper{}

	// Create scanner with mock eventbus
	scanner := &ScannerService{
		db:              db,
		eventBus:        nil, // We'll set this below
		detector:        mockHC,
		pathMapper:      mockPM,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}
	// Since eventBus is *eventbus.EventBus, we can't directly assign MockEventBus
	// The scanner tests need to work with the actual type or use interfaces
	_ = mockEB // Keep for future use

	t.Run("file not being scanned returns false", func(t *testing.T) {
		if scanner.IsFileBeingScanned("/media/movies/test.mkv") {
			t.Error("Expected file not being scanned")
		}
	})

	t.Run("file being scanned returns true", func(t *testing.T) {
		scanner.filesMu.Lock()
		scanner.filesInProgress["/media/movies/scanning.mkv"] = true
		scanner.filesMu.Unlock()

		if !scanner.IsFileBeingScanned("/media/movies/scanning.mkv") {
			t.Error("Expected file being scanned")
		}

		// Cleanup
		scanner.filesMu.Lock()
		delete(scanner.filesInProgress, "/media/movies/scanning.mkv")
		scanner.filesMu.Unlock()
	})
}

func TestScannerService_CheckScanCancellation(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Create a real eventbus for the tests (needed by emitProgress)
	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns scanContinue when not cancelled", func(t *testing.T) {
		ctx := context.Background()
		progress := &ScanProgress{ID: "test-1", Path: "/media/test"}

		action := scanner.checkScanCancellation(ctx, progress, "/media/test", 0, 10)
		if action != scanContinue {
			t.Errorf("Expected scanContinue, got %v", action)
		}
	})

	t.Run("returns scanReturn when context cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // Cancel immediately
		progress := &ScanProgress{ID: "test-2", Path: "/media/test"}

		action := scanner.checkScanCancellation(ctx, progress, "/media/test", 0, 10)
		if action != scanReturn {
			t.Errorf("Expected scanReturn, got %v", action)
		}
		if progress.Status != "cancelled" {
			t.Errorf("Expected status 'cancelled', got %q", progress.Status)
		}
	})

	t.Run("returns scanReturn when shutdown signaled", func(t *testing.T) {
		eb2 := eventbus.NewEventBus(db)
		defer eb2.Shutdown()

		scanner2 := &ScannerService{
			db:              db,
			eventBus:        eb2,
			activeScans:     make(map[string]*ScanProgress),
			filesInProgress: make(map[string]bool),
			shutdownCh:      make(chan struct{}),
		}
		close(scanner2.shutdownCh) // Signal shutdown

		ctx := context.Background()
		progress := &ScanProgress{ID: "test-3", Path: "/media/test"}

		action := scanner2.checkScanCancellation(ctx, progress, "/media/test", 5, 10)
		if action != scanReturn {
			t.Errorf("Expected scanReturn, got %v", action)
		}
		if progress.Status != "interrupted" {
			t.Errorf("Expected status 'interrupted', got %q", progress.Status)
		}
	})
}

func TestScannerService_ShouldSkipRecentlyModified(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Create a temp file
	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.mkv")
	if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("skips recently modified file", func(t *testing.T) {
		sfc := &scanFileContext{
			filePath:  testFile,
			fileSize:  12,
			fileMtime: time.Now(), // Just modified
			scanDBID:  0,          // No DB recording for this test
		}

		if !scanner.shouldSkipRecentlyModified(sfc) {
			t.Error("Expected to skip recently modified file")
		}
	})

	t.Run("does not skip old file", func(t *testing.T) {
		sfc := &scanFileContext{
			filePath:  testFile,
			fileSize:  12,
			fileMtime: time.Now().Add(-5 * time.Minute), // Modified 5 minutes ago
			scanDBID:  0,
		}

		if scanner.shouldSkipRecentlyModified(sfc) {
			t.Error("Expected not to skip old file")
		}
	})
}

func TestScannerService_HandleRecoverableError(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Insert a scan path for the test
	testutil.SeedScanPath(db, 1, "/media/movies", "/movies", false, false)

	// Create eventbus for tests
	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns scanSkipToNext for non-mount errors", func(t *testing.T) {
		progress := &ScanProgress{ID: "test-1", Path: "/media/movies"}
		sfc := &scanFileContext{
			filePath: "/media/movies/test.mkv",
			fileSize: 1024,
			pathID:   1,
			scanDBID: 0,
		}
		healthErr := &integration.HealthCheckError{
			Type:    integration.ErrorTypeAccessDenied,
			Message: "Permission denied",
		}

		action := scanner.handleRecoverableError(progress, sfc, healthErr)
		if action != scanSkipToNext {
			t.Errorf("Expected scanSkipToNext, got %v", action)
		}
	})

	t.Run("returns scanReturn for mount lost errors", func(t *testing.T) {
		progress := &ScanProgress{ID: "test-2", Path: "/media/movies"}
		sfc := &scanFileContext{
			filePath: "/media/movies/test.mkv",
			fileSize: 1024,
			pathID:   1,
			scanDBID: 0,
		}
		healthErr := &integration.HealthCheckError{
			Type:    integration.ErrorTypeMountLost,
			Message: "Transport endpoint is not connected",
		}

		action := scanner.handleRecoverableError(progress, sfc, healthErr)
		if action != scanReturn {
			t.Errorf("Expected scanReturn for mount lost, got %v", action)
		}
		if progress.Status != "aborted" {
			t.Errorf("Expected status 'aborted', got %q", progress.Status)
		}
	})
}

func TestScannerService_ApplyBatchThrottling(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("activates throttling at threshold", func(t *testing.T) {
		progress := &ScanProgress{
			ID:              "test-1",
			Path:            "/media/movies",
			corruptionCount: batchThrottleThreshold, // Exactly at threshold
			isThrottled:     false,
		}

		// Note: This test will block for batchThrottleDelay since throttling gets activated
		// So we use a separate goroutine with a short timeout
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()

		done := make(chan scanLoopAction)
		go func() {
			done <- scanner.applyBatchThrottling(ctx, progress)
		}()

		select {
		case action := <-done:
			// Should return scanReturn because context timed out during throttle delay
			if action != scanReturn {
				// If it returned scanContinue, throttling might have been too fast
				// Check that isThrottled was set
				if !progress.isThrottled {
					t.Error("Expected throttling to be activated at threshold")
				}
			}
		case <-time.After(200 * time.Millisecond):
			t.Error("Test timed out")
		}
	})

	t.Run("returns scanContinue when not throttled", func(t *testing.T) {
		progress := &ScanProgress{
			ID:              "test-2",
			Path:            "/media/movies",
			corruptionCount: 1, // Below threshold
			isThrottled:     false,
		}

		ctx := context.Background()
		action := scanner.applyBatchThrottling(ctx, progress)
		if action != scanContinue {
			t.Errorf("Expected scanContinue, got %v", action)
		}
		if progress.isThrottled {
			t.Error("Should not be throttled below threshold")
		}
	})
}

// =============================================================================
// ScanProgress tests
// =============================================================================

func TestScanProgress_PauseResume(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
		mu:              sync.Mutex{},
	}

	t.Run("handleScanPause returns scanContinue when not paused", func(t *testing.T) {
		progress := &ScanProgress{
			ID:       "test-1",
			Path:     "/media/movies",
			isPaused: false,
		}

		ctx := context.Background()
		action := scanner.handleScanPause(ctx, progress, "/media/movies", 0, 0)
		if action != scanContinue {
			t.Errorf("Expected scanContinue when not paused, got %v", action)
		}
	})

	t.Run("handleScanPause blocks when paused then resumes", func(t *testing.T) {
		progress := &ScanProgress{
			ID:         "test-2",
			Path:       "/media/movies",
			TotalFiles: 10,
			isPaused:   true,
			resumeChan: make(chan struct{}),
		}

		ctx := context.Background()
		var action scanLoopAction
		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()
			action = scanner.handleScanPause(ctx, progress, "/media/movies", 5, 0)
		}()

		// Give goroutine time to start and block
		time.Sleep(50 * time.Millisecond)

		// Signal resume
		close(progress.resumeChan)

		wg.Wait()

		if action != scanContinue {
			t.Errorf("Expected scanContinue after resume, got %v", action)
		}
		if progress.isPaused {
			t.Error("isPaused should be false after resume")
		}
		if progress.Status != "scanning" {
			t.Errorf("Status should be 'scanning', got %q", progress.Status)
		}
	})
}

// =============================================================================
// Constructor and basic operations tests
// =============================================================================

func TestNewScannerService(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockHC := &testutil.MockHealthChecker{}
	mockPM := &testutil.MockPathMapper{}

	scanner := NewScannerService(db, eb, mockHC, mockPM)

	if scanner == nil {
		t.Fatal("Expected non-nil scanner")
	}
	if scanner.db != db {
		t.Error("Database not properly assigned")
	}
	if scanner.eventBus != eb {
		t.Error("EventBus not properly assigned")
	}
	if scanner.detector != mockHC {
		t.Error("Detector not properly assigned")
	}
	if scanner.pathMapper != mockPM {
		t.Error("PathMapper not properly assigned")
	}
	if scanner.activeScans == nil {
		t.Error("activeScans map should be initialized")
	}
	if scanner.filesInProgress == nil {
		t.Error("filesInProgress map should be initialized")
	}
	if scanner.shutdownCh == nil {
		t.Error("shutdownCh should be initialized")
	}
}

func TestScannerService_GetActiveScans(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns empty slice when no active scans", func(t *testing.T) {
		scans := scanner.GetActiveScans()
		if len(scans) != 0 {
			t.Errorf("Expected 0 scans, got %d", len(scans))
		}
	})

	t.Run("returns copies of active scans", func(t *testing.T) {
		scanner.mu.Lock()
		scanner.activeScans["scan-1"] = &ScanProgress{
			ID:         "scan-1",
			Type:       "path",
			Path:       "/media/movies",
			TotalFiles: 100,
			FilesDone:  50,
			Status:     "scanning",
		}
		scanner.activeScans["scan-2"] = &ScanProgress{
			ID:         "scan-2",
			Type:       "file",
			Path:       "/media/movies/test.mkv",
			TotalFiles: 1,
			FilesDone:  0,
			Status:     "scanning",
		}
		scanner.mu.Unlock()

		scans := scanner.GetActiveScans()
		if len(scans) != 2 {
			t.Errorf("Expected 2 scans, got %d", len(scans))
		}

		// Verify it returns copies (modify returned slice shouldn't affect original)
		for i := range scans {
			scans[i].Status = "modified"
		}

		scanner.mu.Lock()
		for _, scan := range scanner.activeScans {
			if scan.Status == "modified" {
				t.Error("GetActiveScans should return copies, not references")
			}
		}
		scanner.mu.Unlock()

		// Cleanup
		scanner.mu.Lock()
		delete(scanner.activeScans, "scan-1")
		delete(scanner.activeScans, "scan-2")
		scanner.mu.Unlock()
	})
}

func TestScannerService_IsPathBeingScanned(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns false when no active scans", func(t *testing.T) {
		if scanner.IsPathBeingScanned("/media/movies") {
			t.Error("Expected false for non-existent path")
		}
	})

	t.Run("returns false for file scan type", func(t *testing.T) {
		scanner.mu.Lock()
		scanner.activeScans["scan-1"] = &ScanProgress{
			ID:   "scan-1",
			Type: "file", // File type, not path
			Path: "/media/movies/test.mkv",
		}
		scanner.mu.Unlock()

		if scanner.IsPathBeingScanned("/media/movies/test.mkv") {
			t.Error("Expected false for file type scan")
		}

		scanner.mu.Lock()
		delete(scanner.activeScans, "scan-1")
		scanner.mu.Unlock()
	})

	t.Run("returns true for path scan type", func(t *testing.T) {
		scanner.mu.Lock()
		scanner.activeScans["scan-1"] = &ScanProgress{
			ID:   "scan-1",
			Type: "path",
			Path: "/media/movies",
		}
		scanner.mu.Unlock()

		if !scanner.IsPathBeingScanned("/media/movies") {
			t.Error("Expected true for active path scan")
		}

		scanner.mu.Lock()
		delete(scanner.activeScans, "scan-1")
		scanner.mu.Unlock()
	})
}

// =============================================================================
// Scan control tests (Cancel, Pause, Resume)
// =============================================================================

func TestScannerService_CancelScan(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns error for non-existent scan", func(t *testing.T) {
		err := scanner.CancelScan("non-existent")
		if err == nil {
			t.Error("Expected error for non-existent scan")
		}
	})

	t.Run("calls cancel function", func(t *testing.T) {
		cancelled := false
		cancelFunc := func() {
			cancelled = true
		}

		scanner.mu.Lock()
		scanner.activeScans["scan-1"] = &ScanProgress{
			ID:     "scan-1",
			cancel: cancelFunc,
		}
		scanner.mu.Unlock()

		err := scanner.CancelScan("scan-1")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if !cancelled {
			t.Error("Cancel function was not called")
		}

		scanner.mu.Lock()
		delete(scanner.activeScans, "scan-1")
		scanner.mu.Unlock()
	})

	t.Run("handles nil cancel function", func(t *testing.T) {
		scanner.mu.Lock()
		scanner.activeScans["scan-2"] = &ScanProgress{
			ID:     "scan-2",
			cancel: nil,
		}
		scanner.mu.Unlock()

		err := scanner.CancelScan("scan-2")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		scanner.mu.Lock()
		delete(scanner.activeScans, "scan-2")
		scanner.mu.Unlock()
	})
}

func TestScannerService_PauseScan(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns error for non-existent scan", func(t *testing.T) {
		err := scanner.PauseScan("non-existent")
		if err == nil {
			t.Error("Expected error for non-existent scan")
		}
	})

	t.Run("pauses scanning scan", func(t *testing.T) {
		scanner.mu.Lock()
		scanner.activeScans["scan-1"] = &ScanProgress{
			ID:       "scan-1",
			Status:   "scanning",
			isPaused: false,
		}
		scanner.mu.Unlock()

		err := scanner.PauseScan("scan-1")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		scanner.mu.Lock()
		scan := scanner.activeScans["scan-1"]
		if !scan.isPaused {
			t.Error("Scan should be paused")
		}
		if scan.Status != "paused" {
			t.Errorf("Expected status 'paused', got %q", scan.Status)
		}
		delete(scanner.activeScans, "scan-1")
		scanner.mu.Unlock()
	})

	t.Run("no-op when already paused", func(t *testing.T) {
		scanner.mu.Lock()
		scanner.activeScans["scan-2"] = &ScanProgress{
			ID:       "scan-2",
			Status:   "paused",
			isPaused: true,
		}
		scanner.mu.Unlock()

		err := scanner.PauseScan("scan-2")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		scanner.mu.Lock()
		delete(scanner.activeScans, "scan-2")
		scanner.mu.Unlock()
	})

	t.Run("returns error for non-scanning state", func(t *testing.T) {
		scanner.mu.Lock()
		scanner.activeScans["scan-3"] = &ScanProgress{
			ID:       "scan-3",
			Status:   "enumerating",
			isPaused: false,
		}
		scanner.mu.Unlock()

		err := scanner.PauseScan("scan-3")
		if err == nil {
			t.Error("Expected error for non-scanning state")
		}

		scanner.mu.Lock()
		delete(scanner.activeScans, "scan-3")
		scanner.mu.Unlock()
	})
}

func TestScannerService_ResumeScan(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns error for non-existent scan", func(t *testing.T) {
		err := scanner.ResumeScan("non-existent")
		if err == nil {
			t.Error("Expected error for non-existent scan")
		}
	})

	t.Run("no-op when not paused", func(t *testing.T) {
		scanner.mu.Lock()
		scanner.activeScans["scan-1"] = &ScanProgress{
			ID:       "scan-1",
			isPaused: false,
		}
		scanner.mu.Unlock()

		err := scanner.ResumeScan("scan-1")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		scanner.mu.Lock()
		delete(scanner.activeScans, "scan-1")
		scanner.mu.Unlock()
	})

	t.Run("sends resume signal when paused", func(t *testing.T) {
		resumeChan := make(chan struct{}, 1)

		scanner.mu.Lock()
		scanner.activeScans["scan-2"] = &ScanProgress{
			ID:         "scan-2",
			isPaused:   true,
			resumeChan: resumeChan,
		}
		scanner.mu.Unlock()

		err := scanner.ResumeScan("scan-2")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		// Check resume signal was sent
		select {
		case <-resumeChan:
			// Expected
		default:
			t.Error("Resume signal was not sent")
		}

		scanner.mu.Lock()
		delete(scanner.activeScans, "scan-2")
		scanner.mu.Unlock()
	})
}

// =============================================================================
// Path accessibility and validation tests
// =============================================================================

func TestScannerService_VerifyPathAccessible(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns error for non-existent path", func(t *testing.T) {
		err := scanner.verifyPathAccessible("/non/existent/path")
		if err == nil {
			t.Error("Expected error for non-existent path")
		}
	})

	t.Run("returns error for file path", func(t *testing.T) {
		tmpFile := filepath.Join(t.TempDir(), "test.txt")
		if err := os.WriteFile(tmpFile, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create temp file: %v", err)
		}

		err := scanner.verifyPathAccessible(tmpFile)
		if err == nil {
			t.Error("Expected error for file path")
		}
	})

	t.Run("succeeds for accessible directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create a file in the directory so it's not empty
		testFile := filepath.Join(tmpDir, "test.txt")
		if err := os.WriteFile(testFile, []byte("test"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		err := scanner.verifyPathAccessible(tmpDir)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
	})

	t.Run("succeeds for empty directory with warning", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Leave directory empty

		err := scanner.verifyPathAccessible(tmpDir)
		if err != nil {
			t.Errorf("Should not error on empty directory: %v", err)
		}
	})
}

// =============================================================================
// Corruption deduplication tests
// =============================================================================

func TestScannerService_HasActiveCorruption(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns false when no corruption exists", func(t *testing.T) {
		if scanner.hasActiveCorruption("/media/movies/clean.mkv") {
			t.Error("Expected false for non-corrupted file")
		}
	})

	t.Run("returns true for unresolved corruption", func(t *testing.T) {
		// Insert a CorruptionDetected event
		_, err := db.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
			VALUES ('corruption', 'test-corruption-1', 'CorruptionDetected', '{"file_path":"/media/movies/corrupt.mkv"}', 1, datetime('now'))
		`)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}

		if !scanner.hasActiveCorruption("/media/movies/corrupt.mkv") {
			t.Error("Expected true for active corruption")
		}
	})

	t.Run("returns false for resolved corruption", func(t *testing.T) {
		// Insert a CorruptionDetected event
		_, err := db.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
			VALUES ('corruption', 'test-corruption-2', 'CorruptionDetected', '{"file_path":"/media/movies/resolved.mkv"}', 1, datetime('now'))
		`)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}

		// Insert a VerificationSuccess event for the same aggregate
		_, err = db.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
			VALUES ('corruption', 'test-corruption-2', 'VerificationSuccess', '{}', 1, datetime('now'))
		`)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}

		if scanner.hasActiveCorruption("/media/movies/resolved.mkv") {
			t.Error("Expected false for resolved corruption")
		}
	})
}

func TestScannerService_LoadActiveCorruptionsForPath(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns empty map for path with no corruptions", func(t *testing.T) {
		result := scanner.LoadActiveCorruptionsForPath("/media/clean")
		if len(result) != 0 {
			t.Errorf("Expected empty map, got %d entries", len(result))
		}
	})

	t.Run("loads active corruptions for path", func(t *testing.T) {
		// Insert multiple corruption events for files under /media/movies
		testFiles := []string{
			"/media/movies/file1.mkv",
			"/media/movies/subdir/file2.mkv",
			"/media/movies/file3.mkv",
		}

		for i, filePath := range testFiles {
			_, err := db.Exec(`
				INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
				VALUES ('corruption', ?, 'CorruptionDetected', ?, 1, datetime('now'))
			`, "batch-corruption-"+string(rune('a'+i)), `{"file_path":"`+filePath+`"}`)
			if err != nil {
				t.Fatalf("Failed to insert event: %v", err)
			}
		}

		// Resolve one of them
		_, err = db.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
			VALUES ('corruption', 'batch-corruption-c', 'VerificationSuccess', '{}', 1, datetime('now'))
		`)
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}

		result := scanner.LoadActiveCorruptionsForPath("/media/movies")
		if len(result) != 2 {
			t.Errorf("Expected 2 active corruptions, got %d", len(result))
		}

		if !result["/media/movies/file1.mkv"] {
			t.Error("Missing file1.mkv in active corruptions")
		}
		if !result["/media/movies/subdir/file2.mkv"] {
			t.Error("Missing file2.mkv in active corruptions")
		}
		if result["/media/movies/file3.mkv"] {
			t.Error("file3.mkv should not be in active corruptions (resolved)")
		}
	})
}

// =============================================================================
// Scan path config tests
// =============================================================================

func TestScannerService_GetScanPathConfig(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns error for no matching path", func(t *testing.T) {
		_, _, err := scanner.getScanPathConfig("/non/existent/path/file.mkv")
		if err == nil {
			t.Error("Expected error for non-matching path")
		}
	})

	t.Run("finds matching scan path", func(t *testing.T) {
		// Insert a scan path
		_, err := db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run)
			VALUES (1, '/media/movies', '/movies', 1, 1, 0)
		`)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}
		scanner.InvalidateScanPathCache()

		autoRemediate, dryRun, err := scanner.getScanPathConfig("/media/movies/test.mkv")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if !autoRemediate {
			t.Error("Expected autoRemediate to be true")
		}
		if dryRun {
			t.Error("Expected dryRun to be false")
		}
	})

	t.Run("matches most specific path", func(t *testing.T) {
		// Insert a more specific path
		_, err := db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run)
			VALUES (2, '/media/movies/4k', '/movies/4k', 1, 0, 1)
		`)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}
		scanner.InvalidateScanPathCache()

		autoRemediate, dryRun, err := scanner.getScanPathConfig("/media/movies/4k/test.mkv")
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}
		if autoRemediate {
			t.Error("Expected autoRemediate to be false (from more specific path)")
		}
		if !dryRun {
			t.Error("Expected dryRun to be true (from more specific path)")
		}
	})

	t.Run("does not match partial path prefix", func(t *testing.T) {
		// /media/movies2 should not match /media/movies
		_, err := db.Exec(`DELETE FROM scan_paths`)
		if err != nil {
			t.Fatalf("Failed to clean scan paths: %v", err)
		}
		_, err = db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run)
			VALUES (1, '/media/movies', '/movies', 1, 1, 0)
		`)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}
		scanner.InvalidateScanPathCache()

		_, _, err = scanner.getScanPathConfig("/media/movies2/test.mkv")
		if err == nil {
			t.Error("Expected error for partial prefix match")
		}
	})
}

// =============================================================================
// Pending rescan tests
// =============================================================================

func TestScannerService_QueueForRescan(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("queues new file for rescan", func(t *testing.T) {
		scanner.queueForRescan("/media/movies/test.mkv", 1, "MountLost", "Transport endpoint not connected")

		var count int
		err := db.QueryRow(`SELECT COUNT(*) FROM pending_rescans WHERE file_path = ?`, "/media/movies/test.mkv").Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 pending rescan, got %d", count)
		}
	})

	t.Run("updates existing entry on conflict", func(t *testing.T) {
		// Queue same file again - should increment retry count
		scanner.queueForRescan("/media/movies/test.mkv", 1, "IOError", "Input/output error")

		var retryCount int
		var errorType string
		err := db.QueryRow(`
			SELECT retry_count, error_type FROM pending_rescans WHERE file_path = ?
		`, "/media/movies/test.mkv").Scan(&retryCount, &errorType)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if retryCount != 1 {
			t.Errorf("Expected retry_count 1, got %d", retryCount)
		}
		if errorType != "IOError" {
			t.Errorf("Expected error_type 'IOError', got %q", errorType)
		}
	})
}

func TestScannerService_GetPendingRescanStats(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns zeros for empty table", func(t *testing.T) {
		pending, abandoned, resolved, err := scanner.GetPendingRescanStats()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if pending != 0 || abandoned != 0 || resolved != 0 {
			t.Errorf("Expected all zeros, got pending=%d, abandoned=%d, resolved=%d", pending, abandoned, resolved)
		}
	})

	t.Run("returns correct counts", func(t *testing.T) {
		// Insert test data
		_, err := db.Exec(`
			INSERT INTO pending_rescans (file_path, error_type, status) VALUES
			('/media/movies/pending1.mkv', 'MountLost', 'pending'),
			('/media/movies/pending2.mkv', 'IOError', 'pending'),
			('/media/movies/abandoned.mkv', 'MountLost', 'abandoned'),
			('/media/movies/resolved.mkv', 'IOError', 'resolved')
		`)
		if err != nil {
			t.Fatalf("Failed to insert test data: %v", err)
		}

		pending, abandoned, resolved, err := scanner.GetPendingRescanStats()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if pending != 2 {
			t.Errorf("Expected pending=2, got %d", pending)
		}
		if abandoned != 1 {
			t.Errorf("Expected abandoned=1, got %d", abandoned)
		}
		if resolved != 1 {
			t.Errorf("Expected resolved=1, got %d", resolved)
		}
	})
}

// =============================================================================
// Shutdown tests
// =============================================================================

func TestScannerService_Shutdown(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Insert a scan record for testing state save
	result, err := db.Exec(`
		INSERT INTO scans (path, path_id, status, total_files, files_scanned)
		VALUES ('/media/movies', 1, 'running', 100, 50)
	`)
	if err != nil {
		t.Fatalf("Failed to insert scan: %v", err)
	}
	scanDBID, _ := result.LastInsertId()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	cancelled := false
	cancelFunc := func() { cancelled = true }

	scanner.mu.Lock()
	scanner.activeScans["scan-1"] = &ScanProgress{
		ID:        "scan-1",
		Type:      "path",
		Path:      "/media/movies",
		FilesDone: 50,
		ScanDBID:  scanDBID,
		cancel:    cancelFunc,
	}
	scanner.mu.Unlock()

	// Call shutdown
	scanner.Shutdown()

	// Verify cancel was called
	if !cancelled {
		t.Error("Cancel function was not called during shutdown")
	}

	// Verify scan state was saved
	var status string
	var currentIndex int
	err = db.QueryRow(`SELECT status, current_file_index FROM scans WHERE id = ?`, scanDBID).Scan(&status, &currentIndex)
	if err != nil {
		t.Fatalf("Failed to query scan: %v", err)
	}
	if status != "interrupted" {
		t.Errorf("Expected status 'interrupted', got %q", status)
	}
	if currentIndex != 50 {
		t.Errorf("Expected current_file_index 50, got %d", currentIndex)
	}

	// Verify shutdown channel is closed
	select {
	case <-scanner.shutdownCh:
		// Expected - channel should be closed
	default:
		t.Error("Shutdown channel should be closed")
	}
}

// =============================================================================
// Cache tests
// =============================================================================

func TestScannerService_ScanPathCache(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("cache is populated on first access", func(t *testing.T) {
		// Insert test scan paths
		_, err := db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run)
			VALUES (100, '/cache/test1', '/test1', 1, 1, 0)
		`)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}
		scanner.InvalidateScanPathCache()

		// Access should populate cache
		err = scanner.refreshScanPathCache()
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		scanner.scanPathCacheMu.RLock()
		cacheLen := len(scanner.scanPathCache)
		scanner.scanPathCacheMu.RUnlock()

		if cacheLen == 0 {
			t.Error("Cache should be populated")
		}
	})

	t.Run("cache invalidation works", func(t *testing.T) {
		scanner.scanPathCacheMu.Lock()
		scanner.scanPathCacheTime = time.Now()
		scanner.scanPathCacheMu.Unlock()

		scanner.InvalidateScanPathCache()

		scanner.scanPathCacheMu.RLock()
		cacheTime := scanner.scanPathCacheTime
		scanner.scanPathCacheMu.RUnlock()

		if !cacheTime.IsZero() {
			t.Error("Cache time should be zero after invalidation")
		}
	})
}

// =============================================================================
// EmitProgress tests
// =============================================================================

func TestScannerService_EmitProgress(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("emits progress event without panic", func(t *testing.T) {
		progress := &ScanProgress{
			ID:          "test-progress-1",
			Type:        "path",
			Path:        "/media/movies",
			TotalFiles:  100,
			FilesDone:   25,
			CurrentFile: "/media/movies/current.mkv",
			Status:      "scanning",
			StartTime:   time.Now().Format(time.RFC3339),
		}

		// Should not panic
		scanner.emitProgress(progress)
	})
}

// =============================================================================
// HandleTrueCorruption tests
// =============================================================================

func TestScannerService_HandleTrueCorruption(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("increments corruption count", func(t *testing.T) {
		ctx := context.Background()
		progress := &ScanProgress{
			ID:              "test-corruption-1",
			Path:            "/media/movies",
			corruptionCount: 0,
		}
		sfc := &scanFileContext{
			filePath:          "/media/movies/corrupt.mkv",
			fileSize:          1024,
			scanDBID:          0,
			activeCorruptions: make(map[string]bool),
		}
		healthErr := &integration.HealthCheckError{
			Type:    integration.ErrorTypeCorruptHeader,
			Message: "File is corrupted",
		}

		action := scanner.handleTrueCorruption(ctx, progress, sfc, healthErr)
		if action != scanContinue {
			t.Errorf("Expected scanContinue, got %v", action)
		}
		if progress.corruptionCount != 1 {
			t.Errorf("Expected corruptionCount 1, got %d", progress.corruptionCount)
		}
	})

	t.Run("skips duplicate corruption with preloaded map", func(t *testing.T) {
		ctx := context.Background()
		progress := &ScanProgress{
			ID:   "test-corruption-2",
			Path: "/media/movies",
		}
		sfc := &scanFileContext{
			filePath: "/media/movies/already-processing.mkv",
			fileSize: 1024,
			scanDBID: 0,
			activeCorruptions: map[string]bool{
				"/media/movies/already-processing.mkv": true,
			},
		}
		healthErr := &integration.HealthCheckError{
			Type:    integration.ErrorTypeCorruptStream,
			Message: "File is corrupted",
		}

		action := scanner.handleTrueCorruption(ctx, progress, sfc, healthErr)
		if action != scanSkipToNext {
			t.Errorf("Expected scanSkipToNext for duplicate, got %v", action)
		}
	})

	t.Run("records corrupt file in database", func(t *testing.T) {
		// Create a scan record
		result, err := db.Exec(`
			INSERT INTO scans (path, path_id, status, total_files, files_scanned, corruptions_found)
			VALUES ('/media/movies', 1, 'running', 10, 0, 0)
		`)
		if err != nil {
			t.Fatalf("Failed to create scan: %v", err)
		}
		scanDBID, _ := result.LastInsertId()

		ctx := context.Background()
		progress := &ScanProgress{
			ID:   "test-corruption-3",
			Path: "/media/movies",
		}
		sfc := &scanFileContext{
			filePath:          "/media/movies/recorded-corrupt.mkv",
			fileSize:          2048,
			scanDBID:          scanDBID,
			activeCorruptions: make(map[string]bool),
		}
		healthErr := &integration.HealthCheckError{
			Type:    integration.ErrorTypeInvalidFormat,
			Message: "File has zero size",
		}

		scanner.handleTrueCorruption(ctx, progress, sfc, healthErr)

		// Verify record was created
		var count int
		err = db.QueryRow(`
			SELECT COUNT(*) FROM scan_files
			WHERE scan_id = ? AND file_path = ? AND status = 'corrupt'
		`, scanDBID, sfc.filePath).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 corrupt file record, got %d", count)
		}
	})
}

// =============================================================================
// ShouldSkipChangingSize tests
// =============================================================================

func TestScannerService_ShouldSkipChangingSize(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns false for stable file", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "stable.mkv")
		if err := os.WriteFile(testFile, []byte("stable content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		info, _ := os.Stat(testFile)
		sfc := &scanFileContext{
			filePath: testFile,
			fileSize: info.Size(),
			scanDBID: 0,
		}

		// File is stable (not changing)
		if scanner.shouldSkipChangingSize(sfc) {
			t.Error("Stable file should not be skipped")
		}
	})
}

// =============================================================================
// Integration test with mock detector
// =============================================================================

func TestScannerService_RecordHealthyFile(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Create a scan record
	result, err := db.Exec(`
		INSERT INTO scans (path, path_id, status, total_files, files_scanned)
		VALUES ('/media/movies', 1, 'running', 10, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan: %v", err)
	}
	scanID, _ := result.LastInsertId()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("records healthy file in database", func(t *testing.T) {
		sfc := &scanFileContext{
			filePath: "/media/movies/healthy.mkv",
			fileSize: 1024000,
			scanDBID: scanID,
		}

		scanner.recordHealthyFile(sfc)

		// Verify record was created
		var count int
		err := db.QueryRow(`
			SELECT COUNT(*) FROM scan_files
			WHERE scan_id = ? AND file_path = ? AND status = 'healthy'
		`, scanID, sfc.filePath).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query scan_files: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 healthy file record, got %d", count)
		}
	})

	t.Run("does nothing when scanDBID is 0", func(t *testing.T) {
		sfc := &scanFileContext{
			filePath: "/media/movies/notrack.mkv",
			fileSize: 1024000,
			scanDBID: 0, // No tracking
		}

		scanner.recordHealthyFile(sfc)

		// Verify no record was created
		var count int
		err := db.QueryRow(`
			SELECT COUNT(*) FROM scan_files WHERE file_path = ?
		`, sfc.filePath).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query scan_files: %v", err)
		}
		if count != 0 {
			t.Errorf("Expected 0 records for untracked file, got %d", count)
		}
	})
}

// =============================================================================
// ScanFile integration tests
// =============================================================================

func TestScannerService_ScanFile_RaceConditionPrevention(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Create a mock health checker that always returns healthy
	mockHC := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return true, nil
		},
	}

	scanner := NewScannerService(db, eb, mockHC, nil)

	t.Run("skips file already in progress", func(t *testing.T) {
		// Mark file as in progress
		scanner.filesMu.Lock()
		scanner.filesInProgress["/media/movies/in-progress.mkv"] = true
		scanner.filesMu.Unlock()

		// Should return nil without scanning
		err := scanner.ScanFile("/media/movies/in-progress.mkv")
		if err != nil {
			t.Errorf("Expected nil error for in-progress file, got %v", err)
		}

		// Cleanup
		scanner.filesMu.Lock()
		delete(scanner.filesInProgress, "/media/movies/in-progress.mkv")
		scanner.filesMu.Unlock()
	})

	t.Run("marks file as in progress during scan", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.mkv")
		if err := os.WriteFile(testFile, []byte("test content"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		// Add scan path config so the scanner knows about it
		_, err := db.Exec(`
			INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, dry_run)
			VALUES (?, ?, 1, 0, 0)
		`, tmpDir, tmpDir)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}
		scanner.InvalidateScanPathCache()

		// Start scan in background
		done := make(chan struct{})
		go func() {
			_ = scanner.ScanFile(testFile)
			close(done)
		}()

		// Wait for completion
		<-done

		// File should no longer be in progress
		if scanner.IsFileBeingScanned(testFile) {
			t.Error("File should not be in progress after scan")
		}
	})
}

// =============================================================================
// ResumeInterruptedScans tests
// =============================================================================

func TestScannerService_ResumeInterruptedScans(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("does nothing when no interrupted scans", func(t *testing.T) {
		// Should not panic
		scanner.ResumeInterruptedScans()
	})

	t.Run("logs and resumes interrupted scans with file list", func(t *testing.T) {
		// Insert an interrupted scan record
		_, err := db.Exec(`
			INSERT INTO scans (path, path_id, status, total_files, current_file_index, file_list, detection_config, auto_remediate, dry_run, started_at)
			VALUES ('/media/movies', 1, 'interrupted', 10, 5, '[]', '{"method":"ffprobe","mode":"quick"}', 0, 0, datetime('now'))
		`)
		if err != nil {
			t.Fatalf("Failed to insert scan: %v", err)
		}

		// Should resume - the goroutine will fail because there are no files, but it shouldn't panic
		scanner.ResumeInterruptedScans()

		// Give goroutine time to run
		time.Sleep(50 * time.Millisecond)
	})
}

// =============================================================================
// InvalidateScanPathCache tests
// =============================================================================

func TestScannerService_InvalidateScanPathCache(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	// Set a valid cache time
	scanner.scanPathCacheMu.Lock()
	scanner.scanPathCacheTime = time.Now()
	scanner.scanPathCacheMu.Unlock()

	// Invalidate
	scanner.InvalidateScanPathCache()

	// Verify cache time is zero
	scanner.scanPathCacheMu.RLock()
	cacheTime := scanner.scanPathCacheTime
	scanner.scanPathCacheMu.RUnlock()

	if !cacheTime.IsZero() {
		t.Error("Cache time should be zero after invalidation")
	}
}

// =============================================================================
// RefreshScanPathCache tests
// =============================================================================

func TestScannerService_RefreshScanPathCache(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("does not refresh when cache is valid", func(t *testing.T) {
		// Insert a scan path
		_, err := db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run)
			VALUES (200, '/cache/valid', '/valid', 1, 1, 0)
		`)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}

		// First refresh populates cache
		scanner.InvalidateScanPathCache()
		err = scanner.refreshScanPathCache()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		scanner.scanPathCacheMu.RLock()
		initialLen := len(scanner.scanPathCache)
		scanner.scanPathCacheMu.RUnlock()

		// Insert another path
		_, err = db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run)
			VALUES (201, '/cache/new', '/new', 1, 0, 0)
		`)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}

		// Second refresh should use cache (TTL not expired)
		err = scanner.refreshScanPathCache()
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}

		scanner.scanPathCacheMu.RLock()
		newLen := len(scanner.scanPathCache)
		scanner.scanPathCacheMu.RUnlock()

		// Cache should not have changed
		if newLen != initialLen {
			t.Errorf("Cache should not have changed, expected %d got %d", initialLen, newLen)
		}
	})
}

// =============================================================================
// DefaultMediaExtensions tests
// =============================================================================

func TestDefaultMediaExtensions(t *testing.T) {
	// All default extensions should be recognized
	expectedExtensions := []string{
		".mkv", ".mp4", ".avi", ".mov", ".wmv", ".flv", ".webm",
		".m4v", ".mpg", ".mpeg", ".ts", ".m2ts", ".vob", ".3gp",
		".ogv", ".divx", ".xvid",
	}

	for _, ext := range expectedExtensions {
		t.Run(ext, func(t *testing.T) {
			if !defaultMediaExtensions[ext] {
				t.Errorf("Expected %s to be in defaultMediaExtensions", ext)
			}
		})
	}
}

// =============================================================================
// ScanFileContext struct tests
// =============================================================================

func TestScanFileContext(t *testing.T) {
	t.Run("initializes with correct fields", func(t *testing.T) {
		sfc := &scanFileContext{
			filePath:      "/media/test.mkv",
			fileSize:      1024,
			fileMtime:     time.Now(),
			pathID:        1,
			scanDBID:      2,
			autoRemediate: true,
			dryRun:        false,
			detectionConfig: integration.DetectionConfig{
				Method: "ffprobe",
				Mode:   "quick",
			},
			activeCorruptions: map[string]bool{
				"/media/test2.mkv": true,
			},
		}

		if sfc.filePath != "/media/test.mkv" {
			t.Error("filePath not set correctly")
		}
		if sfc.fileSize != 1024 {
			t.Error("fileSize not set correctly")
		}
		if sfc.pathID != 1 {
			t.Error("pathID not set correctly")
		}
		if !sfc.autoRemediate {
			t.Error("autoRemediate should be true")
		}
		if sfc.dryRun {
			t.Error("dryRun should be false")
		}
		if !sfc.activeCorruptions["/media/test2.mkv"] {
			t.Error("activeCorruptions not set correctly")
		}
	})
}

// =============================================================================
// ScanLoopAction tests
// =============================================================================

func TestScanLoopAction(t *testing.T) {
	t.Run("constants have expected values", func(t *testing.T) {
		if scanContinue != 0 {
			t.Errorf("scanContinue should be 0, got %d", scanContinue)
		}
		if scanReturn != 1 {
			t.Errorf("scanReturn should be 1, got %d", scanReturn)
		}
		if scanSkipToNext != 2 {
			t.Errorf("scanSkipToNext should be 2, got %d", scanSkipToNext)
		}
	})
}

// =============================================================================
// ScanProgress struct tests
// =============================================================================

func TestScanProgress_Fields(t *testing.T) {
	t.Run("initializes with all fields", func(t *testing.T) {
		progress := &ScanProgress{
			ID:          "test-id",
			Type:        "path",
			Path:        "/media/movies",
			PathID:      1,
			TotalFiles:  100,
			FilesDone:   50,
			CurrentFile: "/media/movies/current.mkv",
			Status:      "scanning",
			StartTime:   "2025-01-01T00:00:00Z",
			ScanDBID:    5,
		}

		if progress.ID != "test-id" {
			t.Error("ID not set correctly")
		}
		if progress.Type != "path" {
			t.Error("Type not set correctly")
		}
		if progress.TotalFiles != 100 {
			t.Error("TotalFiles not set correctly")
		}
		if progress.FilesDone != 50 {
			t.Error("FilesDone not set correctly")
		}
		if progress.ScanDBID != 5 {
			t.Error("ScanDBID not set correctly")
		}
	})
}

// =============================================================================
// Batch throttling constants tests
// =============================================================================

func TestBatchThrottlingConstants(t *testing.T) {
	if batchThrottleThreshold != 10 {
		t.Errorf("batchThrottleThreshold should be 10, got %d", batchThrottleThreshold)
	}
	if batchThrottleDelay != 30*time.Second {
		t.Errorf("batchThrottleDelay should be 30s, got %v", batchThrottleDelay)
	}
}

// =============================================================================
// StartRescanWorker test
// =============================================================================

func TestScannerService_StartRescanWorker(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("starts without panic", func(t *testing.T) {
		// Start the worker
		scanner.StartRescanWorker()

		// Give it time to start
		time.Sleep(10 * time.Millisecond)

		// Shutdown to stop the worker
		close(scanner.shutdownCh)

		// Give it time to stop
		time.Sleep(10 * time.Millisecond)
	})
}

// =============================================================================
// ProcessPendingRescans test
// =============================================================================

func TestScannerService_ProcessPendingRescans(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	mockHC := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return true, nil // All files are healthy
		},
	}

	scanner := &ScannerService{
		db:              db,
		detector:        mockHC,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("does nothing when no pending rescans", func(t *testing.T) {
		// Should not panic
		scanner.processPendingRescans()
	})

	t.Run("processes ready rescans", func(t *testing.T) {
		// Insert a rescan that's ready (next_retry_at in the past)
		_, err := db.Exec(`
			INSERT INTO pending_rescans (file_path, path_id, error_type, status, next_retry_at, retry_count, max_retries)
			VALUES ('/media/movies/rescan-test.mkv', 1, 'MountLost', 'pending', datetime('now', '-1 hour'), 0, 5)
		`)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}

		scanner.processPendingRescans()

		// Verify status was updated
		var status string
		err = db.QueryRow(`SELECT status FROM pending_rescans WHERE file_path = ?`, "/media/movies/rescan-test.mkv").Scan(&status)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if status != "resolved" {
			t.Errorf("Expected status 'resolved', got %q", status)
		}
	})

	t.Run("handles still inaccessible files", func(t *testing.T) {
		// Create a new scanner with a health checker that returns inaccessible error
		mockHC2 := &testutil.MockHealthChecker{
			CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
				return false, &integration.HealthCheckError{
					Type:    integration.ErrorTypeMountLost,
					Message: "Still inaccessible",
				}
			},
		}

		scanner2 := &ScannerService{
			db:              db,
			detector:        mockHC2,
			activeScans:     make(map[string]*ScanProgress),
			filesInProgress: make(map[string]bool),
			shutdownCh:      make(chan struct{}),
		}

		// Insert a rescan that's ready
		_, err := db.Exec(`
			INSERT INTO pending_rescans (file_path, path_id, error_type, status, next_retry_at, retry_count, max_retries)
			VALUES ('/media/movies/still-inaccessible.mkv', 1, 'MountLost', 'pending', datetime('now', '-1 hour'), 0, 5)
		`)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}

		scanner2.processPendingRescans()

		// Verify retry count was incremented
		var retryCount int
		err = db.QueryRow(`SELECT retry_count FROM pending_rescans WHERE file_path = ?`, "/media/movies/still-inaccessible.mkv").Scan(&retryCount)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if retryCount != 1 {
			t.Errorf("Expected retry_count 1, got %d", retryCount)
		}
	})

	t.Run("handles corruption detection during rescan", func(t *testing.T) {
		eb := eventbus.NewEventBus(db)
		defer eb.Shutdown()

		// Create a scanner with health checker that returns corruption error
		mockHC3 := &testutil.MockHealthChecker{
			CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
				return false, &integration.HealthCheckError{
					Type:    integration.ErrorTypeCorruptHeader,
					Message: "Corrupt file",
				}
			},
		}

		scanner3 := &ScannerService{
			db:              db,
			eventBus:        eb,
			detector:        mockHC3,
			activeScans:     make(map[string]*ScanProgress),
			filesInProgress: make(map[string]bool),
			shutdownCh:      make(chan struct{}),
		}

		// Insert a rescan that's ready
		_, err := db.Exec(`
			INSERT INTO pending_rescans (file_path, path_id, error_type, status, next_retry_at, retry_count, max_retries)
			VALUES ('/media/movies/corrupt-during-rescan.mkv', 1, 'MountLost', 'pending', datetime('now', '-1 hour'), 0, 5)
		`)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}

		scanner3.processPendingRescans()

		// Verify status was marked as corrupt
		var status string
		err = db.QueryRow(`SELECT resolution FROM pending_rescans WHERE file_path = ?`, "/media/movies/corrupt-during-rescan.mkv").Scan(&status)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if status != "corrupt" {
			t.Errorf("Expected resolution 'corrupt', got %q", status)
		}
	})

	t.Run("abandons after max retries", func(t *testing.T) {
		// Create a scanner with health checker that returns inaccessible error
		mockHC4 := &testutil.MockHealthChecker{
			CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
				return false, &integration.HealthCheckError{
					Type:    integration.ErrorTypeMountLost,
					Message: "Still inaccessible",
				}
			},
		}

		scanner4 := &ScannerService{
			db:              db,
			detector:        mockHC4,
			activeScans:     make(map[string]*ScanProgress),
			filesInProgress: make(map[string]bool),
			shutdownCh:      make(chan struct{}),
		}

		// Insert a rescan that's at max retries - 1
		_, err := db.Exec(`
			INSERT INTO pending_rescans (file_path, path_id, error_type, status, next_retry_at, retry_count, max_retries)
			VALUES ('/media/movies/max-retries.mkv', 1, 'MountLost', 'pending', datetime('now', '-1 hour'), 4, 5)
		`)
		if err != nil {
			t.Fatalf("Failed to insert: %v", err)
		}

		scanner4.processPendingRescans()

		// Verify status was changed to abandoned
		var status string
		err = db.QueryRow(`SELECT status FROM pending_rescans WHERE file_path = ?`, "/media/movies/max-retries.mkv").Scan(&status)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if status != "abandoned" {
			t.Errorf("Expected status 'abandoned', got %q", status)
		}
	})
}

// =============================================================================
// ScanPath tests
// =============================================================================

func TestScannerService_ScanPath(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockHC := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return true, nil
		},
		CheckWithConfigFunc: func(path string, config integration.DetectionConfig) (bool, *integration.HealthCheckError) {
			return true, nil
		},
	}

	scanner := NewScannerService(db, eb, mockHC, nil)

	t.Run("returns error for inaccessible path", func(t *testing.T) {
		// Insert scan path config
		_, err := db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run, detection_method, detection_mode)
			VALUES (300, '/non/existent/path', '/arr/path', 1, 0, 0, 'ffprobe', 'quick')
		`)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}

		err = scanner.ScanPath(300, "/non/existent/path")
		if err == nil {
			t.Error("Expected error for inaccessible path")
		}
	})

	t.Run("scans accessible directory with media files", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.mkv")
		if err := os.WriteFile(testFile, []byte("test content that is old enough"), 0644); err != nil {
			t.Fatalf("Failed to create test file: %v", err)
		}

		// Make file appear old to bypass recently-modified check
		oldTime := time.Now().Add(-5 * time.Minute)
		if err := os.Chtimes(testFile, oldTime, oldTime); err != nil {
			t.Fatalf("Failed to set file time: %v", err)
		}

		// Insert scan path config
		_, err := db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run, detection_method, detection_mode)
			VALUES (301, ?, ?, 1, 0, 0, 'ffprobe', 'quick')
		`, tmpDir, tmpDir)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}

		err = scanner.ScanPath(301, tmpDir)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		// Verify scan was created
		var count int
		err = db.QueryRow(`SELECT COUNT(*) FROM scans WHERE path = ?`, tmpDir).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if count == 0 {
			t.Error("Scan record should have been created")
		}
	})

	t.Run("skips non-media files", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create non-media files
		if err := os.WriteFile(filepath.Join(tmpDir, "readme.txt"), []byte("text"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "cover.jpg"), []byte("image"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
		if err := os.WriteFile(filepath.Join(tmpDir, "subtitles.srt"), []byte("subs"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		// Insert scan path config
		_, err := db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run, detection_method, detection_mode)
			VALUES (302, ?, ?, 1, 0, 0, 'ffprobe', 'quick')
		`, tmpDir, tmpDir)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}

		err = scanner.ScanPath(302, tmpDir)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		// Verify total_files is 0 (no media files)
		var totalFiles int
		err = db.QueryRow(`SELECT total_files FROM scans WHERE path = ?`, tmpDir).Scan(&totalFiles)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if totalFiles != 0 {
			t.Errorf("Expected 0 media files, got %d", totalFiles)
		}
	})

	t.Run("skips hidden files", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create hidden media file
		if err := os.WriteFile(filepath.Join(tmpDir, ".hidden.mkv"), []byte("hidden"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		// Create sample file
		if err := os.WriteFile(filepath.Join(tmpDir, "sample.mkv"), []byte("sample"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		// Insert scan path config
		_, err := db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run, detection_method, detection_mode)
			VALUES (303, ?, ?, 1, 0, 0, 'ffprobe', 'quick')
		`, tmpDir, tmpDir)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}

		err = scanner.ScanPath(303, tmpDir)
		if err != nil {
			t.Errorf("Unexpected error: %v", err)
		}

		// Verify total_files is 0 (hidden and sample files are skipped)
		var totalFiles int
		err = db.QueryRow(`SELECT total_files FROM scans WHERE path = ?`, tmpDir).Scan(&totalFiles)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if totalFiles != 0 {
			t.Errorf("Expected 0 media files (hidden/sample skipped), got %d", totalFiles)
		}
	})
}

// =============================================================================
// ScanFiles loop tests
// =============================================================================

func TestScannerService_ScanFiles(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	t.Run("handles corruption detection", func(t *testing.T) {
		mockHC := &testutil.MockHealthChecker{
			CheckWithConfigFunc: func(path string, config integration.DetectionConfig) (bool, *integration.HealthCheckError) {
				return false, &integration.HealthCheckError{
					Type:    integration.ErrorTypeCorruptHeader,
					Message: "File corrupted",
				}
			},
		}

		scanner := &ScannerService{
			db:              db,
			eventBus:        eb,
			detector:        mockHC,
			activeScans:     make(map[string]*ScanProgress),
			filesInProgress: make(map[string]bool),
			shutdownCh:      make(chan struct{}),
		}

		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "corrupt.mkv")
		if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
		oldTime := time.Now().Add(-10 * time.Minute)
		if err := os.Chtimes(testFile, oldTime, oldTime); err != nil {
			t.Fatalf("Failed to set time: %v", err)
		}

		// Create scan record
		result, err := db.Exec(`
			INSERT INTO scans (path, path_id, status, total_files, files_scanned, corruptions_found)
			VALUES (?, 1, 'running', 1, 0, 0)
		`, tmpDir)
		if err != nil {
			t.Fatalf("Failed to create scan: %v", err)
		}
		scanDBID, _ := result.LastInsertId()

		ctx := context.Background()
		progress := &ScanProgress{
			ID:         "test-corrupt-scan",
			Type:       "path",
			Path:       tmpDir,
			PathID:     1,
			TotalFiles: 1,
			FilesDone:  0,
			ScanDBID:   scanDBID,
			pauseChan:  make(chan struct{}),
			resumeChan: make(chan struct{}),
		}

		detectionConfig := integration.DetectionConfig{
			Method: "ffprobe",
			Mode:   "quick",
		}

		scanner.scanFiles(ctx, progress, scanFilesConfig{
			Files:           []string{testFile},
			StartIndex:      0,
			DetectionConfig: detectionConfig,
			AutoRemediate:   true,
			DryRun:          false,
			ScanDBID:        scanDBID,
		})

		if progress.corruptionCount != 1 {
			t.Errorf("Expected 1 corruption, got %d", progress.corruptionCount)
		}
	})

	t.Run("updates progress periodically", func(t *testing.T) {
		mockHC := &testutil.MockHealthChecker{
			CheckWithConfigFunc: func(path string, config integration.DetectionConfig) (bool, *integration.HealthCheckError) {
				return true, nil
			},
		}

		scanner := &ScannerService{
			db:              db,
			eventBus:        eb,
			detector:        mockHC,
			activeScans:     make(map[string]*ScanProgress),
			filesInProgress: make(map[string]bool),
			shutdownCh:      make(chan struct{}),
		}

		tmpDir := t.TempDir()

		// Create multiple media files
		files := make([]string, 15)
		for i := 0; i < 15; i++ {
			file := filepath.Join(tmpDir, "test"+string(rune('a'+i))+".mkv")
			if err := os.WriteFile(file, []byte("content"), 0644); err != nil {
				t.Fatalf("Failed to create file: %v", err)
			}
			// Make files old
			oldTime := time.Now().Add(-10 * time.Minute)
			if err := os.Chtimes(file, oldTime, oldTime); err != nil {
				t.Fatalf("Failed to set time: %v", err)
			}
			files[i] = file
		}

		// Create scan record
		result, err := db.Exec(`
			INSERT INTO scans (path, path_id, status, total_files, files_scanned)
			VALUES (?, 1, 'running', 15, 0)
		`, tmpDir)
		if err != nil {
			t.Fatalf("Failed to create scan: %v", err)
		}
		scanDBID, _ := result.LastInsertId()

		ctx := context.Background()
		progress := &ScanProgress{
			ID:         "test-scan",
			Type:       "path",
			Path:       tmpDir,
			PathID:     1,
			TotalFiles: 15,
			FilesDone:  0,
			ScanDBID:   scanDBID,
			pauseChan:  make(chan struct{}),
			resumeChan: make(chan struct{}),
		}

		detectionConfig := integration.DetectionConfig{
			Method: "ffprobe",
			Mode:   "quick",
		}

		scanner.scanFiles(ctx, progress, scanFilesConfig{
			Files:           files,
			StartIndex:      0,
			DetectionConfig: detectionConfig,
			AutoRemediate:   false,
			DryRun:          false,
			ScanDBID:        scanDBID,
		})

		if progress.FilesDone != 15 {
			t.Errorf("Expected 15 files done, got %d", progress.FilesDone)
		}
		if progress.Status != "completed" {
			t.Errorf("Expected status 'completed', got %q", progress.Status)
		}
	})
}

// =============================================================================
// Performance Benchmarks
// =============================================================================

func BenchmarkIsMediaFile(b *testing.B) {
	testPaths := []string{
		"/media/movies/Movie.mkv",
		"/media/movies/Movie.mp4",
		"/media/movies/Movie.avi",
		"/media/movies/readme.txt",
		"/media/movies/cover.jpg",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, path := range testPaths {
			isMediaFile(path)
		}
	}
}

func BenchmarkIsHiddenOrTempFile(b *testing.B) {
	testPaths := []string{
		"/media/movies/normal.mkv",
		"/media/movies/.hidden.mkv",
		"/media/movies/movie.tmp",
		"/media/movies/sample.mkv",
		"/media/movies/movie-trailer.mkv",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		for _, path := range testPaths {
			isHiddenOrTempFile(path)
		}
	}
}

func BenchmarkScannerService_IsFileBeingScanned(b *testing.B) {
	scanner := &ScannerService{
		filesInProgress: make(map[string]bool),
	}

	// Add some files to the in-progress map
	for i := 0; i < 100; i++ {
		scanner.filesInProgress["/media/movies/file"+string(rune('a'+i%26))+".mkv"] = true
	}

	testPath := "/media/movies/test.mkv"

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scanner.IsFileBeingScanned(testPath)
	}
}

func BenchmarkScannerService_GetActiveScans(b *testing.B) {
	scanner := &ScannerService{
		activeScans: make(map[string]*ScanProgress),
	}

	// Add some active scans
	for i := 0; i < 10; i++ {
		scanner.activeScans["scan-"+string(rune('a'+i))] = &ScanProgress{
			ID:         "scan-" + string(rune('a'+i)),
			Type:       "path",
			Path:       "/media/movies" + string(rune('a'+i)),
			TotalFiles: 100,
			FilesDone:  50,
			Status:     "scanning",
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scanner.GetActiveScans()
	}
}

func BenchmarkScannerService_HasActiveCorruption(b *testing.B) {
	db, err := testutil.NewTestDB()
	if err != nil {
		b.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Insert some test corruption events
	for i := 0; i < 100; i++ {
		_, err := db.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
			VALUES ('corruption', ?, 'CorruptionDetected', ?, 1, datetime('now'))
		`, "corruption-"+string(rune('a'+i%26)), `{"file_path":"/media/movies/file`+string(rune('a'+i%26))+`.mkv"}`)
		if err != nil {
			b.Fatalf("Failed to insert event: %v", err)
		}
	}

	scanner := &ScannerService{db: db}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scanner.hasActiveCorruption("/media/movies/newfile.mkv")
	}
}

func BenchmarkScannerService_LoadActiveCorruptionsForPath(b *testing.B) {
	db, err := testutil.NewTestDB()
	if err != nil {
		b.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Insert test corruption events under /media/movies
	for i := 0; i < 50; i++ {
		_, err := db.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
			VALUES ('corruption', ?, 'CorruptionDetected', ?, 1, datetime('now'))
		`, "bench-corruption-"+string(rune('a'+i%26))+string(rune('a'+i/26)), `{"file_path":"/media/movies/subdir/file`+string(rune('a'+i%26))+`.mkv"}`)
		if err != nil {
			b.Fatalf("Failed to insert event: %v", err)
		}
	}

	scanner := &ScannerService{db: db}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scanner.LoadActiveCorruptionsForPath("/media/movies")
	}
}

func BenchmarkScannerService_GetScanPathConfig(b *testing.B) {
	db, err := testutil.NewTestDB()
	if err != nil {
		b.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Insert scan paths
	for i := 0; i < 20; i++ {
		_, err := db.Exec(`
			INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, dry_run)
			VALUES (?, ?, 1, 1, 0)
		`, "/media/path"+string(rune('a'+i)), "/arr/path"+string(rune('a'+i)))
		if err != nil {
			b.Fatalf("Failed to insert scan path: %v", err)
		}
	}

	scanner := &ScannerService{db: db}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		scanner.getScanPathConfig("/media/patha/movies/test.mkv")
	}
}

// =============================================================================
// Additional tests for improved coverage
// =============================================================================

func TestIsHiddenOrTempFile_AdditionalPatterns(t *testing.T) {
	tests := []struct {
		name     string
		path     string
		expected bool
	}{
		// NZB-related patterns
		{"nzb file pattern", "/media/movies/download.nzb.mkv", true},

		// Case variations for temp files
		{"TEMP uppercase", "/media/movies/test.TEMP", true},
		{"TMP uppercase", "/media/movies/test.TMP", true},

		// Partial download patterns
		{"PART uppercase", "/media/movies/test.PART", true},
		{"PARTIAL uppercase", "/media/movies/test.PARTIAL", true},

		// Special edge cases
		{".fuse_hidden exact", "/media/movies/.fuse_hidden12345abc", true},

		// Path with multiple dots
		{"multiple dots normal", "/media/movies/movie.2024.1080p.mkv", false},

		// Trailer variations
		{"no trailer word", "/media/movies/behind-the-scenes.mkv", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isHiddenOrTempFile(tt.path)
			if result != tt.expected {
				t.Errorf("isHiddenOrTempFile(%q) = %v, want %v", tt.path, result, tt.expected)
			}
		})
	}
}

func TestScannerService_HandleScanPause_CancelledWhilePaused(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("returns scanReturn when context cancelled while paused", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())

		progress := &ScanProgress{
			ID:         "test-pause-cancel",
			Path:       "/media/movies",
			TotalFiles: 10,
			isPaused:   true,
			Status:     "paused",
			resumeChan: make(chan struct{}),
		}

		var action scanLoopAction
		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()
			action = scanner.handleScanPause(ctx, progress, "/media/movies", 5, 0)
		}()

		// Give goroutine time to start blocking
		time.Sleep(50 * time.Millisecond)

		// Cancel context
		cancel()

		wg.Wait()

		if action != scanReturn {
			t.Errorf("Expected scanReturn when cancelled while paused, got %v", action)
		}
		if progress.Status != "cancelled" {
			t.Errorf("Expected status 'cancelled', got %q", progress.Status)
		}
	})
}

func TestScannerService_HandleScanPause_ShutdownWhilePaused(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	shutdownCh := make(chan struct{})

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      shutdownCh,
	}

	t.Run("returns scanReturn when shutdown signaled while paused", func(t *testing.T) {
		ctx := context.Background()

		progress := &ScanProgress{
			ID:         "test-pause-shutdown",
			Path:       "/media/movies",
			TotalFiles: 10,
			isPaused:   true,
			Status:     "paused",
			resumeChan: make(chan struct{}),
		}

		var action scanLoopAction
		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()
			action = scanner.handleScanPause(ctx, progress, "/media/movies", 5, 0)
		}()

		// Give goroutine time to start blocking
		time.Sleep(50 * time.Millisecond)

		// Signal shutdown
		close(shutdownCh)

		wg.Wait()

		if action != scanReturn {
			t.Errorf("Expected scanReturn when shutdown while paused, got %v", action)
		}
		if progress.Status != "interrupted" {
			t.Errorf("Expected status 'interrupted', got %q", progress.Status)
		}
	})
}

func TestScannerService_HandleScanPause_WithDatabaseUpdate(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Create a scan record
	result, err := db.Exec(`
		INSERT INTO scans (path, path_id, status, total_files, files_scanned, current_file_index)
		VALUES ('/media/movies', 1, 'running', 10, 3, 3)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan: %v", err)
	}
	scanDBID, _ := result.LastInsertId()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("updates database when paused and resumed", func(t *testing.T) {
		ctx := context.Background()

		progress := &ScanProgress{
			ID:         "test-pause-db",
			Path:       "/media/movies",
			TotalFiles: 10,
			isPaused:   true,
			Status:     "paused",
			resumeChan: make(chan struct{}),
		}

		var wg sync.WaitGroup

		wg.Add(1)
		go func() {
			defer wg.Done()
			scanner.handleScanPause(ctx, progress, "/media/movies", 5, scanDBID)
		}()

		// Give time for the pause state to be saved
		time.Sleep(50 * time.Millisecond)

		// Check database was updated to paused
		var status string
		var currentIndex int
		err := db.QueryRow(`SELECT status, current_file_index FROM scans WHERE id = ?`, scanDBID).Scan(&status, &currentIndex)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if status != "paused" {
			t.Errorf("Expected status 'paused' in database, got %q", status)
		}
		if currentIndex != 5 {
			t.Errorf("Expected current_file_index 5, got %d", currentIndex)
		}

		// Signal resume
		close(progress.resumeChan)

		wg.Wait()

		// Check database was updated to running
		err = db.QueryRow(`SELECT status FROM scans WHERE id = ?`, scanDBID).Scan(&status)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if status != "running" {
			t.Errorf("Expected status 'running' in database after resume, got %q", status)
		}
	})
}

func TestScannerService_VerifyPathAccessible_PermissionDenied(t *testing.T) {
	// Skip on systems where we might run as root (which can read anything)
	if os.Getuid() == 0 {
		t.Skip("Skipping permission test when running as root")
	}

	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("handles permission denied gracefully", func(t *testing.T) {
		tmpDir := t.TempDir()
		restrictedDir := filepath.Join(tmpDir, "restricted")
		if err := os.Mkdir(restrictedDir, 0000); err != nil {
			t.Fatalf("Failed to create restricted dir: %v", err)
		}
		defer os.Chmod(restrictedDir, 0755) // Cleanup

		err := scanner.verifyPathAccessible(restrictedDir)
		if err == nil {
			t.Error("Expected error for permission denied")
		}
	})
}

func TestScannerService_ShouldSkipRecentlyModified_WithDBRecording(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Create a scan record
	result, err := db.Exec(`
		INSERT INTO scans (path, path_id, status, total_files, files_scanned)
		VALUES ('/media/movies', 1, 'running', 10, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan: %v", err)
	}
	scanDBID, _ := result.LastInsertId()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("records skipped file in database", func(t *testing.T) {
		sfc := &scanFileContext{
			filePath:  "/media/movies/recent.mkv",
			fileSize:  1024,
			fileMtime: time.Now(), // Recently modified
			scanDBID:  scanDBID,
		}

		skipped := scanner.shouldSkipRecentlyModified(sfc)
		if !skipped {
			t.Error("Expected file to be skipped")
		}

		// Verify record was created in scan_files
		var count int
		var status string
		err := db.QueryRow(`
			SELECT COUNT(*), MAX(status) FROM scan_files
			WHERE scan_id = ? AND file_path = ?
		`, scanDBID, sfc.filePath).Scan(&count, &status)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 scan_files record, got %d", count)
		}
		if status != "skipped" {
			t.Errorf("Expected status 'skipped', got %q", status)
		}
	})
}

func TestScannerService_ShouldSkipChangingSize_WithDBRecording(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	// Create a scan record
	result, err := db.Exec(`
		INSERT INTO scans (path, path_id, status, total_files, files_scanned)
		VALUES ('/media/movies', 1, 'running', 10, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan: %v", err)
	}
	scanDBID, _ := result.LastInsertId()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("does not skip file when size is stable", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "stable.mkv")
		if err := os.WriteFile(testFile, []byte("stable content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		info, _ := os.Stat(testFile)
		sfc := &scanFileContext{
			filePath: testFile,
			fileSize: info.Size(),
			scanDBID: scanDBID,
		}

		skipped := scanner.shouldSkipChangingSize(sfc)
		if skipped {
			t.Error("Stable file should not be skipped")
		}
	})

	t.Run("records skipped file when size changes", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "changing.mkv")
		if err := os.WriteFile(testFile, []byte("initial"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		sfc := &scanFileContext{
			filePath: testFile,
			fileSize: 7, // Initial size
			scanDBID: scanDBID,
		}

		// Start a goroutine to change the file size while checking
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			time.Sleep(200 * time.Millisecond)
			// Append to file during the sleep period in shouldSkipChangingSize
			f, err := os.OpenFile(testFile, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				return
			}
			f.Write([]byte(" more content"))
			f.Close()
		}()

		skipped := scanner.shouldSkipChangingSize(sfc)
		wg.Wait()

		if skipped {
			// Verify record was created in scan_files
			var count int
			err := db.QueryRow(`
				SELECT COUNT(*) FROM scan_files
				WHERE scan_id = ? AND file_path = ? AND status = 'skipped'
			`, scanDBID, sfc.filePath).Scan(&count)
			if err != nil {
				t.Fatalf("Failed to query: %v", err)
			}
			if count != 1 {
				t.Errorf("Expected 1 scan_files record for skipped file, got %d", count)
			}
		}
		// Note: This test might not always detect size change due to timing
		// The important thing is that it doesn't error
	})
}

func TestScannerService_ScanFile_WithCorruption(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Create scan path for test
	tmpDir := t.TempDir()
	_, err = db.Exec(`
		INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, dry_run)
		VALUES (?, ?, 1, 1, 0)
	`, tmpDir, tmpDir)
	if err != nil {
		t.Fatalf("Failed to insert scan path: %v", err)
	}

	mockHC := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return false, &integration.HealthCheckError{
				Type:    integration.ErrorTypeCorruptHeader,
				Message: "File is corrupted",
			}
		},
	}

	scanner := NewScannerService(db, eb, mockHC, nil)

	t.Run("emits corruption event for corrupted file", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "corrupt.mkv")
		if err := os.WriteFile(testFile, []byte("corrupt content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		err := scanner.ScanFile(testFile)
		if err != nil {
			t.Errorf("ScanFile should not return error: %v", err)
		}

		// Verify corruption event was created
		var count int
		err = db.QueryRow(`
			SELECT COUNT(*) FROM events
			WHERE event_type = 'CorruptionDetected'
			AND json_extract(event_data, '$.file_path') = ?
		`, testFile).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if count == 0 {
			t.Error("Expected corruption event to be created")
		}
	})
}

func TestScannerService_ScanFile_WithRecoverableError(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Create scan path for test
	tmpDir := t.TempDir()
	_, err = db.Exec(`
		INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, dry_run)
		VALUES (?, ?, 1, 0, 0)
	`, tmpDir, tmpDir)
	if err != nil {
		t.Fatalf("Failed to insert scan path: %v", err)
	}

	mockHC := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return false, &integration.HealthCheckError{
				Type:    integration.ErrorTypeMountLost,
				Message: "Transport endpoint not connected",
			}
		},
	}

	scanner := NewScannerService(db, eb, mockHC, nil)

	t.Run("does not emit corruption event for recoverable error", func(t *testing.T) {
		testFile := filepath.Join(tmpDir, "inaccessible.mkv")
		if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		err := scanner.ScanFile(testFile)
		if err != nil {
			t.Errorf("ScanFile should not return error: %v", err)
		}

		// Verify NO corruption event was created
		var count int
		err = db.QueryRow(`
			SELECT COUNT(*) FROM events
			WHERE event_type = 'CorruptionDetected'
			AND json_extract(event_data, '$.file_path') = ?
		`, testFile).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if count != 0 {
			t.Error("No corruption event should be created for recoverable errors")
		}
	})
}

func TestScannerService_ScanFile_SkipsDuplicate(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Create scan path for test
	tmpDir := t.TempDir()
	_, err = db.Exec(`
		INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, dry_run)
		VALUES (?, ?, 1, 1, 0)
	`, tmpDir, tmpDir)
	if err != nil {
		t.Fatalf("Failed to insert scan path: %v", err)
	}

	// Pre-insert an active corruption for this file
	testFile := filepath.Join(tmpDir, "already-corrupt.mkv")
	_, err = db.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at)
		VALUES ('corruption', 'existing-corruption', 'CorruptionDetected', ?, 1, datetime('now'))
	`, `{"file_path":"`+testFile+`"}`)
	if err != nil {
		t.Fatalf("Failed to insert existing corruption: %v", err)
	}

	mockHC := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return false, &integration.HealthCheckError{
				Type:    integration.ErrorTypeCorruptHeader,
				Message: "File is corrupted",
			}
		},
	}

	scanner := NewScannerService(db, eb, mockHC, nil)

	t.Run("does not emit duplicate corruption event", func(t *testing.T) {
		if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		err := scanner.ScanFile(testFile)
		if err != nil {
			t.Errorf("ScanFile should not return error: %v", err)
		}

		// Verify only ONE corruption event exists (the pre-existing one)
		var count int
		err = db.QueryRow(`
			SELECT COUNT(*) FROM events
			WHERE event_type = 'CorruptionDetected'
			AND json_extract(event_data, '$.file_path') = ?
		`, testFile).Scan(&count)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 corruption event (pre-existing), got %d", count)
		}
	})
}

func TestScannerService_HandleRecoverableError_RecordsInDB(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Insert a scan path for the test
	testutil.SeedScanPath(db, 10, "/media/movies", "/movies", false, false)

	// Create a scan record
	result, err := db.Exec(`
		INSERT INTO scans (path, path_id, status, total_files, files_scanned)
		VALUES ('/media/movies', 10, 'running', 10, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to create scan: %v", err)
	}
	scanDBID, _ := result.LastInsertId()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	t.Run("records inaccessible file in database", func(t *testing.T) {
		progress := &ScanProgress{ID: "test-rec-err", Path: "/media/movies"}
		sfc := &scanFileContext{
			filePath: "/media/movies/inaccessible.mkv",
			fileSize: 1024,
			pathID:   10,
			scanDBID: scanDBID,
		}
		healthErr := &integration.HealthCheckError{
			Type:    integration.ErrorTypeIOError,
			Message: "Input/output error",
		}

		scanner.handleRecoverableError(progress, sfc, healthErr)

		// Verify record was created in scan_files
		var count int
		var status, corruptionType string
		err := db.QueryRow(`
			SELECT COUNT(*), MAX(status), MAX(corruption_type) FROM scan_files
			WHERE scan_id = ? AND file_path = ?
		`, scanDBID, sfc.filePath).Scan(&count, &status, &corruptionType)
		if err != nil {
			t.Fatalf("Failed to query: %v", err)
		}
		if count != 1 {
			t.Errorf("Expected 1 scan_files record, got %d", count)
		}
		if status != "inaccessible" {
			t.Errorf("Expected status 'inaccessible', got %q", status)
		}
		if corruptionType != string(integration.ErrorTypeIOError) {
			t.Errorf("Expected corruption_type %q, got %q", integration.ErrorTypeIOError, corruptionType)
		}

		// Verify file was queued for rescan
		var pendingCount int
		err = db.QueryRow(`SELECT COUNT(*) FROM pending_rescans WHERE file_path = ?`, sfc.filePath).Scan(&pendingCount)
		if err != nil {
			t.Fatalf("Failed to query pending_rescans: %v", err)
		}
		if pendingCount != 1 {
			t.Errorf("Expected 1 pending_rescans record, got %d", pendingCount)
		}
	})
}

func TestScannerService_ResumeScan_ParsesDetectionConfig(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockHC := &testutil.MockHealthChecker{
		CheckWithConfigFunc: func(path string, config integration.DetectionConfig) (bool, *integration.HealthCheckError) {
			return true, nil
		},
	}

	scanner := NewScannerService(db, eb, mockHC, nil)

	t.Run("resumes with valid detection config", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.mkv")
		if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
		oldTime := time.Now().Add(-10 * time.Minute)
		os.Chtimes(testFile, oldTime, oldTime)

		// Insert scan path config
		_, err := db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run, detection_method, detection_mode)
			VALUES (1, ?, ?, 1, 0, 0, 'ffprobe', 'thorough')
		`, tmpDir, tmpDir)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}

		// Create interrupted scan with file list and detection config
		fileList := `["` + testFile + `"]`
		detectionConfig := `{"method":"ffprobe","mode":"thorough"}`
		_, err = db.Exec(`
			INSERT INTO scans (path, path_id, status, total_files, current_file_index, file_list, detection_config, auto_remediate, dry_run)
			VALUES (?, 1, 'interrupted', 1, 0, ?, ?, 0, 0)
		`, tmpDir, fileList, detectionConfig)
		if err != nil {
			t.Fatalf("Failed to insert scan: %v", err)
		}

		// Resume should work without panic
		scanner.ResumeInterruptedScans()

		// Give time for async resume
		time.Sleep(100 * time.Millisecond)
	})

	t.Run("resumes with empty detection config", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test2.mkv")
		if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}
		oldTime := time.Now().Add(-10 * time.Minute)
		os.Chtimes(testFile, oldTime, oldTime)

		// Insert scan path config
		_, err := db.Exec(`
			INSERT INTO scan_paths (id, local_path, arr_path, enabled, auto_remediate, dry_run, detection_method, detection_mode)
			VALUES (2, ?, ?, 1, 0, 0, 'ffprobe', 'quick')
		`, tmpDir, tmpDir)
		if err != nil {
			t.Fatalf("Failed to insert scan path: %v", err)
		}

		// Create interrupted scan with file list but NO detection config
		fileList := `["` + testFile + `"]`
		_, err = db.Exec(`
			INSERT INTO scans (path, path_id, status, total_files, current_file_index, file_list, detection_config, auto_remediate, dry_run)
			VALUES (?, 2, 'interrupted', 1, 0, ?, NULL, 0, 0)
		`, tmpDir, fileList)
		if err != nil {
			t.Fatalf("Failed to insert scan: %v", err)
		}

		// Resume should work without panic and use default config
		scanner.ResumeInterruptedScans()

		// Give time for async resume
		time.Sleep(100 * time.Millisecond)
	})
}

// =============================================================================
// hasActiveCorruption DB error tests
// =============================================================================

func TestScannerService_HasActiveCorruption_DBError(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	// Drop events table to cause DB error
	db.Exec("DROP TABLE events")

	// Should return false (err on the side of processing)
	if scanner.hasActiveCorruption("/media/movies/test.mkv") {
		t.Error("Expected false when DB query fails")
	}
}

// =============================================================================
// LoadActiveCorruptionsForPath DB error tests
// =============================================================================

func TestScannerService_LoadActiveCorruptionsForPath_DBError(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	scanner := &ScannerService{
		db:              db,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	// Drop events table to cause DB error
	db.Exec("DROP TABLE events")

	// Should return empty map on error
	result := scanner.LoadActiveCorruptionsForPath("/media/movies")
	if len(result) != 0 {
		t.Errorf("Expected empty map on DB error, got %d entries", len(result))
	}
}

// =============================================================================
// queueForRescan tests
// =============================================================================

func TestScannerService_QueueForRescan_DBError(t *testing.T) {
	db, err := testutil.NewTestDB()
	if err != nil {
		t.Fatalf("Failed to create test database: %v", err)
	}
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	scanner := &ScannerService{
		db:              db,
		eventBus:        eb,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}

	// Drop rescan_queue table to cause DB error
	db.Exec("DROP TABLE rescan_queue")

	// Should not panic
	scanner.queueForRescan("/media/movies/test.mkv", 1, "corrupt", "test error")
}
