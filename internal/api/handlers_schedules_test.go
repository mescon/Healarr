package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupSchedulesTestDB creates a test database with schedules schema
func setupSchedulesTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	db, cleanup := setupPathsTestDB(t)

	// Add schedules table
	schema := `
		CREATE TABLE IF NOT EXISTS scan_schedules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_path_id INTEGER NOT NULL REFERENCES scan_paths(id) ON DELETE CASCADE,
			cron_expression TEXT NOT NULL,
			enabled INTEGER DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`
	_, err := db.Exec(schema)
	require.NoError(t, err)

	return db, cleanup
}

// setupSchedulesTestServer creates a test server with schedule routes
// Returns router, apiKey, and cleanup function that must be called to release resources
func setupSchedulesTestServer(t *testing.T, db *sql.DB, scheduler *testutil.MockSchedulerService) (*gin.Engine, string, func()) {
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
		scheduler: scheduler,
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
		protected.GET("/config/schedules", s.getSchedules)
		protected.POST("/config/schedules", s.addSchedule)
		protected.PUT("/config/schedules/:id", s.updateSchedule)
		protected.DELETE("/config/schedules/:id", s.deleteSchedule)
	}

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, apiKey, cleanup
}

// createTestPathWithSchedule helper to create arr instance, path, and optionally schedule
func createTestPathWithSchedule(t *testing.T, db *sql.DB, withSchedule bool) (arrID, pathID, scheduleID int64) {
	// Create arr instance
	encryptedKey, _ := crypto.Encrypt("api-key")
	result, _ := db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES (?, ?, ?, ?)",
		"Sonarr", "sonarr", "http://localhost:8989", encryptedKey)
	arrID, _ = result.LastInsertId()

	// Create scan path
	result, _ = db.Exec(`INSERT INTO scan_paths (local_path, arr_path, arr_instance_id, enabled)
		VALUES (?, ?, ?, ?)`, "/media/tv", "/tv", arrID, true)
	pathID, _ = result.LastInsertId()

	if withSchedule {
		result, _ = db.Exec(`INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled)
			VALUES (?, ?, ?)`, pathID, "0 0 * * *", true)
		scheduleID, _ = result.LastInsertId()
	}

	return
}

// =============================================================================
// getSchedules Tests
// =============================================================================

func TestGetSchedules_Empty(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	mockScheduler := &testutil.MockSchedulerService{}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/schedules", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response)
}

func TestGetSchedules_WithData(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	// Create test data
	_, _, _ = createTestPathWithSchedule(t, db, true)

	mockScheduler := &testutil.MockSchedulerService{}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/schedules", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, response, 1)
	assert.Equal(t, "/media/tv", response[0]["local_path"])
	assert.Equal(t, "0 0 * * *", response[0]["cron_expression"])
	assert.Equal(t, true, response[0]["enabled"])
}

// =============================================================================
// addSchedule Tests
// =============================================================================

func TestAddSchedule_Success(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	// Create test path
	_, pathID, _ := createTestPathWithSchedule(t, db, false)

	mockScheduler := &testutil.MockSchedulerService{
		AddScheduleFunc: func(scanPathID int, cronExpr string) (int64, error) {
			return 1, nil
		},
	}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	body := bytes.NewBufferString(fmt.Sprintf(`{
		"scan_path_id": %d,
		"cron_expression": "0 2 * * *"
	}`, pathID))

	req, _ := http.NewRequest("POST", "/api/config/schedules", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, float64(1), response["id"])
	assert.Equal(t, "Schedule added", response["message"])

	// Verify scheduler was called
	assert.Equal(t, 1, mockScheduler.CallCount("AddSchedule"))
}

func TestAddSchedule_ServiceError(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	mockScheduler := &testutil.MockSchedulerService{
		AddScheduleFunc: func(scanPathID int, cronExpr string) (int64, error) {
			return 0, errors.New("invalid cron expression")
		},
	}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"scan_path_id": 1,
		"cron_expression": "invalid"
	}`)

	req, _ := http.NewRequest("POST", "/api/config/schedules", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response["error"], "invalid cron expression")
}

func TestAddSchedule_InvalidJSON(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	mockScheduler := &testutil.MockSchedulerService{}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid}`)

	req, _ := http.NewRequest("POST", "/api/config/schedules", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// =============================================================================
// updateSchedule Tests
// =============================================================================

func TestUpdateSchedule_Success(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	mockScheduler := &testutil.MockSchedulerService{
		UpdateScheduleFunc: func(id int, cronExpr string, enabled bool) error {
			return nil
		},
	}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"cron_expression": "0 3 * * *",
		"enabled": false
	}`)

	req, _ := http.NewRequest("PUT", "/api/config/schedules/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Schedule updated", response["message"])

	// Verify scheduler was called
	assert.Equal(t, 1, mockScheduler.CallCount("UpdateSchedule"))
}

func TestUpdateSchedule_DefaultEnabled(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	var capturedEnabled bool
	mockScheduler := &testutil.MockSchedulerService{
		UpdateScheduleFunc: func(id int, cronExpr string, enabled bool) error {
			capturedEnabled = enabled
			return nil
		},
	}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	// Don't include enabled - should default to true
	body := bytes.NewBufferString(`{
		"cron_expression": "0 4 * * *"
	}`)

	req, _ := http.NewRequest("PUT", "/api/config/schedules/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, capturedEnabled)
}

func TestUpdateSchedule_InvalidID(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	mockScheduler := &testutil.MockSchedulerService{}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"cron_expression": "0 0 * * *"}`)

	req, _ := http.NewRequest("PUT", "/api/config/schedules/invalid", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Invalid ID", response["error"])
}

func TestUpdateSchedule_InvalidJSON(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	mockScheduler := &testutil.MockSchedulerService{}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	body := bytes.NewBufferString(`{bad json}`)

	req, _ := http.NewRequest("PUT", "/api/config/schedules/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateSchedule_ServiceError(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	mockScheduler := &testutil.MockSchedulerService{
		UpdateScheduleFunc: func(id int, cronExpr string, enabled bool) error {
			return errors.New("schedule not found")
		},
	}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"cron_expression": "0 0 * * *",
		"enabled": true
	}`)

	req, _ := http.NewRequest("PUT", "/api/config/schedules/999", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// =============================================================================
// deleteSchedule Tests
// =============================================================================

func TestDeleteSchedule_Success(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	mockScheduler := &testutil.MockSchedulerService{
		DeleteScheduleFunc: func(id int) error {
			return nil
		},
	}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	req, _ := http.NewRequest("DELETE", "/api/config/schedules/1", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Schedule deleted", response["message"])

	// Verify scheduler was called
	assert.Equal(t, 1, mockScheduler.CallCount("DeleteSchedule"))
}

func TestDeleteSchedule_InvalidID(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	mockScheduler := &testutil.MockSchedulerService{}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	req, _ := http.NewRequest("DELETE", "/api/config/schedules/notanumber", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Invalid ID", response["error"])
}

func TestDeleteSchedule_ServiceError(t *testing.T) {
	db, cleanup := setupSchedulesTestDB(t)
	defer cleanup()

	mockScheduler := &testutil.MockSchedulerService{
		DeleteScheduleFunc: func(id int) error {
			return errors.New("schedule not found")
		},
	}
	router, apiKey, serverCleanup := setupSchedulesTestServer(t, db, mockScheduler)
	defer serverCleanup()

	req, _ := http.NewRequest("DELETE", "/api/config/schedules/999", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
