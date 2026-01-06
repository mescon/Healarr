package api

import (
	"archive/zip"
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
)

// setupLogsTestDB creates a test database for logs tests
func setupLogsTestDB(t *testing.T) (*sql.DB, string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "healarr-logs-test-*")
	require.NoError(t, err)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	// Create log directory
	logDir := filepath.Join(tmpDir, "logs")
	err = os.MkdirAll(logDir, 0755)
	require.NoError(t, err)

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	// Basic schema
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
	`
	_, err = db.Exec(schema)
	require.NoError(t, err)

	return db, tmpDir, cleanup
}

// setupLogsTestServer creates a test server with logs routes
// Returns router, apiKey, and cleanup function that must be called to release resources
func setupLogsTestServer(t *testing.T, db *sql.DB) (*gin.Engine, string, func()) {
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
		protected.GET("/logs/recent", s.handleRecentLogs)
		protected.GET("/logs/download", s.handleDownloadLogs)
	}

	cleanup := func() {
		hub.Shutdown()
		eb.Shutdown()
	}

	return r, apiKey, cleanup
}

// =============================================================================
// handleRecentLogs Tests
// =============================================================================

func TestHandleRecentLogs_NoLogFile(t *testing.T) {
	db, tmpDir, cleanup := setupLogsTestDB(t)
	defer cleanup()

	logDir := filepath.Join(tmpDir, "logs")
	config.SetForTesting(&config.Config{
		LogDir: logDir,
	})

	router, apiKey, serverCleanup := setupLogsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/logs/recent", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []interface{}
	err := json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Empty(t, response)
}

func TestHandleRecentLogs_WithLogEntries(t *testing.T) {
	db, tmpDir, cleanup := setupLogsTestDB(t)
	defer cleanup()

	logDir := filepath.Join(tmpDir, "logs")
	config.SetForTesting(&config.Config{
		LogDir: logDir,
	})

	// Create a log file with some entries
	logFile := filepath.Join(logDir, "healarr.log")
	logContent := `2025-01-15T10:00:00Z [INFO] Server started
2025-01-15T10:01:00Z [DEBUG] Processing request
2025-01-15T10:02:00Z [ERROR] Something went wrong
`
	err := os.WriteFile(logFile, []byte(logContent), 0644)
	require.NoError(t, err)

	router, apiKey, serverCleanup := setupLogsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/logs/recent", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	assert.Len(t, response, 3)

	// Check first entry
	assert.Equal(t, "2025-01-15T10:00:00Z", response[0]["timestamp"])
	assert.Equal(t, "INFO", response[0]["level"])
	assert.Equal(t, "Server started", response[0]["message"])

	// Check last entry
	assert.Equal(t, "ERROR", response[2]["level"])
	assert.Equal(t, "Something went wrong", response[2]["message"])
}

func TestHandleRecentLogs_EmptyLines(t *testing.T) {
	db, tmpDir, cleanup := setupLogsTestDB(t)
	defer cleanup()

	logDir := filepath.Join(tmpDir, "logs")
	config.SetForTesting(&config.Config{
		LogDir: logDir,
	})

	// Create a log file with empty lines
	logFile := filepath.Join(logDir, "healarr.log")
	logContent := `2025-01-15T10:00:00Z [INFO] Entry 1

2025-01-15T10:01:00Z [INFO] Entry 2

`
	err := os.WriteFile(logFile, []byte(logContent), 0644)
	require.NoError(t, err)

	router, apiKey, serverCleanup := setupLogsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/logs/recent", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	// Empty lines should be skipped
	assert.Len(t, response, 2)
}

func TestHandleRecentLogs_MoreThan100Lines(t *testing.T) {
	db, tmpDir, cleanup := setupLogsTestDB(t)
	defer cleanup()

	logDir := filepath.Join(tmpDir, "logs")
	config.SetForTesting(&config.Config{
		LogDir: logDir,
	})

	// Create a log file with more than 100 lines
	logFile := filepath.Join(logDir, "healarr.log")
	var logContent bytes.Buffer
	for i := 1; i <= 150; i++ {
		logContent.WriteString("2025-01-15T10:00:00Z [INFO] Line " + string(rune('0'+i/100)) + string(rune('0'+(i/10)%10)) + string(rune('0'+i%10)) + "\n")
	}
	err := os.WriteFile(logFile, logContent.Bytes(), 0644)
	require.NoError(t, err)

	router, apiKey, serverCleanup := setupLogsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/logs/recent", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]interface{}
	err = json.Unmarshal(w.Body.Bytes(), &response)
	assert.NoError(t, err)
	// Should return only the last 100 lines
	assert.Len(t, response, 100)
}

// =============================================================================
// handleDownloadLogs Tests
// =============================================================================

func TestHandleDownloadLogs_Success(t *testing.T) {
	db, tmpDir, cleanup := setupLogsTestDB(t)
	defer cleanup()

	logDir := filepath.Join(tmpDir, "logs")
	config.SetForTesting(&config.Config{
		LogDir: logDir,
	})

	// Create a log file
	logFile := filepath.Join(logDir, "healarr.log")
	logContent := "2025-01-15T10:00:00Z [INFO] Test log entry\n"
	err := os.WriteFile(logFile, []byte(logContent), 0644)
	require.NoError(t, err)

	router, apiKey, serverCleanup := setupLogsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/logs/download", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "application/zip", w.Header().Get("Content-Type"))
	assert.Contains(t, w.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, w.Header().Get("Content-Disposition"), "healarr_logs.zip")

	// Verify it's a valid zip file
	zipReader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	assert.NoError(t, err)
	assert.Len(t, zipReader.File, 1)

	// Check the file was renamed to .txt
	assert.Equal(t, "healarr.txt", zipReader.File[0].Name)
}

func TestHandleDownloadLogs_MultipleFiles(t *testing.T) {
	db, tmpDir, cleanup := setupLogsTestDB(t)
	defer cleanup()

	logDir := filepath.Join(tmpDir, "logs")
	config.SetForTesting(&config.Config{
		LogDir: logDir,
	})

	// Create multiple log files
	err := os.WriteFile(filepath.Join(logDir, "healarr.log"), []byte("main log\n"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(logDir, "error.log"), []byte("error log\n"), 0644)
	require.NoError(t, err)

	router, apiKey, serverCleanup := setupLogsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/logs/download", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify zip has 2 files
	zipReader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	assert.NoError(t, err)
	assert.Len(t, zipReader.File, 2)

	// Collect file names
	fileNames := make(map[string]bool)
	for _, f := range zipReader.File {
		fileNames[f.Name] = true
	}
	assert.True(t, fileNames["healarr.txt"])
	assert.True(t, fileNames["error.txt"])
}

func TestHandleDownloadLogs_EmptyLogDir(t *testing.T) {
	db, tmpDir, cleanup := setupLogsTestDB(t)
	defer cleanup()

	logDir := filepath.Join(tmpDir, "logs")
	config.SetForTesting(&config.Config{
		LogDir: logDir,
	})

	// Don't create any log files

	router, apiKey, serverCleanup := setupLogsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/logs/download", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify empty zip file is valid
	zipReader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	assert.NoError(t, err)
	assert.Empty(t, zipReader.File)
}

func TestHandleRecentLogs_InvalidLevel(t *testing.T) {
	db, _, cleanup := setupLogsTestDB(t)
	defer cleanup()

	router, apiKey, serverCleanup := setupLogsTestServer(t, db)
	defer serverCleanup()

	req, _ := http.NewRequest("GET", "/api/logs/recent?level=invalid", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	// Should still return 200 with all logs (level filter defaults)
	assert.Equal(t, http.StatusOK, w.Code)
}
