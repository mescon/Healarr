package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/services"
	_ "modernc.org/sqlite"
)

// setupCorruptionsTestDB creates a test database with schema for corruption tests
func setupCorruptionsTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "healarr-corruptions-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to open database: %v", err)
	}

	// Configure SQLite for concurrent access in tests
	// WAL mode allows concurrent reads while writes are happening
	if _, err := db.Exec("PRAGMA journal_mode=WAL"); err != nil {
		db.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to set WAL mode: %v", err)
	}
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to enable foreign keys: %v", err)
	}
	// Allow multiple connections for concurrent EventBus and handler operations
	db.SetMaxOpenConns(5)

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
			(SELECT json_extract(event_data, '$.error') FROM events e5
			 WHERE e5.aggregate_id = e.aggregate_id
			 AND e5.event_type LIKE '%Failed'
			 ORDER BY id DESC LIMIT 1) as last_error,
			MIN(created_at) as detected_at,
			MAX(created_at) as last_updated_at,
			(SELECT json_extract(event_data, '$.corruption_type') FROM events e6
			 WHERE e6.aggregate_id = e.aggregate_id
			 AND e6.event_type = 'CorruptionDetected'
			 LIMIT 1) as corruption_type
		FROM events e
		WHERE aggregate_type = 'corruption'
		GROUP BY aggregate_id;
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

func seedCorruptionEvent(t *testing.T, db *sql.DB, aggregateID string, eventType domain.EventType, eventData map[string]interface{}, createdAt time.Time) {
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

// createCorruptionsTestServer creates a minimal RESTServer for corruption testing
func createCorruptionsTestServer(t *testing.T, db *sql.DB, eb *eventbus.EventBus) *RESTServer {
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

func TestGetCorruptions_EmptyDB(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	req, _ := http.NewRequest("GET", "/corruptions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	data := response["data"].([]interface{})
	if len(data) != 0 {
		t.Errorf("Expected empty data, got %d items", len(data))
	}
}

func TestGetCorruptions_WithData(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed various corruptions
	seedCorruptionEvent(t, db, "corruption-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path":       "/test/file1.mkv",
		"corruption_type": "TruncatedFile",
	}, now)

	seedCorruptionEvent(t, db, "corruption-2", domain.CorruptionDetected, map[string]interface{}{
		"file_path":       "/test/file2.mkv",
		"corruption_type": "InvalidContainer",
	}, now)
	seedCorruptionEvent(t, db, "corruption-2", domain.VerificationSuccess, map[string]interface{}{
		"file_path": "/test/file2.mkv",
	}, now)

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	req, _ := http.NewRequest("GET", "/corruptions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	data := response["data"].([]interface{})
	if len(data) != 2 {
		t.Errorf("Expected 2 corruptions, got %d", len(data))
	}

	pagination := response["pagination"].(map[string]interface{})
	if pagination["total"].(float64) != 2 {
		t.Errorf("Expected total 2, got %v", pagination["total"])
	}
}

func TestGetCorruptions_StatusFilters(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed corruptions in different states
	// Active (detected)
	seedCorruptionEvent(t, db, "active-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/active.mkv",
	}, now)

	// Resolved
	seedCorruptionEvent(t, db, "resolved-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/resolved.mkv",
	}, now)
	seedCorruptionEvent(t, db, "resolved-1", domain.VerificationSuccess, map[string]interface{}{
		"file_path": "/test/resolved.mkv",
	}, now)

	// Orphaned
	seedCorruptionEvent(t, db, "orphaned-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/orphaned.mkv",
	}, now)
	seedCorruptionEvent(t, db, "orphaned-1", domain.MaxRetriesReached, map[string]interface{}{
		"file_path": "/test/orphaned.mkv",
	}, now)

	// Ignored
	seedCorruptionEvent(t, db, "ignored-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/ignored.mkv",
	}, now)
	seedCorruptionEvent(t, db, "ignored-1", domain.CorruptionIgnored, map[string]interface{}{
		"file_path": "/test/ignored.mkv",
		"reason":    "Test",
	}, now)

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	tests := []struct {
		filter   string
		expected int
	}{
		{"all", 4},
		{"pending", 1},
		{"resolved", 1},
		{"orphaned", 1},
		{"ignored", 1},
	}

	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/corruptions?status="+tt.filter, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			var response map[string]interface{}
			json.Unmarshal(w.Body.Bytes(), &response)

			pagination := response["pagination"].(map[string]interface{})
			if int(pagination["total"].(float64)) != tt.expected {
				t.Errorf("Filter '%s': expected %d, got %v", tt.filter, tt.expected, pagination["total"])
			}
		})
	}
}

func TestGetCorruptions_AdditionalStatusFilters(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// In progress state (RemediationQueued)
	seedCorruptionEvent(t, db, "in-progress-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/in-progress.mkv",
	}, now)
	seedCorruptionEvent(t, db, "in-progress-1", domain.RemediationQueued, map[string]interface{}{
		"file_path": "/test/in-progress.mkv",
	}, now)

	// Failed state (DeletionFailed)
	seedCorruptionEvent(t, db, "failed-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/failed.mkv",
	}, now)
	seedCorruptionEvent(t, db, "failed-1", domain.DeletionFailed, map[string]interface{}{
		"file_path": "/test/failed.mkv",
		"error":     "Test error",
	}, now)

	// Manual intervention (ImportBlocked)
	seedCorruptionEvent(t, db, "blocked-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/blocked.mkv",
	}, now)
	seedCorruptionEvent(t, db, "blocked-1", domain.ImportBlocked, map[string]interface{}{
		"file_path": "/test/blocked.mkv",
	}, now)

	// Manual intervention (ManuallyRemoved)
	seedCorruptionEvent(t, db, "removed-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/removed.mkv",
	}, now)
	seedCorruptionEvent(t, db, "removed-1", domain.ManuallyRemoved, map[string]interface{}{
		"file_path": "/test/removed.mkv",
	}, now)

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	tests := []struct {
		filter   string
		expected int
	}{
		{"active", 4},               // All non-resolved/orphaned/ignored
		{"in_progress", 1},          // Only RemediationQueued
		{"failed", 1},               // Only DeletionFailed
		{"manual_intervention", 2},  // ImportBlocked + ManuallyRemoved
		{"invalid_filter", 4},       // Invalid filter should return all
	}

	for _, tt := range tests {
		t.Run(tt.filter, func(t *testing.T) {
			req, _ := http.NewRequest("GET", "/corruptions?status="+tt.filter, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			var response map[string]interface{}
			json.Unmarshal(w.Body.Bytes(), &response)

			pagination := response["pagination"].(map[string]interface{})
			if int(pagination["total"].(float64)) != tt.expected {
				t.Errorf("Filter '%s': expected %d, got %v", tt.filter, tt.expected, pagination["total"])
			}
		})
	}
}

func TestGetCorruptions_Pagination(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed 5 corruptions
	for i := 0; i < 5; i++ {
		seedCorruptionEvent(t, db, "corruption-"+string(rune('a'+i)), domain.CorruptionDetected, map[string]interface{}{
			"file_path": "/test/file" + string(rune('0'+i)) + ".mkv",
		}, now)
	}

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	req, _ := http.NewRequest("GET", "/corruptions?page=1&limit=2", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	data := response["data"].([]interface{})
	if len(data) != 2 {
		t.Errorf("Expected 2 items on page 1, got %d", len(data))
	}

	pagination := response["pagination"].(map[string]interface{})
	if pagination["total_pages"].(float64) != 3 {
		t.Errorf("Expected 3 total pages, got %v", pagination["total_pages"])
	}
}

func TestGetCorruptions_Sorting(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed with different timestamps
	seedCorruptionEvent(t, db, "older", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/older.mkv",
	}, now.Add(-1*time.Hour))

	seedCorruptionEvent(t, db, "newer", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/newer.mkv",
	}, now)

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	// Sort by detected_at ascending (older first)
	req, _ := http.NewRequest("GET", "/corruptions?sort_by=detected_at&sort_order=asc", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	data := response["data"].([]interface{})
	if len(data) != 2 {
		t.Fatalf("Expected 2 items, got %d", len(data))
	}

	first := data[0].(map[string]interface{})
	if first["id"].(string) != "older" {
		t.Errorf("Expected 'older' first, got %v", first["id"])
	}
}

func TestGetCorruptions_PathIDFilter(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed corruptions with different path_ids
	seedCorruptionEvent(t, db, "path1-corruption", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/path1/file.mkv",
		"path_id":   1,
	}, now)

	seedCorruptionEvent(t, db, "path2-corruption", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/path2/file.mkv",
		"path_id":   2,
	}, now)

	seedCorruptionEvent(t, db, "no-path-corruption", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/nopath/file.mkv",
	}, now)

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	// Filter by path_id=1
	req, _ := http.NewRequest("GET", "/corruptions?path_id=1", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	pagination := response["pagination"].(map[string]interface{})
	if int(pagination["total"].(float64)) != 1 {
		t.Errorf("Expected 1 corruption for path_id=1, got %v", pagination["total"])
	}

	data := response["data"].([]interface{})
	if len(data) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(data))
	}

	// Verify path_id is returned in response
	first := data[0].(map[string]interface{})
	if first["id"].(string) != "path1-corruption" {
		t.Errorf("Expected 'path1-corruption', got %v", first["id"])
	}
}

func TestGetCorruptions_PathIDFilterWithStatusFilter(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed active corruption for path 1
	seedCorruptionEvent(t, db, "path1-active", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/path1/active.mkv",
		"path_id":   1,
	}, now)

	// Seed resolved corruption for path 1
	seedCorruptionEvent(t, db, "path1-resolved", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/path1/resolved.mkv",
		"path_id":   1,
	}, now)
	seedCorruptionEvent(t, db, "path1-resolved", domain.VerificationSuccess, map[string]interface{}{
		"file_path": "/test/path1/resolved.mkv",
	}, now)

	// Seed corruption for path 2
	seedCorruptionEvent(t, db, "path2-active", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/path2/active.mkv",
		"path_id":   2,
	}, now)

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	// Filter by path_id=1 AND status=pending (should only return active, not resolved)
	req, _ := http.NewRequest("GET", "/corruptions?path_id=1&status=pending", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	pagination := response["pagination"].(map[string]interface{})
	if int(pagination["total"].(float64)) != 1 {
		t.Errorf("Expected 1 pending corruption for path_id=1, got %v", pagination["total"])
	}
}

func TestGetCorruptions_InvalidSortColumn(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	// Invalid sort column (SQL injection attempt)
	req, _ := http.NewRequest("GET", "/corruptions?sort_by=id;DROP TABLE events;--", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should succeed (invalid column ignored, defaults used)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestGetCorruptions_InvalidSortOrder(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	// Invalid sort order
	req, _ := http.NewRequest("GET", "/corruptions?sort_order=invalid", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestGetCorruptions_InvalidPagination(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	// Test with negative page and invalid limit
	req, _ := http.NewRequest("GET", "/corruptions?page=-1&limit=-5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	pagination := response["pagination"].(map[string]interface{})
	if int(pagination["page"].(float64)) != 1 {
		t.Errorf("Expected page 1, got %v", pagination["page"])
	}
	if int(pagination["limit"].(float64)) != 50 {
		t.Errorf("Expected limit 50, got %v", pagination["limit"])
	}
}

func TestGetCorruptions_LimitOver1000(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	// Test with limit over 1000
	req, _ := http.NewRequest("GET", "/corruptions?limit=2000", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	pagination := response["pagination"].(map[string]interface{})
	if int(pagination["limit"].(float64)) != 50 {
		t.Errorf("Expected limit 50, got %v", pagination["limit"])
	}
}

func TestGetRemediations_EmptyDB(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/remediations", server.getRemediations)

	req, _ := http.NewRequest("GET", "/remediations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	data := response["data"].([]interface{})
	if len(data) != 0 {
		t.Errorf("Expected empty data, got %d items", len(data))
	}
}

func TestGetRemediations_WithData(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	// Insert corruption events that result in 'VerificationSuccess' state
	// The view uses the most recent event_type as current_state
	for i := 1; i <= 5; i++ {
		corruptionID := fmt.Sprintf("test-corruption-%d", i)
		// First, create a CorruptionDetected event (so file_path is populated)
		seedCorruptionEvent(t, db, corruptionID, domain.CorruptionDetected,
			map[string]interface{}{"file_path": fmt.Sprintf("/media/file%d.mkv", i)},
			time.Now().Add(-time.Hour))
		// Then add a VerificationSuccess event as the most recent one
		seedCorruptionEvent(t, db, corruptionID, domain.VerificationSuccess,
			map[string]interface{}{"file_path": fmt.Sprintf("/media/file%d.mkv", i)},
			time.Now())
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/remediations", server.getRemediations)

	req, _ := http.NewRequest("GET", "/remediations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	data := response["data"].([]interface{})
	if len(data) != 5 {
		t.Errorf("Expected 5 items, got %d", len(data))
	}

	pagination := response["pagination"].(map[string]interface{})
	if int(pagination["total"].(float64)) != 5 {
		t.Errorf("Expected total of 5, got %v", pagination["total"])
	}
}

func TestGetRemediations_Pagination(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	// Insert 15 VerificationSuccess corruption events
	for i := 1; i <= 15; i++ {
		corruptionID := fmt.Sprintf("test-corruption-%d", i)
		seedCorruptionEvent(t, db, corruptionID, domain.CorruptionDetected,
			map[string]interface{}{"file_path": fmt.Sprintf("/media/file%d.mkv", i)},
			time.Now().Add(-time.Hour))
		seedCorruptionEvent(t, db, corruptionID, domain.VerificationSuccess,
			map[string]interface{}{"file_path": fmt.Sprintf("/media/file%d.mkv", i)},
			time.Now())
	}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/remediations", server.getRemediations)

	// Request page 1 with limit 5
	req, _ := http.NewRequest("GET", "/remediations?page=1&limit=5", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	data := response["data"].([]interface{})
	if len(data) != 5 {
		t.Errorf("Expected 5 items on page 1, got %d", len(data))
	}

	pagination := response["pagination"].(map[string]interface{})
	if int(pagination["total_pages"].(float64)) != 3 {
		t.Errorf("Expected 3 total pages, got %v", pagination["total_pages"])
	}
}

func TestGetRemediations_InvalidPageNumber(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/remediations", server.getRemediations)

	// Request with invalid page (negative)
	req, _ := http.NewRequest("GET", "/remediations?page=-1&limit=10", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Page should be corrected to 1
	pagination := response["pagination"].(map[string]interface{})
	if int(pagination["page"].(float64)) != 1 {
		t.Errorf("Expected page to be corrected to 1, got %v", pagination["page"])
	}
}

func TestGetRemediations_InvalidLimit(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/remediations", server.getRemediations)

	// Request with limit too high (over 500)
	req, _ := http.NewRequest("GET", "/remediations?limit=1000", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Limit should be corrected to 50
	pagination := response["pagination"].(map[string]interface{})
	if int(pagination["limit"].(float64)) != 50 {
		t.Errorf("Expected limit to be corrected to 50, got %v", pagination["limit"])
	}
}

func TestGetCorruptionHistory_EmptyHistory(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions/:id/history", server.getCorruptionHistory)

	req, _ := http.NewRequest("GET", "/corruptions/nonexistent/history", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var history []interface{}
	json.Unmarshal(w.Body.Bytes(), &history)

	if len(history) != 0 {
		t.Errorf("Expected empty history, got %d items", len(history))
	}
}

func TestGetCorruptionHistory_WithEvents(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed a corruption with multiple events
	seedCorruptionEvent(t, db, "test-corruption", domain.CorruptionDetected, map[string]interface{}{
		"file_path":       "/test/file.mkv",
		"corruption_type": "TruncatedFile",
	}, now.Add(-1*time.Hour))

	seedCorruptionEvent(t, db, "test-corruption", domain.SearchStarted, map[string]interface{}{
		"file_path": "/test/file.mkv",
	}, now.Add(-30*time.Minute))

	seedCorruptionEvent(t, db, "test-corruption", domain.VerificationSuccess, map[string]interface{}{
		"file_path": "/test/file.mkv",
	}, now)

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions/:id/history", server.getCorruptionHistory)

	req, _ := http.NewRequest("GET", "/corruptions/test-corruption/history", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var history []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &history)

	if len(history) != 3 {
		t.Errorf("Expected 3 history events, got %d", len(history))
	}

	// Events should be in ascending order
	if history[0]["event_type"].(string) != string(domain.CorruptionDetected) {
		t.Errorf("Expected first event to be CorruptionDetected, got %v", history[0]["event_type"])
	}
	if history[2]["event_type"].(string) != string(domain.VerificationSuccess) {
		t.Errorf("Expected last event to be VerificationSuccess, got %v", history[2]["event_type"])
	}
}

func TestRetryCorruptions_NoIDs(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/corruptions/retry", server.retryCorruptions)

	body := strings.NewReader(`{"ids": []}`)
	req, _ := http.NewRequest("POST", "/corruptions/retry", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestRetryCorruptions_Success(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed a corruption
	seedCorruptionEvent(t, db, "retry-test", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/retry.mkv",
		"path_id":   1,
	}, now)

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/corruptions/retry", server.retryCorruptions)

	body := strings.NewReader(`{"ids": ["retry-test"]}`)
	req, _ := http.NewRequest("POST", "/corruptions/retry", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["retried"].(float64) != 1 {
		t.Errorf("Expected 1 retried, got %v", response["retried"])
	}
}

func TestRetryCorruptions_NotFound(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/corruptions/retry", server.retryCorruptions)

	body := strings.NewReader(`{"ids": ["nonexistent"]}`)
	req, _ := http.NewRequest("POST", "/corruptions/retry", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Should return 0 retried since the corruption doesn't exist
	if response["retried"].(float64) != 0 {
		t.Errorf("Expected 0 retried, got %v", response["retried"])
	}
}

func TestIgnoreCorruptions_NoIDs(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/corruptions/ignore", server.ignoreCorruptions)

	body := strings.NewReader(`{"ids": []}`)
	req, _ := http.NewRequest("POST", "/corruptions/ignore", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestIgnoreCorruptions_Success(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/corruptions/ignore", server.ignoreCorruptions)

	body := strings.NewReader(`{"ids": ["ignore-test"]}`)
	req, _ := http.NewRequest("POST", "/corruptions/ignore", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["ignored"].(float64) != 1 {
		t.Errorf("Expected 1 ignored, got %v", response["ignored"])
	}
}

func TestDeleteCorruptions_NoIDs(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/corruptions", server.deleteCorruptions)

	body := strings.NewReader(`{"ids": []}`)
	req, _ := http.NewRequest("DELETE", "/corruptions", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestDeleteCorruptions_Success(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Seed corruptions to delete
	seedCorruptionEvent(t, db, "delete-test-1", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/delete1.mkv",
	}, now)
	seedCorruptionEvent(t, db, "delete-test-2", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/delete2.mkv",
	}, now)

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/corruptions", server.deleteCorruptions)

	body := strings.NewReader(`{"ids": ["delete-test-1", "delete-test-2"]}`)
	req, _ := http.NewRequest("DELETE", "/corruptions", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	if response["deleted"].(float64) != 2 {
		t.Errorf("Expected 2 deleted, got %v", response["deleted"])
	}

	// Verify events are deleted
	var count int
	db.QueryRow("SELECT COUNT(*) FROM events").Scan(&count)
	if count != 0 {
		t.Errorf("Expected 0 events remaining, got %d", count)
	}
}

func TestDeleteCorruptions_Partial(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	now := time.Now()

	// Only seed one corruption
	seedCorruptionEvent(t, db, "exists", domain.CorruptionDetected, map[string]interface{}{
		"file_path": "/test/exists.mkv",
	}, now)

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/corruptions", server.deleteCorruptions)

	// Try to delete one that exists and one that doesn't
	body := strings.NewReader(`{"ids": ["exists", "nonexistent"]}`)
	req, _ := http.NewRequest("DELETE", "/corruptions", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Only 1 should be deleted
	if response["deleted"].(float64) != 1 {
		t.Errorf("Expected 1 deleted, got %v", response["deleted"])
	}
}

// =============================================================================
// deleteCorruptions Error Path Tests
// =============================================================================

func TestDeleteCorruptions_BadJSON(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/corruptions", server.deleteCorruptions)

	body := strings.NewReader(`{invalid json}`)
	req, _ := http.NewRequest("DELETE", "/corruptions", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d: %s", w.Code, w.Body.String())
	}
}

func TestDeleteCorruptions_DBError(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	// Drop events table to cause DB error
	db.Exec("DROP TABLE events")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.DELETE("/corruptions", server.deleteCorruptions)

	body := strings.NewReader(`{"ids": ["test-id-1"]}`)
	req, _ := http.NewRequest("DELETE", "/corruptions", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should return 200 with 0 deleted (error is logged but not returned)
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["deleted"].(float64) != 0 {
		t.Errorf("Expected 0 deleted when DB error occurs, got %v", response["deleted"])
	}
}

func TestRetryCorruptions_BadJSON(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/corruptions/retry", server.retryCorruptions)

	body := strings.NewReader(`{invalid json}`)
	req, _ := http.NewRequest("POST", "/corruptions/retry", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

// =============================================================================
// ignoreCorruptions Error Path Tests
// =============================================================================

func TestIgnoreCorruptions_BadJSON(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/corruptions/ignore", server.ignoreCorruptions)

	body := strings.NewReader(`{invalid json}`)
	req, _ := http.NewRequest("POST", "/corruptions/ignore", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestIgnoreCorruptions_WithReason(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/corruptions/ignore", server.ignoreCorruptions)

	body := strings.NewReader(`{"ids": ["ignore-test"], "reason": "Test reason"}`)
	req, _ := http.NewRequest("POST", "/corruptions/ignore", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

// =============================================================================
// getCorruptions DB Error Tests
// =============================================================================

func TestGetCorruptions_DBError(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	// Drop the view to cause DB error
	db.Exec("DROP VIEW corruption_status")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions", server.getCorruptions)

	req, _ := http.NewRequest("GET", "/corruptions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d: %s", w.Code, w.Body.String())
	}
}

// =============================================================================
// getRemediations DB Error Tests
// =============================================================================

func TestGetRemediations_DBError(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	// Drop the view to cause DB error
	db.Exec("DROP VIEW corruption_status")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/remediations", server.getRemediations)

	req, _ := http.NewRequest("GET", "/remediations", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d: %s", w.Code, w.Body.String())
	}
}

// =============================================================================
// getCorruptionHistory DB Error Tests
// =============================================================================

func TestGetCorruptionHistory_DBError(t *testing.T) {
	db, cleanup := setupCorruptionsTestDB(t)
	defer cleanup()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()

	server := createCorruptionsTestServer(t, db, eb)
	defer server.scanner.Shutdown()

	// Drop events table to cause DB error
	db.Exec("DROP TABLE events")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/corruptions/:id/history", server.getCorruptionHistory)

	req, _ := http.NewRequest("GET", "/corruptions/any-id/history", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d: %s", w.Code, w.Body.String())
	}
}
