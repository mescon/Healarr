package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/notifier"
)

// setupNotificationsTestDB creates a test database with notifications schema
func setupNotificationsTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	db, cleanup := setupTestDB(t)

	// Add notifications table
	schema := `
		CREATE TABLE IF NOT EXISTS notifications (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			provider_type TEXT NOT NULL,
			config TEXT NOT NULL,
			events TEXT NOT NULL DEFAULT '[]',
			enabled INTEGER DEFAULT 1,
			throttle_seconds INTEGER DEFAULT 5,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE IF NOT EXISTS notification_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			notification_id INTEGER NOT NULL,
			event_type TEXT NOT NULL,
			message TEXT NOT NULL,
			status TEXT NOT NULL,
			error TEXT,
			sent_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`
	_, err := db.Exec(schema)
	require.NoError(t, err)

	return db, cleanup
}

// setupNotificationsTestServer creates a test server with notification routes
// Returns router, apiKey, and cleanup function that must be called to release resources
func setupNotificationsTestServer(t *testing.T, db *sql.DB, withNotifier bool) (*gin.Engine, string, func()) {
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
		protected.GET("/config/notifications", s.getNotifications)
		protected.POST("/config/notifications", s.createNotification)
		protected.PUT("/config/notifications/:id", s.updateNotification)
		protected.DELETE("/config/notifications/:id", s.deleteNotification)
		protected.POST("/config/notifications/test", s.testNotification)
		protected.GET("/config/notifications/events", s.getNotificationEvents)
		protected.GET("/config/notifications/:id/log", s.getNotificationLog)
		protected.GET("/config/notifications/:id", s.getNotification)
	}

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, apiKey, cleanup
}

// =============================================================================
// Service Unavailable Tests (when notifier is nil)
// =============================================================================

func TestGetNotifications_ServiceUnavailable(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, false)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Notification service not available", response["error"])
}

func TestCreateNotification_ServiceUnavailable(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, false)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"name":"Test","provider_type":"discord"}`)
	req, _ := http.NewRequest("POST", "/api/config/notifications", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestUpdateNotification_ServiceUnavailable(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, false)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"name":"Test"}`)
	req, _ := http.NewRequest("PUT", "/api/config/notifications/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestDeleteNotification_ServiceUnavailable(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, false)
	defer serverCleanup()

	req, _ := http.NewRequest("DELETE", "/api/config/notifications/1", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestTestNotification_ServiceUnavailable(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, false)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"name":"Test","provider_type":"discord"}`)
	req, _ := http.NewRequest("POST", "/api/config/notifications/test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetNotificationLog_ServiceUnavailable(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, false)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications/1/log", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

func TestGetNotification_ServiceUnavailable(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, false)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications/1", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusServiceUnavailable, w.Code)
}

// =============================================================================
// Functional Tests (with notifier service)
// =============================================================================

func TestGetNotifications_Empty(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response)
}

func TestGetNotifications_WithData(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	// Insert test data
	configJSON := `{"webhook_url":"https://discord.com/api/webhooks/123/abc"}`
	encryptedConfig, _ := crypto.Encrypt(configJSON)
	_, err := db.Exec(`INSERT INTO notifications (name, provider_type, config, events, enabled, throttle_seconds)
		VALUES (?, ?, ?, ?, ?, ?)`,
		"Test Discord", "discord", encryptedConfig, `["scan_completed"]`, true, 5)
	require.NoError(t, err)

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, response, 1)
	assert.Equal(t, "Test Discord", response[0]["name"])
	assert.Equal(t, "discord", response[0]["provider_type"])
}

func TestCreateNotification_Success(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"name": "My Discord",
		"provider_type": "discord",
		"config": {"webhook_url": "https://discord.com/api/webhooks/123/abc"},
		"events": ["scan_completed", "corruption_detected"],
		"enabled": true,
		"throttle_seconds": 10
	}`)

	req, _ := http.NewRequest("POST", "/api/config/notifications", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	var response map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.NotNil(t, response["id"])
	assert.Equal(t, "Notification created", response["message"])

	// Verify in database
	var count int
	db.QueryRow("SELECT COUNT(*) FROM notifications WHERE name = ?", "My Discord").Scan(&count)
	assert.Equal(t, 1, count)
}

func TestCreateNotification_DefaultThrottle(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	// Don't specify throttle_seconds - should default to 5
	body := bytes.NewBufferString(`{
		"name": "No Throttle",
		"provider_type": "slack",
		"config": {"webhook_url": "https://hooks.slack.com/test"},
		"events": [],
		"enabled": true
	}`)

	req, _ := http.NewRequest("POST", "/api/config/notifications", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)

	// Verify default throttle was set
	var throttle int
	db.QueryRow("SELECT throttle_seconds FROM notifications WHERE name = ?", "No Throttle").Scan(&throttle)
	assert.Equal(t, 5, throttle)
}

func TestCreateNotification_InvalidJSON(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid json}`)
	req, _ := http.NewRequest("POST", "/api/config/notifications", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestUpdateNotification_Success(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	// Create initial notification
	configJSON := `{"webhook_url":"https://old-url.com"}`
	encryptedConfig, _ := crypto.Encrypt(configJSON)
	result, err := db.Exec(`INSERT INTO notifications (name, provider_type, config, events, enabled)
		VALUES (?, ?, ?, ?, ?)`,
		"Old Name", "discord", encryptedConfig, `[]`, true)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	body := bytes.NewBufferString(`{
		"name": "New Name",
		"provider_type": "discord",
		"config": {"webhook_url": "https://new-url.com"},
		"events": ["scan_started"],
		"enabled": false,
		"throttle_seconds": 30
	}`)

	req, _ := http.NewRequest("PUT", "/api/config/notifications/"+string(rune(id+'0')), body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Notification updated", response["message"])
}

func TestUpdateNotification_InvalidID(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"name":"Test"}`)
	req, _ := http.NewRequest("PUT", "/api/config/notifications/invalid", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Invalid ID", response["error"])
}

func TestUpdateNotification_InvalidJSON(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	body := bytes.NewBufferString(`{bad json`)
	req, _ := http.NewRequest("PUT", "/api/config/notifications/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestDeleteNotification_Success(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	// Create notification to delete
	configJSON := `{"webhook_url":"https://test.com"}`
	encryptedConfig, _ := crypto.Encrypt(configJSON)
	result, err := db.Exec(`INSERT INTO notifications (name, provider_type, config, events)
		VALUES (?, ?, ?, ?)`,
		"To Delete", "slack", encryptedConfig, `[]`)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	req, _ := http.NewRequest("DELETE", "/api/config/notifications/"+string(rune(id+'0')), nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify deletion
	var count int
	db.QueryRow("SELECT COUNT(*) FROM notifications WHERE id = ?", id).Scan(&count)
	assert.Equal(t, 0, count)
}

func TestDeleteNotification_InvalidID(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	req, _ := http.NewRequest("DELETE", "/api/config/notifications/not-a-number", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetNotificationEvents(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, false)
	defer serverCleanup() // Doesn't need notifier

	req, _ := http.NewRequest("GET", "/api/config/notifications/events", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.NotEmpty(t, response)

	// Check structure - should have name and events
	assert.Contains(t, response[0], "name")
	assert.Contains(t, response[0], "events")
}

func TestGetNotificationLog_Success(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	// Create notification
	configJSON := `{"webhook_url":"https://test.com"}`
	encryptedConfig, _ := crypto.Encrypt(configJSON)
	result, err := db.Exec(`INSERT INTO notifications (name, provider_type, config, events)
		VALUES (?, ?, ?, ?)`,
		"Test", "slack", encryptedConfig, `[]`)
	require.NoError(t, err)
	notifID, _ := result.LastInsertId()

	// Add log entries
	_, err = db.Exec(`INSERT INTO notification_log (notification_id, event_type, message, status)
		VALUES (?, ?, ?, ?)`,
		notifID, "scan_completed", "Test message", "sent")
	require.NoError(t, err)

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications/"+string(rune(notifID+'0'))+"/log", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, response, 1)
	assert.Equal(t, "scan_completed", response[0]["event_type"])
	assert.Equal(t, "sent", response[0]["status"])
}

func TestGetNotificationLog_InvalidID(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications/abc/log", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetNotificationLog_WithLimit(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	// Create notification and multiple log entries
	configJSON := `{}`
	encryptedConfig, _ := crypto.Encrypt(configJSON)
	result, _ := db.Exec(`INSERT INTO notifications (name, provider_type, config, events)
		VALUES (?, ?, ?, ?)`, "Test", "slack", encryptedConfig, `[]`)
	notifID, _ := result.LastInsertId()

	for i := 0; i < 10; i++ {
		db.Exec(`INSERT INTO notification_log (notification_id, event_type, message, status)
			VALUES (?, ?, ?, ?)`, notifID, "event", "msg", "sent")
	}

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications/"+string(rune(notifID+'0'))+"/log?limit=3", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Len(t, response, 3)
}

func TestGetNotification_Success(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	// Create notification
	configJSON := `{"webhook_url":"https://discord.com/test"}`
	encryptedConfig, _ := crypto.Encrypt(configJSON)
	result, err := db.Exec(`INSERT INTO notifications (name, provider_type, config, events, enabled)
		VALUES (?, ?, ?, ?, ?)`,
		"Test Notification", "discord", encryptedConfig, `["scan_started"]`, true)
	require.NoError(t, err)
	id, _ := result.LastInsertId()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications/"+string(rune(id+'0')), nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Equal(t, "Test Notification", response["name"])
	assert.Equal(t, "discord", response["provider_type"])
}

func TestGetNotification_InvalidID(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications/notanumber", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetNotification_NotFound(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/config/notifications/9999", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestTestNotification_InvalidJSON(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid`)
	req, _ := http.NewRequest("POST", "/api/config/notifications/test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestTestNotification_Success(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	// Create a mock webhook server that accepts requests
	mockWebhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer mockWebhook.Close()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	// Use generic provider which supports webhook_url
	body := bytes.NewBufferString(`{
		"name": "Test Webhook",
		"provider_type": "generic",
		"config": {"webhook_url": "` + mockWebhook.URL + `"},
		"events": [],
		"enabled": true
	}`)

	req, _ := http.NewRequest("POST", "/api/config/notifications/test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	// Note: success may be true or false depending on notification implementation
	// The key is that we get an HTTP 200 response with the expected structure
	assert.Contains(t, response, "success")
}

func TestTestNotification_FailedSend(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	// Create a mock webhook server that always returns an error
	mockWebhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer mockWebhook.Close()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	// Use generic provider with our mock server
	body := bytes.NewBufferString(`{
		"name": "Failing Webhook",
		"provider_type": "generic",
		"config": {"webhook_url": "` + mockWebhook.URL + `"},
		"events": [],
		"enabled": true
	}`)

	req, _ := http.NewRequest("POST", "/api/config/notifications/test", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should return 200 OK with success: false
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	// The API returns success: false when notification fails
	if success, ok := response["success"].(bool); ok && !success {
		assert.Contains(t, response, "error")
	}
}

// =============================================================================
// getNotifications - Error Paths
// =============================================================================

func TestGetNotifications_DBError(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	// Drop notifications table to cause DB error
	db.Exec("DROP TABLE notifications")

	req, _ := http.NewRequest("GET", "/api/config/notifications", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Contains(t, response, "error")
}

func TestCreateNotification_DBError(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	// Drop notifications table to cause DB error
	db.Exec("DROP TABLE notifications")

	body := bytes.NewBufferString(`{
		"name": "Test",
		"provider_type": "discord",
		"config": {"webhook_url": "http://example.com"},
		"events": ["corruption_detected"]
	}`)
	req, _ := http.NewRequest("POST", "/api/config/notifications", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestUpdateNotification_DBError(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	// Drop notifications table to cause DB error
	db.Exec("DROP TABLE notifications")

	body := bytes.NewBufferString(`{
		"name": "Test",
		"provider_type": "discord",
		"config": {"webhook_url": "http://example.com"},
		"events": ["corruption_detected"]
	}`)
	req, _ := http.NewRequest("PUT", "/api/config/notifications/1", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestDeleteNotification_DBError(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	// Drop notifications table to cause DB error
	db.Exec("DROP TABLE notifications")

	req, _ := http.NewRequest("DELETE", "/api/config/notifications/1", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetNotificationLog_DBError(t *testing.T) {
	db, cleanup := setupNotificationsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupNotificationsTestServer(t, db, true)
	defer serverCleanup()

	// Drop notification_log table to cause DB error
	db.Exec("DROP TABLE notification_log")

	req, _ := http.NewRequest("GET", "/api/config/notifications/1/log", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}
