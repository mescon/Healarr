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

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	_ "modernc.org/sqlite"
)

// setupTestDB creates a temporary database with schema for testing
func setupTestDB(t *testing.T) (*sql.DB, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "healarr-api-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to open database: %v", err)
	}

	// Configure SQLite
	db.SetMaxOpenConns(1)
	if _, err := db.Exec("PRAGMA foreign_keys=ON"); err != nil {
		db.Close()
		os.RemoveAll(tmpDir)
		t.Fatalf("Failed to enable foreign keys: %v", err)
	}

	// Create schema
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
			arr_instance_id INTEGER NOT NULL REFERENCES arr_instances(id) ON DELETE CASCADE,
			enabled INTEGER DEFAULT 1,
			auto_remediate INTEGER DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE VIEW corruption_status AS
		SELECT 'CorruptionDetected' as current_state, 0 as count;
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

// setupTestServer creates a minimal test server
// Returns router and cleanup function that must be called to release resources
func setupTestServer(t *testing.T, db *sql.DB) (*gin.Engine, func()) {
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

	// Register routes manually for testing
	api := r.Group("/api")
	api.POST("/auth/setup", s.handleAuthSetup)
	api.POST("/auth/login", s.handleLogin)
	api.GET("/auth/status", s.handleAuthStatus)

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, cleanup
}

func TestHandleAuthStatus_NotSetup(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, serverCleanup := setupTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/auth/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response["is_setup"] != false {
		t.Errorf("Expected is_setup=false, got %v", response["is_setup"])
	}
}

func TestHandleAuthSetup_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, serverCleanup := setupTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"password":"testpassword123"}`)
	req, _ := http.NewRequest("POST", "/api/auth/setup", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatalf("Failed to unmarshal response: %v", err)
	}

	if response["message"] != "Setup complete" {
		t.Errorf("Expected message 'Setup complete', got %v", response["message"])
	}

	if response["token"] == nil || response["token"] == "" {
		t.Error("Expected non-empty token")
	}
}

func TestHandleAuthSetup_PasswordTooShort(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, serverCleanup := setupTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"password":"short"}`)
	req, _ := http.NewRequest("POST", "/api/auth/setup", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["error"] != "Password must be at least 8 characters" {
		t.Errorf("Expected password length error, got %v", response["error"])
	}
}

func TestHandleAuthSetup_AlreadySetup(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Pre-setup: Insert existing password
	hash, _ := auth.HashPassword("existingpass")
	db.Exec("INSERT INTO settings (key, value) VALUES ('password_hash', ?)", hash)

	router, serverCleanup := setupTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"password":"newpassword123"}`)
	req, _ := http.NewRequest("POST", "/api/auth/setup", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["error"] != "Setup already completed" {
		t.Errorf("Expected 'Setup already completed' error, got %v", response["error"])
	}
}

func TestHandleAuthSetup_InvalidJSON(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, serverCleanup := setupTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid json}`)
	req, _ := http.NewRequest("POST", "/api/auth/setup", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestHandleLogin_InvalidJSON(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Setup auth first
	hash, _ := auth.HashPassword("testpassword123")
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('password_hash', ?), ('api_key', ?)", hash, encryptedKey)

	router, serverCleanup := setupTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{not valid json}`)
	req, _ := http.NewRequest("POST", "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestHandleLogin_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Setup auth first
	password := "testpassword123"
	hash, _ := auth.HashPassword(password)
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('password_hash', ?), ('api_key', ?)", hash, encryptedKey)

	router, serverCleanup := setupTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"password":"testpassword123"}`)
	req, _ := http.NewRequest("POST", "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["token"] != apiKey {
		t.Errorf("Expected token to match API key")
	}
}

func TestHandleLogin_InvalidPassword(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Setup auth
	hash, _ := auth.HashPassword("correctpassword")
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('password_hash', ?), ('api_key', ?)", hash, encryptedKey)

	router, serverCleanup := setupTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"password":"wrongpassword"}`)
	req, _ := http.NewRequest("POST", "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["error"] != "Invalid password" {
		t.Errorf("Expected 'Invalid password' error, got %v", response["error"])
	}
}

func TestHandleLogin_NotSetup(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, serverCleanup := setupTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"password":"anypassword"}`)
	req, _ := http.NewRequest("POST", "/api/auth/login", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["error"] != "Setup required" {
		t.Errorf("Expected 'Setup required' error, got %v", response["error"])
	}
}

func TestHandleAuthStatus_IsSetup(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	// Pre-setup
	hash, _ := auth.HashPassword("password123")
	db.Exec("INSERT INTO settings (key, value) VALUES ('password_hash', ?)", hash)

	router, serverCleanup := setupTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/auth/status", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["is_setup"] != true {
		t.Errorf("Expected is_setup=true, got %v", response["is_setup"])
	}
}

func TestAuthMiddleware_NoToken(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()
	s := &RESTServer{
		router:   r,
		db:       db,
		eventBus: eb,
	}

	// Setup API key
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)

	protected := r.Group("/api")
	protected.Use(s.authMiddleware())
	protected.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest("GET", "/api/protected", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()
	s := &RESTServer{
		router:   r,
		db:       db,
		eventBus: eb,
	}

	// Setup API key
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)

	protected := r.Group("/api")
	protected.Use(s.authMiddleware())
	protected.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest("GET", "/api/protected", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}
}

func TestAuthMiddleware_InvalidToken(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()
	s := &RESTServer{
		router:   r,
		db:       db,
		eventBus: eb,
	}

	// Setup API key
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)

	protected := r.Group("/api")
	protected.Use(s.authMiddleware())
	protected.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest("GET", "/api/protected", nil)
	req.Header.Set("X-API-Key", "invalid-token")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

func TestAuthMiddleware_BearerToken(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()
	s := &RESTServer{
		router:   r,
		db:       db,
		eventBus: eb,
	}

	// Setup API key
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)

	protected := r.Group("/api")
	protected.Use(s.authMiddleware())
	protected.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest("GET", "/api/protected", nil)
	req.Header.Set("Authorization", "Bearer "+apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestAuthMiddleware_QueryToken(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)
	defer eb.Shutdown()
	s := &RESTServer{
		router:   r,
		db:       db,
		eventBus: eb,
	}

	// Setup API key
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)

	protected := r.Group("/api")
	protected.Use(s.authMiddleware())
	protected.GET("/protected", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"message": "success"})
	})

	req, _ := http.NewRequest("GET", "/api/protected?token="+apiKey, nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestRequestIDMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Add the request ID middleware
	r.Use(func(c *gin.Context) {
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = "generated-id"
		}
		c.Set("request_id", reqID)
		c.Header("X-Request-ID", reqID)
		c.Next()
	})

	r.GET("/test", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"request_id": c.GetString("request_id")})
	})

	// Test with provided request ID
	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "custom-id-123")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Header().Get("X-Request-ID") != "custom-id-123" {
		t.Errorf("Expected X-Request-ID header 'custom-id-123', got %s", w.Header().Get("X-Request-ID"))
	}

	// Test without provided request ID (should generate one)
	req2, _ := http.NewRequest("GET", "/test", nil)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, req2)

	if w2.Header().Get("X-Request-ID") == "" {
		t.Error("Expected X-Request-ID header to be set")
	}
}

func TestPanicRecoveryMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()

	// Add panic recovery middleware
	r.Use(gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error": "Internal server error",
		})
	}))

	r.GET("/panic", func(c *gin.Context) {
		panic("test panic")
	})

	req, _ := http.NewRequest("GET", "/panic", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["error"] != "Internal server error" {
		t.Errorf("Expected error message, got %v", response["error"])
	}
}

// =============================================================================
// getAPIKey Tests
// =============================================================================

// setupAuthTestServer creates a test server with auth routes
func setupAuthTestServer(t *testing.T, db *sql.DB) (*gin.Engine, string, func()) {
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
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)

	// Setup password hash
	hash, _ := auth.HashPassword("testpassword123")
	db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('password_hash', ?)", hash)

	// Register routes
	api := r.Group("/api")
	api.POST("/auth/setup", s.handleAuthSetup)
	api.POST("/auth/login", s.handleLogin)
	api.GET("/auth/status", s.handleAuthStatus)

	protected := api.Group("")
	protected.Use(s.authMiddleware())
	{
		protected.GET("/auth/api-key", s.getAPIKey)
		protected.POST("/auth/api-key/regenerate", s.regenerateAPIKey)
		protected.POST("/auth/password", s.changePassword)
	}

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, apiKey, cleanup
}

func TestGetAPIKey_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/auth/api-key", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["api_key"] != apiKey {
		t.Errorf("Expected api_key to match, got %v", response["api_key"])
	}
}

func TestGetAPIKey_Unauthorized(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, _, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/auth/api-key", nil)
	// No API key provided
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

// =============================================================================
// regenerateAPIKey Tests
// =============================================================================

func TestRegenerateAPIKey_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("POST", "/api/auth/api-key/regenerate", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)

	// New API key should be different from original
	newAPIKey, ok := response["api_key"].(string)
	if !ok {
		t.Fatal("Expected api_key in response")
	}
	if newAPIKey == apiKey {
		t.Error("Expected new API key to be different from original")
	}
	if len(newAPIKey) == 0 {
		t.Error("Expected non-empty API key")
	}

	// Verify it was saved in database
	var encryptedKey string
	db.QueryRow("SELECT value FROM settings WHERE key = 'api_key'").Scan(&encryptedKey)
	decrypted, _ := crypto.Decrypt(encryptedKey)
	if decrypted != newAPIKey {
		t.Error("Expected new key to be saved in database")
	}
}

func TestRegenerateAPIKey_Unauthorized(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, _, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("POST", "/api/auth/api-key/regenerate", nil)
	// No API key provided
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

// =============================================================================
// changePassword Tests
// =============================================================================

func TestChangePassword_Success(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"current_password":"testpassword123","new_password":"newpassword456"}`)
	req, _ := http.NewRequest("POST", "/api/auth/password", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["message"] != "Password changed successfully" {
		t.Errorf("Expected success message, got %v", response["message"])
	}

	// Verify new password works by checking the hash
	var hash string
	db.QueryRow("SELECT value FROM settings WHERE key = 'password_hash'").Scan(&hash)
	if !auth.CheckPasswordHash("newpassword456", hash) {
		t.Error("New password should be verified")
	}
}

func TestChangePassword_InvalidCurrentPassword(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"current_password":"wrongpassword","new_password":"newpassword456"}`)
	req, _ := http.NewRequest("POST", "/api/auth/password", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["error"] != "Invalid current password" {
		t.Errorf("Expected 'Invalid current password' error, got %v", response["error"])
	}
}

func TestChangePassword_NewPasswordTooShort(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"current_password":"testpassword123","new_password":"short"}`)
	req, _ := http.NewRequest("POST", "/api/auth/password", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["error"] != "New password must be at least 8 characters" {
		t.Errorf("Expected password length error, got %v", response["error"])
	}
}

func TestChangePassword_InvalidJSON(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{invalid json}`)
	req, _ := http.NewRequest("POST", "/api/auth/password", body)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("Expected status 400, got %d", w.Code)
	}
}

func TestChangePassword_Unauthorized(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, _, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	body := bytes.NewBufferString(`{"current_password":"testpassword123","new_password":"newpassword456"}`)
	req, _ := http.NewRequest("POST", "/api/auth/password", body)
	req.Header.Set("Content-Type", "application/json")
	// No API key
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

// =============================================================================
// authMiddleware Error Path Tests
// =============================================================================

// TestAuthMiddleware_DBError tests that middleware returns 500 when database is unavailable
func TestAuthMiddleware_DBError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	// Delete the api_key from settings to cause DB query to return no rows
	db.Exec("DELETE FROM settings WHERE key = 'api_key'")

	req, _ := http.NewRequest("GET", "/api/auth/api-key", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Middleware should catch this and return 500
	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500 for missing API key in DB, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["error"] != "Authentication error" {
		t.Errorf("Expected 'Authentication error' message, got %v", response["error"])
	}
}

// TestAuthMiddleware_DecryptionError tests that middleware returns 500 when decryption fails
func TestAuthMiddleware_DecryptionError(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	// Set an encrypted-prefixed but invalid value to cause decryption error
	// The prefix "enc:v1:" marks it as encrypted, but the data after is invalid
	db.Exec("UPDATE settings SET value = 'enc:v1:invalid-base64-!!!' WHERE key = 'api_key'")

	req, _ := http.NewRequest("GET", "/api/auth/api-key", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Middleware should catch this and return 500
	if w.Code != http.StatusInternalServerError {
		t.Errorf("Expected status 500 for decryption error, got %d: %s", w.Code, w.Body.String())
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["error"] != "Authentication error" {
		t.Errorf("Expected 'Authentication error' message, got %v", response["error"])
	}
}

// TestAuthMiddleware_InvalidToken_OnProtectedEndpoint tests 401 for wrong API key on protected endpoint
func TestAuthMiddleware_InvalidToken_OnProtectedEndpoint(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, _, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/auth/api-key", nil)
	req.Header.Set("X-API-Key", "wrong-api-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401 for invalid token, got %d", w.Code)
	}

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	if response["error"] != "Invalid authentication token" {
		t.Errorf("Expected 'Invalid authentication token' message, got %v", response["error"])
	}
}

// TestAuthMiddleware_QueryParam_Token tests that 'token' query parameter auth works
func TestAuthMiddleware_QueryParam_Token(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/auth/api-key?token="+apiKey, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for token query param auth, got %d: %s", w.Code, w.Body.String())
	}
}

// TestAuthMiddleware_QueryParam_ApiKey tests that 'apikey' query parameter auth works
func TestAuthMiddleware_QueryParam_ApiKey(t *testing.T) {
	db, cleanup := setupTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupAuthTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/auth/api-key?apikey="+apiKey, nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200 for apikey query param auth, got %d: %s", w.Code, w.Body.String())
	}
}
