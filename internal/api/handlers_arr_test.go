package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
)

// mockArrClient is a mock implementation of integration.ArrClient for testing
type mockArrClient struct {
	rootFolders      []integration.RootFolder
	rootFoldersError error
}

func (m *mockArrClient) FindMediaByPath(_ string) (int64, error) {
	return 0, nil
}

func (m *mockArrClient) DeleteFile(_ int64, _ string) (map[string]interface{}, error) {
	return nil, nil
}

func (m *mockArrClient) GetFilePath(_ int64, _ map[string]interface{}, _ string) (string, error) {
	return "", nil
}

func (m *mockArrClient) GetAllFilePaths(_ int64, _ map[string]interface{}, _ string) ([]string, error) {
	return nil, nil
}

func (m *mockArrClient) TriggerSearch(_ int64, _ string, _ []int64) error {
	return nil
}

func (m *mockArrClient) GetAllInstances() ([]*integration.ArrInstanceInfo, error) {
	return nil, nil
}

func (m *mockArrClient) GetInstanceByID(_ int64) (*integration.ArrInstanceInfo, error) {
	return nil, nil
}

func (m *mockArrClient) CheckInstanceHealth(_ int64) error {
	return nil
}

func (m *mockArrClient) GetRootFolders(_ int64) ([]integration.RootFolder, error) {
	if m.rootFoldersError != nil {
		return nil, m.rootFoldersError
	}
	return m.rootFolders, nil
}

func (m *mockArrClient) GetQueueForPath(_ string) ([]integration.QueueItemInfo, error) {
	return nil, nil
}

func (m *mockArrClient) FindQueueItemsByMediaIDForPath(_ string, _ int64) ([]integration.QueueItemInfo, error) {
	return nil, nil
}

func (m *mockArrClient) GetDownloadStatusForPath(_, _ string) (status string, progress float64, errMsg string, err error) {
	return "", 0, "", nil
}

func (m *mockArrClient) GetRecentHistoryForMediaByPath(_ string, _ int64, _ int) ([]integration.HistoryItemInfo, error) {
	return nil, nil
}

func (m *mockArrClient) RemoveFromQueueByPath(_ string, _ int64, _, _ bool) error {
	return nil
}

func (m *mockArrClient) RefreshMonitoredDownloadsByPath(_ string) error {
	return nil
}

func (m *mockArrClient) GetMediaDetails(_ int64, _ string) (*integration.MediaDetails, error) {
	return nil, nil
}

// setupArrTestServer creates a test server with arr routes and authentication
// Returns router, apiKey, and cleanup function that must be called to release resources
func setupArrTestServer(t *testing.T, db *sql.DB) (*gin.Engine, string, func()) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	hub := NewWebSocketHub(eb)

	s := &RESTServer{
		router:   r,
		db:       db,
		eventBus: eb,
		hub:      hub,
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
		protected.GET("/config/arr", s.getArrInstances)
		protected.POST("/config/arr", s.createArrInstance)
		protected.POST("/config/arr/test", s.testArrConnection)
		protected.PUT("/config/arr/:id", s.updateArrInstance)
		protected.DELETE("/config/arr/:id", s.deleteArrInstance)
	}

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, apiKey, cleanup
}

func TestGetArrInstances_Empty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/arr", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response)
}

func TestGetArrInstances_WithData(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Insert test data with encrypted API key
	testAPIKey := "test-arr-api-key-12345"
	encryptedKey, err := crypto.Encrypt(testAPIKey)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
		"Test Sonarr", "sonarr", "http://localhost:8989", encryptedKey, true)
	require.NoError(t, err)

	req, _ := http.NewRequest("GET", "/api/config/arr", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, response, 1)
	assert.Equal(t, "Test Sonarr", response[0]["name"])
	assert.Equal(t, "sonarr", response[0]["type"])
	assert.Equal(t, "http://localhost:8989", response[0]["url"])
	assert.Equal(t, testAPIKey, response[0]["api_key"]) // Should be decrypted
	assert.Equal(t, true, response[0]["enabled"])
}

func TestGetArrInstances_PlaintextAPIKey(t *testing.T) {
	// When encryption is disabled (no HEALARR_ENCRYPTION_KEY), API keys are stored as plaintext
	// and returned as-is for backwards compatibility
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Insert test data with plaintext API key (no enc:v1: prefix)
	// When encryption is disabled, this is the expected behavior
	_, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
		"Test Sonarr", "sonarr", "http://localhost:8989", "plaintext-api-key", true)
	require.NoError(t, err)

	req, _ := http.NewRequest("GET", "/api/config/arr", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, response, 1)
	// When encryption is disabled, plaintext values are returned as-is
	assert.Equal(t, "plaintext-api-key", response[0]["api_key"])
}

func TestGetArrInstances_Unauthorized(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, _, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/arr", nil)
	// No API key provided
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestCreateArrInstance_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"name": "My Sonarr",
		"type": "sonarr",
		"url": "http://localhost:8989",
		"api_key": "my-secret-api-key",
		"enabled": true
	}`)

	req, _ := http.NewRequest("POST", "/api/config/arr", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	// Verify it was created in the database
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM arr_instances WHERE name = ?", "My Sonarr").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 1, count)

	// Verify the API key was processed (encrypted or stored based on encryption config)
	var storedKey string
	err = db.QueryRow("SELECT api_key FROM arr_instances WHERE name = ?", "My Sonarr").Scan(&storedKey)
	assert.NoError(t, err)

	// When decrypted, should match original
	decrypted, err := crypto.Decrypt(storedKey)
	assert.NoError(t, err)
	assert.Equal(t, "my-secret-api-key", decrypted)
}

func TestCreateArrInstance_InvalidJSON(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid json}`)

	req, _ := http.NewRequest("POST", "/api/config/arr", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestCreateArrInstance_MultipleInstances(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	instances := []struct {
		name    string
		arrType string
		url     string
	}{
		{"Sonarr", "sonarr", "http://localhost:8989"},
		{"Radarr", "radarr", "http://localhost:7878"},
	}

	for _, inst := range instances {
		body := bytes.NewBufferString(`{
			"name": "` + inst.name + `",
			"type": "` + inst.arrType + `",
			"url": "` + inst.url + `",
			"api_key": "api-key-123",
			"enabled": true
		}`)

		req, _ := http.NewRequest("POST", "/api/config/arr", body)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", apiKey)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusCreated, w.Code)
	}

	// Verify both were created
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM arr_instances").Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 2, count)
}

func TestCreateArrInstance_DBError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Drop the table to cause DB error
	_, err := db.Exec("DROP TABLE arr_instances")
	require.NoError(t, err)

	body := bytes.NewBufferString(`{
		"name": "Test",
		"type": "sonarr",
		"url": "http://localhost:8989",
		"api_key": "api-key-123",
		"enabled": true
	}`)

	req, _ := http.NewRequest("POST", "/api/config/arr", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateArrInstance_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Create initial instance
	encryptedKey, _ := crypto.Encrypt("old-api-key")
	result, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
		"Old Name", "sonarr", "http://old-url:8989", encryptedKey, true)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	body := bytes.NewBufferString(`{
		"name": "New Name",
		"type": "sonarr",
		"url": "http://new-url:8989",
		"api_key": "new-api-key",
		"enabled": false
	}`)

	req, _ := http.NewRequest("PUT", "/api/config/arr/"+string(rune(id+'0')), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify the update
	var name, url string
	var enabled bool
	err = db.QueryRow("SELECT name, url, enabled FROM arr_instances WHERE id = ?", id).Scan(&name, &url, &enabled)
	assert.NoError(t, err)
	assert.Equal(t, "New Name", name)
	assert.Equal(t, "http://new-url:8989", url)
	assert.False(t, enabled)
}

func TestUpdateArrInstance_InvalidJSON(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{not valid json}`)

	req, _ := http.NewRequest("PUT", "/api/config/arr/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteArrInstance_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Create instance to delete
	encryptedKey, _ := crypto.Encrypt("api-key")
	result, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
		"To Delete", "sonarr", "http://localhost:8989", encryptedKey, true)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	req, _ := http.NewRequest("DELETE", "/api/config/arr/"+string(rune(id+'0')), nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify deletion
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM arr_instances WHERE id = ?", id).Scan(&count)
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestDeleteArrInstance_CascadeDeletesScanPaths(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Create instance
	encryptedKey, _ := crypto.Encrypt("api-key")
	result, err := db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
		"Instance", "sonarr", "http://localhost:8989", encryptedKey, true)
	require.NoError(t, err)
	instanceID, _ := result.LastInsertId()

	// Create associated scan path
	_, err = db.Exec("INSERT INTO scan_paths (local_path, arr_path, arr_instance_id, enabled) VALUES (?, ?, ?, ?)",
		"/media/tv", "/tv", instanceID, true)
	require.NoError(t, err)

	// Verify scan path exists
	var scanPathCount int
	db.QueryRow("SELECT COUNT(*) FROM scan_paths WHERE arr_instance_id = ?", instanceID).Scan(&scanPathCount)
	assert.Equal(t, 1, scanPathCount)

	// Delete the instance
	req, _ := http.NewRequest("DELETE", "/api/config/arr/"+string(rune(instanceID+'0')), nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)

	// Verify cascade delete of scan paths
	db.QueryRow("SELECT COUNT(*) FROM scan_paths WHERE arr_instance_id = ?", instanceID).Scan(&scanPathCount)
	assert.Equal(t, 0, scanPathCount)
}

func TestDeleteArrInstance_DBError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Drop the table to cause DB error
	_, err := db.Exec("DROP TABLE arr_instances")
	require.NoError(t, err)

	req, _ := http.NewRequest("DELETE", "/api/config/arr/1", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateArrInstance_DBError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Drop the table to cause DB error
	_, err := db.Exec("DROP TABLE arr_instances")
	require.NoError(t, err)

	body := bytes.NewBufferString(`{
		"name": "Updated",
		"type": "sonarr",
		"url": "http://localhost:8989",
		"api_key": "api-key-123",
		"enabled": true
	}`)

	req, _ := http.NewRequest("PUT", "/api/config/arr/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestTestArrConnection_Success(t *testing.T) {
	// Create a mock arr server
	mockArr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/system/status" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"appName":"Sonarr","version":"3.0.0"}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockArr.Close()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"url": "` + mockArr.URL + `",
		"api_key": "test-api-key"
	}`)

	req, _ := http.NewRequest("POST", "/api/config/arr/test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, true, response["success"])
	assert.Equal(t, "Connection successful", response["message"])
}

func TestTestArrConnection_TrailingSlash(t *testing.T) {
	// Create a mock arr server
	mockArr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/system/status" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte(`{"appName":"Sonarr"}`))
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer mockArr.Close()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// URL with trailing slash
	body := bytes.NewBufferString(`{
		"url": "` + mockArr.URL + `/",
		"api_key": "test-api-key"
	}`)

	req, _ := http.NewRequest("POST", "/api/config/arr/test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, true, response["success"])
}

func TestTestArrConnection_Failure_ConnectionError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"url": "http://localhost:59999",
		"api_key": "test-api-key"
	}`)

	req, _ := http.NewRequest("POST", "/api/config/arr/test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, false, response["success"])
	assert.Contains(t, response["error"], "Connection failed")
}

func TestTestArrConnection_Failure_BadStatus(t *testing.T) {
	// Create a mock arr server that returns 401
	mockArr := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer mockArr.Close()

	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"url": "` + mockArr.URL + `",
		"api_key": "wrong-api-key"
	}`)

	req, _ := http.NewRequest("POST", "/api/config/arr/test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, false, response["success"])
	assert.Contains(t, response["error"], "Server returned status 401")
}

func TestTestArrConnection_InvalidJSON(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid}`)

	req, _ := http.NewRequest("POST", "/api/config/arr/test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetArrInstances_DBError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Drop the arr_instances table to cause DB error
	db.Exec("DROP TABLE arr_instances")

	req, _ := http.NewRequest("GET", "/api/config/arr", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetArrInstances_DecryptError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Insert a row with an encrypted-looking value that will fail to decrypt
	// The enc:v1: prefix makes crypto.Decrypt try to decode it, but "invalid" is not valid base64
	_, err := db.Exec(`
		INSERT INTO arr_instances (name, type, url, api_key, enabled)
		VALUES ('test', 'sonarr', 'http://test', 'enc:v1:not-valid-base64!!!', 1)
	`)
	assert.NoError(t, err)

	req, _ := http.NewRequest("GET", "/api/config/arr", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should still return 200 (continues on decrypt error)
	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, response, 1)
	// Check that the decryption error was handled gracefully
	assert.Equal(t, "[DECRYPTION_ERROR]", response[0]["api_key"])
}

func TestUpdateArrInstance_NotFound(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"name": "updated", "type": "sonarr", "url": "http://test", "api_key": "test"}`)

	req, _ := http.NewRequest("PUT", "/api/config/arr/999", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Row not found means RowsAffected == 0, but the code returns success regardless
	// Let's check what the actual behavior is
	assert.Equal(t, http.StatusOK, w.Code)
}

// setupArrTestServerWithClient creates a test server with arr routes including root folders
// Takes a mock ArrClient for testing the root folders endpoint
func setupArrTestServerWithClient(t *testing.T, db *sql.DB, arrClient integration.ArrClient) (*gin.Engine, string, func()) {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	hub := NewWebSocketHub(eb)

	s := &RESTServer{
		router:    r,
		db:        db,
		eventBus:  eb,
		hub:       hub,
		arrClient: arrClient,
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
		protected.GET("/config/arr/:id/rootfolders", s.getArrRootFolders)
	}

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, apiKey, cleanup
}

func TestGetArrRootFolders_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mockClient := &mockArrClient{
		rootFolders: []integration.RootFolder{
			{ID: 1, Path: "/data/media/Movies", FreeSpace: 1000000000, TotalSpace: 2000000000},
			{ID: 2, Path: "/data/media/TV", FreeSpace: 500000000, TotalSpace: 1000000000},
		},
	}

	router, apiKey, serverCleanup := setupArrTestServerWithClient(t, db, mockClient)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/arr/1/rootfolders", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, response, 2)

	// Check first folder
	assert.Equal(t, float64(1), response[0]["id"])
	assert.Equal(t, "/data/media/Movies", response[0]["path"])
	assert.Equal(t, float64(1000000000), response[0]["free_space"])
	assert.Equal(t, float64(2000000000), response[0]["total_space"])

	// Check second folder
	assert.Equal(t, float64(2), response[1]["id"])
	assert.Equal(t, "/data/media/TV", response[1]["path"])
}

func TestGetArrRootFolders_Empty(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mockClient := &mockArrClient{
		rootFolders: []integration.RootFolder{},
	}

	router, apiKey, serverCleanup := setupArrTestServerWithClient(t, db, mockClient)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/arr/1/rootfolders", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response)
}

func TestGetArrRootFolders_ClientError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mockClient := &mockArrClient{
		rootFoldersError: errors.New("connection refused"),
	}

	router, apiKey, serverCleanup := setupArrTestServerWithClient(t, db, mockClient)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/arr/1/rootfolders", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Contains(t, response["error"], "connection refused")
}

func TestGetArrRootFolders_InvalidInstanceID(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mockClient := &mockArrClient{}

	router, apiKey, serverCleanup := setupArrTestServerWithClient(t, db, mockClient)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/arr/invalid/rootfolders", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "Invalid instance ID", response["error"])
}

func TestGetArrRootFolders_NoClient(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Create server without arrClient
	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	hub := NewWebSocketHub(eb)

	s := &RESTServer{
		router:    r,
		db:        db,
		eventBus:  eb,
		hub:       hub,
		arrClient: nil, // No client
	}

	// Setup API key
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)

	api := r.Group("/api")
	protected := api.Group("")
	protected.Use(s.authMiddleware())
	protected.GET("/config/arr/:id/rootfolders", s.getArrRootFolders)

	defer func() {
		hub.Shutdown()
		eb.Shutdown()
	}()

	req, _ := http.NewRequest("GET", "/api/config/arr/1/rootfolders", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "Arr client not available", response["error"])
}

func TestGetArrRootFolders_Unauthorized(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	mockClient := &mockArrClient{}

	router, _, serverCleanup := setupArrTestServerWithClient(t, db, mockClient)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/arr/1/rootfolders", nil)
	// No API key
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// =============================================================================
// validateArrURL Security Tests (SSRF Prevention)
// =============================================================================

func TestValidateArrURL_ValidURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"http localhost", "http://localhost:8989"},
		{"http ip", "http://192.168.1.1:8989"},
		{"https hostname", "https://sonarr.example.com"},
		{"https with path", "https://media.example.com/sonarr"},
		{"http with port", "http://sonarr.local:8989"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArrURL(tc.url)
			assert.NoError(t, err)
		})
	}
}

func TestValidateArrURL_BlocksSSRFSchemes(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"file scheme", "file:///etc/passwd"},
		{"gopher scheme", "gopher://localhost:70/_Hello"},
		{"dict scheme", "dict://localhost:11111/info"},
		{"ftp scheme", "ftp://localhost/file"},
		{"ldap scheme", "ldap://localhost:389"},
		{"javascript scheme", "javascript:alert(1)"},
		{"data scheme", "data:text/plain,hello"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArrURL(tc.url)
			assert.Error(t, err)
			assert.Equal(t, errInvalidURLScheme, err)
		})
	}
}

func TestValidateArrURL_BlocksEmptyHost(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"empty host", "http:///api"},
		{"no host https", "https:///secret"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArrURL(tc.url)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "host")
		})
	}
}

func TestValidateArrURL_BlocksInvalidURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"completely invalid", "not-a-url"},
		{"missing scheme", "localhost:8989"},
		{"malformed url", "http://[invalid"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateArrURL(tc.url)
			assert.Error(t, err)
		})
	}
}

func TestCreateArrInstance_SSRFBlocked(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Try to create an instance with file:// URL (SSRF attempt)
	body := bytes.NewBufferString(`{
		"name": "Malicious",
		"type": "sonarr",
		"url": "file:///etc/passwd",
		"api_key": "api-key-123",
		"enabled": true
	}`)

	req, _ := http.NewRequest("POST", "/api/config/arr", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response["error"], "Invalid URL")
}

func TestUpdateArrInstance_SSRFBlocked(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Create a valid instance first
	encryptedKey, _ := crypto.Encrypt("api-key")
	result, _ := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Test", "sonarr", "http://localhost:8989", encryptedKey)
	id, _ := result.LastInsertId()

	// Try to update with a gopher:// URL (SSRF attempt)
	body := bytes.NewBufferString(`{
		"name": "Updated",
		"type": "sonarr",
		"url": "gopher://localhost:70/_test",
		"api_key": "api-key-123",
		"enabled": true
	}`)

	req, _ := http.NewRequest("PUT", "/api/config/arr/"+string(rune(id+'0')), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response["error"], "Invalid URL")
}

func TestTestArrConnection_SSRFBlocked(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupArrTestServer(t, db)
	defer serverCleanup()

	// Try to test with a file:// URL (SSRF attempt)
	body := bytes.NewBufferString(`{
		"url": "file:///etc/passwd",
		"api_key": "test-api-key"
	}`)

	req, _ := http.NewRequest("POST", "/api/config/arr/test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Invalid URL should return 400 Bad Request (not 200 with success=false)
	// because the URL validation fails before any connection attempt
	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, false, response["success"])
	assert.Contains(t, response["error"], "Invalid URL")
}
