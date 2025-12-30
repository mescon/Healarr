package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	// Initialize config for tests that use config.Get()
	config.SetForTesting(&config.Config{
		DefaultMaxRetries: 3,
	})
}

// setupPathsTestDB creates a test database with full scan_paths schema
func setupPathsTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	db, cleanup := setupTestDB(t)

	// Add full scan_paths schema
	schema := `
		ALTER TABLE scan_paths ADD COLUMN detection_method TEXT DEFAULT 'ffprobe';
		ALTER TABLE scan_paths ADD COLUMN detection_args TEXT;
		ALTER TABLE scan_paths ADD COLUMN detection_mode TEXT DEFAULT 'quick';
		ALTER TABLE scan_paths ADD COLUMN max_retries INTEGER DEFAULT 3;
		ALTER TABLE scan_paths ADD COLUMN verification_timeout_hours INTEGER;
	`
	_, err := db.Exec(schema)
	require.NoError(t, err)

	return db, cleanup
}

// setupPathsTestServer creates a test server with path routes
// Returns router, apiKey, and cleanup function that must be called to release resources
func setupPathsTestServer(t *testing.T, db *sql.DB) (*gin.Engine, string, func()) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	hub := NewWebSocketHub(eb)

	// Create a mock path mapper
	mockPathMapper := &testutil.MockPathMapper{}

	s := &RESTServer{
		router:     r,
		db:         db,
		eventBus:   eb,
		hub:        hub,
		pathMapper: mockPathMapper,
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
		protected.GET("/config/paths", s.getScanPaths)
		protected.POST("/config/paths", s.createScanPath)
		protected.PUT("/config/paths/:id", s.updateScanPath)
		protected.DELETE("/config/paths/:id", s.deleteScanPath)
		protected.GET("/config/browse", s.browseDirectory)
		protected.GET("/config/detection-preview", s.getDetectionPreview)
	}

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, apiKey, cleanup
}

// =============================================================================
// getScanPaths Tests
// =============================================================================

func TestGetScanPaths_Empty(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/paths", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response)
}

func TestGetScanPaths_WithData(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create an arr instance first (foreign key)
	encryptedKey, _ := crypto.Encrypt("api-key")
	result, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	require.NoError(t, err)
	arrID, _ := result.LastInsertId()

	// Create scan path
	_, err = db.Exec(`INSERT INTO scan_paths
		(local_path, arr_path, arr_instance_id, enabled, auto_remediate, detection_method, detection_mode, max_retries)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		"/media/tv", "/tv", arrID, true, true, "ffprobe", "thorough", 5)
	require.NoError(t, err)

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/paths", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, response, 1)
	assert.Equal(t, "/media/tv", response[0]["local_path"])
	assert.Equal(t, "/tv", response[0]["arr_path"])
	assert.Equal(t, "ffprobe", response[0]["detection_method"])
	assert.Equal(t, "thorough", response[0]["detection_mode"])
	assert.Equal(t, float64(5), response[0]["max_retries"])
}

func TestGetScanPaths_WithNullDetectionArgs(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create arr instance
	encryptedKey, _ := crypto.Encrypt("api-key")
	result, _ := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	arrID, _ := result.LastInsertId()

	// Create scan path with NULL detection_args
	_, err := db.Exec(`INSERT INTO scan_paths
		(local_path, arr_path, arr_instance_id, enabled, auto_remediate, detection_args)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"/media/movies", "/movies", arrID, true, false, nil)
	require.NoError(t, err)

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/paths", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Len(t, response, 1)
	assert.Equal(t, "", response[0]["detection_args"]) // NULL becomes empty string
}

// =============================================================================
// createScanPath Tests
// =============================================================================

func TestCreateScanPath_Success(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create arr instance
	encryptedKey, _ := crypto.Encrypt("api-key")
	result, _ := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	arrID, _ := result.LastInsertId()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"local_path": "/media/tv",
		"arr_path": "/tv",
		"arr_instance_id": ` + string(rune(arrID+'0')) + `,
		"enabled": true,
		"auto_remediate": true,
		"detection_method": "ffprobe",
		"detection_mode": "thorough",
		"max_retries": 5
	}`)

	req, _ := http.NewRequest("POST", "/api/config/paths", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	// Verify in database
	var count int
	db.QueryRow("SELECT COUNT(*) FROM scan_paths WHERE local_path = ?", "/media/tv").Scan(&count)
	assert.Equal(t, 1, count)
}

func TestCreateScanPath_DefaultValues(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create arr instance first
	encryptedKey, _ := crypto.Encrypt("api-key")
	result, _ := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	arrID, _ := result.LastInsertId()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Minimal request - should use defaults
	body := bytes.NewBufferString(`{
		"local_path": "/media/videos",
		"arr_instance_id": ` + string(rune(arrID+'0')) + `,
		"enabled": true
	}`)

	req, _ := http.NewRequest("POST", "/api/config/paths", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	// Verify defaults were applied
	var method, mode, arrPath string
	db.QueryRow("SELECT detection_method, detection_mode, arr_path FROM scan_paths WHERE local_path = ?",
		"/media/videos").Scan(&method, &mode, &arrPath)
	assert.Equal(t, "ffprobe", method)
	assert.Equal(t, "quick", mode)
	assert.Equal(t, "/media/videos", arrPath) // arr_path defaults to local_path
}

func TestCreateScanPath_WithDetectionArgs(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create arr instance first
	encryptedKey, _ := crypto.Encrypt("api-key")
	result, _ := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	arrID, _ := result.LastInsertId()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"local_path": "/media/custom",
		"arr_instance_id": ` + string(rune(arrID+'0')) + `,
		"enabled": true,
		"detection_args": ["-v", "error", "-show_streams"]
	}`)

	req, _ := http.NewRequest("POST", "/api/config/paths", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	// Verify detection args were stored as JSON
	var argsJSON string
	db.QueryRow("SELECT detection_args FROM scan_paths WHERE local_path = ?", "/media/custom").Scan(&argsJSON)
	assert.Contains(t, argsJSON, "-v")
	assert.Contains(t, argsJSON, "error")
}

func TestCreateScanPath_InvalidJSON(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{bad json}`)
	req, _ := http.NewRequest("POST", "/api/config/paths", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// =============================================================================
// updateScanPath Tests
// =============================================================================

func TestUpdateScanPath_Success(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create arr instance first
	encryptedKey, _ := crypto.Encrypt("api-key")
	arrResult, _ := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	arrID, _ := arrResult.LastInsertId()

	// Create initial path
	result, err := db.Exec(`INSERT INTO scan_paths
		(local_path, arr_path, arr_instance_id, enabled, auto_remediate)
		VALUES (?, ?, ?, ?, ?)`,
		"/old/path", "/old/arr", arrID, true, false)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"local_path": "/new/path",
		"arr_path": "/new/arr",
		"arr_instance_id": ` + string(rune(arrID+'0')) + `,
		"enabled": false,
		"auto_remediate": true,
		"detection_method": "mediainfo",
		"detection_mode": "thorough"
	}`)

	req, _ := http.NewRequest("PUT", "/api/config/paths/"+string(rune(id+'0')), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify update
	var localPath, method string
	var enabled bool
	db.QueryRow("SELECT local_path, detection_method, enabled FROM scan_paths WHERE id = ?", id).
		Scan(&localPath, &method, &enabled)
	assert.Equal(t, "/new/path", localPath)
	assert.Equal(t, "mediainfo", method)
	assert.False(t, enabled)
}

func TestUpdateScanPath_InvalidJSON(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid}`)
	req, _ := http.NewRequest("PUT", "/api/config/paths/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateScanPath_DefaultValues(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create arr instance first
	encryptedKey, _ := crypto.Encrypt("api-key")
	arrResult, _ := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	arrID, _ := arrResult.LastInsertId()

	// Create initial path
	result, err := db.Exec(`INSERT INTO scan_paths
		(local_path, arr_path, arr_instance_id, enabled, auto_remediate)
		VALUES (?, ?, ?, ?, ?)`,
		"/test/path", "/test/arr", arrID, true, false)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Send update without detection_method, detection_mode, max_retries, arr_path
	// These should get default values
	body := bytes.NewBufferString(fmt.Sprintf(`{
		"local_path": "/updated/path",
		"arr_instance_id": %d,
		"enabled": true,
		"auto_remediate": true
	}`, arrID))

	req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/config/paths/%d", id), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify defaults were applied
	var localPath, arrPath, method, mode string
	db.QueryRow("SELECT local_path, arr_path, detection_method, detection_mode FROM scan_paths WHERE id = ?", id).
		Scan(&localPath, &arrPath, &method, &mode)
	assert.Equal(t, "/updated/path", localPath)
	assert.Equal(t, "/updated/path", arrPath) // Should default to local_path when empty
	assert.Equal(t, "ffprobe", method)        // Default detection method
	assert.Equal(t, "quick", mode)            // Default detection mode
}

// =============================================================================
// deleteScanPath Tests
// =============================================================================

func TestDeleteScanPath_Success(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create arr instance first
	encryptedKey, _ := crypto.Encrypt("api-key")
	arrResult, _ := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	arrID, _ := arrResult.LastInsertId()

	// Create path to delete
	result, err := db.Exec(`INSERT INTO scan_paths (local_path, arr_path, arr_instance_id, enabled)
		VALUES (?, ?, ?, ?)`, "/to/delete", "/to/delete", arrID, true)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("DELETE", "/api/config/paths/"+string(rune(id+'0')), nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify deletion
	var count int
	db.QueryRow("SELECT COUNT(*) FROM scan_paths WHERE id = ?", id).Scan(&count)
	assert.Equal(t, 0, count)
}

// =============================================================================
// browseDirectory Tests
// =============================================================================

func TestBrowseDirectory_Root(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/browse?path=/", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "/", response["current_path"])
	assert.Nil(t, response["parent_path"])
}

func TestBrowseDirectory_DefaultsToRoot(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// No path parameter
	req, _ := http.NewRequest("GET", "/api/config/browse", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "/", response["current_path"])
}

func TestBrowseDirectory_TempDir(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create a temp directory with subdirectories
	tmpDir, err := os.MkdirTemp("", "healarr-browse-test-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create subdirectories
	os.Mkdir(filepath.Join(tmpDir, "subdir1"), 0755)
	os.Mkdir(filepath.Join(tmpDir, "subdir2"), 0755)
	os.Mkdir(filepath.Join(tmpDir, ".hidden"), 0755) // Hidden directory

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/browse?path="+tmpDir, nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, tmpDir, response["current_path"])

	entries := response["entries"].([]interface{})
	assert.Len(t, entries, 2) // Only non-hidden directories

	// Check entry structure
	entry := entries[0].(map[string]interface{})
	assert.Contains(t, entry, "name")
	assert.Contains(t, entry, "path")
	assert.Equal(t, true, entry["is_dir"])
}

func TestBrowseDirectory_NonExistent(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/browse?path=/nonexistent/path/12345", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Directory not found", response["error"])
}

func TestBrowseDirectory_File(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create a temp file
	tmpFile, err := os.CreateTemp("", "healarr-test-*")
	require.NoError(t, err)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Request a file path - should go to parent directory
	req, _ := http.NewRequest("GET", "/api/config/browse?path="+tmpFile.Name(), nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	// Should return parent directory
	assert.Equal(t, filepath.Dir(tmpFile.Name()), response["current_path"])
}

func TestBrowseDirectory_ParentPath(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/browse?path=/tmp", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "/tmp", response["current_path"])
	assert.Equal(t, "/", response["parent_path"])
}

// =============================================================================
// getDetectionPreview Tests
// =============================================================================

func TestGetDetectionPreview_FFprobe_Quick(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/detection-preview?method=ffprobe&mode=quick", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "ffprobe", response["method"])
	assert.Equal(t, "quick", response["mode"])
	assert.NotEmpty(t, response["command"])
	assert.NotEmpty(t, response["timeout"])
	assert.NotEmpty(t, response["mode_description"])
}

func TestGetDetectionPreview_FFprobe_Thorough(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/detection-preview?method=ffprobe&mode=thorough", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "thorough", response["mode"])
	assert.Contains(t, response["mode_description"], "Decodes the entire file")
}

func TestGetDetectionPreview_MediaInfo(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/detection-preview?method=mediainfo", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "mediainfo", response["method"])
}

func TestGetDetectionPreview_HandBrake(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/detection-preview?method=handbrake&mode=thorough", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "handbrake", response["method"])
	assert.Contains(t, response["mode_description"], "preview frames")
}

func TestGetDetectionPreview_ZeroByte(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/detection-preview?method=zero_byte", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "zero_byte", response["method"])
}

func TestGetDetectionPreview_InvalidMethod(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/detection-preview?method=invalid", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "invalid detection method", response["error"])
}

func TestGetDetectionPreview_WithCustomArgs(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/detection-preview?method=ffprobe&args=-v,error,-show_streams", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	// Command should contain the custom args
	command := response["command"].(string)
	assert.Contains(t, command, "-v")
}

func TestGetDetectionPreview_DefaultValues(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// No method or mode specified - should use defaults
	req, _ := http.NewRequest("GET", "/api/config/detection-preview", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "ffprobe", response["method"])
	assert.Equal(t, "quick", response["mode"])
}
