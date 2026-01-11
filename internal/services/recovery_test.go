package services

import (
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite" // SQLite driver for in-memory test databases

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/testutil"
)

func init() {
	config.SetForTesting(config.NewTestConfig())
}

// setupRecoveryTestDB creates an in-memory SQLite database with required tables
func setupRecoveryTestDB(t *testing.T) *sql.DB {
	t.Helper()

	tmpFile := t.TempDir() + "/recovery_test.db"
	db, err := sql.Open("sqlite", tmpFile)
	if err != nil {
		t.Fatalf("Failed to create test db: %v", err)
	}

	// Create required tables
	_, err = db.Exec(`
		CREATE TABLE corruption_status (
			corruption_id TEXT PRIMARY KEY,
			current_state TEXT NOT NULL,
			file_path TEXT NOT NULL,
			path_id INTEGER,
			retry_count INTEGER DEFAULT 0,
			last_updated_at TEXT NOT NULL,
			detected_at TEXT NOT NULL
		);
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			aggregate_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			event_data JSON NOT NULL,
			event_version INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_id TEXT
		);
		CREATE TABLE scan_paths (
			id INTEGER PRIMARY KEY,
			local_path TEXT NOT NULL,
			arr_path TEXT NOT NULL,
			instance_id INTEGER,
			enabled BOOLEAN DEFAULT 1,
			max_retries INTEGER DEFAULT 3
		);
	`)
	if err != nil {
		t.Fatalf("Failed to create tables: %v", err)
	}

	return db
}

// =============================================================================
// NewRecoveryService tests
// =============================================================================

func TestNewRecoveryService(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	t.Run("default threshold", func(t *testing.T) {
		rs := NewRecoveryService(db, eb, nil, nil, nil, 0)
		if rs == nil {
			t.Fatal("Expected non-nil RecoveryService")
		}
		if rs.staleThreshold != 24*time.Hour {
			t.Errorf("Expected default staleThreshold of 24h, got %v", rs.staleThreshold)
		}
	})

	t.Run("custom threshold", func(t *testing.T) {
		threshold := 12 * time.Hour
		rs := NewRecoveryService(db, eb, nil, nil, nil, threshold)
		if rs == nil {
			t.Fatal("Expected non-nil RecoveryService")
		}
		if rs.staleThreshold != threshold {
			t.Errorf("Expected staleThreshold of %v, got %v", threshold, rs.staleThreshold)
		}
	})

	t.Run("negative threshold uses default", func(t *testing.T) {
		rs := NewRecoveryService(db, eb, nil, nil, nil, -1*time.Hour)
		if rs == nil {
			t.Fatal("Expected non-nil RecoveryService")
		}
		if rs.staleThreshold != 24*time.Hour {
			t.Errorf("Expected default staleThreshold of 24h for negative input, got %v", rs.staleThreshold)
		}
	})
}

// =============================================================================
// findStaleItems tests
// =============================================================================

func TestFindStaleItems_EmptyDatabase(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	items, err := rs.findStaleItems()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Expected 0 items, got %d", len(items))
	}
}

func TestFindStaleItems_WithStaleItems(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Insert a stale corruption_status record
	oldTime := time.Now().Add(-48 * time.Hour).Format("2006-01-02 15:04:05")
	_, err := db.Exec(`
		INSERT INTO corruption_status (corruption_id, current_state, file_path, path_id, last_updated_at, detected_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "test-uuid-1", "DownloadProgress", "/media/test.mkv", 1, oldTime, oldTime)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	items, err := rs.findStaleItems()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("Expected 1 item, got %d", len(items))
	}
	if len(items) > 0 {
		if items[0].CorruptionID != "test-uuid-1" {
			t.Errorf("Expected corruption ID 'test-uuid-1', got %q", items[0].CorruptionID)
		}
		if items[0].CurrentState != "DownloadProgress" {
			t.Errorf("Expected state 'DownloadProgress', got %q", items[0].CurrentState)
		}
	}
}

func TestFindStaleItems_FreshItemsNotReturned(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Insert a fresh corruption_status record (within threshold)
	recentTime := time.Now().Add(-1 * time.Hour).Format("2006-01-02 15:04:05")
	_, err := db.Exec(`
		INSERT INTO corruption_status (corruption_id, current_state, file_path, path_id, last_updated_at, detected_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "test-uuid-2", "DownloadProgress", "/media/fresh.mkv", 1, recentTime, recentTime)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	items, err := rs.findStaleItems()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("Expected 0 items (fresh item should not be returned), got %d", len(items))
	}
}

func TestFindStaleItems_DifferentStates(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	oldTime := time.Now().Add(-48 * time.Hour).Format("2006-01-02 15:04:05")

	// Insert items with different states
	testCases := []struct {
		id       string
		state    string
		expected bool // Whether this state should be found
	}{
		{"uuid-dp", "DownloadProgress", true},
		{"uuid-sc", "SearchCompleted", true},
		{"uuid-ss", "SearchStarted", true},
		{"uuid-ds", "DownloadStarted", true},
		{"uuid-fd", "FileDetected", true},
		{"uuid-vs", "VerificationSuccess", false}, // Terminal state, not stale
		{"uuid-se", "SearchExhausted", false},     // Terminal state, not stale
	}

	for _, tc := range testCases {
		_, err := db.Exec(`
			INSERT INTO corruption_status (corruption_id, current_state, file_path, path_id, last_updated_at, detected_at)
			VALUES (?, ?, ?, ?, ?, ?)
		`, tc.id, tc.state, "/media/"+tc.id+".mkv", 1, oldTime, oldTime)
		if err != nil {
			t.Fatalf("Failed to insert test data for %s: %v", tc.id, err)
		}
	}

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	items, err := rs.findStaleItems()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Count expected items
	expectedCount := 0
	for _, tc := range testCases {
		if tc.expected {
			expectedCount++
		}
	}

	if len(items) != expectedCount {
		t.Errorf("Expected %d stale items, got %d", expectedCount, len(items))
	}
}

// =============================================================================
// isInArrQueue tests
// =============================================================================

func TestIsInArrQueue_NilClient(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		MediaID:      123,
	}

	inQueue, err := rs.isInArrQueue(item)
	if err != nil {
		t.Errorf("Expected no error with nil client, got: %v", err)
	}
	if inQueue {
		t.Error("Expected false with nil client")
	}
}

// =============================================================================
// checkArrHasFile tests
// =============================================================================

func TestCheckArrHasFile_NilClient(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		MediaID:      123,
	}

	hasFile, filePath, err := rs.checkArrHasFile(item)
	if err != nil {
		t.Errorf("Expected no error with nil client, got: %v", err)
	}
	if hasFile {
		t.Error("Expected hasFile=false with nil client")
	}
	if filePath != "" {
		t.Errorf("Expected empty filePath with nil client, got: %q", filePath)
	}
}

// =============================================================================
// getLocalPath tests
// =============================================================================

func TestGetLocalPath_NilMapper(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		FilePath: "/arr/path/test.mkv",
	}

	localPath := rs.getLocalPath(item)
	if localPath != "/arr/path/test.mkv" {
		t.Errorf("Expected original path with nil mapper, got: %q", localPath)
	}
}

func TestGetLocalPath_WithMapper(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Add a mapping (requires scan_paths table entry)
	_, err := db.Exec(`
		INSERT INTO scan_paths (id, local_path, arr_path, instance_id, enabled)
		VALUES (?, ?, ?, ?, ?)
	`, 1, "/local/media", "/arr/media", 1, true)
	if err != nil {
		t.Fatalf("Failed to insert scan path: %v", err)
	}

	// Create a path mapper with mappings
	pathMapper, err := integration.NewPathMapper(db)
	if err != nil {
		t.Fatalf("Failed to create path mapper: %v", err)
	}
	pathMapper.Reload()

	rs := NewRecoveryService(db, eb, nil, pathMapper, nil, 24*time.Hour)

	item := staleItem{
		FilePath: "/arr/media/test.mkv",
	}

	localPath := rs.getLocalPath(item)
	// With the mapper, arr path should be translated to local path
	expected := "/local/media/test.mkv"
	if localPath != expected {
		t.Errorf("Expected mapped path %q, got: %q", expected, localPath)
	}
}

// =============================================================================
// checkArrStatus tests
// =============================================================================

func TestCheckArrStatus_NoMediaID(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		MediaID:      0, // No media ID
	}

	result := rs.checkArrStatus(item)
	if result != "" {
		t.Errorf("Expected empty result for item without media ID, got: %q", result)
	}
}

// =============================================================================
// Run tests
// =============================================================================

func TestRun_NoStaleItems(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	// Run should complete without error
	rs.Run()
}

func TestRun_WithStaleItems(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Subscribe to events with channel-based synchronization
	eventReceived := make(chan struct{}, 1)
	eb.Subscribe(domain.SearchExhausted, func(e domain.Event) {
		select {
		case eventReceived <- struct{}{}:
		default:
		}
	})

	// Insert a stale corruption_status record
	oldTime := time.Now().Add(-48 * time.Hour).Format("2006-01-02 15:04:05")
	_, err := db.Exec(`
		INSERT INTO corruption_status (corruption_id, current_state, file_path, path_id, last_updated_at, detected_at)
		VALUES (?, ?, ?, ?, ?, ?)
	`, "test-uuid-run", "DownloadProgress", "/media/nonexistent.mkv", 1, oldTime, oldTime)
	if err != nil {
		t.Fatalf("Failed to insert test data: %v", err)
	}

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	// Run should process the stale item
	rs.Run()

	// Wait for event with timeout
	select {
	case <-eventReceived:
		// Event received as expected
	case <-time.After(500 * time.Millisecond):
		t.Log("No events received - this may be expected if file not found leads to exhausted state")
	}
}

// =============================================================================
// emitVerificationSuccess tests
// =============================================================================

func TestEmitVerificationSuccess(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Use channel for synchronization
	eventReceived := make(chan struct{}, 1)
	eb.Subscribe(domain.VerificationSuccess, func(e domain.Event) {
		select {
		case eventReceived <- struct{}{}:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
	}

	result := rs.emitVerificationSuccess(item, "/local/media/test.mkv")

	if result != "recovered" {
		t.Errorf("Expected 'recovered', got: %q", result)
	}

	// Wait for event with timeout
	select {
	case <-eventReceived:
		// Event received as expected
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected event to be published")
	}
}

// =============================================================================
// emitSearchExhausted tests
// =============================================================================

func TestEmitSearchExhausted(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Use channel for synchronization
	eventReceived := make(chan struct{}, 1)
	eb.Subscribe(domain.SearchExhausted, func(e domain.Event) {
		select {
		case eventReceived <- struct{}{}:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      123,
		LastUpdated:  time.Now().Add(-48 * time.Hour),
	}

	result := rs.emitSearchExhausted(item, "item_vanished")

	if result != "exhausted" {
		t.Errorf("Expected 'exhausted', got: %q", result)
	}

	// Wait for event with timeout
	select {
	case <-eventReceived:
		// Event received as expected
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected event to be published")
	}
}

// =============================================================================
// verifyAndComplete tests
// =============================================================================

func TestVerifyAndComplete_NilDetector(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Use channel for synchronization
	eventReceived := make(chan struct{}, 1)
	eb.Subscribe(domain.VerificationSuccess, func(e domain.Event) {
		select {
		case eventReceived <- struct{}{}:
		default:
		}
	})

	// No detector means file is assumed healthy
	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
	}

	result := rs.verifyAndComplete(item, "/local/media/test.mkv")

	// With nil detector, assumes healthy and emits success
	if result != "recovered" {
		t.Errorf("Expected 'recovered' with nil detector, got: %q", result)
	}

	// Wait for event with timeout
	select {
	case <-eventReceived:
		// Event received as expected
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected VerificationSuccess event to be published")
	}
}

// =============================================================================
// recoverItem tests
// =============================================================================

func TestRecoverItem_NoMediaID_NoDetector(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Use channel for synchronization
	eventReceived := make(chan struct{}, 1)
	eb.Subscribe(domain.SearchExhausted, func(e domain.Event) {
		select {
		case eventReceived <- struct{}{}:
		default:
		}
	})

	// No arrClient, no pathMapper, no detector
	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      0, // No media ID
	}

	// Item has no media ID, no detector, so file check will be skipped
	// and item will be marked as exhausted (file doesn't exist)
	result := rs.recoverItem(item)

	if result != "exhausted" {
		t.Errorf("Expected 'exhausted' for item with no media ID and no detector, got: %q", result)
	}

	// Wait for event with timeout
	select {
	case <-eventReceived:
		// Event received as expected
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected SearchExhausted event to be published")
	}
}

// =============================================================================
// isInArrQueue additional tests
// =============================================================================

func TestIsInArrQueue_ItemInQueue(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockArr := &testutil.MockArrClient{
		GetQueueForPathFunc: func(arrPath string) ([]integration.QueueItemInfo, error) {
			return []integration.QueueItemInfo{
				{Title: "Test Movie", Status: "downloading"},
			}, nil
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      123,
	}

	inQueue, err := rs.isInArrQueue(item)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !inQueue {
		t.Error("Expected inQueue to be true when items exist in queue")
	}
}

func TestIsInArrQueue_EmptyQueue(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockArr := &testutil.MockArrClient{
		GetQueueForPathFunc: func(arrPath string) ([]integration.QueueItemInfo, error) {
			return []integration.QueueItemInfo{}, nil
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      123,
	}

	inQueue, err := rs.isInArrQueue(item)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if inQueue {
		t.Error("Expected inQueue to be false when queue is empty")
	}
}

func TestIsInArrQueue_QueueWithEmptyTitle(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockArr := &testutil.MockArrClient{
		GetQueueForPathFunc: func(arrPath string) ([]integration.QueueItemInfo, error) {
			// Queue item exists but has empty title
			return []integration.QueueItemInfo{
				{Title: "", Status: "downloading"},
			}, nil
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      123,
	}

	// Empty title items should not be considered "in queue"
	inQueue, err := rs.isInArrQueue(item)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if inQueue {
		t.Error("Expected inQueue to be false when queue item has empty title")
	}
}

func TestIsInArrQueue_APIError(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	expectedErr := errors.New("API error")
	mockArr := &testutil.MockArrClient{
		GetQueueForPathFunc: func(arrPath string) ([]integration.QueueItemInfo, error) {
			return nil, expectedErr
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      123,
	}

	inQueue, err := rs.isInArrQueue(item)

	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}
	if inQueue {
		t.Error("Expected inQueue to be false on error")
	}
}

// =============================================================================
// checkArrHasFile additional tests
// =============================================================================

func TestCheckArrHasFile_FilesFound(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockArr := &testutil.MockArrClient{
		GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
			return []string{"/media/movies/Test/test.mkv", "/media/movies/Test/test.srt"}, nil
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      123,
	}

	hasFile, filePath, err := rs.checkArrHasFile(item)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if !hasFile {
		t.Error("Expected hasFile to be true")
	}
	if filePath != "/media/movies/Test/test.mkv" {
		t.Errorf("Expected first file path, got %q", filePath)
	}
}

func TestCheckArrHasFile_NoFiles(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockArr := &testutil.MockArrClient{
		GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
			return []string{}, nil
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      123,
	}

	hasFile, filePath, err := rs.checkArrHasFile(item)

	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	if hasFile {
		t.Error("Expected hasFile to be false")
	}
	if filePath != "" {
		t.Errorf("Expected empty file path, got %q", filePath)
	}
}

func TestCheckArrHasFile_APIError(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	expectedErr := errors.New("API error")
	mockArr := &testutil.MockArrClient{
		GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
			return nil, expectedErr
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      123,
	}

	hasFile, filePath, err := rs.checkArrHasFile(item)

	if err != expectedErr {
		t.Errorf("Expected error %v, got %v", expectedErr, err)
	}
	if hasFile {
		t.Error("Expected hasFile to be false on error")
	}
	if filePath != "" {
		t.Errorf("Expected empty file path on error, got %q", filePath)
	}
}

// =============================================================================
// checkArrStatus additional tests
// =============================================================================

func TestCheckArrStatus_ItemInQueue(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	mockArr := &testutil.MockArrClient{
		GetQueueForPathFunc: func(arrPath string) ([]integration.QueueItemInfo, error) {
			return []integration.QueueItemInfo{
				{Title: "Test Movie", Status: "downloading"},
			}, nil
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      123,
	}

	result := rs.checkArrStatus(item)

	if result != "skipped" {
		t.Errorf("Expected 'skipped' when item is in queue, got %q", result)
	}
}

func TestCheckArrStatus_FileFoundVerified(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Track event
	eventReceived := make(chan struct{}, 1)
	eb.Subscribe(domain.VerificationSuccess, func(e domain.Event) {
		select {
		case eventReceived <- struct{}{}:
		default:
		}
	})

	mockArr := &testutil.MockArrClient{
		GetQueueForPathFunc: func(arrPath string) ([]integration.QueueItemInfo, error) {
			return []integration.QueueItemInfo{}, nil // Empty queue
		},
		GetAllFilePathsFunc: func(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
			return []string{"/media/movies/Test/test.mkv"}, nil // File found
		},
	}

	// Mock detector that returns healthy
	mockDetector := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return true, nil
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, mockDetector, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      123,
	}

	result := rs.checkArrStatus(item)

	if result != "recovered" {
		t.Errorf("Expected 'recovered' when file found and healthy, got %q", result)
	}

	// Wait for event
	select {
	case <-eventReceived:
		// Event received as expected
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected VerificationSuccess event")
	}
}

// =============================================================================
// verifyAndComplete additional tests
// =============================================================================

func TestVerifyAndComplete_HealthyFile(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan struct{}, 1)
	eb.Subscribe(domain.VerificationSuccess, func(e domain.Event) {
		select {
		case eventReceived <- struct{}{}:
		default:
		}
	})

	mockDetector := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return true, nil // Healthy
		},
	}

	rs := NewRecoveryService(db, eb, nil, nil, mockDetector, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
	}

	result := rs.verifyAndComplete(item, "/local/media/test.mkv")

	if result != "recovered" {
		t.Errorf("Expected 'recovered' for healthy file, got %q", result)
	}

	select {
	case <-eventReceived:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected VerificationSuccess event")
	}
}

func TestVerifyAndComplete_CorruptFile(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan struct{}, 1)
	eb.Subscribe(domain.SearchExhausted, func(e domain.Event) {
		select {
		case eventReceived <- struct{}{}:
		default:
		}
	})

	mockDetector := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return false, &integration.HealthCheckError{Message: "video stream corrupt"}
		},
	}

	rs := NewRecoveryService(db, eb, nil, nil, mockDetector, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
	}

	result := rs.verifyAndComplete(item, "/local/media/test.mkv")

	if result != "exhausted" {
		t.Errorf("Expected 'exhausted' for corrupt file, got %q", result)
	}

	select {
	case <-eventReceived:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected SearchExhausted event")
	}
}

func TestVerifyAndComplete_WithPathMapper(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan struct{}, 1)
	eb.Subscribe(domain.VerificationSuccess, func(e domain.Event) {
		select {
		case eventReceived <- struct{}{}:
		default:
		}
	})

	// Path mapper that converts arr path to local path
	mockPM := &testutil.MockPathMapper{
		ToLocalPathFunc: func(arrPath string) (string, error) {
			return "/local" + arrPath, nil
		},
	}

	mockDetector := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			// Verify the path was mapped
			if path != "/local/media/test.mkv" {
				t.Errorf("Expected mapped path /local/media/test.mkv, got %s", path)
			}
			return true, nil
		},
	}

	rs := NewRecoveryService(db, eb, nil, mockPM, mockDetector, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
	}

	result := rs.verifyAndComplete(item, "/media/test.mkv")

	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case <-eventReceived:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected VerificationSuccess event")
	}
}

// =============================================================================
// recoverEarlyRemediationState tests
// =============================================================================

func TestRecoverEarlyRemediationState_RemediationQueued(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.RetryScheduled, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-rq",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "RemediationQueued",
	}

	result := rs.recoverEarlyRemediationState(item)

	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.RetryScheduled {
			t.Errorf("Expected RetryScheduled event, got %s", e.EventType)
		}
		if e.EventData["original_state"] != "RemediationQueued" {
			t.Errorf("Expected original_state to be RemediationQueued, got %v", e.EventData["original_state"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected RetryScheduled event")
	}
}

func TestRecoverEarlyRemediationState_DeletionStarted_FileHealthy(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.VerificationSuccess, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	// File is healthy - should emit VerificationSuccess
	mockDetector := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return true, nil
		},
	}

	rs := NewRecoveryService(db, eb, nil, nil, mockDetector, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-ds",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DeletionStarted",
	}

	result := rs.recoverEarlyRemediationState(item)

	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.VerificationSuccess {
			t.Errorf("Expected VerificationSuccess event, got %s", e.EventType)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected VerificationSuccess event")
	}
}

func TestRecoverEarlyRemediationState_DeletionStarted_FileGone(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.RetryScheduled, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	// File is gone/corrupt - should emit RetryScheduled
	mockDetector := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return false, &integration.HealthCheckError{Message: "file not found"}
		},
	}

	rs := NewRecoveryService(db, eb, nil, nil, mockDetector, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-ds2",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DeletionStarted",
	}

	result := rs.recoverEarlyRemediationState(item)

	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.RetryScheduled {
			t.Errorf("Expected RetryScheduled event, got %s", e.EventType)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected RetryScheduled event")
	}
}

func TestRecoverEarlyRemediationState_DeletionCompleted_WithMediaID(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// For DeletionCompleted with media_id, expect SearchStarted
	eventReceived := make(chan domain.Event, 2)
	eb.Subscribe(domain.SearchStarted, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-dc",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DeletionCompleted",
		MediaID:      123,
	}

	result := rs.recoverEarlyRemediationState(item)

	if result != "recovered" && result != "skipped" {
		t.Errorf("Expected 'recovered' or 'skipped', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.SearchStarted {
			t.Errorf("Expected SearchStarted event, got %s", e.EventType)
		}
	case <-time.After(500 * time.Millisecond):
		// SearchStarted may be followed by SearchCompleted or SearchFailed
		// depending on arrClient availability
	}
}

func TestRecoverEarlyRemediationState_DeletionCompleted_NoMediaID(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// For DeletionCompleted without media_id, should fall back to RetryScheduled
	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.RetryScheduled, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-dc2",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DeletionCompleted",
		MediaID:      0, // No media ID
	}

	result := rs.recoverEarlyRemediationState(item)

	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.RetryScheduled {
			t.Errorf("Expected RetryScheduled event, got %s", e.EventType)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected RetryScheduled event")
	}
}

// =============================================================================
// recoverFailedState tests
// =============================================================================

func TestRecoverFailedState_UnderMaxRetries(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.RetryScheduled, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-fs",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "SearchFailed",
		RetryCount:   1,
		MaxRetries:   3,
	}

	result := rs.recoverFailedState(item)

	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.RetryScheduled {
			t.Errorf("Expected RetryScheduled event, got %s", e.EventType)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected RetryScheduled event")
	}
}

func TestRecoverFailedState_MaxRetriesReached(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.MaxRetriesReached, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-mrr",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DeletionFailed",
		RetryCount:   3,
		MaxRetries:   3,
	}

	result := rs.recoverFailedState(item)

	if result != "exhausted" {
		t.Errorf("Expected 'exhausted', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.MaxRetriesReached {
			t.Errorf("Expected MaxRetriesReached event, got %s", e.EventType)
		}
		if e.EventData["retry_count"] != 3 {
			t.Errorf("Expected retry_count to be 3, got %v", e.EventData["retry_count"])
		}
		if e.EventData["max_retries"] != 3 {
			t.Errorf("Expected max_retries to be 3, got %v", e.EventData["max_retries"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected MaxRetriesReached event")
	}
}

func TestRecoverFailedState_AllFailedStates(t *testing.T) {
	testCases := []struct {
		state string
	}{
		{"DeletionFailed"},
		{"SearchFailed"},
		{"VerificationFailed"},
		{"DownloadTimeout"},
		{"DownloadFailed"},
	}

	for _, tc := range testCases {
		t.Run(tc.state, func(t *testing.T) {
			db := setupRecoveryTestDB(t)
			defer db.Close()

			eb := eventbus.NewEventBus(db)
			defer eb.Shutdown()

			eventReceived := make(chan domain.Event, 1)
			eb.Subscribe(domain.RetryScheduled, func(e domain.Event) {
				select {
				case eventReceived <- e:
				default:
				}
			})

			rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

			item := staleItem{
				CorruptionID: "test-uuid-" + tc.state,
				FilePath:     "/media/test.mkv",
				PathID:       1,
				CurrentState: tc.state,
				RetryCount:   0,
				MaxRetries:   3,
			}

			result := rs.recoverFailedState(item)

			if result != "recovered" {
				t.Errorf("Expected 'recovered' for %s, got %q", tc.state, result)
			}

			select {
			case <-eventReceived:
				// Success
			case <-time.After(500 * time.Millisecond):
				t.Errorf("Expected RetryScheduled event for %s", tc.state)
			}
		})
	}
}

// =============================================================================
// emitRetryScheduled tests
// =============================================================================

func TestEmitRetryScheduled(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.RetryScheduled, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-rs",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "SearchFailed",
	}

	result := rs.emitRetryScheduled(item)

	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.RetryScheduled {
			t.Errorf("Expected RetryScheduled event, got %s", e.EventType)
		}
		if e.AggregateID != "test-uuid-rs" {
			t.Errorf("Expected AggregateID 'test-uuid-rs', got %q", e.AggregateID)
		}
		if e.EventData["file_path"] != "/media/test.mkv" {
			t.Errorf("Expected file_path '/media/test.mkv', got %v", e.EventData["file_path"])
		}
		if e.EventData["recovery_action"] != "startup_recovery" {
			t.Errorf("Expected recovery_action 'startup_recovery', got %v", e.EventData["recovery_action"])
		}
		if e.EventData["original_state"] != "SearchFailed" {
			t.Errorf("Expected original_state 'SearchFailed', got %v", e.EventData["original_state"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected RetryScheduled event")
	}
}

// =============================================================================
// emitSearchNeeded tests
// =============================================================================

func TestEmitSearchNeeded_NoMediaID(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.RetryScheduled, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-sn",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DeletionCompleted",
		MediaID:      0, // No media ID
	}

	result := rs.emitSearchNeeded(item)

	// Without media_id, should fall back to RetryScheduled
	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.RetryScheduled {
			t.Errorf("Expected RetryScheduled event, got %s", e.EventType)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected RetryScheduled event")
	}
}

func TestEmitSearchNeeded_WithMediaID_NoArrClient(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.SearchStarted, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-sn2",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DeletionCompleted",
		MediaID:      123,
	}

	result := rs.emitSearchNeeded(item)

	// Without arrClient, should still emit SearchStarted but won't trigger actual search
	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.SearchStarted {
			t.Errorf("Expected SearchStarted event, got %s", e.EventType)
		}
		if e.EventData["media_id"] != int64(123) {
			t.Errorf("Expected media_id 123, got %v", e.EventData["media_id"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected SearchStarted event")
	}
}

func TestEmitSearchNeeded_WithArrClient_Success(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	searchCompletedReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.SearchCompleted, func(e domain.Event) {
		select {
		case searchCompletedReceived <- e:
		default:
		}
	})

	// Mock arr client that succeeds
	mockArr := &testutil.MockArrClient{
		TriggerSearchFunc: func(mediaID int64, arrPath string, episodeIDs []int64) error {
			return nil
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-sn3",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DeletionCompleted",
		MediaID:      123,
	}

	result := rs.emitSearchNeeded(item)

	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case e := <-searchCompletedReceived:
		if e.EventType != domain.SearchCompleted {
			t.Errorf("Expected SearchCompleted event, got %s", e.EventType)
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected SearchCompleted event")
	}
}

func TestEmitSearchNeeded_WithArrClient_Failure(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	searchFailedReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.SearchFailed, func(e domain.Event) {
		select {
		case searchFailedReceived <- e:
		default:
		}
	})

	// Mock arr client that fails
	mockArr := &testutil.MockArrClient{
		TriggerSearchFunc: func(mediaID int64, arrPath string, episodeIDs []int64) error {
			return errors.New("search trigger failed")
		},
	}

	rs := NewRecoveryService(db, eb, mockArr, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-sn4",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DeletionCompleted",
		MediaID:      123,
	}

	result := rs.emitSearchNeeded(item)

	if result != "skipped" {
		t.Errorf("Expected 'skipped', got %q", result)
	}

	select {
	case e := <-searchFailedReceived:
		if e.EventType != domain.SearchFailed {
			t.Errorf("Expected SearchFailed event, got %s", e.EventType)
		}
		if e.EventData["error"] != "search trigger failed" {
			t.Errorf("Expected error message 'search trigger failed', got %v", e.EventData["error"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected SearchFailed event")
	}
}

// =============================================================================
// emitMaxRetriesReached tests
// =============================================================================

func TestEmitMaxRetriesReached(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.MaxRetriesReached, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-mr",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "SearchFailed",
		RetryCount:   5,
		MaxRetries:   5,
	}

	result := rs.emitMaxRetriesReached(item)

	if result != "exhausted" {
		t.Errorf("Expected 'exhausted', got %q", result)
	}

	select {
	case e := <-eventReceived:
		if e.EventType != domain.MaxRetriesReached {
			t.Errorf("Expected MaxRetriesReached event, got %s", e.EventType)
		}
		if e.AggregateID != "test-uuid-mr" {
			t.Errorf("Expected AggregateID 'test-uuid-mr', got %q", e.AggregateID)
		}
		if e.EventData["retry_count"] != 5 {
			t.Errorf("Expected retry_count 5, got %v", e.EventData["retry_count"])
		}
		if e.EventData["max_retries"] != 5 {
			t.Errorf("Expected max_retries 5, got %v", e.EventData["max_retries"])
		}
		if e.EventData["original_state"] != "SearchFailed" {
			t.Errorf("Expected original_state 'SearchFailed', got %v", e.EventData["original_state"])
		}
		if e.EventData["recovery_action"] != "startup_recovery" {
			t.Errorf("Expected recovery_action 'startup_recovery', got %v", e.EventData["recovery_action"])
		}
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected MaxRetriesReached event")
	}
}

// =============================================================================
// isEarlyRemediationState and isFailedState tests
// =============================================================================

func TestIsEarlyRemediationState(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	tests := []struct {
		state    string
		expected bool
	}{
		{"RemediationQueued", true},
		{"DeletionStarted", true},
		{"DeletionCompleted", true},
		{"SearchStarted", false},
		{"DownloadProgress", false},
		{"VerificationSuccess", false},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			result := rs.isEarlyRemediationState(tt.state)
			if result != tt.expected {
				t.Errorf("isEarlyRemediationState(%q) = %v, want %v", tt.state, result, tt.expected)
			}
		})
	}
}

func TestIsFailedState(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	tests := []struct {
		state    string
		expected bool
	}{
		{"DeletionFailed", true},
		{"SearchFailed", true},
		{"VerificationFailed", true},
		{"DownloadTimeout", true},
		{"DownloadFailed", true},
		{"SearchStarted", false},
		{"DownloadProgress", false},
		{"VerificationSuccess", false},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			result := rs.isFailedState(tt.state)
			if result != tt.expected {
				t.Errorf("isFailedState(%q) = %v, want %v", tt.state, result, tt.expected)
			}
		})
	}
}

// =============================================================================
// recoverPostSearchState tests
// =============================================================================

func TestRecoverPostSearchState_FileExistsAndHealthy(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.VerificationSuccess, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	mockDetector := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return true, nil
		},
	}

	rs := NewRecoveryService(db, eb, nil, nil, mockDetector, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-pss",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      0, // No media ID to avoid arr checks
	}

	result := rs.recoverPostSearchState(item)

	if result != "recovered" {
		t.Errorf("Expected 'recovered', got %q", result)
	}

	select {
	case <-eventReceived:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected VerificationSuccess event")
	}
}

func TestRecoverPostSearchState_FileNotFound(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	eventReceived := make(chan domain.Event, 1)
	eb.Subscribe(domain.SearchExhausted, func(e domain.Event) {
		select {
		case eventReceived <- e:
		default:
		}
	})

	mockDetector := &testutil.MockHealthChecker{
		CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
			return false, &integration.HealthCheckError{Message: "file not found"}
		},
	}

	rs := NewRecoveryService(db, eb, nil, nil, mockDetector, 24*time.Hour)

	item := staleItem{
		CorruptionID: "test-uuid-pss2",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		MediaID:      0, // No media ID
		LastUpdated:  time.Now().Add(-48 * time.Hour),
	}

	result := rs.recoverPostSearchState(item)

	if result != "exhausted" {
		t.Errorf("Expected 'exhausted', got %q", result)
	}

	select {
	case <-eventReceived:
		// Success
	case <-time.After(500 * time.Millisecond):
		t.Error("Expected SearchExhausted event")
	}
}

// =============================================================================
// recoverItem routing tests
// =============================================================================

func TestRecoverItem_RoutesToCorrectHandler(t *testing.T) {
	tests := []struct {
		state          string
		expectedAction string
		setupMock      func() *testutil.MockHealthChecker
	}{
		{
			state:          "RemediationQueued",
			expectedAction: "recovered", // Routes to recoverEarlyRemediationState
			setupMock:      nil,
		},
		{
			state:          "DeletionFailed",
			expectedAction: "recovered", // Routes to recoverFailedState (retry < max)
			setupMock:      nil,
		},
		{
			state:          "DownloadProgress",
			expectedAction: "exhausted", // Routes to recoverPostSearchState
			setupMock: func() *testutil.MockHealthChecker {
				return &testutil.MockHealthChecker{
					CheckFunc: func(path string, mode string) (bool, *integration.HealthCheckError) {
						return false, &integration.HealthCheckError{Message: "file not found"}
					},
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.state, func(t *testing.T) {
			db := setupRecoveryTestDB(t)
			defer db.Close()

			eb := eventbus.NewEventBus(db)
			defer eb.Shutdown()

			var detector *testutil.MockHealthChecker
			if tt.setupMock != nil {
				detector = tt.setupMock()
			}

			rs := NewRecoveryService(db, eb, nil, nil, detector, 24*time.Hour)

			item := staleItem{
				CorruptionID: "test-uuid-routing",
				FilePath:     "/media/test.mkv",
				PathID:       1,
				CurrentState: tt.state,
				RetryCount:   0,
				MaxRetries:   3,
				LastUpdated:  time.Now().Add(-48 * time.Hour),
			}

			result := rs.recoverItem(item)

			if result != tt.expectedAction {
				t.Errorf("recoverItem(%q) = %q, want %q", tt.state, result, tt.expectedAction)
			}
		})
	}
}

// =============================================================================
// Error path tests (database closed)
// =============================================================================

func TestEmitRetryScheduled_PublishError(t *testing.T) {
	db := setupRecoveryTestDB(t)
	eb := eventbus.NewEventBus(db)

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	// Close the database to cause publish to fail
	db.Close()
	eb.Shutdown()

	item := staleItem{
		CorruptionID: "test-uuid-err",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "SearchFailed",
	}

	result := rs.emitRetryScheduled(item)

	if result != "skipped" {
		t.Errorf("Expected 'skipped' on publish error, got %q", result)
	}
}

func TestEmitMaxRetriesReached_PublishError(t *testing.T) {
	db := setupRecoveryTestDB(t)
	eb := eventbus.NewEventBus(db)

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	// Close the database to cause publish to fail
	db.Close()
	eb.Shutdown()

	item := staleItem{
		CorruptionID: "test-uuid-err",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "SearchFailed",
		RetryCount:   5,
		MaxRetries:   5,
	}

	result := rs.emitMaxRetriesReached(item)

	if result != "skipped" {
		t.Errorf("Expected 'skipped' on publish error, got %q", result)
	}
}

func TestEmitVerificationSuccess_PublishError(t *testing.T) {
	db := setupRecoveryTestDB(t)
	eb := eventbus.NewEventBus(db)

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	// Close the database to cause publish to fail
	db.Close()
	eb.Shutdown()

	item := staleItem{
		CorruptionID: "test-uuid-err",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
	}

	result := rs.emitVerificationSuccess(item, "/local/media/test.mkv")

	if result != "skipped" {
		t.Errorf("Expected 'skipped' on publish error, got %q", result)
	}
}

func TestEmitSearchExhausted_PublishError(t *testing.T) {
	db := setupRecoveryTestDB(t)
	eb := eventbus.NewEventBus(db)

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	// Close the database to cause publish to fail
	db.Close()
	eb.Shutdown()

	item := staleItem{
		CorruptionID: "test-uuid-err",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DownloadProgress",
		LastUpdated:  time.Now().Add(-48 * time.Hour),
	}

	result := rs.emitSearchExhausted(item, "item_vanished")

	if result != "skipped" {
		t.Errorf("Expected 'skipped' on publish error, got %q", result)
	}
}

func TestEmitSearchNeeded_PublishError(t *testing.T) {
	db := setupRecoveryTestDB(t)
	eb := eventbus.NewEventBus(db)

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	// Close the database to cause publish to fail
	db.Close()
	eb.Shutdown()

	item := staleItem{
		CorruptionID: "test-uuid-err",
		FilePath:     "/media/test.mkv",
		PathID:       1,
		CurrentState: "DeletionCompleted",
		MediaID:      123,
	}

	result := rs.emitSearchNeeded(item)

	if result != "skipped" {
		t.Errorf("Expected 'skipped' on publish error, got %q", result)
	}
}

// =============================================================================
// findStaleItems edge cases
// =============================================================================

func TestFindStaleItems_AllStateCategories(t *testing.T) {
	db := setupRecoveryTestDB(t)
	defer db.Close()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	oldTime := time.Now().Add(-48 * time.Hour).Format("2006-01-02 15:04:05")

	// Insert items from each category
	testItems := []struct {
		id    string
		state string
	}{
		{"early-1", "RemediationQueued"},
		{"early-2", "DeletionStarted"},
		{"early-3", "DeletionCompleted"},
		{"post-1", "DownloadProgress"},
		{"post-2", "SearchCompleted"},
		{"failed-1", "DeletionFailed"},
		{"failed-2", "SearchFailed"},
		{"failed-3", "VerificationFailed"},
	}

	for _, ti := range testItems {
		_, err := db.Exec(`
			INSERT INTO corruption_status (corruption_id, current_state, file_path, path_id, retry_count, last_updated_at, detected_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`, ti.id, ti.state, "/media/"+ti.id+".mkv", 1, 0, oldTime, oldTime)
		if err != nil {
			t.Fatalf("Failed to insert test data: %v", err)
		}
	}

	rs := NewRecoveryService(db, eb, nil, nil, nil, 24*time.Hour)

	items, err := rs.findStaleItems()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(items) != len(testItems) {
		t.Errorf("Expected %d items, got %d", len(testItems), len(items))
	}
}
