package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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
