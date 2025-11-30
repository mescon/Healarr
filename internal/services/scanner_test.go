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
