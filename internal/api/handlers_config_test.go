package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3" // Register CGo SQLite driver for database/sql
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/notifier"
	"github.com/mescon/Healarr/internal/testutil"
)

// setupConfigTestDB creates a test database with full schema for config tests
func setupConfigTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "healarr-config-test-*")
	require.NoError(t, err)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	// Complete schema for config tests
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
			type TEXT NOT NULL CHECK(type IN ('sonarr', 'radarr', 'lidarr', 'readarr', 'whisparr')),
			url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE scan_paths (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			local_path TEXT NOT NULL,
			arr_path TEXT NOT NULL,
			arr_instance_id INTEGER REFERENCES arr_instances(id) ON DELETE SET NULL,
			enabled INTEGER DEFAULT 1,
			auto_remediate INTEGER DEFAULT 1,
			dry_run INTEGER DEFAULT 0,
			detection_method TEXT DEFAULT 'ffprobe',
			detection_args TEXT DEFAULT NULL,
			detection_mode TEXT DEFAULT 'quick',
			max_retries INTEGER DEFAULT 3,
			verification_timeout_hours INTEGER DEFAULT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL REFERENCES scan_paths(id) ON DELETE CASCADE,
			cron_expression TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			provider_type TEXT NOT NULL,
			config TEXT NOT NULL,
			events TEXT DEFAULT '[]',
			enabled INTEGER DEFAULT 1,
			throttle_seconds INTEGER DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`
	_, err = db.Exec(schema)
	require.NoError(t, err)

	return db, cleanup
}

// setupConfigTestServer creates a test server with config routes
// If withNotifier is true, creates a real notifier; otherwise sets it to nil.
// Returns router, apiKey, and cleanup function that must be called to release resources
func setupConfigTestServer(t *testing.T, db *sql.DB, pm *testutil.MockPathMapper, withNotifier bool) (*gin.Engine, string, func()) {
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
	}

	// Optionally add notifier service
	if withNotifier {
		s.notifier = notifier.NewNotifier(db, eb)
	}

	// Setup API key for authentication
	apiKey, err := auth.GenerateAPIKey()
	require.NoError(t, err)
	encryptedKey, err := crypto.Encrypt(apiKey)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)
	require.NoError(t, err)

	// Register routes with authentication
	api := r.Group("/api")
	protected := api.Group("")
	protected.Use(s.authMiddleware())
	{
		protected.PUT("/config/settings", s.updateSettings)
		protected.GET("/config/export", s.exportConfig)
		protected.POST("/config/import", s.importConfig)
		protected.GET("/config/backup", s.downloadDatabaseBackup)
		protected.POST("/config/restart", s.restartServer)
	}

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, apiKey, cleanup
}

func setupConfigTestServerWithScheduler(t *testing.T, db *sql.DB, pm *testutil.MockPathMapper, scheduler *testutil.MockSchedulerService, n *notifier.Notifier) (*gin.Engine, string, func()) {
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
		scheduler:  scheduler,
		notifier:   n,
	}

	// Setup API key for authentication
	apiKey, err := auth.GenerateAPIKey()
	require.NoError(t, err)
	encryptedKey, err := crypto.Encrypt(apiKey)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)
	require.NoError(t, err)

	// Register routes with authentication
	api := r.Group("/api")
	protected := api.Group("")
	protected.Use(s.authMiddleware())
	{
		protected.PUT("/config/settings", s.updateSettings)
		protected.GET("/config/export", s.exportConfig)
		protected.POST("/config/import", s.importConfig)
		protected.GET("/config/backup", s.downloadDatabaseBackup)
		protected.POST("/config/restart", s.restartServer)
	}

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, apiKey, cleanup
}

// =============================================================================
// updateSettings Tests
// =============================================================================

func TestUpdateSettings_Success(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"base_path": "/healarr"}`)

	req, _ := http.NewRequest("PUT", "/api/config/settings", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "/healarr", response["base_path"])
	assert.Equal(t, true, response["restart_required"])

	// Verify it was saved in the database
	var savedPath string
	err := db.QueryRow("SELECT value FROM settings WHERE key = 'base_path'").Scan(&savedPath)
	assert.NoError(t, err)
	assert.Equal(t, "/healarr", savedPath)
}

func TestUpdateSettings_NormalizesPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"adds leading slash", "healarr", "/healarr"},
		{"removes trailing slash", "/healarr/", "/healarr"},
		{"empty becomes root", "", "/"},
		{"root stays root", "/", "/"},
		{"normalizes both", "healarr/", "/healarr"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, cleanup := setupConfigTestDB(t)
			defer cleanup()

			mockPathMapper := &testutil.MockPathMapper{}
			router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
			defer serverCleanup()

			body := bytes.NewBufferString(`{"base_path": "` + tt.input + `"}`)

			req, _ := http.NewRequest("PUT", "/api/config/settings", body)
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-API-Key", apiKey)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, req)

			assert.Equal(t, http.StatusOK, w.Code)

			var response map[string]interface{}
			json.Unmarshal(w.Body.Bytes(), &response)
			assert.Equal(t, tt.expected, response["base_path"])
		})
	}
}

func TestUpdateSettings_InvalidJSON(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid}`)

	req, _ := http.NewRequest("PUT", "/api/config/settings", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// =============================================================================
// exportConfig Tests
// =============================================================================

func TestExportConfig_Empty(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/export", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response, "exported_at")
	assert.Contains(t, response, "version")
}

func TestExportConfig_WithData(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Create test arr instance
	encryptedKey, _ := crypto.Encrypt("test-api-key")
	_, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey, true)
	require.NoError(t, err)

	// Create test path
	_, err = db.Exec(`INSERT INTO scan_paths (local_path, arr_path, enabled, auto_remediate, dry_run, detection_method, detection_mode, max_retries)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"/media/tv", "/tv", true, true, false, "ffprobe", "quick", 3)
	require.NoError(t, err)

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup() // No notifier for this test

	req, _ := http.NewRequest("GET", "/api/config/export", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Check arr_instances
	instances, ok := response["arr_instances"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, instances, 1)
	inst := instances[0].(map[string]interface{})
	assert.Equal(t, "Sonarr", inst["name"])
	assert.Equal(t, "sonarr", inst["type"])

	// Check scan_paths
	paths, ok := response["scan_paths"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, paths, 1)
	path := paths[0].(map[string]interface{})
	assert.Equal(t, "/media/tv", path["local_path"])
}

// =============================================================================
// importConfig Tests
// =============================================================================

func TestImportConfig_Success(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	mockPathMapper := &testutil.MockPathMapper{
		ReloadFunc: func() error {
			return nil
		},
	}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"arr_instances": [
			{"name": "Sonarr", "type": "sonarr", "url": "http://localhost:8989", "api_key": "test-key", "enabled": true}
		],
		"scan_paths": [
			{"local_path": "/media/tv", "arr_path": "/tv", "enabled": true, "auto_remediate": true, "dry_run": false}
		]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Import complete", response["message"])

	imported := response["imported"].(map[string]interface{})
	assert.Equal(t, float64(1), imported["arr_instances"])
	assert.Equal(t, float64(1), imported["scan_paths"])

	// Verify data was imported
	var count int
	db.QueryRow("SELECT COUNT(*) FROM arr_instances").Scan(&count)
	assert.Equal(t, 1, count)

	db.QueryRow("SELECT COUNT(*) FROM scan_paths").Scan(&count)
	assert.Equal(t, 1, count)

	// Verify path mapper was reloaded
	assert.Equal(t, 1, mockPathMapper.CallCount("Reload"))
}

func TestImportConfig_EmptyArrPath(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	mockPathMapper := &testutil.MockPathMapper{
		ReloadFunc: func() error {
			return nil
		},
	}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	// If arr_path is empty, it should default to local_path
	body := bytes.NewBufferString(`{
		"arr_instances": [],
		"scan_paths": [
			{"local_path": "/media/tv", "arr_path": "", "enabled": true}
		]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify arr_path was set to local_path
	var arrPath string
	db.QueryRow("SELECT arr_path FROM scan_paths WHERE local_path = ?", "/media/tv").Scan(&arrPath)
	assert.Equal(t, "/media/tv", arrPath)
}

func TestImportConfig_DefaultValues(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	mockPathMapper := &testutil.MockPathMapper{
		ReloadFunc: func() error {
			return nil
		},
	}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	// Import with minimal data - should use defaults
	body := bytes.NewBufferString(`{
		"arr_instances": [],
		"scan_paths": [
			{"local_path": "/media/tv", "arr_path": "/tv", "enabled": true}
		]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify defaults were applied
	var detectionMethod, detectionMode string
	var maxRetries int
	db.QueryRow("SELECT detection_method, detection_mode, max_retries FROM scan_paths WHERE local_path = ?",
		"/media/tv").Scan(&detectionMethod, &detectionMode, &maxRetries)
	assert.Equal(t, "ffprobe", detectionMethod)
	assert.Equal(t, "quick", detectionMode)
	// maxRetries depends on config, just check it's set
	assert.Greater(t, maxRetries, 0)
}

func TestImportConfig_InvalidJSON(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestImportConfig_ArrInstanceDBError(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	// Drop arr_instances table to cause insert error
	db.Exec("DROP TABLE arr_instances")

	body := bytes.NewBufferString(`{
		"arr_instances": [{
			"name": "Test",
			"type": "sonarr",
			"url": "http://localhost:8989",
			"api_key": "test-key",
			"enabled": true
		}]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should still return 200 (continues on DB error, reports 0 imported)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	imported := response["imported"].(map[string]interface{})
	assert.Equal(t, float64(0), imported["arr_instances"])
}

func TestImportConfig_ScanPathDBError(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	// Drop scan_paths table to cause insert error
	db.Exec("DROP TABLE scan_paths")

	body := bytes.NewBufferString(`{
		"scan_paths": [{
			"local_path": "/media/movies",
			"enabled": true
		}]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should still return 200 (continues on DB error, reports 0 imported)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	imported := response["imported"].(map[string]interface{})
	assert.Equal(t, float64(0), imported["scan_paths"])
}

// =============================================================================
// downloadDatabaseBackup Tests
// =============================================================================

func TestDownloadDatabaseBackup_Success(t *testing.T) {
	// Create temp directory for database
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create actual database file
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	defer db.Close()

	// Create required schema
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	require.NoError(t, err)

	// Set config to use this database path
	config.SetForTesting(&config.Config{
		DatabasePath:      dbPath,
		DefaultMaxRetries: 3,
	})

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)

	s := &RESTServer{
		router:   r,
		db:       db,
		eventBus: eb,
		hub:      NewWebSocketHub(eb),
	}

	// Setup API key for authentication
	apiKey, err := auth.GenerateAPIKey()
	require.NoError(t, err)
	encryptedKey, err := crypto.Encrypt(apiKey)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)
	require.NoError(t, err)

	// Register routes
	api := r.Group("/api")
	protected := api.Group("")
	protected.Use(s.authMiddleware())
	{
		protected.GET("/config/backup", s.downloadDatabaseBackup)
	}

	req, _ := http.NewRequest("GET", "/api/config/backup", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "healarr_backup_")
	assert.Equal(t, "application/octet-stream", w.Header().Get("Content-Type"))

	// Verify backup file was created (and will be cleaned up)
	backupDir := filepath.Join(tmpDir, "backups")
	files, err := os.ReadDir(backupDir)
	if err == nil {
		// Backup files exist - they'll be cleaned up by the background goroutine
		assert.Greater(t, len(files), 0)
	}
}

// =============================================================================
// restartServer Tests
// =============================================================================

func TestExportConfig_DBError_ArrInstances(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Drop arr_instances table to trigger error
	_, err := db.Exec("DROP TABLE arr_instances")
	require.NoError(t, err)

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/export", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Export should still succeed (with empty arr_instances) even if query fails
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response, "exported_at")
}

func TestExportConfig_DBError_ScanPaths(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Create an arr instance first
	encryptedKey, _ := crypto.Encrypt("test-key")
	_, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	require.NoError(t, err)

	// Drop scan_paths table to trigger error
	_, err = db.Exec("DROP TABLE scan_paths")
	require.NoError(t, err)

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/export", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Export should still succeed (with empty scan_paths) even if query fails
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	// arr_instances should still be exported
	assert.Contains(t, response, "arr_instances")
}

func TestExportConfig_WithSchedules(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Create path and schedule
	_, err := db.Exec("INSERT INTO scan_paths (local_path, arr_path) VALUES (?, ?)", "/media/tv", "/tv")
	require.NoError(t, err)

	_, err = db.Exec("INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled) VALUES (?, ?, ?)", 1, "0 * * * *", true)
	require.NoError(t, err)

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/export", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Schedules are exported at top level with local_path for reference
	schedules, ok := response["schedules"].([]interface{})
	assert.True(t, ok, "schedules should be in response")
	assert.Len(t, schedules, 1)

	schedule := schedules[0].(map[string]interface{})
	assert.Equal(t, "0 * * * *", schedule["cron_expression"])
	assert.Equal(t, "/media/tv", schedule["local_path"], "schedule should include local_path for import reference")
	assert.Equal(t, true, schedule["enabled"])
}

func TestExportConfig_WithNotifications(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, true)
	defer serverCleanup()

	// Create notification via the notifier's CreateConfig (which handles encryption)
	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()
	n := notifier.NewNotifier(db, eb)
	require.NoError(t, n.Start())
	defer n.Stop()

	cfg := &notifier.NotificationConfig{
		Name:         "Discord",
		ProviderType: notifier.ProviderDiscord,
		Config:       json.RawMessage(`{"webhook_url":"http://example.com/webhook"}`),
		Events:       []string{"corruption_detected"},
		Enabled:      true,
	}
	_, err := n.CreateConfig(cfg)
	require.NoError(t, err)

	req, _ := http.NewRequest("GET", "/api/config/export", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	notifications, ok := response["notifications"].([]interface{})
	assert.True(t, ok)
	assert.Len(t, notifications, 1)
}

func TestExportConfig_DecryptError(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Insert arr instance with invalid encrypted key
	_, err := db.Exec(`INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)`,
		"Test", "sonarr", "http://localhost:8989", "enc:v1:invalid-key", 1)
	require.NoError(t, err)

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/export", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should still return 200 (continues on decrypt error)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// Check that the decryption error was handled gracefully
	instances := response["arr_instances"].([]interface{})
	assert.Len(t, instances, 1)
	instance := instances[0].(map[string]interface{})
	assert.Equal(t, "[DECRYPTION_ERROR]", instance["api_key"])
}

// Note: TestDownloadDatabaseBackup_SourceOpenError was removed because with VACUUM INTO,
// the handler uses the already-connected s.db rather than opening from config.DatabasePath.
// There's no "source open" step that can fail independently.

func TestDownloadDatabaseBackup_BackupDirCreationError(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Create a file where the backup directory should be (to cause mkdir to fail)
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create the database file
	testDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = testDB.Exec("CREATE TABLE test (id INTEGER)")
	require.NoError(t, err)
	testDB.Close()

	// Create a FILE named "backups" where the backup directory should be
	backupsPath := filepath.Join(tmpDir, "backups")
	err = os.WriteFile(backupsPath, []byte("blocker"), 0644)
	require.NoError(t, err)

	config.SetForTesting(&config.Config{
		DatabasePath:      dbPath,
		DefaultMaxRetries: 3,
	})

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/backup", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should return error when backup directory can't be created
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Failed to create backup directory", response["error"])
}

func TestDownloadDatabaseBackup_BackupFileCreationError(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Create temp directory structure
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create the database file
	testDB, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)
	_, err = testDB.Exec("CREATE TABLE test (id INTEGER)")
	require.NoError(t, err)
	testDB.Close()

	// Create backups directory but make it read-only
	backupsPath := filepath.Join(tmpDir, "backups")
	err = os.MkdirAll(backupsPath, 0755)
	require.NoError(t, err)

	// Make backups directory read-only to prevent file creation
	err = os.Chmod(backupsPath, 0555)
	require.NoError(t, err)
	defer os.Chmod(backupsPath, 0755) // Restore for cleanup

	config.SetForTesting(&config.Config{
		DatabasePath:      dbPath,
		DefaultMaxRetries: 3,
	})

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/backup", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should return error when backup file can't be created (VACUUM INTO fails)
	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	// With VACUUM INTO, the error message is "Failed to create backup"
	assert.Equal(t, "Failed to create backup", response["error"])
}

func TestRestartServer_ReturnsOK(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Mock the restart function to prevent actual process replacement
	originalRestartFunc := restartProcessFunc
	restartCalled := false
	restartProcessFunc = func() {
		restartCalled = true
	}
	defer func() {
		restartProcessFunc = originalRestartFunc
	}()

	mockPathMapper := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, mockPathMapper, false)
	defer serverCleanup()

	req, _ := http.NewRequest("POST", "/api/config/restart", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// The handler should return OK before restarting
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Server restarting...", response["message"])

	// Wait for the goroutine to call the restart function
	time.Sleep(600 * time.Millisecond)
	assert.True(t, restartCalled, "restart function should have been called")
}

func TestImportConfig_WithSchedulesAndNotifications(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Create a mock scheduler that tracks LoadSchedules calls
	loadSchedulesCalled := 0
	mockScheduler := &testutil.MockSchedulerService{
		LoadSchedulesFunc: func() error {
			loadSchedulesCalled++
			return nil
		},
	}

	mockPathMapper := &testutil.MockPathMapper{
		ReloadFunc: func() error {
			return nil
		},
	}

	// Set up server with notifier
	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()
	n := notifier.NewNotifier(db, eb)
	require.NoError(t, n.Start())
	defer n.Stop()

	router, apiKey, serverCleanup := setupConfigTestServerWithScheduler(t, db, mockPathMapper, mockScheduler, n)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"arr_instances": [],
		"scan_paths": [
			{"local_path": "/media/tv", "arr_path": "/tv", "enabled": true}
		],
		"schedules": [
			{"local_path": "/media/tv", "cron_expression": "0 3 * * *", "enabled": true}
		],
		"notifications": [
			{
				"name": "Discord Test",
				"provider_type": "discord",
				"config": {"webhook_url": "https://discord.com/api/webhooks/test"},
				"events": ["CorruptionDetected"],
				"enabled": true,
				"throttle_seconds": 60
			}
		]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)
	assert.Equal(t, "Import complete", response["message"])

	imported := response["imported"].(map[string]interface{})
	assert.Equal(t, float64(1), imported["scan_paths"])
	assert.Equal(t, float64(1), imported["schedules"])
	assert.Equal(t, float64(1), imported["notifications"])

	// Verify schedule was imported
	var schedCount int
	err = db.QueryRow("SELECT COUNT(*) FROM scan_schedules").Scan(&schedCount)
	require.NoError(t, err)
	assert.Equal(t, 1, schedCount)

	// Verify notification was imported
	var notifCount int
	err = db.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&notifCount)
	require.NoError(t, err)
	assert.Equal(t, 1, notifCount)

	// Verify scheduler was reloaded
	assert.Equal(t, 1, loadSchedulesCalled)
}

func TestImportConfig_ScheduleWithExistingPath(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Pre-create a scan path
	_, err := db.Exec("INSERT INTO scan_paths (local_path, arr_path) VALUES (?, ?)", "/media/movies", "/movies")
	require.NoError(t, err)

	mockScheduler := &testutil.MockSchedulerService{
		LoadSchedulesFunc: func() error { return nil },
	}
	mockPathMapper := &testutil.MockPathMapper{
		ReloadFunc: func() error { return nil },
	}

	router, apiKey, serverCleanup := setupConfigTestServerWithScheduler(t, db, mockPathMapper, mockScheduler, nil)
	defer serverCleanup()

	// Import schedule referencing existing path (not in import)
	body := bytes.NewBufferString(`{
		"arr_instances": [],
		"scan_paths": [],
		"schedules": [
			{"local_path": "/media/movies", "cron_expression": "0 4 * * *", "enabled": true}
		]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	imported := response["imported"].(map[string]interface{})
	assert.Equal(t, float64(1), imported["schedules"], "should find existing path and import schedule")
}

// TestImportConfigSkipsDuplicateArrInstances verifies that importing arr instances
// skips duplicates based on URL to prevent creating multiple entries for the same instance.
func TestImportConfigSkipsDuplicateArrInstances(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Insert an existing arr instance
	encryptedKey, _ := crypto.Encrypt("existingkey123")
	_, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
		"Existing Sonarr", "sonarr", "http://sonarr:8989", encryptedKey, true)
	require.NoError(t, err)

	pm := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, pm, false)
	defer serverCleanup()

	// Try to import an instance with the same URL
	body := bytes.NewBufferString(`{
		"arr_instances": [
			{
				"name": "Duplicate Sonarr",
				"type": "sonarr",
				"url": "http://sonarr:8989",
				"api_key": "newkey456",
				"enabled": true
			},
			{
				"name": "New Radarr",
				"type": "radarr",
				"url": "http://radarr:7878",
				"api_key": "radarrkey",
				"enabled": true
			}
		]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	imported := response["imported"].(map[string]interface{})
	// Only the new Radarr should be imported, Sonarr is skipped as duplicate
	assert.Equal(t, float64(1), imported["arr_instances"], "should only import non-duplicate instances")

	// Verify only 2 instances in database (original + new radarr)
	var count int
	db.QueryRow("SELECT COUNT(*) FROM arr_instances").Scan(&count)
	assert.Equal(t, 2, count, "should have 2 total instances")
}

// TestImportConfigSkipsDuplicateScanPaths verifies that importing scan paths
// skips duplicates based on local_path to prevent creating multiple entries.
func TestImportConfigSkipsDuplicateScanPaths(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Insert an existing scan path
	_, err := db.Exec("INSERT INTO scan_paths (local_path, arr_path, enabled) VALUES (?, ?, ?)",
		"/media/tv", "/tv", true)
	require.NoError(t, err)

	pm := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, pm, false)
	defer serverCleanup()

	// Try to import paths including a duplicate
	body := bytes.NewBufferString(`{
		"scan_paths": [
			{
				"local_path": "/media/tv",
				"arr_path": "/tv",
				"enabled": true
			},
			{
				"local_path": "/media/movies",
				"arr_path": "/movies",
				"enabled": true
			}
		]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	imported := response["imported"].(map[string]interface{})
	// Only /media/movies should be imported, /media/tv is skipped as duplicate
	assert.Equal(t, float64(1), imported["scan_paths"], "should only import non-duplicate paths")

	// Verify only 2 paths in database
	var count int
	db.QueryRow("SELECT COUNT(*) FROM scan_paths").Scan(&count)
	assert.Equal(t, 2, count, "should have 2 total scan paths")
}

// TestImportConfigSkipsDuplicateSchedules verifies that importing schedules
// skips duplicates based on scan_path_id + cron_expression combination.
func TestImportConfigSkipsDuplicateSchedules(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Insert an existing scan path and schedule
	result, err := db.Exec("INSERT INTO scan_paths (local_path, arr_path, enabled) VALUES (?, ?, ?)",
		"/media/tv", "/tv", true)
	require.NoError(t, err)
	pathID, _ := result.LastInsertId()

	_, err = db.Exec("INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled) VALUES (?, ?, ?)",
		pathID, "0 2 * * *", true)
	require.NoError(t, err)

	pm := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, pm, false)
	defer serverCleanup()

	// Try to import schedules including a duplicate
	body := bytes.NewBufferString(`{
		"schedules": [
			{
				"local_path": "/media/tv",
				"cron_expression": "0 2 * * *",
				"enabled": true
			},
			{
				"local_path": "/media/tv",
				"cron_expression": "0 4 * * *",
				"enabled": true
			}
		]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	imported := response["imported"].(map[string]interface{})
	// Only the 4am schedule should be imported, 2am is skipped as duplicate
	assert.Equal(t, float64(1), imported["schedules"], "should only import non-duplicate schedules")

	// Verify only 2 schedules in database
	var count int
	db.QueryRow("SELECT COUNT(*) FROM scan_schedules").Scan(&count)
	assert.Equal(t, 2, count, "should have 2 total schedules")
}

// TestImportConfigSkipsDuplicateNotifications verifies that importing notifications
// skips duplicates based on name.
func TestImportConfigSkipsDuplicateNotifications(t *testing.T) {
	db, cleanup := setupConfigTestDB(t)
	defer cleanup()

	// Insert an existing notification
	_, err := db.Exec(`INSERT INTO notifications (name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"Discord Alerts", "discord", `{"webhook_url":"http://example.com"}`, `["CorruptionDetected"]`, true, 0)
	require.NoError(t, err)

	pm := &testutil.MockPathMapper{}
	router, apiKey, serverCleanup := setupConfigTestServer(t, db, pm, true)
	defer serverCleanup()

	// Try to import notifications including a duplicate
	body := bytes.NewBufferString(`{
		"notifications": [
			{
				"name": "Discord Alerts",
				"provider_type": "discord",
				"config": {"webhook_url": "http://new-webhook.com"},
				"events": ["ScanComplete"],
				"enabled": true,
				"throttle_seconds": 0
			},
			{
				"name": "Slack Alerts",
				"provider_type": "slack",
				"config": {"webhook_url": "http://slack.example.com"},
				"events": ["CorruptionDetected"],
				"enabled": true,
				"throttle_seconds": 0
			}
		]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/import", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	imported := response["imported"].(map[string]interface{})
	// Only Slack Alerts should be imported, Discord Alerts is skipped as duplicate
	assert.Equal(t, float64(1), imported["notifications"], "should only import non-duplicate notifications")

	// Verify only 2 notifications in database
	var count int
	db.QueryRow("SELECT COUNT(*) FROM notifications").Scan(&count)
	assert.Equal(t, 2, count, "should have 2 total notifications")
}

// TestNormalizeScanPathDefaults verifies the default value normalization for scan paths.
func TestNormalizeScanPathDefaults(t *testing.T) {
	// Save and restore config
	cfg := config.Get()
	originalRetries := cfg.DefaultMaxRetries

	tests := []struct {
		name     string
		input    importScanPath
		expected importScanPath
	}{
		{
			name: "fills all defaults",
			input: importScanPath{
				LocalPath: "/media/test",
			},
			expected: importScanPath{
				LocalPath:       "/media/test",
				ArrPath:         "/media/test",
				DetectionMethod: "ffprobe",
				DetectionMode:   "quick",
				MaxRetries:      originalRetries,
			},
		},
		{
			name: "preserves existing values",
			input: importScanPath{
				LocalPath:       "/media/test",
				ArrPath:         "/custom/path",
				DetectionMethod: "mediainfo",
				DetectionMode:   "thorough",
				MaxRetries:      5,
			},
			expected: importScanPath{
				LocalPath:       "/media/test",
				ArrPath:         "/custom/path",
				DetectionMethod: "mediainfo",
				DetectionMode:   "thorough",
				MaxRetries:      5,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := tt.input
			normalizeScanPathDefaults(&path)
			assert.Equal(t, tt.expected.ArrPath, path.ArrPath)
			assert.Equal(t, tt.expected.DetectionMethod, path.DetectionMethod)
			assert.Equal(t, tt.expected.DetectionMode, path.DetectionMode)
			assert.Equal(t, tt.expected.MaxRetries, path.MaxRetries)
		})
	}
}
