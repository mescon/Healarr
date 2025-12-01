package integration

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
	_ "github.com/mattn/go-sqlite3"
)

// testDB is a helper for creating test databases without import cycles
type testDB struct {
	DB   *sql.DB
	path string
}

func newTestDB(t *testing.T) *testDB {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := sql.Open("sqlite3", dbPath+"?_journal_mode=WAL&_busy_timeout=5000")
	if err != nil {
		t.Fatalf("Failed to open test DB: %v", err)
	}

	// Create minimal schema needed for tests
	schema := `
		CREATE TABLE IF NOT EXISTS arr_instances (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			enabled INTEGER DEFAULT 1
		);
		CREATE TABLE IF NOT EXISTS scan_paths (
			id INTEGER PRIMARY KEY,
			local_path TEXT NOT NULL,
			arr_path TEXT NOT NULL,
			arr_instance_id INTEGER,
			auto_remediate INTEGER DEFAULT 0,
			is_4k INTEGER DEFAULT 0,
			verification_timeout_hours INTEGER,
			FOREIGN KEY (arr_instance_id) REFERENCES arr_instances(id)
		);
	`
	if _, err := db.Exec(schema); err != nil {
		t.Fatalf("Failed to create test schema: %v", err)
	}

	return &testDB{DB: db, path: dbPath}
}

func (tdb *testDB) Close() {
	tdb.DB.Close()
	os.Remove(tdb.path)
}

// =============================================================================
// RateLimiter tests
// =============================================================================

func TestNewRateLimiter(t *testing.T) {
	rl := NewRateLimiter(10.0, 5)

	if rl.maxTokens != 5 {
		t.Errorf("Expected maxTokens=5, got %f", rl.maxTokens)
	}
	if rl.refillRate != 10.0 {
		t.Errorf("Expected refillRate=10.0, got %f", rl.refillRate)
	}
	if rl.tokens != 5 {
		t.Errorf("Expected initial tokens=5, got %f", rl.tokens)
	}
}

func TestRateLimiter_Wait_Immediate(t *testing.T) {
	rl := NewRateLimiter(100.0, 10) // High rate, plenty of tokens

	start := time.Now()
	for i := 0; i < 5; i++ {
		if err := rl.Wait(t.Context()); err != nil {
			t.Fatalf("Wait failed: %v", err)
		}
	}
	elapsed := time.Since(start)

	// Should complete almost immediately (< 100ms)
	if elapsed > 100*time.Millisecond {
		t.Errorf("Expected fast completion, took %v", elapsed)
	}
}

func TestRateLimiter_Wait_Throttle(t *testing.T) {
	rl := NewRateLimiter(10.0, 1) // 10 RPS, burst of 1

	// First call should be immediate
	start := time.Now()
	if err := rl.Wait(t.Context()); err != nil {
		t.Fatalf("First wait failed: %v", err)
	}

	// Second call should wait ~100ms
	if err := rl.Wait(t.Context()); err != nil {
		t.Fatalf("Second wait failed: %v", err)
	}
	elapsed := time.Since(start)

	// Should take at least 90ms (accounting for timing variance)
	if elapsed < 90*time.Millisecond {
		t.Errorf("Expected throttling delay, got only %v", elapsed)
	}
}

// =============================================================================
// isRetryableError tests
// =============================================================================

func TestIsRetryableError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"nil error", nil, false},
		{"connection refused", &testError{"connection refused"}, true},
		{"connection reset", &testError{"connection reset by peer"}, true},
		{"i/o timeout", &testError{"i/o timeout"}, true},
		{"EOF", &testError{"unexpected EOF"}, true},
		{"network unreachable", &testError{"network is unreachable"}, true},
		{"regular error", &testError{"something went wrong"}, false},
		{"not found error", &testError{"not found"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			if result != tt.expected {
				t.Errorf("isRetryableError(%v) = %v, want %v", tt.err, result, tt.expected)
			}
		})
	}
}

type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}

// =============================================================================
// HTTPArrClient tests with mock servers
// =============================================================================

func setupTestClient(t *testing.T) (*HTTPArrClient, *testDB) {
	config.SetForTesting(config.NewTestConfig())

	db := newTestDB(t)
	client := NewArrClient(db.DB)
	return client, db
}

func TestHTTPArrClient_FindMediaByPath_Radarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "test-api-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case r.URL.Path == "/api/v3/parse":
			// Return successful parse result
			json.NewEncoder(w).Encode(ParseResult{
				Movie: &MediaItem{
					ID:    123,
					Title: "Test Movie",
					Path:  "/movies/Test Movie (2024)",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	// Encrypt API key and insert test instance
	encryptedKey, err := crypto.Encrypt("test-api-key")
	if err != nil {
		t.Fatalf("Failed to encrypt API key: %v", err)
	}

	_, err = db.DB.Exec(`
		INSERT INTO arr_instances (id, name, type, url, api_key, enabled)
		VALUES (1, 'Test Radarr', 'radarr', ?, ?, 1)
	`, server.URL, encryptedKey)
	if err != nil {
		t.Fatalf("Failed to insert instance: %v", err)
	}

	_, err = db.DB.Exec(`
		INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k)
		VALUES (1, '/local/movies', '/movies', 1, 0, 0)
	`)
	if err != nil {
		t.Fatalf("Failed to insert scan path: %v", err)
	}

	// Test FindMediaByPath
	mediaID, err := client.FindMediaByPath("/movies/Test Movie (2024)/movie.mkv")
	if err != nil {
		t.Fatalf("FindMediaByPath failed: %v", err)
	}

	if mediaID != 123 {
		t.Errorf("Expected mediaID=123, got %d", mediaID)
	}
}

func TestHTTPArrClient_FindMediaByPath_Sonarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Api-Key") != "sonarr-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		switch {
		case r.URL.Path == "/api/v3/parse":
			json.NewEncoder(w).Encode(ParseResult{
				Series: &MediaItem{
					ID:    456,
					Title: "Test Show",
					Path:  "/tv/Test Show",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("sonarr-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Test Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	mediaID, err := client.FindMediaByPath("/tv/Test Show/Season 01/episode.mkv")
	if err != nil {
		t.Fatalf("FindMediaByPath failed: %v", err)
	}

	if mediaID != 456 {
		t.Errorf("Expected mediaID=456, got %d", mediaID)
	}
}

func TestHTTPArrClient_FindMediaByPath_Fallback(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/parse":
			// Parse fails - return empty result
			json.NewEncoder(w).Encode(ParseResult{})
		case r.URL.Path == "/api/v3/movie":
			// Fallback to listing all movies
			json.NewEncoder(w).Encode([]MediaItem{
				{ID: 1, Title: "Other Movie", Path: "/movies/Other Movie (2023)"},
				{ID: 2, Title: "Target Movie", Path: "/movies/Target Movie (2024)"},
				{ID: 3, Title: "Another Movie", Path: "/movies/Another Movie (2022)"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Test Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	mediaID, err := client.FindMediaByPath("/movies/Target Movie (2024)/movie.mkv")
	if err != nil {
		t.Fatalf("FindMediaByPath fallback failed: %v", err)
	}

	if mediaID != 2 {
		t.Errorf("Expected mediaID=2, got %d", mediaID)
	}
}

func TestHTTPArrClient_FindMediaByPath_NotFound(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/parse":
			json.NewEncoder(w).Encode(ParseResult{})
		case r.URL.Path == "/api/v3/movie":
			json.NewEncoder(w).Encode([]MediaItem{
				{ID: 1, Title: "Some Movie", Path: "/movies/Some Movie (2023)"},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	_, err := client.FindMediaByPath("/movies/Nonexistent Movie (2024)/movie.mkv")
	if err == nil {
		t.Error("Expected error for nonexistent media")
	}
}

func TestHTTPArrClient_GetAllInstances(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Insert test instances
	key1, _ := crypto.Encrypt("key1")
	key2, _ := crypto.Encrypt("key2")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', 'http://radarr:7878', ?, 1)`, key1)
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (2, 'Sonarr', 'sonarr', 'http://sonarr:8989', ?, 1)`, key2)
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (3, 'Disabled', 'radarr', 'http://disabled:7878', ?, 0)`, key1)

	instances, err := client.GetAllInstances()
	if err != nil {
		t.Fatalf("GetAllInstances failed: %v", err)
	}

	// Should only return enabled instances
	if len(instances) != 2 {
		t.Errorf("Expected 2 enabled instances, got %d", len(instances))
	}

	// Verify instances are returned with decrypted keys
	for _, inst := range instances {
		if inst.APIKey == "" {
			t.Error("API key should be decrypted")
		}
	}
}

func TestHTTPArrClient_GetInstanceByID(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	encryptedKey, _ := crypto.Encrypt("test-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (42, 'Test Instance', 'radarr', 'http://test:7878', ?, 1)`, encryptedKey)

	instance, err := client.GetInstanceByID(42)
	if err != nil {
		t.Fatalf("GetInstanceByID failed: %v", err)
	}

	if instance.ID != 42 {
		t.Errorf("Expected ID=42, got %d", instance.ID)
	}
	if instance.Name != "Test Instance" {
		t.Errorf("Expected name='Test Instance', got %q", instance.Name)
	}
	if instance.APIKey != "test-key" {
		t.Error("API key was not properly decrypted")
	}
}

func TestHTTPArrClient_GetInstanceByID_NotFound(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	_, err := client.GetInstanceByID(999)
	if err == nil {
		t.Error("Expected error for nonexistent instance")
	}
}

// =============================================================================
// Queue and History tests
// =============================================================================

func TestHTTPArrClient_GetQueueForPath(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/queue" {
			json.NewEncoder(w).Encode(QueueResponse{
				Page:         1,
				PageSize:     50,
				TotalRecords: 2,
				Records: []QueueItem{
					{ID: 1, Title: "Download 1", Status: "downloading", DownloadID: "abc123"},
					{ID: 2, Title: "Download 2", Status: "completed", DownloadID: "def456"},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	items, err := client.GetQueueForPath("/movies/Test Movie/movie.mkv")
	if err != nil {
		t.Fatalf("GetQueueForPath failed: %v", err)
	}

	if len(items) != 2 {
		t.Errorf("Expected 2 queue items, got %d", len(items))
	}
}

func TestHTTPArrClient_GetRecentHistoryForMediaByPath(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The actual endpoint is /api/v3/history/movie for Radarr
		if r.URL.Path == "/api/v3/history/movie" {
			// Returns array directly, not paginated response
			json.NewEncoder(w).Encode([]HistoryItem{
				{ID: 1, EventType: "grabbed", SourceTitle: "Test.Movie.2024", MovieID: 123},
				{ID: 2, EventType: "downloadFolderImported", SourceTitle: "Test.Movie.2024", MovieID: 123},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	history, err := client.GetRecentHistoryForMediaByPath("/movies/Test Movie/movie.mkv", 123, 10)
	if err != nil {
		t.Fatalf("GetRecentHistoryForMediaByPath failed: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("Expected 2 history items, got %d", len(history))
	}
}

// =============================================================================
// Error handling tests
// =============================================================================

func TestHTTPArrClient_NoInstanceForPath(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Don't insert any instances - should fail
	_, err := client.FindMediaByPath("/unknown/path/file.mkv")
	if err == nil {
		t.Error("Expected error when no instance matches path")
	}
}

func TestHTTPArrClient_ServerError_Retry(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	attempts := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Third attempt succeeds
		json.NewEncoder(w).Encode(ParseResult{
			Movie: &MediaItem{ID: 1, Title: "Movie", Path: "/movies/Movie"},
		})
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	mediaID, err := client.FindMediaByPath("/movies/Movie/file.mkv")
	if err != nil {
		t.Fatalf("Expected retry to succeed: %v", err)
	}
	if mediaID != 1 {
		t.Errorf("Expected mediaID=1, got %d", mediaID)
	}
	if attempts != 3 {
		t.Errorf("Expected 3 attempts, got %d", attempts)
	}
}

// =============================================================================
// Path matching tests
// =============================================================================

func TestHTTPArrClient_PathMatching(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(ParseResult{
			Movie: &MediaItem{ID: 1, Title: "Test", Path: "/movies/Test"},
		})
	}))
	defer server.Close()

	// Create instances with different paths
	key, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Movies', 'radarr', ?, ?, 1)`, server.URL, key)
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (2, 'Movies-Archive', 'radarr', ?, ?, 1)`, server.URL, key)

	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (2, '/local/movies-archive', '/movies-archive', 2, 0, 0)`)

	// Should match /movies, not /movies-archive
	_, err := client.FindMediaByPath("/movies/Test/file.mkv")
	if err != nil {
		t.Fatalf("Path matching failed: %v", err)
	}

	// Should match /movies-archive
	_, err = client.FindMediaByPath("/movies-archive/Old/file.mkv")
	if err != nil {
		t.Fatalf("Path matching failed for archive: %v", err)
	}
}
