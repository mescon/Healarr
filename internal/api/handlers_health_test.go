package api

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/metrics"
	"github.com/mescon/Healarr/internal/services"
	_ "modernc.org/sqlite"
)

// Global metrics service to avoid Prometheus duplicate registration
var (
	globalMetricsOnce    sync.Once
	globalMetricsService *metrics.MetricsService
)

// setupTestDBForHealth creates a full test database with extended schema
func setupTestDBForHealth(t *testing.T) (*sql.DB, string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "healarr-health-test-*")
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

	// Create full schema for health tests
	schema := `
		CREATE TABLE settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
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

		CREATE TABLE arr_instances (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE scan_paths (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			local_path TEXT NOT NULL,
			arr_path TEXT NOT NULL,
			arr_instance_id INTEGER REFERENCES arr_instances(id),
			enabled INTEGER DEFAULT 1,
			auto_remediate INTEGER DEFAULT 0,
			dry_run INTEGER DEFAULT 0,
			detection_method TEXT DEFAULT 'ffprobe',
			detection_mode TEXT DEFAULT 'quick',
			max_retries INTEGER DEFAULT 3,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE scans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL,
			path_id INTEGER,
			status TEXT NOT NULL,
			files_scanned INTEGER DEFAULT 0,
			corruptions_found INTEGER DEFAULT 0,
			total_files INTEGER DEFAULT 0,
			current_file_index INTEGER DEFAULT 0,
			file_list TEXT,
			detection_config TEXT,
			auto_remediate INTEGER DEFAULT 0,
			dry_run INTEGER DEFAULT 0,
			error_message TEXT,
			started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			completed_at TIMESTAMP
		);

		CREATE TABLE pending_rescans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			file_path TEXT NOT NULL UNIQUE,
			path_id INTEGER,
			error_type TEXT NOT NULL,
			error_message TEXT,
			retry_count INTEGER DEFAULT 0,
			max_retries INTEGER DEFAULT 5,
			first_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			last_attempt_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			next_retry_at TIMESTAMP,
			status TEXT DEFAULT 'pending',
			resolved_at TIMESTAMP,
			resolution TEXT
		);

		CREATE VIEW corruption_status AS
		SELECT
			aggregate_id as corruption_id,
			(SELECT event_type FROM events e2 WHERE e2.aggregate_id = e.aggregate_id ORDER BY id DESC LIMIT 1) as current_state,
			MIN(created_at) as detected_at,
			MAX(created_at) as last_updated_at
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

	return db, dbPath, cleanup
}

// mockHealthChecker implements integration.HealthChecker for testing
type mockHealthChecker struct {
	healthy bool
	err     *integration.HealthCheckError
}

func (m *mockHealthChecker) Check(path string, mode string) (bool, *integration.HealthCheckError) {
	return m.healthy, m.err
}

func (m *mockHealthChecker) CheckWithConfig(path string, config integration.DetectionConfig) (bool, *integration.HealthCheckError) {
	return m.healthy, m.err
}

// mockPathMapper implements integration.PathMapper for testing
type mockPathMapper struct{}

func (m *mockPathMapper) ToArrPath(localPath string) (string, error) {
	return localPath, nil
}

func (m *mockPathMapper) ToLocalPath(arrPath string) (string, error) {
	return arrPath, nil
}

func (m *mockPathMapper) Reload() error {
	return nil
}

// getGlobalMetricsService returns a shared metrics service to avoid duplicate Prometheus registration
func getGlobalMetricsService(eb *eventbus.EventBus) *metrics.MetricsService {
	globalMetricsOnce.Do(func() {
		globalMetricsService = metrics.NewMetricsService(eb)
	})
	return globalMetricsService
}

// setupHealthTestServer creates a test server with all necessary components
// Returns router, server, and cleanup function that must be called to release resources
func setupHealthTestServer(t *testing.T, db *sql.DB, dbPath string) (*gin.Engine, *RESTServer, func()) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	hub := NewWebSocketHub(eb)

	// Create a minimal scanner service
	hc := &mockHealthChecker{healthy: true}
	pm := &mockPathMapper{}
	scanner := services.NewScannerService(db, eb, hc, pm)

	// Use shared metrics service to avoid Prometheus registration conflicts
	metricsService := getGlobalMetricsService(eb)

	s := &RESTServer{
		router:     r,
		db:         db,
		eventBus:   eb,
		scanner:    scanner,
		pathMapper: pm,
		metrics:    metricsService,
		hub:        hub,
		startTime:  time.Now().Add(-1 * time.Hour), // Started 1 hour ago
	}

	// Set up a test config with the database path
	config.SetForTesting(&config.Config{
		DatabasePath: dbPath,
	})

	// Register routes
	api := r.Group("/api")
	api.GET("/health", s.handleHealth)

	cleanup := func() {
		scanner.Shutdown()
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, s, cleanup
}

func TestHandleHealth_Healthy(t *testing.T) {
	db, dbPath, cleanup := setupTestDBForHealth(t)
	defer cleanup()

	router, _, serverCleanup := setupHealthTestServer(t, db, dbPath)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %v", response["status"])
	}

	if response["version"] == nil {
		t.Error("Expected version to be present")
	}

	if response["uptime"] == nil {
		t.Error("Expected uptime to be present")
	}

	// Check uptime format (1h 0m)
	uptime, ok := response["uptime"].(string)
	if !ok {
		t.Error("Expected uptime to be a string")
	}
	if uptime == "" {
		t.Error("Expected uptime to be non-empty")
	}

	// Check database status
	dbStatus, ok := response["database"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected database to be a map")
	}
	if dbStatus["status"] != "connected" {
		t.Errorf("Expected database status 'connected', got %v", dbStatus["status"])
	}

	// Check arr_instances
	arrInstances, ok := response["arr_instances"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected arr_instances to be a map")
	}
	if arrInstances["total"] != float64(0) {
		t.Errorf("Expected arr_instances total 0, got %v", arrInstances["total"])
	}

	// Check active_scans
	if response["active_scans"] != float64(0) {
		t.Errorf("Expected active_scans 0, got %v", response["active_scans"])
	}

	// Check pending_corruptions
	if response["pending_corruptions"] != float64(0) {
		t.Errorf("Expected pending_corruptions 0, got %v", response["pending_corruptions"])
	}

	// Check websocket_clients
	if response["websocket_clients"] != float64(0) {
		t.Errorf("Expected websocket_clients 0, got %v", response["websocket_clients"])
	}
}

func TestHandleHealth_WithArrInstances(t *testing.T) {
	db, dbPath, cleanup := setupTestDBForHealth(t)
	defer cleanup()

	// Create a mock arr server
	mockArr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/system/status" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"version":"4.0.0"}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockArr.Close()

	// Insert an arr instance
	apiKey := "test-api-key"
	encryptedKey, _ := crypto.Encrypt(apiKey)
	_, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, 1)",
		"Test Sonarr", "sonarr", mockArr.URL, encryptedKey)
	if err != nil {
		t.Fatalf("Failed to insert arr instance: %v", err)
	}

	router, _, serverCleanup := setupHealthTestServer(t, db, dbPath)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Check arr_instances shows the configured instance
	arrInstances, ok := response["arr_instances"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected arr_instances to be a map")
	}

	if arrInstances["total"] != float64(1) {
		t.Errorf("Expected arr_instances total 1, got %v", arrInstances["total"])
	}

	// The mock server should respond successfully
	if arrInstances["online"] != float64(1) {
		t.Errorf("Expected arr_instances online 1, got %v", arrInstances["online"])
	}

	// Status should be healthy since all instances are online
	if response["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %v", response["status"])
	}
}

func TestHandleHealth_DegradedWithOfflineArr(t *testing.T) {
	db, dbPath, cleanup := setupTestDBForHealth(t)
	defer cleanup()

	// Insert an arr instance pointing to a non-existent server
	apiKey := "test-api-key"
	encryptedKey, _ := crypto.Encrypt(apiKey)
	_, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, 1)",
		"Test Sonarr", "sonarr", "http://127.0.0.1:59999", encryptedKey)
	if err != nil {
		t.Fatalf("Failed to insert arr instance: %v", err)
	}

	router, _, serverCleanup := setupHealthTestServer(t, db, dbPath)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Check arr_instances
	arrInstances, ok := response["arr_instances"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected arr_instances to be a map")
	}

	if arrInstances["total"] != float64(1) {
		t.Errorf("Expected arr_instances total 1, got %v", arrInstances["total"])
	}

	// The instance should be offline since server doesn't exist
	if arrInstances["online"] != float64(0) {
		t.Errorf("Expected arr_instances online 0, got %v", arrInstances["online"])
	}

	// Status should be degraded since arr instance is offline
	if response["status"] != "degraded" {
		t.Errorf("Expected status 'degraded', got %v", response["status"])
	}
}

func TestHandleHealth_WithPendingCorruptions(t *testing.T) {
	db, dbPath, cleanup := setupTestDBForHealth(t)
	defer cleanup()

	// Insert some corruption events
	for i := 0; i < 3; i++ {
		_, err := db.Exec(`
			INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data)
			VALUES ('corruption', ?, 'CorruptionDetected', '{"file_path":"/test/file.mkv"}')
		`, "corr-"+string(rune('a'+i)))
		if err != nil {
			t.Fatalf("Failed to insert event: %v", err)
		}
	}

	router, _, serverCleanup := setupHealthTestServer(t, db, dbPath)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	// Check pending_corruptions
	if response["pending_corruptions"] != float64(3) {
		t.Errorf("Expected pending_corruptions 3, got %v", response["pending_corruptions"])
	}
}

func TestHandleHealth_UptimeFormatting(t *testing.T) {
	db, dbPath, cleanup := setupTestDBForHealth(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	r := gin.New()
	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()
	hc := &mockHealthChecker{healthy: true}
	pm := &mockPathMapper{}
	scanner := services.NewScannerService(db, eb, hc, pm)
	defer scanner.Shutdown()
	metricsService := getGlobalMetricsService(eb)

	tests := []struct {
		name          string
		startTime     time.Time
		expectedMatch string // Substring to look for
	}{
		{
			name:          "minutes only",
			startTime:     time.Now().Add(-30 * time.Minute),
			expectedMatch: "30m",
		},
		{
			name:          "hours and minutes",
			startTime:     time.Now().Add(-3*time.Hour - 15*time.Minute),
			expectedMatch: "3h",
		},
		{
			name:          "days hours minutes",
			startTime:     time.Now().Add(-2*24*time.Hour - 5*time.Hour - 30*time.Minute),
			expectedMatch: "2d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hub := NewWebSocketHub(eb)
			defer hub.Shutdown()

			s := &RESTServer{
				router:     r,
				db:         db,
				eventBus:   eb,
				scanner:    scanner,
				pathMapper: pm,
				metrics:    metricsService,
				hub:        hub,
				startTime:  tt.startTime,
			}

			config.SetForTesting(&config.Config{
				DatabasePath: dbPath,
			})

			testRouter := gin.New()
			testRouter.GET("/api/health", s.handleHealth)

			req, _ := http.NewRequest("GET", "/api/health", nil)
			w := httptest.NewRecorder()
			testRouter.ServeHTTP(w, req)

			var response map[string]interface{}
			json.Unmarshal(w.Body.Bytes(), &response)

			uptime, ok := response["uptime"].(string)
			if !ok {
				t.Error("Expected uptime to be a string")
				return
			}

			if len(uptime) == 0 {
				t.Error("Expected uptime to be non-empty")
				return
			}

			// Basic sanity check that uptime contains expected pattern
			if tt.expectedMatch != "" && !containsSubstring(uptime, tt.expectedMatch) {
				t.Errorf("Expected uptime to contain %q, got %q", tt.expectedMatch, uptime)
			}
		})
	}
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAny(s, substr))
}

func containsAny(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestHandleHealth_DatabaseSizeIncluded(t *testing.T) {
	db, dbPath, cleanup := setupTestDBForHealth(t)
	defer cleanup()

	router, _, serverCleanup := setupHealthTestServer(t, db, dbPath)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	dbStatus, ok := response["database"].(map[string]interface{})
	if !ok {
		t.Fatal("Expected database to be a map")
	}

	// Size should be present since we have a real database file
	if dbStatus["size_bytes"] == nil {
		t.Error("Expected database size_bytes to be present")
	}

	sizeBytes, ok := dbStatus["size_bytes"].(float64)
	if !ok {
		t.Error("Expected size_bytes to be a number")
	}

	if sizeBytes <= 0 {
		t.Errorf("Expected size_bytes > 0, got %v", sizeBytes)
	}
}

func TestHandleHealth_DisabledArrInstancesIgnored(t *testing.T) {
	db, dbPath, cleanup := setupTestDBForHealth(t)
	defer cleanup()

	// Insert a disabled arr instance
	apiKey := "test-api-key"
	encryptedKey, _ := crypto.Encrypt(apiKey)
	_, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, 0)",
		"Disabled Sonarr", "sonarr", "http://127.0.0.1:59999", encryptedKey)
	if err != nil {
		t.Fatalf("Failed to insert arr instance: %v", err)
	}

	router, _, serverCleanup := setupHealthTestServer(t, db, dbPath)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/health", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Disabled instances should not be counted
	arrInstances := response["arr_instances"].(map[string]interface{})
	if arrInstances["total"] != float64(0) {
		t.Errorf("Expected arr_instances total 0 (disabled ignored), got %v", arrInstances["total"])
	}

	// Status should be healthy since there are no enabled instances to be offline
	if response["status"] != "healthy" {
		t.Errorf("Expected status 'healthy', got %v", response["status"])
	}
}
