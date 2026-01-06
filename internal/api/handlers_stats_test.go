package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "modernc.org/sqlite"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/services"
)

// mockHealthChecker and mockPathMapper are defined in handlers_health_test.go

// setupStatsTestDB creates a test database with the full schema needed for stats
func setupStatsTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "healarr-stats-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to open database: %v", err)
	}

	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to enable foreign keys: %v", err)
	}

	// Create full schema
	schema := `
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

		CREATE TABLE scans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path_id INTEGER,
			path TEXT NOT NULL,
			status TEXT NOT NULL DEFAULT 'pending',
			started_at TIMESTAMP,
			completed_at TIMESTAMP,
			files_scanned INTEGER DEFAULT 0,
			corruptions_found INTEGER DEFAULT 0
		);

		CREATE TABLE scan_paths (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL UNIQUE,
			enabled BOOLEAN DEFAULT 1
		);

		CREATE VIEW corruption_status AS
		SELECT
			aggregate_id as corruption_id,
			(SELECT event_type FROM events e2
			 WHERE e2.aggregate_id = e.aggregate_id
			 ORDER BY id DESC LIMIT 1) as current_state,
			(SELECT COUNT(*) FROM events e3
			 WHERE e3.aggregate_id = e.aggregate_id
			 AND e3.event_type LIKE '%Failed') as retry_count,
			(SELECT json_extract(event_data, '$.file_path') FROM events e4
			 WHERE e4.aggregate_id = e.aggregate_id
			 AND e4.event_type = 'CorruptionDetected'
			 LIMIT 1) as file_path,
			(SELECT json_extract(event_data, '$.path_id') FROM events e7
			 WHERE e7.aggregate_id = e.aggregate_id
			 AND e7.event_type = 'CorruptionDetected'
			 LIMIT 1) as path_id,
			MIN(created_at) as detected_at,
			MAX(created_at) as last_updated_at
		FROM events e
		WHERE aggregate_type = 'corruption'
		GROUP BY aggregate_id;

		CREATE VIEW dashboard_stats AS
		SELECT
			COUNT(DISTINCT CASE
				WHEN current_state != 'VerificationSuccess'
				AND current_state != 'MaxRetriesReached'
				AND current_state != 'CorruptionIgnored'
				AND current_state != 'ImportBlocked'
				AND current_state != 'ManuallyRemoved'
				THEN corruption_id END) as active_corruptions,
			COUNT(DISTINCT CASE
				WHEN current_state = 'VerificationSuccess'
				THEN corruption_id END) as resolved_corruptions,
			COUNT(DISTINCT CASE
				WHEN current_state = 'MaxRetriesReached'
				THEN corruption_id END) as orphaned_corruptions,
			COUNT(DISTINCT CASE
				WHEN (current_state LIKE '%Started'
				OR current_state LIKE '%Queued'
				OR current_state LIKE '%Progress'
				OR current_state = 'SearchCompleted'
				OR current_state = 'DeletionCompleted'
				OR current_state = 'FileDetected')
				AND current_state != 'CorruptionIgnored'
				THEN corruption_id END) as in_progress,
			COUNT(DISTINCT CASE
				WHEN current_state = 'ImportBlocked'
				OR current_state = 'ManuallyRemoved'
				THEN corruption_id END) as manual_intervention_required
		FROM corruption_status
		WHERE current_state != 'CorruptionIgnored';
	`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to create schema: %v", err)
	}

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	return db, cleanup
}

func seedStatsEvent(t *testing.T, db *sql.DB, aggregateID string, eventType domain.EventType, eventData map[string]interface{}, createdAt time.Time) {
	t.Helper()

	dataJSON, err := json.Marshal(eventData)
	if err != nil {
		t.Fatalf("Failed to marshal event data: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, created_at)
		VALUES (?, ?, ?, ?, ?)
	`, "corruption", aggregateID, eventType, dataJSON, createdAt.Format("2006-01-02 15:04:05"))
	if err != nil {
		t.Fatalf("Failed to seed event: %v", err)
	}
}

// createStatsTestServer creates a minimal RESTServer for stats testing
func createStatsTestServer(t *testing.T, db *sql.DB, eb *eventbus.EventBus) *RESTServer {
	t.Helper()

	hc := &mockHealthChecker{healthy: true}
	pm := &mockPathMapper{}
	scanner := services.NewScannerService(db, eb, hc, pm)

	gin.SetMode(gin.TestMode)
	r := gin.New()

	return &RESTServer{
		router:     r,
		db:         db,
		eventBus:   eb,
		scanner:    scanner,
		pathMapper: pm,
		metrics:    getGlobalMetricsService(eb),
		hub:        NewWebSocketHub(eb),
		startTime:  time.Now(),
	}
}

func TestGetDashboardStats_EmptyDB(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/dashboard", server.getDashboardStats)

	req, _ := http.NewRequest("GET", "/stats/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var stats map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// With empty DB, most stats should be 0 but success_rate should be 100 (no failures)
	if stats["total_corruptions"].(float64) != 0 {
		t.Errorf("total_corruptions = %v, want 0", stats["total_corruptions"])
	}
	if stats["success_rate"].(float64) != 100 {
		t.Errorf("success_rate = %v, want 100 (empty DB)", stats["success_rate"])
	}
}

func TestGetDashboardStats_WithCorruptions(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed various corruption states
	// 1. Active corruption (detected, not resolved)
	seedStatsEvent(t, db, "corruption-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path":       "/test/file1.mkv",
		"corruption_type": "TruncatedFile",
	}, now)

	// 2. Resolved corruption
	seedStatsEvent(t, db, "corruption-2", domain.CorruptionDetected, map[string]interface{}{
		"file_path":       "/test/file2.mkv",
		"corruption_type": "InvalidContainer",
	}, now)
	seedStatsEvent(t, db, "corruption-2", domain.VerificationSuccess, map[string]interface{}{
		"file_path": "/test/file2.mkv",
	}, now)

	// 3. Orphaned corruption (max retries reached)
	seedStatsEvent(t, db, "corruption-3", domain.CorruptionDetected, map[string]interface{}{
		"file_path":       "/test/file3.mkv",
		"corruption_type": "CorruptedAudio",
	}, now)
	seedStatsEvent(t, db, "corruption-3", domain.MaxRetriesReached, map[string]interface{}{
		"file_path": "/test/file3.mkv",
	}, now)

	// 4. In progress corruption
	seedStatsEvent(t, db, "corruption-4", domain.CorruptionDetected, map[string]interface{}{
		"file_path":       "/test/file4.mkv",
		"corruption_type": "TruncatedFile",
	}, now)
	seedStatsEvent(t, db, "corruption-4", domain.SearchStarted, map[string]interface{}{
		"file_path": "/test/file4.mkv",
	}, now)

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/dashboard", server.getDashboardStats)

	req, _ := http.NewRequest("GET", "/stats/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var stats map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Check various stats
	if stats["resolved_corruptions"].(float64) != 1 {
		t.Errorf("resolved_corruptions = %v, want 1", stats["resolved_corruptions"])
	}
	if stats["orphaned_corruptions"].(float64) != 1 {
		t.Errorf("orphaned_corruptions = %v, want 1", stats["orphaned_corruptions"])
	}
	if stats["in_progress_corruptions"].(float64) != 1 {
		t.Errorf("in_progress_corruptions = %v, want 1", stats["in_progress_corruptions"])
	}
	// Success rate = resolved / (resolved + orphaned) = 1 / 2 = 50%
	if stats["success_rate"].(float64) != 50 {
		t.Errorf("success_rate = %v, want 50", stats["success_rate"])
	}
}

func TestGetDashboardStats_WithScans(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Use date('now') from SQLite to match what the query uses
	// This avoids timezone mismatches between Go and SQLite
	_, err := db.Exec(`
		INSERT INTO scans (path, status, started_at, files_scanned)
		VALUES (?, 'running', date('now') || ' 12:00:00', 100)
	`, "/test/path1")
	if err != nil {
		t.Fatalf("Failed to insert scan: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO scans (path, status, started_at, completed_at, files_scanned)
		VALUES (?, 'completed', date('now') || ' 12:00:00', date('now') || ' 12:30:00', 200)
	`, "/test/path2")
	if err != nil {
		t.Fatalf("Failed to insert scan: %v", err)
	}

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/dashboard", server.getDashboardStats)

	req, _ := http.NewRequest("GET", "/stats/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var stats map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if stats["active_scans"].(float64) != 1 {
		t.Errorf("active_scans = %v, want 1", stats["active_scans"])
	}
	if stats["total_scans"].(float64) != 2 {
		t.Errorf("total_scans = %v, want 2", stats["total_scans"])
	}
	if stats["files_scanned_today"].(float64) != 300 {
		t.Errorf("files_scanned_today = %v, want 300", stats["files_scanned_today"])
	}
}

func TestGetDashboardStats_IgnoredCorruptions(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed an ignored corruption
	seedStatsEvent(t, db, "corruption-ignored", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/ignored.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-ignored", domain.CorruptionIgnored, map[string]interface{}{
		"file_path": "/test/ignored.mkv",
		"reason":    "User requested",
	}, now)

	// Seed a normal active corruption
	seedStatsEvent(t, db, "corruption-active", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/active.mkv",
	}, now)

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/dashboard", server.getDashboardStats)

	req, _ := http.NewRequest("GET", "/stats/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var stats map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &stats)

	// Ignored corruption should not be in total
	if stats["ignored_corruptions"].(float64) != 1 {
		t.Errorf("ignored_corruptions = %v, want 1", stats["ignored_corruptions"])
	}
	// Active should only count the non-ignored one
	if stats["pending_corruptions"].(float64) != 1 {
		t.Errorf("pending_corruptions = %v, want 1", stats["pending_corruptions"])
	}
}

func TestGetStatsHistory_EmptyDB(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/history", server.getStatsHistory)

	req, _ := http.NewRequest("GET", "/stats/history", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var stats []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(stats) != 0 {
		t.Errorf("Expected empty history, got %d entries", len(stats))
	}
}

func TestGetStatsHistory_WithData(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed events for today
	seedStatsEvent(t, db, "corruption-today-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/file1.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-today-2", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/file2.mkv",
	}, now)

	// Seed event for yesterday
	yesterday := now.Add(-24 * time.Hour)
	seedStatsEvent(t, db, "corruption-yesterday", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/file3.mkv",
	}, yesterday)

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/history", server.getStatsHistory)

	req, _ := http.NewRequest("GET", "/stats/history", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var stats []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(stats) != 2 {
		t.Errorf("Expected 2 history entries (2 different days), got %d", len(stats))
	}

	// Check that dates and counts are present
	for _, entry := range stats {
		if _, ok := entry["date"]; !ok {
			t.Error("History entry missing 'date' field")
		}
		if _, ok := entry["count"]; !ok {
			t.Error("History entry missing 'count' field")
		}
	}
}

func TestGetStatsTypes_EmptyDB(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/types", server.getStatsTypes)

	req, _ := http.NewRequest("GET", "/stats/types", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var stats []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(stats) != 0 {
		t.Errorf("Expected empty types list, got %d entries", len(stats))
	}
}

func TestGetStatsTypes_WithData(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed events with different corruption types
	seedStatsEvent(t, db, "corruption-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path":       "/test/file1.mkv",
		"corruption_type": "TruncatedFile",
	}, now)
	seedStatsEvent(t, db, "corruption-2", domain.CorruptionDetected, map[string]interface{}{
		"file_path":       "/test/file2.mkv",
		"corruption_type": "TruncatedFile",
	}, now)
	seedStatsEvent(t, db, "corruption-3", domain.CorruptionDetected, map[string]interface{}{
		"file_path":       "/test/file3.mkv",
		"corruption_type": "InvalidContainer",
	}, now)

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/types", server.getStatsTypes)

	req, _ := http.NewRequest("GET", "/stats/types", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var stats []map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if len(stats) != 2 {
		t.Errorf("Expected 2 corruption types, got %d", len(stats))
	}

	// Verify type names and counts
	typeMap := make(map[string]float64)
	for _, entry := range stats {
		typeName := entry["type"].(string)
		count := entry["count"].(float64)
		typeMap[typeName] = count
	}

	if typeMap["TruncatedFile"] != 2 {
		t.Errorf("TruncatedFile count = %v, want 2", typeMap["TruncatedFile"])
	}
	if typeMap["InvalidContainer"] != 1 {
		t.Errorf("InvalidContainer count = %v, want 1", typeMap["InvalidContainer"])
	}
}

func TestGetStatsTypes_UnknownType(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed event without corruption_type field
	seedStatsEvent(t, db, "corruption-unknown", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/file.mkv",
		// No corruption_type
	}, now)

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/types", server.getStatsTypes)

	req, _ := http.NewRequest("GET", "/stats/types", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var stats []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &stats)

	if len(stats) != 1 {
		t.Errorf("Expected 1 entry, got %d", len(stats))
	}

	// Should use "Unknown" as fallback
	if stats[0]["type"].(string) != "Unknown" {
		t.Errorf("Expected type 'Unknown', got %v", stats[0]["type"])
	}
}

func TestGetDashboardStats_ManualIntervention(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed ImportBlocked corruption
	seedStatsEvent(t, db, "corruption-blocked", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/blocked.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-blocked", domain.ImportBlocked, map[string]interface{}{
		"file_path": "/test/blocked.mkv",
		"reason":    "Import blocked by arr",
	}, now)

	// Seed ManuallyRemoved corruption
	seedStatsEvent(t, db, "corruption-removed", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/removed.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-removed", domain.ManuallyRemoved, map[string]interface{}{
		"file_path": "/test/removed.mkv",
	}, now)

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/dashboard", server.getDashboardStats)

	req, _ := http.NewRequest("GET", "/stats/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var stats map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &stats)

	if stats["manual_intervention_corruptions"].(float64) != 2 {
		t.Errorf("manual_intervention_corruptions = %v, want 2", stats["manual_intervention_corruptions"])
	}
}

func TestGetDashboardStats_InProgressOnly(t *testing.T) {
	// Test the branch where we have in-progress corruptions but no resolved/orphaned
	// This should result in success_rate = 0 (can't calculate yet)
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed only in-progress corruptions (no resolved, no orphaned)
	seedStatsEvent(t, db, "corruption-in-progress-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/file1.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-in-progress-1", domain.SearchStarted, map[string]interface{}{
		"file_path": "/test/file1.mkv",
	}, now)

	seedStatsEvent(t, db, "corruption-in-progress-2", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/file2.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-in-progress-2", domain.RemediationQueued, map[string]interface{}{
		"file_path": "/test/file2.mkv",
	}, now)

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/dashboard", server.getDashboardStats)

	req, _ := http.NewRequest("GET", "/stats/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var stats map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &stats)

	// With only in-progress, success_rate should be 0 (can't calculate)
	if stats["success_rate"].(float64) != 0 {
		t.Errorf("success_rate = %v, want 0 (in-progress only)", stats["success_rate"])
	}
	if stats["in_progress_corruptions"].(float64) != 2 {
		t.Errorf("in_progress_corruptions = %v, want 2", stats["in_progress_corruptions"])
	}
	if stats["resolved_corruptions"].(float64) != 0 {
		t.Errorf("resolved_corruptions = %v, want 0", stats["resolved_corruptions"])
	}
}

func TestGetDashboardStats_FailedCorruptions(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed corruptions with *Failed states (not MaxRetriesReached, not CorruptionIgnored)
	seedStatsEvent(t, db, "corruption-search-failed", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/file1.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-search-failed", domain.SearchFailed, map[string]interface{}{
		"file_path": "/test/file1.mkv",
		"error":     "No results found",
	}, now)

	seedStatsEvent(t, db, "corruption-download-failed", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/file2.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-download-failed", domain.DownloadFailed, map[string]interface{}{
		"file_path": "/test/file2.mkv",
		"error":     "Download timeout",
	}, now)

	// MaxRetriesReached should NOT count as "failed"
	seedStatsEvent(t, db, "corruption-maxretries", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/file3.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-maxretries", domain.MaxRetriesReached, map[string]interface{}{
		"file_path": "/test/file3.mkv",
	}, now)

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/dashboard", server.getDashboardStats)

	req, _ := http.NewRequest("GET", "/stats/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var stats map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &stats)

	// Should count only *Failed states (2), not MaxRetriesReached
	if stats["failed_corruptions"].(float64) != 2 {
		t.Errorf("failed_corruptions = %v, want 2", stats["failed_corruptions"])
	}
	// MaxRetriesReached counts as orphaned
	if stats["orphaned_corruptions"].(float64) != 1 {
		t.Errorf("orphaned_corruptions = %v, want 1", stats["orphaned_corruptions"])
	}
}

func TestGetDashboardStats_CorruptionsToday(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Use UTC to match SQLite's date('now') behavior
	now := time.Now().UTC()
	yesterday := now.Add(-24 * time.Hour)

	// Seed corruptions for today
	seedStatsEvent(t, db, "corruption-today-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/today1.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-today-2", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/today2.mkv",
	}, now)

	// Seed corruption for yesterday (should not count)
	seedStatsEvent(t, db, "corruption-yesterday", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/yesterday.mkv",
	}, yesterday)

	// Seed ignored corruption for today (should not count)
	seedStatsEvent(t, db, "corruption-today-ignored", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/ignored.mkv",
	}, now)
	seedStatsEvent(t, db, "corruption-today-ignored", domain.CorruptionIgnored, map[string]interface{}{
		"file_path": "/test/ignored.mkv",
		"reason":    "User requested",
	}, now)

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/dashboard", server.getDashboardStats)

	req, _ := http.NewRequest("GET", "/stats/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var stats map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &stats)

	// Should count 2 non-ignored corruptions from today
	if stats["corruptions_today"].(float64) != 2 {
		t.Errorf("corruptions_today = %v, want 2", stats["corruptions_today"])
	}
}

func TestGetDashboardStats_FilesScannedWeek(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	// Insert scans within the last 7 days
	_, err := db.Exec(`
		INSERT INTO scans (path, status, started_at, files_scanned)
		VALUES (?, 'completed', date('now', '-1 day') || ' 12:00:00', 50)
	`, "/test/path1")
	if err != nil {
		t.Fatalf("Failed to insert scan: %v", err)
	}

	_, err = db.Exec(`
		INSERT INTO scans (path, status, started_at, files_scanned)
		VALUES (?, 'completed', date('now', '-3 days') || ' 12:00:00', 75)
	`, "/test/path2")
	if err != nil {
		t.Fatalf("Failed to insert scan: %v", err)
	}

	// Insert scan older than 7 days (should not count)
	_, err = db.Exec(`
		INSERT INTO scans (path, status, started_at, files_scanned)
		VALUES (?, 'completed', date('now', '-10 days') || ' 12:00:00', 1000)
	`, "/test/old")
	if err != nil {
		t.Fatalf("Failed to insert scan: %v", err)
	}

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/dashboard", server.getDashboardStats)

	req, _ := http.NewRequest("GET", "/stats/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var stats map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &stats)

	// Should count 50 + 75 = 125 from last 7 days
	if stats["files_scanned_week"].(float64) != 125 {
		t.Errorf("files_scanned_week = %v, want 125", stats["files_scanned_week"])
	}
}

func TestGetStatsHistory_DBError(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	// Close DB to force error
	db.Close()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/history", server.getStatsHistory)

	req, _ := http.NewRequest("GET", "/stats/history", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}
}

func TestGetStatsTypes_DBError(t *testing.T) {
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	// Close DB to force error
	db.Close()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/types", server.getStatsTypes)

	req, _ := http.NewRequest("GET", "/stats/types", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}
}

func TestGetDashboardStats_DBError_WithWarnings(t *testing.T) {
	// Test that getDashboardStats returns partial results with warnings when some queries fail
	db, cleanup := setupStatsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createStatsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	// Drop the corruption_status view to force errors in the corruption stats query
	_, err := db.Exec("DROP VIEW corruption_status")
	if err != nil {
		t.Fatalf("Failed to drop view: %v", err)
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/stats/dashboard", server.getDashboardStats)

	req, _ := http.NewRequest("GET", "/stats/dashboard", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should still return 200 with partial data and warnings
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var stats map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &stats); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Should have warnings about failed queries
	warnings, ok := stats["warnings"].([]interface{})
	if !ok || len(warnings) == 0 {
		t.Error("Expected warnings in response for partial query failure")
	}
}
