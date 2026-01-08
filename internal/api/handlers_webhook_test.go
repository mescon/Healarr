package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3" // Register CGo SQLite driver for database/sql
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/services"
	"github.com/mescon/Healarr/internal/testutil"
)

// webhookMockScanner implements services.Scanner for webhook tests.
// Only ScanFile is used by handleWebhook.
type webhookMockScanner struct {
	ScanFileFunc func(path string) error
}

func (m *webhookMockScanner) ScanFile(path string) error {
	if m.ScanFileFunc != nil {
		return m.ScanFileFunc(path)
	}
	return nil
}

func (m *webhookMockScanner) ScanPath(_ int64, _ string) error        { return nil }
func (m *webhookMockScanner) IsPathBeingScanned(_ string) bool        { return false }
func (m *webhookMockScanner) GetActiveScans() []services.ScanProgress { return nil }
func (m *webhookMockScanner) CancelScan(_ string) error               { return nil }
func (m *webhookMockScanner) PauseScan(_ string) error                { return nil }
func (m *webhookMockScanner) ResumeScan(_ string) error               { return nil }
func (m *webhookMockScanner) Shutdown()                               {}

// setupWebhookTestDB creates a test database for webhook tests
func setupWebhookTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "healarr-webhook-test-*")
	require.NoError(t, err)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	// Schema for webhook tests
	schema := `
		CREATE TABLE settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			event_type TEXT NOT NULL,
			aggregate_id TEXT,
			event_data TEXT NOT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
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
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`
	_, err = db.Exec(schema)
	require.NoError(t, err)

	return db, cleanup
}

// setupWebhookTestServer creates a test server with webhook routes
// Returns router, apiKey, and cleanup function that must be called to release resources
func setupWebhookTestServer(t *testing.T, db *sql.DB, pm *testutil.MockPathMapper, scanner *webhookMockScanner) (*gin.Engine, string, func()) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	hub := NewWebSocketHub(eb)

	s := &RESTServer{
		router:     r,
		db:         db,
		eventBus:   eb,
		hub:        hub,
		pathMapper: pm,
		scanner:    scanner,
	}

	// Setup API key for authentication
	apiKey, err := auth.GenerateAPIKey()
	require.NoError(t, err)
	encryptedKey, err := crypto.Encrypt(apiKey)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)
	require.NoError(t, err)

	// Register webhook route (with rate limiter in real app, but we skip for tests)
	api := r.Group("/api")
	api.POST("/webhook/:instance_id", s.handleWebhook)

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, apiKey, cleanup
}

// createTestArrInstance creates a test arr instance
func createTestArrInstance(t *testing.T, db *sql.DB, enabled bool) int64 {
	t.Helper()
	encryptedKey, _ := crypto.Encrypt("test-arr-api-key")
	result, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
		"Test Sonarr", "sonarr", "http://localhost:8989", encryptedKey, enabled)
	require.NoError(t, err)
	id, _ := result.LastInsertId()
	return id
}

// =============================================================================
// Authentication Tests
// =============================================================================

func TestWebhook_MissingAPIKey(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	mockPM := &testutil.MockPathMapper{}
	mockScanner := &webhookMockScanner{}
	router, _, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"eventType": "Download"}`)
	req, _ := http.NewRequest("POST", "/api/webhook/1", body)
	req.Header.Set("Content-Type", "application/json")
	// No API key
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "API key required", response["error"])
}

func TestWebhook_InvalidAPIKey(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	mockPM := &testutil.MockPathMapper{}
	mockScanner := &webhookMockScanner{}
	router, _, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"eventType": "Download"}`)
	req, _ := http.NewRequest("POST", "/api/webhook/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "wrong-api-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Invalid API key", response["error"])
}

func TestWebhook_APIKeyFromQueryParam(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	arrID := createTestArrInstance(t, db, true)

	mockPM := &testutil.MockPathMapper{
		ToLocalPathFunc: func(arrPath string) (string, error) {
			return "/local" + arrPath, nil
		},
	}
	mockScanner := &webhookMockScanner{
		ScanFileFunc: func(path string) error {
			return nil
		},
	}
	router, apiKey, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"eventType": "Download",
		"episodeFile": {"path": "/tv/show/episode.mkv"}
	}`)
	req, _ := http.NewRequest("POST", "/api/webhook/"+string(rune('0'+arrID))+"?apikey="+apiKey, body)
	req.Header.Set("Content-Type", "application/json")
	// API key in query param, not header
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

// =============================================================================
// Instance Validation Tests
// =============================================================================

func TestWebhook_InvalidInstanceID(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	mockPM := &testutil.MockPathMapper{}
	mockScanner := &webhookMockScanner{}
	router, apiKey, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"eventType": "Download"}`)
	req, _ := http.NewRequest("POST", "/api/webhook/invalid", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Invalid instance ID", response["error"])
}

func TestWebhook_InstanceNotFound(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	mockPM := &testutil.MockPathMapper{}
	mockScanner := &webhookMockScanner{}
	router, apiKey, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"eventType": "Download"}`)
	req, _ := http.NewRequest("POST", "/api/webhook/999", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Instance not found", response["error"])
}

func TestWebhook_InstanceDisabled(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	arrID := createTestArrInstance(t, db, false) // disabled

	mockPM := &testutil.MockPathMapper{}
	mockScanner := &webhookMockScanner{}
	router, apiKey, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"eventType": "Download"}`)
	req, _ := http.NewRequest("POST", "/api/webhook/"+string(rune('0'+arrID)), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response["error"], "disabled")
}

// =============================================================================
// Request Parsing Tests
// =============================================================================

func TestWebhook_InvalidJSON(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	arrID := createTestArrInstance(t, db, true)

	mockPM := &testutil.MockPathMapper{}
	mockScanner := &webhookMockScanner{}
	router, apiKey, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid json}`)
	req, _ := http.NewRequest("POST", "/api/webhook/"+string(rune('0'+arrID)), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestWebhook_NoFilePath(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	arrID := createTestArrInstance(t, db, true)

	mockPM := &testutil.MockPathMapper{}
	mockScanner := &webhookMockScanner{}
	router, apiKey, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	// Webhook with no file path (e.g., series added event)
	body := bytes.NewBufferString(`{
		"eventType": "SeriesAdd",
		"series": {"path": "/tv/show"}
	}`)
	req, _ := http.NewRequest("POST", "/api/webhook/"+string(rune('0'+arrID)), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Ignored: No file path", response["message"])
}

// =============================================================================
// Path Mapping Tests
// =============================================================================

func TestWebhook_PathMappingFails(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	arrID := createTestArrInstance(t, db, true)

	mockPM := &testutil.MockPathMapper{
		ToLocalPathFunc: func(arrPath string) (string, error) {
			return "", errors.New("no matching path configured")
		},
	}
	mockScanner := &webhookMockScanner{}
	router, apiKey, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"eventType": "Download",
		"episodeFile": {"path": "/unmapped/path/file.mkv"}
	}`)
	req, _ := http.NewRequest("POST", "/api/webhook/"+string(rune('0'+arrID)), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Ignored: Path not mapped", response["message"])
}

// =============================================================================
// Success Tests
// =============================================================================

func TestWebhook_SuccessEpisodeFile(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	arrID := createTestArrInstance(t, db, true)

	var scannedPath string
	mockPM := &testutil.MockPathMapper{
		ToLocalPathFunc: func(arrPath string) (string, error) {
			return "/local" + arrPath, nil
		},
	}
	mockScanner := &webhookMockScanner{
		ScanFileFunc: func(path string) error {
			scannedPath = path
			return nil
		},
	}
	router, apiKey, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"eventType": "Download",
		"episodeFile": {"path": "/tv/show/episode.mkv"}
	}`)
	req, _ := http.NewRequest("POST", "/api/webhook/"+string(rune('0'+arrID)), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Scan queued", response["message"])
	assert.Equal(t, "/local/tv/show/episode.mkv", response["local_path"])

	// Note: Scanner is called in a goroutine, so we can't assert scannedPath immediately
	_ = scannedPath // Variable used in goroutine
}

func TestWebhook_SuccessMovieFile(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	arrID := createTestArrInstance(t, db, true)

	mockPM := &testutil.MockPathMapper{
		ToLocalPathFunc: func(arrPath string) (string, error) {
			return "/local" + arrPath, nil
		},
	}
	mockScanner := &webhookMockScanner{
		ScanFileFunc: func(path string) error {
			return nil
		},
	}
	router, apiKey, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"eventType": "Download",
		"movieFile": {"path": "/movies/movie.mkv"}
	}`)
	req, _ := http.NewRequest("POST", "/api/webhook/"+string(rune('0'+arrID)), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Scan queued", response["message"])
	assert.Equal(t, "/local/movies/movie.mkv", response["local_path"])
}

// =============================================================================
// Error Path Tests
// =============================================================================

func TestWebhook_DBError(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	// Setup everything normally first
	mockPM := &testutil.MockPathMapper{}
	mockScanner := &webhookMockScanner{}
	router, apiKey, serverCleanup := setupWebhookTestServer(t, db, mockPM, mockScanner)
	defer serverCleanup()

	// Drop settings table to cause DB query error
	_, err := db.Exec("DROP TABLE settings")
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"eventType": "Download"}`)
	req, _ := http.NewRequest("POST", "/api/webhook/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Authentication error", response["error"])
}

func TestWebhook_DecryptError(t *testing.T) {
	db, cleanup := setupWebhookTestDB(t)
	defer cleanup()

	// Setup without adding an API key through normal flow
	mockPM := &testutil.MockPathMapper{}
	mockScanner := &webhookMockScanner{}

	gin.SetMode(gin.TestMode)
	r := gin.New()
	eb := eventbus.NewEventBus(db)
	hub := NewWebSocketHub(eb)

	s := &RESTServer{
		router:     r,
		db:         db,
		eventBus:   eb,
		hub:        hub,
		pathMapper: mockPM,
		scanner:    mockScanner,
	}

	r.POST("/api/webhook/:instance_id", s.handleWebhook)

	// Insert an invalid encrypted key that will fail decryption
	_, err := db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', 'enc:v1:invalid-encrypted-data')")
	require.NoError(t, err)

	body := bytes.NewBufferString(`{"eventType": "Download"}`)
	req, _ := http.NewRequest("POST", "/api/webhook/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", "any-key-value")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Authentication error", response["error"])

	hub.Shutdown()
	eb.Shutdown()
}
