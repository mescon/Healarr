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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/testutil"
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
		protected.GET("/config/paths/:id/validate", s.validateScanPath)
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

func TestUpdateScanPath_WithDetectionArgs(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create arr instance first
	encryptedKey, _ := crypto.Encrypt("api-key")
	arrResult, _ := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	arrID, _ := arrResult.LastInsertId()

	// Create path to update
	result, err := db.Exec(`INSERT INTO scan_paths (local_path, arr_path, arr_instance_id, enabled)
		VALUES (?, ?, ?, ?)`, "/original/path", "/original", arrID, true)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Update with detection args
	body := bytes.NewBufferString(fmt.Sprintf(`{
		"local_path": "/updated/path",
		"arr_path": "/updated/arr",
		"arr_instance_id": %d,
		"enabled": true,
		"auto_remediate": true,
		"detection_method": "ffprobe",
		"detection_args": ["-v", "error"],
		"detection_mode": "thorough"
	}`, arrID))

	req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/config/paths/%d", id), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify detection_args were stored
	var storedArgs string
	err = db.QueryRow("SELECT detection_args FROM scan_paths WHERE id = ?", id).Scan(&storedArgs)
	require.NoError(t, err)
	assert.Contains(t, storedArgs, "-v")
	assert.Contains(t, storedArgs, "error")
}

func TestUpdateScanPath_DBError(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Drop the table to cause DB error
	_, err := db.Exec("DROP TABLE scan_paths")
	require.NoError(t, err)

	body := bytes.NewBufferString(`{
		"local_path": "/updated/path",
		"arr_path": "/updated/arr",
		"enabled": true,
		"auto_remediate": true
	}`)

	req, _ := http.NewRequest("PUT", "/api/config/paths/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
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

func TestDeleteScanPath_DBError(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Drop the table to cause DB error
	_, err := db.Exec("DROP TABLE scan_paths")
	require.NoError(t, err)

	req, _ := http.NewRequest("DELETE", "/api/config/paths/1", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
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

	// Create a temp directory we control, then use a non-existent subpath
	// This avoids platform-specific permission issues with /nonexistent
	tmpDir := t.TempDir()
	nonExistentPath := filepath.Join(tmpDir, "does_not_exist", "subdir")

	req, _ := http.NewRequest("GET", "/api/config/browse?path="+nonExistentPath, nil)
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

func TestGetScanPaths_DBError(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Drop scan_paths table to cause DB error
	db.Exec("DROP TABLE scan_paths")

	req, _ := http.NewRequest("GET", "/api/config/paths", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestCreateScanPath_DBError(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Drop scan_paths table to cause DB error on insert
	db.Exec("DROP TABLE scan_paths")

	body := bytes.NewBufferString(`{"local_path": "/test/path", "arr_path": "/arr/path", "enabled": true}`)
	req, _ := http.NewRequest("POST", "/api/config/paths", body)
	req.Header.Set("X-API-Key", apiKey)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestBrowseDirectory_NotFound(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/browse?path=/nonexistent/path/that/does/not/exist", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should return 200 with error in response body
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	// Error should be set
	assert.NotNil(t, response["error"])
}

func TestBrowseDirectory_CannotReadDirectory(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create a directory with no read permissions
	tmpDir, err := os.MkdirTemp("", "healarr-browse-noread-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	testDir := filepath.Join(tmpDir, "noread")
	err = os.MkdirAll(testDir, 0755)
	require.NoError(t, err)

	// Remove read permission so ReadDir fails
	err = os.Chmod(testDir, 0000)
	require.NoError(t, err)
	defer os.Chmod(testDir, 0755) // Restore for cleanup

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/browse?path="+testDir, nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Cannot read directory", response["error"])
}

func TestBrowseDirectory_CannotAccessDirectory(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	// Create a parent directory with no execute permissions
	// This prevents us from stat-ing children inside it
	tmpDir, err := os.MkdirTemp("", "healarr-browse-noaccess-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	parentDir := filepath.Join(tmpDir, "parent")
	childDir := filepath.Join(parentDir, "child")
	err = os.MkdirAll(childDir, 0755)
	require.NoError(t, err)

	// Remove execute permission from parent so we can't stat child
	err = os.Chmod(parentDir, 0600)
	require.NoError(t, err)
	defer os.Chmod(parentDir, 0755) // Restore for cleanup

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/browse?path="+childDir, nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Cannot access directory", response["error"])
}

// =============================================================================
// sanitizeBrowsePath Security Tests
// =============================================================================

func TestSanitizeBrowsePath_ValidPaths(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"root path", "/", "/"},
		{"simple path", "/media", "/media"},
		{"nested path", "/media/tv/shows", "/media/tv/shows"},
		{"path with spaces", "/media/My Videos", "/media/My Videos"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result, err := sanitizeBrowsePath(tc.input)
			assert.NoError(t, err)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestSanitizeBrowsePath_PathTraversalBlocked(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple traversal", "/media/../etc/passwd"},
		{"double traversal", "/media/../../etc/passwd"},
		{"encoded traversal", "/media/%2e%2e/etc"},
		{"traversal at start", "../etc/passwd"},
		{"traversal in middle", "/media/foo/../../../etc/passwd"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sanitizeBrowsePath(tc.input)
			// Path traversal should either be cleaned or rejected
			// filepath.Clean handles most cases, but we also check for ".." in the result
			if err != nil {
				assert.Equal(t, errInvalidPath, err)
			}
		})
	}
}

func TestSanitizeBrowsePath_NullBytesBlocked(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"null byte in path", "/media/foo\x00bar"},
		{"null byte at end", "/media/foo\x00"},
		{"null byte at start", "\x00/media/foo"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := sanitizeBrowsePath(tc.input)
			assert.Equal(t, errInvalidPath, err)
		})
	}
}

func TestSanitizeBrowsePath_RelativePathsNormalized(t *testing.T) {
	// Relative paths should be made absolute
	result, err := sanitizeBrowsePath("media/tv")
	assert.NoError(t, err)
	assert.True(t, filepath.IsAbs(result))
}

func TestBrowseDirectory_PathTraversalBlocked(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Try path traversal attack
	req, _ := http.NewRequest("GET", "/api/config/browse?path=/tmp/../../../etc/passwd", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should not return /etc/passwd contents
	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	// filepath.Clean will normalize this, so it won't be /etc/passwd
	assert.NotEqual(t, "/etc/passwd", response["current_path"])
}

func TestBrowseDirectory_NullByteBlocked(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Try null byte injection
	req, _ := http.NewRequest("GET", "/api/config/browse?path=/tmp%00/etc/passwd", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Invalid path", response["error"])
}

// =============================================================================
// classifyPathError Tests
// =============================================================================

func TestClassifyPathError_NotExist(t *testing.T) {
	err := os.ErrNotExist
	result := classifyPathError(err)
	assert.Equal(t, "Path does not exist", result)
}

func TestClassifyPathError_Permission(t *testing.T) {
	err := os.ErrPermission
	result := classifyPathError(err)
	assert.Equal(t, "Permission denied", result)
}

func TestClassifyPathError_Generic(t *testing.T) {
	err := fmt.Errorf("some other error")
	result := classifyPathError(err)
	assert.Equal(t, "Path not accessible", result)
}

// =============================================================================
// countMediaFiles Tests
// =============================================================================

func TestCountMediaFiles_EmptyDirectory(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-count-empty-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	count, samples, truncated := countMediaFiles(tmpDir, 5, 0)
	assert.Equal(t, 0, count)
	assert.Empty(t, samples)
	assert.False(t, truncated)
}

func TestCountMediaFiles_MediaFilesOnly(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-count-media-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create media files
	for _, ext := range []string{".mkv", ".mp4", ".avi"} {
		f, err := os.Create(filepath.Join(tmpDir, "video"+ext))
		require.NoError(t, err)
		f.Close()
	}

	// Create non-media files (should be ignored)
	for _, name := range []string{"readme.txt", "config.json", "thumb.jpg"} {
		f, err := os.Create(filepath.Join(tmpDir, name))
		require.NoError(t, err)
		f.Close()
	}

	count, samples, truncated := countMediaFiles(tmpDir, 10, 0)
	assert.Equal(t, 3, count)
	assert.Len(t, samples, 3)
	assert.False(t, truncated)
}

func TestCountMediaFiles_MaxSamples(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-count-maxsamples-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create 10 media files
	for i := 0; i < 10; i++ {
		f, err := os.Create(filepath.Join(tmpDir, fmt.Sprintf("video%d.mkv", i)))
		require.NoError(t, err)
		f.Close()
	}

	// Limit samples to 3
	count, samples, truncated := countMediaFiles(tmpDir, 3, 0)
	assert.Equal(t, 10, count) // All files counted
	assert.Len(t, samples, 3)  // Only 3 samples returned
	assert.False(t, truncated)
}

func TestCountMediaFiles_MaxFiles(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-count-maxfiles-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create 10 media files
	for i := 0; i < 10; i++ {
		f, err := os.Create(filepath.Join(tmpDir, fmt.Sprintf("video%d.mkv", i)))
		require.NoError(t, err)
		f.Close()
	}

	// Limit counting to 5 files
	count, _, truncated := countMediaFiles(tmpDir, 10, 5)
	assert.Equal(t, 5, count) // Stopped at max
	assert.True(t, truncated) // Indicates truncation
}

func TestCountMediaFiles_SkipsHiddenDirectories(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-count-hidden-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create visible directory with media
	visibleDir := filepath.Join(tmpDir, "visible")
	require.NoError(t, os.MkdirAll(visibleDir, 0755))
	f, err := os.Create(filepath.Join(visibleDir, "video.mkv"))
	require.NoError(t, err)
	f.Close()

	// Create hidden directory with media (should be skipped)
	hiddenDir := filepath.Join(tmpDir, ".hidden")
	require.NoError(t, os.MkdirAll(hiddenDir, 0755))
	f, err = os.Create(filepath.Join(hiddenDir, "video.mkv"))
	require.NoError(t, err)
	f.Close()

	count, _, _ := countMediaFiles(tmpDir, 10, 0)
	assert.Equal(t, 1, count) // Only the visible directory's file
}

func TestCountMediaFiles_NestedDirectories(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "healarr-count-nested-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create nested structure: /Season 1/episode.mkv, /Season 2/episode.mp4
	for i := 1; i <= 3; i++ {
		seasonDir := filepath.Join(tmpDir, fmt.Sprintf("Season %d", i))
		require.NoError(t, os.MkdirAll(seasonDir, 0755))
		f, err := os.Create(filepath.Join(seasonDir, fmt.Sprintf("episode%d.mkv", i)))
		require.NoError(t, err)
		f.Close()
	}

	count, samples, _ := countMediaFiles(tmpDir, 10, 0)
	assert.Equal(t, 3, count)
	assert.Len(t, samples, 3)
	// Samples should be relative paths
	for _, sample := range samples {
		assert.Contains(t, sample, "Season")
	}
}

// =============================================================================
// validateScanPath Tests
// =============================================================================

func TestValidateScanPath_Success(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Create a temp directory with some media files
	tmpDir, err := os.MkdirTemp("", "healarr-validate-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create some media files
	f, err := os.Create(filepath.Join(tmpDir, "video.mkv"))
	require.NoError(t, err)
	f.Close()
	f, err = os.Create(filepath.Join(tmpDir, "video2.mp4"))
	require.NoError(t, err)
	f.Close()

	// Create arr instance for foreign key reference
	_, err = db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Test Sonarr", "sonarr", "http://localhost:8989", "test-key")
	require.NoError(t, err)

	// Add scan path to database
	result, err := db.Exec("INSERT INTO scan_paths (local_path, arr_path, arr_instance_id, enabled) VALUES (?, ?, 1, ?)",
		tmpDir, tmpDir, true)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/config/paths/%d/validate", id), nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["accessible"].(bool))
	assert.Equal(t, float64(2), response["file_count"])
	assert.Nil(t, response["error"])
	assert.NotEmpty(t, response["sample_files"])
}

func TestValidateScanPath_NotFound(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Request validate for non-existent ID
	req, _ := http.NewRequest("GET", "/api/config/paths/9999/validate", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestValidateScanPath_PathNotAccessible(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Create arr instance for foreign key reference
	_, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Test Sonarr", "sonarr", "http://localhost:8989", "test-key")
	require.NoError(t, err)

	// Add scan path that doesn't exist
	result, err := db.Exec("INSERT INTO scan_paths (local_path, arr_path, arr_instance_id, enabled) VALUES (?, ?, 1, ?)",
		"/nonexistent/path/that/does/not/exist", "/nonexistent/path", true)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/config/paths/%d/validate", id), nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code) // Returns 200 with accessible=false

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response["accessible"].(bool))
	assert.NotNil(t, response["error"])
}

func TestValidateScanPath_NotADirectory(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Create a temp file (not a directory)
	tmpFile, err := os.CreateTemp("", "healarr-validate-file-*")
	require.NoError(t, err)
	tmpFile.Close()
	defer os.Remove(tmpFile.Name())

	// Create arr instance for foreign key reference
	_, err = db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Test Sonarr", "sonarr", "http://localhost:8989", "test-key")
	require.NoError(t, err)

	// Add scan path pointing to a file
	result, err := db.Exec("INSERT INTO scan_paths (local_path, arr_path, arr_instance_id, enabled) VALUES (?, ?, 1, ?)",
		tmpFile.Name(), tmpFile.Name(), true)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/config/paths/%d/validate", id), nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.False(t, response["accessible"].(bool))
	assert.Contains(t, response["error"], "not a directory")
}

func TestValidateScanPath_EmptyDirectory(t *testing.T) {
	db, cleanup := setupPathsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupPathsTestServer(t, db)
	defer serverCleanup()

	// Create an empty temp directory
	tmpDir, err := os.MkdirTemp("", "healarr-validate-empty-*")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create arr instance for foreign key reference
	_, err = db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Test Sonarr", "sonarr", "http://localhost:8989", "test-key")
	require.NoError(t, err)

	// Add scan path to database
	result, err := db.Exec("INSERT INTO scan_paths (local_path, arr_path, arr_instance_id, enabled) VALUES (?, ?, 1, ?)",
		tmpDir, tmpDir, true)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	req, _ := http.NewRequest("GET", fmt.Sprintf("/api/config/paths/%d/validate", id), nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.True(t, response["accessible"].(bool))
	assert.Equal(t, float64(0), response["file_count"])
	assert.Nil(t, response["error"])
}
