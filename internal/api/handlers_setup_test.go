package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
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

// =============================================================================
// Setup Test Helpers
// =============================================================================

// setupSetupTestDB creates a test database with full schema for setup testing
func setupSetupTestDB(t *testing.T) (*sql.DB, string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "healarr-setup-test-*")
	require.NoError(t, err)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := sql.Open("sqlite3", dbPath)
	require.NoError(t, err)

	cleanup := func() {
		db.Close()
		os.RemoveAll(tmpDir)
	}

	schema := `
		CREATE TABLE settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE arr_instances (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			enabled INTEGER DEFAULT 1
		);

		CREATE TABLE scan_paths (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			local_path TEXT NOT NULL,
			arr_path TEXT,
			enabled INTEGER DEFAULT 1
		);

		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			aggregate_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			event_data JSON NOT NULL,
			event_version INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);

		CREATE TABLE schema_migrations (
			version TEXT PRIMARY KEY,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`
	_, err = db.Exec(schema)
	require.NoError(t, err)

	return db, tmpDir, cleanup
}

// createSetupTestServer creates a RESTServer for setup testing
func createSetupTestServer(t *testing.T, db *sql.DB) *RESTServer {
	t.Helper()

	gin.SetMode(gin.TestMode)
	r := gin.New()

	eb := eventbus.NewEventBus(db)

	return &RESTServer{
		router:   r,
		db:       db,
		eventBus: eb,
	}
}

// =============================================================================
// handleSetupStatus Tests
// =============================================================================

func TestHandleSetupStatus_FreshInstall(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/setup/status", server.handleSetupStatus)

	req, _ := http.NewRequest("GET", "/setup/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var status SetupStatus
	err := json.Unmarshal(w.Body.Bytes(), &status)
	require.NoError(t, err)

	assert.True(t, status.NeedsSetup, "Fresh install should need setup")
	assert.False(t, status.HasPassword)
	assert.False(t, status.HasAPIKey)
	assert.False(t, status.HasInstances)
	assert.False(t, status.HasScanPaths)
	assert.False(t, status.OnboardingDismissed)
}

func TestHandleSetupStatus_SetupComplete(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	// Setup password and API key
	hash, _ := auth.HashPassword("testpassword")
	db.Exec("INSERT INTO settings (key, value) VALUES ('password_hash', ?)", hash)
	apiKey, _ := auth.GenerateAPIKey()
	encryptedKey, _ := crypto.Encrypt(apiKey)
	db.Exec("INSERT INTO settings (key, value) VALUES ('api_key', ?)", encryptedKey)

	// Add an instance
	db.Exec("INSERT INTO arr_instances (name, type, url, api_key) VALUES ('Sonarr', 'sonarr', 'http://localhost:8989', 'test')")

	// Add a scan path
	db.Exec("INSERT INTO scan_paths (local_path, arr_path) VALUES ('/media/tv', '/tv')")

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/setup/status", server.handleSetupStatus)

	req, _ := http.NewRequest("GET", "/setup/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var status SetupStatus
	err := json.Unmarshal(w.Body.Bytes(), &status)
	require.NoError(t, err)

	assert.False(t, status.NeedsSetup, "Completed setup should not need setup")
	assert.True(t, status.HasPassword)
	assert.True(t, status.HasAPIKey)
	assert.True(t, status.HasInstances)
	assert.True(t, status.HasScanPaths)
}

func TestHandleSetupStatus_OnboardingDismissed(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	// Set onboarding dismissed
	db.Exec("INSERT INTO settings (key, value) VALUES ('onboarding_dismissed', 'true')")

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/setup/status", server.handleSetupStatus)

	req, _ := http.NewRequest("GET", "/setup/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var status SetupStatus
	err := json.Unmarshal(w.Body.Bytes(), &status)
	require.NoError(t, err)

	assert.True(t, status.OnboardingDismissed)
}

func TestHandleSetupStatus_DBError(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	// Drop settings table to cause error
	db.Exec("DROP TABLE settings")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/setup/status", server.handleSetupStatus)

	req, _ := http.NewRequest("GET", "/setup/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Database error", response["error"])
}

// =============================================================================
// handleSetupDismiss Tests
// =============================================================================

func TestHandleSetupDismiss_Success(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/setup/dismiss", server.handleSetupDismiss)

	req, _ := http.NewRequest("POST", "/setup/dismiss", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Onboarding dismissed", response["message"])

	// Verify it was stored
	var value string
	db.QueryRow("SELECT value FROM settings WHERE key = 'onboarding_dismissed'").Scan(&value)
	assert.Equal(t, "true", value)
}

func TestHandleSetupDismiss_Idempotent(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	// Already dismissed
	db.Exec("INSERT INTO settings (key, value) VALUES ('onboarding_dismissed', 'true')")

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/setup/dismiss", server.handleSetupDismiss)

	req, _ := http.NewRequest("POST", "/setup/dismiss", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleSetupDismiss_DBError(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	// Drop settings table
	db.Exec("DROP TABLE settings")

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/setup/dismiss", server.handleSetupDismiss)

	req, _ := http.NewRequest("POST", "/setup/dismiss", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

// =============================================================================
// handleDatabaseRestore Tests
// =============================================================================

func TestHandleDatabaseRestore_NoConfirmHeader(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/config/restore", server.handleDatabaseRestore)

	req, _ := http.NewRequest("POST", "/config/restore", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Confirmation required", response["error"])
}

func TestHandleDatabaseRestore_NoFile(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/config/restore", server.handleDatabaseRestore)

	req, _ := http.NewRequest("POST", "/config/restore", nil)
	req.Header.Set("X-Confirm-Restore", "true")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "No file uploaded", response["error"])
}

func TestHandleDatabaseRestore_InvalidExtension(t *testing.T) {
	db, tmpDir, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/config/restore", server.handleDatabaseRestore)

	// Create a fake file with wrong extension
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "backup.txt")
	part.Write([]byte("not a database"))
	writer.Close()

	// Need to set the data dir for config
	os.Setenv("HEALARR_DATA_DIR", tmpDir)
	defer os.Unsetenv("HEALARR_DATA_DIR")

	req, _ := http.NewRequest("POST", "/config/restore", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Confirm-Restore", "true")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Invalid file type. Expected .db file", response["error"])
}

func TestHandleDatabaseRestore_InvalidDatabase(t *testing.T) {
	db, tmpDir, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/config/restore", server.handleDatabaseRestore)

	// Create a file with .db extension but not a valid SQLite database
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "backup.db")
	part.Write([]byte("not a real database content"))
	writer.Close()

	// Set data dir config
	os.Setenv("HEALARR_DATA_DIR", tmpDir)
	os.Setenv("HEALARR_DATABASE_PATH", filepath.Join(tmpDir, "test.db"))
	defer os.Unsetenv("HEALARR_DATA_DIR")
	defer os.Unsetenv("HEALARR_DATABASE_PATH")

	req, _ := http.NewRequest("POST", "/config/restore", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Confirm-Restore", "true")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	// Should fail validation - error message varies by failure type
	errorMsg := response["error"].(string)
	assert.True(t, len(errorMsg) > 0, "Error message should not be empty")
}

func TestHandleDatabaseRestore_ValidDatabase(t *testing.T) {
	db, tmpDir, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	// Create a valid backup database
	backupPath := filepath.Join(tmpDir, "valid_backup.db")
	backupDB, err := sql.Open("sqlite3", backupPath)
	require.NoError(t, err)

	_, err = backupDB.Exec(`
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE arr_instances (id INTEGER PRIMARY KEY, name TEXT, type TEXT, url TEXT, api_key TEXT);
		CREATE TABLE scan_paths (id INTEGER PRIMARY KEY, local_path TEXT);
		CREATE TABLE schema_migrations (version TEXT PRIMARY KEY);
		INSERT INTO schema_migrations (version) VALUES ('001_schema.sql');
	`)
	require.NoError(t, err)
	backupDB.Close()

	// Read the backup file
	backupContent, err := os.ReadFile(backupPath)
	require.NoError(t, err)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/config/restore", server.handleDatabaseRestore)

	// Create multipart form with the valid database
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "backup.db")
	part.Write(backupContent)
	writer.Close()

	// Set data dir config and reload config
	dbPath := filepath.Join(tmpDir, "test.db")
	os.Setenv("HEALARR_DATA_DIR", tmpDir)
	os.Setenv("HEALARR_DATABASE_PATH", dbPath)
	config.Load() // Reload config after setting env vars
	defer func() {
		os.Unsetenv("HEALARR_DATA_DIR")
		os.Unsetenv("HEALARR_DATABASE_PATH")
		config.Load() // Reset config
	}()

	req, _ := http.NewRequest("POST", "/config/restore", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Confirm-Restore", "true")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Database restore staged successfully", response["message"])
	assert.Equal(t, true, response["restart_required"])

	// Verify .pending file was created at the configured database path
	pendingPath := dbPath + ".pending"
	_, err = os.Stat(pendingPath)
	assert.NoError(t, err, "Pending file should exist at %s", pendingPath)
}

// =============================================================================
// validateUploadedDatabase Tests
// =============================================================================

func TestValidateUploadedDatabase_NotSQLite(t *testing.T) {
	db, tmpDir, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	// Create a non-SQLite file
	fakePath := filepath.Join(tmpDir, "fake.db")
	os.WriteFile(fakePath, []byte("not a database"), 0644)

	err := server.validateUploadedDatabase(fakePath)
	assert.Error(t, err)
	// Error can be "invalid SQLite database" or "database integrity check failed"
	assert.True(t, len(err.Error()) > 0, "Error should not be empty")
}

func TestValidateUploadedDatabase_MissingTables(t *testing.T) {
	db, tmpDir, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	// Create a valid SQLite database but without Healarr tables
	emptyDbPath := filepath.Join(tmpDir, "empty.db")
	emptyDb, _ := sql.Open("sqlite3", emptyDbPath)
	emptyDb.Exec("CREATE TABLE random_table (id INTEGER)")
	emptyDb.Close()

	err := server.validateUploadedDatabase(emptyDbPath)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not a valid Healarr database")
}

func TestValidateUploadedDatabase_Valid(t *testing.T) {
	db, tmpDir, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	// Create a valid Healarr database
	validPath := filepath.Join(tmpDir, "valid.db")
	validDb, _ := sql.Open("sqlite3", validPath)
	validDb.Exec(`
		CREATE TABLE schema_migrations (version TEXT PRIMARY KEY);
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE arr_instances (id INTEGER PRIMARY KEY);
		CREATE TABLE scan_paths (id INTEGER PRIMARY KEY);
	`)
	validDb.Close()

	err := server.validateUploadedDatabase(validPath)
	assert.NoError(t, err)
}

// =============================================================================
// handleConfigImportPublic Tests
// =============================================================================

func TestHandleConfigImportPublic_NoPassword_Allowed(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/setup/import", server.handleConfigImportPublic)

	// Empty import (valid JSON but no data)
	body := bytes.NewBufferString(`{}`)

	req, _ := http.NewRequest("POST", "/setup/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// Should succeed (200 OK with empty imports)
	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Import complete", response["message"])
}

func TestHandleConfigImportPublic_WithPassword_Rejected(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	// Setup password
	hash, _ := auth.HashPassword("testpassword")
	db.Exec("INSERT INTO settings (key, value) VALUES ('password_hash', ?)", hash)

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/setup/import", server.handleConfigImportPublic)

	body := bytes.NewBufferString(`{}`)

	req, _ := http.NewRequest("POST", "/setup/import", body)
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Authentication required", response["error"])
}

// =============================================================================
// handleDatabaseRestorePublic Tests
// =============================================================================

func TestHandleDatabaseRestorePublic_NoPassword_Allowed(t *testing.T) {
	db, tmpDir, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/setup/restore", server.handleDatabaseRestorePublic)

	// Create a valid backup database
	backupPath := filepath.Join(tmpDir, "valid_backup.db")
	backupDB, _ := sql.Open("sqlite3", backupPath)
	backupDB.Exec(`
		CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
		CREATE TABLE arr_instances (id INTEGER PRIMARY KEY, name TEXT, type TEXT, url TEXT, api_key TEXT);
		CREATE TABLE scan_paths (id INTEGER PRIMARY KEY, local_path TEXT);
		CREATE TABLE schema_migrations (version TEXT PRIMARY KEY);
	`)
	backupDB.Close()

	backupContent, _ := os.ReadFile(backupPath)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "backup.db")
	part.Write(backupContent)
	writer.Close()

	os.Setenv("HEALARR_DATA_DIR", tmpDir)
	os.Setenv("HEALARR_DATABASE_PATH", filepath.Join(tmpDir, "test.db"))
	defer os.Unsetenv("HEALARR_DATA_DIR")
	defer os.Unsetenv("HEALARR_DATABASE_PATH")

	req, _ := http.NewRequest("POST", "/setup/restore", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Confirm-Restore", "true")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestHandleDatabaseRestorePublic_WithPassword_Rejected(t *testing.T) {
	db, _, cleanup := setupSetupTestDB(t)
	defer cleanup()

	// Setup password
	hash, _ := auth.HashPassword("testpassword")
	db.Exec("INSERT INTO settings (key, value) VALUES ('password_hash', ?)", hash)

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.POST("/setup/restore", server.handleDatabaseRestorePublic)

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "backup.db")
	part.Write([]byte("content"))
	writer.Close()

	req, _ := http.NewRequest("POST", "/setup/restore", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("X-Confirm-Restore", "true")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)

	var response map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &response)
	assert.Equal(t, "Authentication required", response["error"])
}

// =============================================================================
// Integration test for setup flow
// =============================================================================

func TestSetupFlow_FullIntegration(t *testing.T) {
	db, tmpDir, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/setup/status", server.handleSetupStatus)
	r.POST("/setup/dismiss", server.handleSetupDismiss)

	// Step 1: Check initial status (needs setup)
	req, _ := http.NewRequest("GET", "/setup/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	var status SetupStatus
	json.Unmarshal(w.Body.Bytes(), &status)
	assert.True(t, status.NeedsSetup)
	assert.False(t, status.OnboardingDismissed)

	// Step 2: Power user dismisses onboarding
	req, _ = http.NewRequest("POST", "/setup/dismiss", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Step 3: Check status again (still needs setup but onboarding is dismissed)
	req, _ = http.NewRequest("GET", "/setup/status", nil)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, req)

	json.Unmarshal(w.Body.Bytes(), &status)
	assert.True(t, status.NeedsSetup, "Still needs setup (no password)")
	assert.True(t, status.OnboardingDismissed, "Onboarding should be dismissed")

	// Note: tmpDir is used to ensure test isolation
	_ = tmpDir
}

// Test for corrupted database (integrity check fails)
func TestValidateUploadedDatabase_Corrupted(t *testing.T) {
	db, tmpDir, cleanup := setupSetupTestDB(t)
	defer cleanup()

	server := createSetupTestServer(t, db)

	// Create a file that looks like SQLite header but is corrupted
	corruptPath := filepath.Join(tmpDir, "corrupt.db")
	// SQLite header magic: "SQLite format 3\x00"
	// Create minimal header that SQLite will recognize but fail integrity check
	header := []byte("SQLite format 3\x00")
	header = append(header, make([]byte, 100)...) // Pad with zeros (invalid page)
	os.WriteFile(corruptPath, header, 0644)

	err := server.validateUploadedDatabase(corruptPath)
	assert.Error(t, err)
	// The error could be about integrity or invalid database depending on SQLite version
}

// =============================================================================
// validatePathWithinDir Tests
// =============================================================================

func TestValidatePathWithinDir_ValidPath(t *testing.T) {
	baseDir := "/home/user/backups"

	tests := []struct {
		name       string
		targetPath string
		wantPath   string
	}{
		{
			name:       "simple file",
			targetPath: "/home/user/backups/file.db",
			wantPath:   "/home/user/backups/file.db",
		},
		{
			name:       "nested path",
			targetPath: "/home/user/backups/sub/dir/file.db",
			wantPath:   "/home/user/backups/sub/dir/file.db",
		},
		{
			name:       "with redundant slashes",
			targetPath: "/home/user/backups//file.db",
			wantPath:   "/home/user/backups/file.db",
		},
		{
			name:       "with dot segments",
			targetPath: "/home/user/backups/./file.db",
			wantPath:   "/home/user/backups/file.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotPath, err := validatePathWithinDir(tt.targetPath, baseDir)
			assert.NoError(t, err)
			assert.Equal(t, tt.wantPath, gotPath)
		})
	}
}

func TestValidatePathWithinDir_InvalidPath(t *testing.T) {
	baseDir := "/home/user/backups"

	tests := []struct {
		name       string
		targetPath string
	}{
		{
			name:       "parent directory escape",
			targetPath: "/home/user/backups/../secrets/file.db",
		},
		{
			name:       "completely different path",
			targetPath: "/etc/passwd",
		},
		{
			name:       "sibling directory",
			targetPath: "/home/user/documents/file.db",
		},
		{
			name:       "prefix attack (backups2)",
			targetPath: "/home/user/backups2/file.db",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := validatePathWithinDir(tt.targetPath, baseDir)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "is not within")
		})
	}
}

// Ensure unused imports don't cause issues
var _ = fmt.Sprintf
