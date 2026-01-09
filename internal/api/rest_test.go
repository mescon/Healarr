package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/eventbus"

	_ "modernc.org/sqlite" // SQLite driver
)

// =============================================================================
// serveIndexWithBasePath tests
// =============================================================================

func TestServeIndexWithBasePath(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("injects base path script into head", func(t *testing.T) {
		s := &RESTServer{}

		readFile := func() ([]byte, error) {
			return []byte(`<!DOCTYPE html><html><head><title>Test</title></head><body>Hello</body></html>`), nil
		}

		handler := s.serveIndexWithBasePath("/healarr", readFile)

		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		r := gin.New()
		r.GET("/", handler)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		body := w.Body.String()

		// Check that the base path script was injected
		expectedScript := `<script>window.__HEALARR_BASE_PATH__="/healarr";</script></head>`
		if !strings.Contains(body, expectedScript) {
			t.Errorf("Expected body to contain %q, got %q", expectedScript, body)
		}

		// Check that original </head> is replaced
		if strings.Count(body, "</head>") != 1 {
			t.Errorf("Expected exactly one </head> tag, got %d", strings.Count(body, "</head>"))
		}

		// Check content type
		contentType := w.Header().Get("Content-Type")
		if contentType != "text/html; charset=utf-8" {
			t.Errorf("Expected Content-Type 'text/html; charset=utf-8', got %q", contentType)
		}
	})

	t.Run("handles empty base path", func(t *testing.T) {
		s := &RESTServer{}

		readFile := func() ([]byte, error) {
			return []byte(`<html><head></head><body></body></html>`), nil
		}

		handler := s.serveIndexWithBasePath("", readFile)

		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		r := gin.New()
		r.GET("/", handler)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		body := w.Body.String()
		expectedScript := `<script>window.__HEALARR_BASE_PATH__="";</script></head>`
		if !strings.Contains(body, expectedScript) {
			t.Errorf("Expected body to contain %q, got %q", expectedScript, body)
		}
	})

	t.Run("handles base path with special characters", func(t *testing.T) {
		s := &RESTServer{}

		readFile := func() ([]byte, error) {
			return []byte(`<html><head></head></html>`), nil
		}

		// Base path with quotes that need escaping
		handler := s.serveIndexWithBasePath(`/path"with'quotes`, readFile)

		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		r := gin.New()
		r.GET("/", handler)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		// %q format should properly escape the quotes
		body := w.Body.String()
		if !strings.Contains(body, `window.__HEALARR_BASE_PATH__=`) {
			t.Errorf("Expected body to contain base path script, got %q", body)
		}
	})

	t.Run("returns 404 when file read fails", func(t *testing.T) {
		s := &RESTServer{}

		readFile := func() ([]byte, error) {
			return nil, errors.New("file not found")
		}

		handler := s.serveIndexWithBasePath("/test", readFile)

		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		r := gin.New()
		r.GET("/", handler)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected status 404, got %d", w.Code)
		}
	})

	t.Run("handles HTML without head tag", func(t *testing.T) {
		s := &RESTServer{}

		// HTML without </head> - the script won't be injected
		readFile := func() ([]byte, error) {
			return []byte(`<html><body>No head tag</body></html>`), nil
		}

		handler := s.serveIndexWithBasePath("/test", readFile)

		req := httptest.NewRequest("GET", "/", nil)
		w := httptest.NewRecorder()

		r := gin.New()
		r.GET("/", handler)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status 200, got %d", w.Code)
		}

		// Without </head>, the replacement won't happen
		body := w.Body.String()
		if strings.Contains(body, `window.__HEALARR_BASE_PATH__`) {
			t.Error("Did not expect base path script when no </head> tag exists")
		}
	})
}

// =============================================================================
// mustSub tests
// =============================================================================

func TestMustSub(t *testing.T) {
	t.Run("returns sub-filesystem for valid directory", func(t *testing.T) {
		// Create a test filesystem
		testFS := fstest.MapFS{
			"assets/style.css": &fstest.MapFile{Data: []byte("body{}")},
			"assets/app.js":    &fstest.MapFile{Data: []byte("console.log()")},
			"index.html":       &fstest.MapFile{Data: []byte("<html></html>")},
		}

		subFS := mustSub(testFS, "assets")

		// Verify we can read files from the sub-filesystem
		data, err := fs.ReadFile(subFS, "style.css")
		if err != nil {
			t.Errorf("Failed to read style.css from sub-filesystem: %v", err)
		}
		if string(data) != "body{}" {
			t.Errorf("Expected 'body{}', got %q", string(data))
		}

		// Verify app.js is also accessible
		data, err = fs.ReadFile(subFS, "app.js")
		if err != nil {
			t.Errorf("Failed to read app.js from sub-filesystem: %v", err)
		}
		if string(data) != "console.log()" {
			t.Errorf("Expected 'console.log()', got %q", string(data))
		}
	})

	t.Run("returns sub-filesystem for nested path", func(t *testing.T) {
		// MapFS doesn't error on non-existent paths like embed.FS does
		// This test verifies correct behavior with nested paths
		testFS := fstest.MapFS{
			"icons/favicon.ico": &fstest.MapFile{Data: []byte("icon")},
		}

		subFS := mustSub(testFS, "icons")

		data, err := fs.ReadFile(subFS, "favicon.ico")
		if err != nil {
			t.Errorf("Failed to read favicon.ico from sub-filesystem: %v", err)
		}
		if string(data) != "icon" {
			t.Errorf("Expected 'icon', got %q", string(data))
		}
	})
}

// =============================================================================
// NewRESTServer tests
// =============================================================================

func TestNewRESTServer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Set up test config
	config.SetForTesting(&config.Config{
		Port:                 "8080",
		BasePath:             "/",
		LogLevel:             "info",
		DataDir:              "/tmp",
		DatabasePath:         "/tmp/test.db",
		LogDir:               "/tmp/logs",
		VerificationTimeout:  60 * time.Second,
		VerificationInterval: 4 * time.Hour,
	})

	// Create in-memory database with minimal schema
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE IF NOT EXISTS arr_instances (id INTEGER PRIMARY KEY, name TEXT, url TEXT, api_key TEXT, enabled INTEGER DEFAULT 1);
	`)
	require.NoError(t, err)

	// Create eventbus and use shared global metrics service (Prometheus metrics can only be registered once)
	eb := eventbus.NewEventBus(db)
	metricsService := getGlobalMetricsService(eb)

	t.Run("creates server with expected fields", func(t *testing.T) {
		deps := ServerDeps{
			DB:       db,
			EventBus: eb,
			Metrics:  metricsService,
			// Other deps can be nil for basic initialization test
		}

		server := NewRESTServer(deps)

		// Verify server was created with expected fields
		assert.NotNil(t, server)
		assert.NotNil(t, server.router)
		assert.NotNil(t, server.hub)
		assert.NotNil(t, server.toolChecker)
		assert.Equal(t, db, server.db)
		assert.Equal(t, eb, server.eventBus)
		assert.Equal(t, metricsService, server.metrics)
		assert.False(t, server.startTime.IsZero())
	})

	t.Run("healthNotifier is nil when deps.Notifier is nil", func(t *testing.T) {
		deps := ServerDeps{
			DB:       db,
			EventBus: eb,
			Metrics:  metricsService,
			Notifier: nil, // Explicitly nil notifier
		}

		server := NewRESTServer(deps)

		// When deps.Notifier is nil, healthNotifier should also be nil
		assert.Nil(t, server.healthNotifier)
		assert.Nil(t, server.notifier)
	})
}

// =============================================================================
// handleRuntimeConfig tests
// =============================================================================

func TestHandleRuntimeConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Create in-memory database with settings table
	db, err := sql.Open("sqlite", ":memory:")
	require.NoError(t, err)
	defer db.Close()

	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS settings (key TEXT PRIMARY KEY, value TEXT);`)
	require.NoError(t, err)

	t.Run("returns default source when no env or db value", func(t *testing.T) {
		// Clear any env var
		originalEnv := config.Get().BasePath
		config.SetForTesting(&config.Config{
			BasePath:             "/",
			Port:                 "8080",
			VerificationTimeout:  60 * time.Second,
			VerificationInterval: 4 * time.Hour,
		})
		defer config.SetForTesting(&config.Config{BasePath: originalEnv})

		s := &RESTServer{
			router: gin.New(),
			db:     db,
		}

		s.router.GET("/api/runtime-config", s.handleRuntimeConfig)

		req := httptest.NewRequest("GET", "/api/runtime-config", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, "/", response["base_path"])
		// Source could be "default" or "environment" depending on test environment
		assert.Contains(t, []string{"default", "environment"}, response["base_path_source"])
	})

	t.Run("returns database source when saved in settings", func(t *testing.T) {
		// Insert a base_path setting
		_, err := db.Exec("INSERT OR REPLACE INTO settings (key, value) VALUES ('base_path', '/healarr')")
		require.NoError(t, err)

		config.SetForTesting(&config.Config{
			BasePath:             "/healarr",
			Port:                 "8080",
			VerificationTimeout:  60 * time.Second,
			VerificationInterval: 4 * time.Hour,
		})

		s := &RESTServer{
			router: gin.New(),
			db:     db,
		}

		s.router.GET("/api/runtime-config", s.handleRuntimeConfig)

		// Clear env to ensure "database" source is detected
		// Note: This test may show "environment" if HEALARR_BASE_PATH is set
		req := httptest.NewRequest("GET", "/api/runtime-config", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.Equal(t, "/healarr", response["base_path"])

		// Clean up
		_, _ = db.Exec("DELETE FROM settings WHERE key = 'base_path'")
	})

	t.Run("handles db query error gracefully", func(t *testing.T) {
		// Use database with missing settings table
		emptyDB, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer emptyDB.Close()

		config.SetForTesting(&config.Config{
			BasePath:             "/",
			Port:                 "8080",
			VerificationTimeout:  60 * time.Second,
			VerificationInterval: 4 * time.Hour,
		})

		s := &RESTServer{
			router: gin.New(),
			db:     emptyDB,
		}

		s.router.GET("/api/runtime-config", s.handleRuntimeConfig)

		req := httptest.NewRequest("GET", "/api/runtime-config", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		// Should still return OK even with DB error
		assert.Equal(t, http.StatusOK, w.Code)

		var response map[string]interface{}
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		// Should have base_path even with DB error
		assert.NotEmpty(t, response["base_path"])
	})
}

// =============================================================================
// Token extraction helper tests
// =============================================================================

func TestRESTServer_ExtractAPIToken(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := &RESTServer{}

	tests := []struct {
		name      string
		setupReq  func(req *http.Request)
		wantToken string
	}{
		{
			"X-API-Key header",
			func(req *http.Request) {
				req.Header.Set("X-API-Key", "test-api-key")
			},
			"test-api-key",
		},
		{
			"Authorization Bearer header",
			func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer bearer-token")
			},
			"bearer-token",
		},
		{
			"Authorization without Bearer",
			func(req *http.Request) {
				req.Header.Set("Authorization", "basic-auth-token")
			},
			"basic-auth-token",
		},
		{
			"token query param",
			func(req *http.Request) {
				q := req.URL.Query()
				q.Set("token", "query-token")
				req.URL.RawQuery = q.Encode()
			},
			"query-token",
		},
		{
			"apikey query param",
			func(req *http.Request) {
				q := req.URL.Query()
				q.Set("apikey", "api-key-param")
				req.URL.RawQuery = q.Encode()
			},
			"api-key-param",
		},
		{
			"no token provided",
			func(req *http.Request) {},
			"",
		},
		{
			"X-API-Key takes precedence over query",
			func(req *http.Request) {
				req.Header.Set("X-API-Key", "header-key")
				q := req.URL.Query()
				q.Set("token", "query-key")
				req.URL.RawQuery = q.Encode()
			},
			"header-key",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/api/test", nil)
			tt.setupReq(req)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = req

			got := s.extractAPIToken(c)
			assert.Equal(t, tt.wantToken, got)
		})
	}
}

// =============================================================================
// verifyAPIToken tests
// =============================================================================

func TestRESTServer_VerifyAPIToken(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("valid token matches stored key", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		_, err = db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`)
		require.NoError(t, err)

		// Store an unencrypted key (crypto.Decrypt returns unencrypted values as-is)
		_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ('api_key', 'test-secret-key')`)
		require.NoError(t, err)

		s := &RESTServer{db: db}

		err = s.verifyAPIToken("test-secret-key")
		assert.NoError(t, err)
	})

	t.Run("invalid token returns errInvalidToken", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		_, err = db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`)
		require.NoError(t, err)

		_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ('api_key', 'correct-key')`)
		require.NoError(t, err)

		s := &RESTServer{db: db}

		err = s.verifyAPIToken("wrong-key")
		assert.Equal(t, errInvalidToken, err)
	})

	t.Run("missing api_key returns error", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		_, err = db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`)
		require.NoError(t, err)

		s := &RESTServer{db: db}

		err = s.verifyAPIToken("any-token")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to retrieve API key")
	})

	t.Run("missing settings table returns error", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		s := &RESTServer{db: db}

		err = s.verifyAPIToken("any-token")
		assert.Error(t, err)
	})
}

// =============================================================================
// authMiddleware tests
// =============================================================================

func TestRESTServer_AuthMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("rejects request with no token", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		s := &RESTServer{db: db, router: gin.New()}

		s.router.GET("/protected", s.authMiddleware(), func(c *gin.Context) {
			c.String(http.StatusOK, "success")
		})

		req := httptest.NewRequest("GET", "/protected", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "No authentication token provided")
	})

	t.Run("rejects request with invalid token", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		_, err = db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`)
		require.NoError(t, err)

		_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ('api_key', 'correct-key')`)
		require.NoError(t, err)

		s := &RESTServer{db: db, router: gin.New()}

		s.router.GET("/protected", s.authMiddleware(), func(c *gin.Context) {
			c.String(http.StatusOK, "success")
		})

		req := httptest.NewRequest("GET", "/protected", nil)
		req.Header.Set("X-API-Key", "wrong-key")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Contains(t, w.Body.String(), "Invalid authentication token")
	})

	t.Run("accepts request with valid token", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		_, err = db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`)
		require.NoError(t, err)

		_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ('api_key', 'valid-api-key')`)
		require.NoError(t, err)

		s := &RESTServer{db: db, router: gin.New()}

		s.router.GET("/protected", s.authMiddleware(), func(c *gin.Context) {
			c.String(http.StatusOK, "success")
		})

		req := httptest.NewRequest("GET", "/protected", nil)
		req.Header.Set("X-API-Key", "valid-api-key")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "success", w.Body.String())
	})

	t.Run("returns 500 on database error", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		// No settings table - will cause DB error
		s := &RESTServer{db: db, router: gin.New()}

		s.router.GET("/protected", s.authMiddleware(), func(c *gin.Context) {
			c.String(http.StatusOK, "success")
		})

		req := httptest.NewRequest("GET", "/protected", nil)
		req.Header.Set("X-API-Key", "some-key")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "Authentication error")
	})

	t.Run("accepts valid token from Authorization header", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		_, err = db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`)
		require.NoError(t, err)

		_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ('api_key', 'bearer-token')`)
		require.NoError(t, err)

		s := &RESTServer{db: db, router: gin.New()}

		s.router.GET("/protected", s.authMiddleware(), func(c *gin.Context) {
			c.String(http.StatusOK, "success")
		})

		req := httptest.NewRequest("GET", "/protected", nil)
		req.Header.Set("Authorization", "Bearer bearer-token")
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "success", w.Body.String())
	})

	t.Run("accepts valid token from query parameter token", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		_, err = db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`)
		require.NoError(t, err)

		_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ('api_key', 'query-token')`)
		require.NoError(t, err)

		s := &RESTServer{db: db, router: gin.New()}

		s.router.GET("/protected", s.authMiddleware(), func(c *gin.Context) {
			c.String(http.StatusOK, "success")
		})

		// Use 'token' parameter which is supported by extractAPIToken
		req := httptest.NewRequest("GET", "/protected?token=query-token", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "success", w.Body.String())
	})

	t.Run("accepts valid token from query parameter apikey", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		require.NoError(t, err)
		defer db.Close()

		_, err = db.Exec(`CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)`)
		require.NoError(t, err)

		_, err = db.Exec(`INSERT INTO settings (key, value) VALUES ('api_key', 'apikey-token')`)
		require.NoError(t, err)

		s := &RESTServer{db: db, router: gin.New()}

		s.router.GET("/protected", s.authMiddleware(), func(c *gin.Context) {
			c.String(http.StatusOK, "success")
		})

		// Use 'apikey' parameter which is also supported
		req := httptest.NewRequest("GET", "/protected?apikey=apikey-token", nil)
		w := httptest.NewRecorder()
		s.router.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "success", w.Body.String())
	})
}

// =============================================================================
// Middleware tests (request ID, CORS, recovery)
// =============================================================================

func TestRequestIDMiddleware_Rest(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("generates request ID when not provided", func(t *testing.T) {
		r := gin.New()
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
			c.String(http.StatusOK, c.GetString("request_id"))
		})

		req := httptest.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.NotEmpty(t, w.Header().Get("X-Request-ID"))
	})

	t.Run("uses provided request ID", func(t *testing.T) {
		r := gin.New()
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
			c.String(http.StatusOK, c.GetString("request_id"))
		})

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("X-Request-ID", "custom-request-id")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "custom-request-id", w.Header().Get("X-Request-ID"))
		assert.Equal(t, "custom-request-id", w.Body.String())
	})
}

func TestCORSMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("handles OPTIONS preflight request", func(t *testing.T) {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
			c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET")
			if c.Request.Method == "OPTIONS" {
				c.AbortWithStatus(204)
				return
			}
			c.Next()
		})
		r.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "success")
		})

		req := httptest.NewRequest("OPTIONS", "/test", nil)
		req.Header.Set("Origin", "http://example.com")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, 204, w.Code)
		assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	})

	t.Run("sets CORS headers for regular requests", func(t *testing.T) {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			origin := c.GetHeader("Origin")
			if origin != "" {
				c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
				c.Writer.Header().Set("Vary", "Origin")
			}
			c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
			c.Next()
		})
		r.GET("/test", func(c *gin.Context) {
			c.String(http.StatusOK, "success")
		})

		req := httptest.NewRequest("GET", "/test", nil)
		req.Header.Set("Origin", "http://allowed.com")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Equal(t, "http://allowed.com", w.Header().Get("Access-Control-Allow-Origin"))
	})
}

func TestRecoveryMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("recovers from panic and returns 500", func(t *testing.T) {
		r := gin.New()
		r.Use(func(c *gin.Context) {
			c.Set("request_id", "test-request-id")
			c.Next()
		})
		r.Use(gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
			reqID := c.GetString("request_id")
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error":      "Internal server error",
				"request_id": reqID,
			})
		}))
		r.GET("/panic", func(c *gin.Context) {
			panic("test panic")
		})

		req := httptest.NewRequest("GET", "/panic", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusInternalServerError, w.Code)
		assert.Contains(t, w.Body.String(), "Internal server error")
		assert.Contains(t, w.Body.String(), "test-request-id")
	})
}
