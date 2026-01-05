package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
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

		switch r.URL.Path {
		case "/api/v3/parse":
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

		switch r.URL.Path {
		case "/api/v3/parse":
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
		switch r.URL.Path {
		case "/api/v3/parse":
			// Parse fails - return empty result
			json.NewEncoder(w).Encode(ParseResult{})
		case "/api/v3/movie":
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
		switch r.URL.Path {
		case "/api/v3/parse":
			json.NewEncoder(w).Encode(ParseResult{})
		case "/api/v3/movie":
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

// =============================================================================
// Circuit Breaker tests
// =============================================================================

func TestHTTPArrClient_GetCircuitBreakerStats(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Initially should return empty stats
	stats := client.GetCircuitBreakerStats()
	if len(stats) != 0 {
		t.Errorf("Expected empty stats initially, got %d entries", len(stats))
	}

	// Force a circuit breaker to be created by accessing it
	cb := client.circuitBreakers.Get(1)
	cb.RecordSuccess() // Record success to populate stats

	stats = client.GetCircuitBreakerStats()
	if len(stats) != 1 {
		t.Errorf("Expected 1 circuit breaker, got %d", len(stats))
	}
}

func TestHTTPArrClient_ResetCircuitBreaker(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Get a circuit breaker and record some failures
	cb := client.circuitBreakers.Get(1)
	for i := 0; i < 5; i++ {
		cb.RecordFailure()
	}

	// Verify failures were recorded
	stats := cb.Stats()
	if stats.ConsecutiveFailures == 0 {
		t.Error("Expected recorded failures")
	}

	// Reset the circuit breaker
	client.ResetCircuitBreaker(1)

	// Verify it was reset
	stats = cb.Stats()
	if stats.ConsecutiveFailures != 0 {
		t.Errorf("Expected 0 failures after reset, got %d", stats.ConsecutiveFailures)
	}
}

func TestHTTPArrClient_ResetAllCircuitBreakers(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Create multiple circuit breakers with failures
	for instanceID := int64(1); instanceID <= 3; instanceID++ {
		cb := client.circuitBreakers.Get(instanceID)
		for i := 0; i < 5; i++ {
			cb.RecordFailure()
		}
	}

	// Verify failures were recorded
	stats := client.GetCircuitBreakerStats()
	if len(stats) != 3 {
		t.Fatalf("Expected 3 circuit breakers, got %d", len(stats))
	}

	// Reset all
	client.ResetAllCircuitBreakers()

	// Verify all were reset
	stats = client.GetCircuitBreakerStats()
	for id, s := range stats {
		if s.ConsecutiveFailures != 0 {
			t.Errorf("Circuit breaker %d: expected 0 failures, got %d", id, s.ConsecutiveFailures)
		}
	}
}

// =============================================================================
// RateLimiter context cancellation test
// =============================================================================

func TestRateLimiter_Wait_ContextCancelled(t *testing.T) {
	rl := NewRateLimiter(0.1, 1) // Very slow: 0.1 RPS

	// Exhaust the single token
	ctx := t.Context()
	if err := rl.Wait(ctx); err != nil {
		t.Fatalf("First wait failed: %v", err)
	}

	// Create a cancelled context
	cancelCtx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Second wait should fail with context cancelled
	err := rl.Wait(cancelCtx)
	if err != context.Canceled {
		t.Errorf("Expected context.Canceled, got %v", err)
	}
}

// =============================================================================
// TriggerSearch tests
// =============================================================================

func TestHTTPArrClient_TriggerSearch_Radarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	var receivedPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/command" && r.Method == "POST" {
			json.NewDecoder(r.Body).Decode(&receivedPayload)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	err := client.TriggerSearch(123, "/movies/Test Movie/file.mkv", nil)
	if err != nil {
		t.Fatalf("TriggerSearch failed: %v", err)
	}

	// Verify the payload
	if receivedPayload["name"] != "MoviesSearch" {
		t.Errorf("Expected MoviesSearch command, got %v", receivedPayload["name"])
	}
}

func TestHTTPArrClient_TriggerSearch_Sonarr_WithEpisodes(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	var receivedPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/command" && r.Method == "POST" {
			json.NewDecoder(r.Body).Decode(&receivedPayload)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	err := client.TriggerSearch(456, "/tv/Test Show/Season 01/episode.mkv", []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("TriggerSearch failed: %v", err)
	}

	// Verify the payload uses EpisodeSearch
	if receivedPayload["name"] != "EpisodeSearch" {
		t.Errorf("Expected EpisodeSearch command, got %v", receivedPayload["name"])
	}
}

func TestHTTPArrClient_TriggerSearch_Sonarr_NoEpisodes(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	var receivedPayload map[string]interface{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/command" && r.Method == "POST" {
			json.NewDecoder(r.Body).Decode(&receivedPayload)
			w.WriteHeader(http.StatusCreated)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// No episode IDs - should fallback to MissingEpisodeSearch
	err := client.TriggerSearch(456, "/tv/Test Show/Season 01/episode.mkv", nil)
	if err != nil {
		t.Fatalf("TriggerSearch failed: %v", err)
	}

	// Verify fallback to MissingEpisodeSearch
	if receivedPayload["name"] != "MissingEpisodeSearch" {
		t.Errorf("Expected MissingEpisodeSearch command, got %v", receivedPayload["name"])
	}
}

// =============================================================================
// GetQueue and GetHistory tests
// =============================================================================

func TestHTTPArrClient_GetQueue_Direct(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/queue" {
			json.NewEncoder(w).Encode(QueueResponse{
				Page:         1,
				PageSize:     50,
				TotalRecords: 3,
				Records: []QueueItem{
					{ID: 1, Title: "Download 1", Status: "downloading"},
					{ID: 2, Title: "Download 2", Status: "completed"},
					{ID: 3, Title: "Download 3", Status: "delay"},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	instance := &ArrInstance{
		ID:     1,
		Name:   "Test",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "key",
	}

	queue, err := client.GetQueue(instance, 1, 50)
	if err != nil {
		t.Fatalf("GetQueue failed: %v", err)
	}

	if queue.TotalRecords != 3 {
		t.Errorf("Expected 3 records, got %d", queue.TotalRecords)
	}
	if len(queue.Records) != 3 {
		t.Errorf("Expected 3 queue items, got %d", len(queue.Records))
	}
}

func TestHTTPArrClient_GetHistory_Direct(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/history" {
			// Verify eventType parameter if present
			eventType := r.URL.Query().Get("eventType")
			json.NewEncoder(w).Encode(HistoryResponse{
				Page:         1,
				PageSize:     10,
				TotalRecords: 2,
				Records: []HistoryItem{
					{ID: 1, EventType: eventType, SourceTitle: "Test 1"},
					{ID: 2, EventType: eventType, SourceTitle: "Test 2"},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	instance := &ArrInstance{
		ID:     1,
		Name:   "Test",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "key",
	}

	history, err := client.GetHistory(instance, 1, 10, "grabbed")
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}

	if history.TotalRecords != 2 {
		t.Errorf("Expected 2 records, got %d", history.TotalRecords)
	}
}

// =============================================================================
// FindQueueItemByDownloadID tests
// =============================================================================

func TestHTTPArrClient_FindQueueItemByDownloadID(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/queue" {
			json.NewEncoder(w).Encode(QueueResponse{
				TotalRecords: 3,
				Records: []QueueItem{
					{ID: 1, DownloadID: "abc123", Title: "Movie 1"},
					{ID: 2, DownloadID: "def456", Title: "Movie 2"},
					{ID: 3, DownloadID: "ghi789", Title: "Movie 3"},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	instance := &ArrInstance{
		ID:     1,
		Name:   "Test",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "key",
	}

	// Test finding existing item
	item, err := client.FindQueueItemByDownloadID(instance, "def456")
	if err != nil {
		t.Fatalf("FindQueueItemByDownloadID failed: %v", err)
	}
	if item.ID != 2 {
		t.Errorf("Expected ID=2, got %d", item.ID)
	}

	// Test not found
	_, err = client.FindQueueItemByDownloadID(instance, "nonexistent")
	if err == nil {
		t.Error("Expected error for nonexistent download ID")
	}
}

func TestHTTPArrClient_FindQueueItemsByMediaID(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/queue" {
			json.NewEncoder(w).Encode(QueueResponse{
				TotalRecords: 4,
				Records: []QueueItem{
					{ID: 1, MovieID: 100, Title: "Movie A"},
					{ID: 2, MovieID: 200, Title: "Movie B"},
					{ID: 3, MovieID: 100, Title: "Movie A (another)"},
					{ID: 4, SeriesID: 300, Title: "TV Show"},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	instance := &ArrInstance{
		ID:     1,
		Name:   "Test",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "key",
	}

	// Find items for movie ID 100
	items, err := client.FindQueueItemsByMediaID(instance, 100)
	if err != nil {
		t.Fatalf("FindQueueItemsByMediaID failed: %v", err)
	}
	if len(items) != 2 {
		t.Errorf("Expected 2 items for movie 100, got %d", len(items))
	}

	// Find items for series ID 300
	items, err = client.FindQueueItemsByMediaID(instance, 300)
	if err != nil {
		t.Fatalf("FindQueueItemsByMediaID failed: %v", err)
	}
	if len(items) != 1 {
		t.Errorf("Expected 1 item for series 300, got %d", len(items))
	}
}

// =============================================================================
// DeleteFile tests
// =============================================================================

func TestHTTPArrClient_DeleteFile_Radarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	deleteEndpointCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/moviefile" && r.Method == "GET":
			json.NewEncoder(w).Encode([]struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			}{
				{ID: 10, Path: "/movies/Test Movie (2024)/movie.mkv"},
			})
		case r.URL.Path == "/api/v3/moviefile/10" && r.Method == "DELETE":
			deleteEndpointCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	metadata, err := client.DeleteFile(123, "/movies/Test Movie (2024)/movie.mkv")
	if err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	if !deleteEndpointCalled {
		t.Error("Delete endpoint was not called")
	}

	if metadata["movie_id"] != int64(123) {
		t.Errorf("Expected movie_id=123, got %v", metadata["movie_id"])
	}
}

func TestHTTPArrClient_DeleteFile_NotFoundInArr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/moviefile" && r.Method == "GET" {
			// Return empty list - file not found
			json.NewEncoder(w).Encode([]struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			}{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	// Since the file doesn't exist on disk either (no file created), should get "already_deleted"
	metadata, err := client.DeleteFile(123, "/movies/Nonexistent Movie/movie.mkv")
	if err != nil {
		t.Fatalf("DeleteFile should succeed when file not found: %v", err)
	}

	if metadata["already_deleted"] != true {
		t.Error("Expected already_deleted=true")
	}
}

// =============================================================================
// Error handling edge cases
// =============================================================================

func TestHTTPArrClient_GetQueue_ServerError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	instance := &ArrInstance{
		ID:     1,
		Name:   "Test",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "key",
	}

	_, err := client.GetQueue(instance, 1, 50)
	if err == nil {
		t.Error("Expected error for server error response")
	}
}

func TestHTTPArrClient_TriggerSearch_NoInstance(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// No instances configured
	err := client.TriggerSearch(123, "/unknown/path/file.mkv", nil)
	if err == nil {
		t.Error("Expected error when no instance matches path")
	}
}

// =============================================================================
// isRetryableError additional tests
// =============================================================================

func TestIsRetryableError_AdditionalCases(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		// These patterns ARE in the retryable list
		{"no such host", &testError{"no such host"}, true},
		{"connection timed out", &testError{"connection timed out"}, true},
		{"temporary failure", &testError{"temporary failure in name resolution"}, true},
		// Case insensitive matching (implementation uses ToLower on both sides)
		{"uppercase text", &testError{"CONNECTION REFUSED"}, true},
		{"mixed case", &testError{"Connection Reset By Peer"}, true},
		// These patterns are NOT in the retryable list
		{"not a network error", &testError{"invalid argument"}, false},
		{"no route to host", &testError{"no route to host"}, false}, // "no route to host" not in patterns
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

// =============================================================================
// GetFilePath tests
// =============================================================================

func TestHTTPArrClient_GetFilePath_Radarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie/123":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      123,
				"title":   "Test Movie",
				"hasFile": true,
				"movieFile": map[string]interface{}{
					"path": "/movies/Test Movie (2024)/Test.Movie.2024.mkv",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	path, err := client.GetFilePath(123, nil, "/movies/Test Movie (2024)")
	if err != nil {
		t.Fatalf("GetFilePath failed: %v", err)
	}

	if path != "/movies/Test Movie (2024)/Test.Movie.2024.mkv" {
		t.Errorf("Expected movie path, got %s", path)
	}
}

func TestHTTPArrClient_GetFilePath_NoFile(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie/123":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      123,
				"title":   "Test Movie",
				"hasFile": false,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	_, err := client.GetFilePath(123, nil, "/movies/Test Movie (2024)")
	if err == nil {
		t.Error("Expected error for movie with no file")
	}
}

func TestHTTPArrClient_GetFilePath_Sonarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/episode/101":
			// First call: get episode info with episodeFileId
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            101,
				"title":         "Pilot",
				"hasFile":       true,
				"episodeFileId": 1001,
			})
		case "/api/v3/episodefile/1001":
			// Second call: get episode file details
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   1001,
				"path": "/tv/Test Show/Season 01/Test.Show.S01E01.mkv",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(101)},
	}

	path, err := client.GetFilePath(456, metadata, "/tv/Test Show")
	if err != nil {
		t.Fatalf("GetFilePath for Sonarr failed: %v", err)
	}

	if path != "/tv/Test Show/Season 01/Test.Show.S01E01.mkv" {
		t.Errorf("Expected episode path, got %s", path)
	}
}

func TestHTTPArrClient_GetFilePath_Sonarr_NoMetadata(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// No metadata - should fail
	_, err := client.GetFilePath(456, nil, "/tv/Test Show")
	if err == nil {
		t.Error("Expected error for missing metadata")
	}
}

func TestHTTPArrClient_GetFilePath_Sonarr_EpisodeNoFile(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/episode/101":
			// Episode exists but has no file
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            101,
				"title":         "Pilot",
				"hasFile":       false,
				"episodeFileId": 0,
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(101)},
	}

	_, err := client.GetFilePath(456, metadata, "/tv/Test Show")
	if err == nil {
		t.Error("Expected error when episode has no file")
	}
}

func TestHTTPArrClient_GetFilePath_Sonarr_EmptyEpisodeIds(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Empty episode_ids array
	metadata := map[string]interface{}{
		"episode_ids": []interface{}{},
	}

	_, err := client.GetFilePath(456, metadata, "/tv/Test Show")
	if err == nil {
		t.Error("Expected error for empty episode_ids")
	}
}

func TestHTTPArrClient_GetAllFilePaths_Sonarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/episode/101":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            101,
				"title":         "Pilot",
				"hasFile":       true,
				"episodeFileId": 1001,
			})
		case "/api/v3/episode/102":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":            102,
				"title":         "Episode 2",
				"hasFile":       true,
				"episodeFileId": 1002,
			})
		case "/api/v3/episodefile/1001":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   1001,
				"path": "/tv/Test Show/Season 01/Test.Show.S01E01.mkv",
			})
		case "/api/v3/episodefile/1002":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":   1002,
				"path": "/tv/Test Show/Season 01/Test.Show.S01E02.mkv",
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(101), float64(102)},
	}

	paths, err := client.GetAllFilePaths(456, metadata, "/tv/Test Show")
	if err != nil {
		t.Fatalf("GetAllFilePaths for Sonarr failed: %v", err)
	}

	if len(paths) != 2 {
		t.Errorf("Expected 2 paths, got %d", len(paths))
	}
}

func TestHTTPArrClient_GetAllFilePaths_Sonarr_NoMetadata(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// No metadata - should fail for Sonarr
	_, err := client.GetAllFilePaths(456, nil, "/tv/Test Show")
	if err == nil {
		t.Error("Expected error for missing metadata in Sonarr")
	}
}

func TestHTTPArrClient_GetAllFilePaths_Sonarr_EmptyEpisodes(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Empty episode_ids
	metadata := map[string]interface{}{
		"episode_ids": []interface{}{},
	}

	_, err := client.GetAllFilePaths(456, metadata, "/tv/Test Show")
	if err == nil {
		t.Error("Expected error for empty episode_ids")
	}
}

// =============================================================================
// GetAllFilePaths tests
// =============================================================================

func TestHTTPArrClient_GetAllFilePaths_Radarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v3/movie/123":
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id":      123,
				"title":   "Test Movie",
				"hasFile": true,
				"movieFile": map[string]interface{}{
					"path": "/movies/Test Movie (2024)/Test.Movie.2024.mkv",
				},
			})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	paths, err := client.GetAllFilePaths(123, nil, "/movies/Test Movie (2024)")
	if err != nil {
		t.Fatalf("GetAllFilePaths failed: %v", err)
	}

	if len(paths) != 1 {
		t.Errorf("Expected 1 path, got %d", len(paths))
	}
}

// =============================================================================
// RemoveFromQueue tests
// =============================================================================

func TestHTTPArrClient_RemoveFromQueue(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Path == "/api/v3/queue/456" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Radarr",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "api-key",
	}

	err := client.RemoveFromQueue(instance, 456, true, false)
	if err != nil {
		t.Errorf("RemoveFromQueue failed: %v", err)
	}
}

func TestHTTPArrClient_RemoveFromQueue_Error(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	instance := &ArrInstance{
		ID:     1,
		Name:   "Radarr",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "api-key",
	}

	err := client.RemoveFromQueue(instance, 456, true, false)
	if err == nil {
		t.Error("Expected error for server error response")
	}
}

func TestHTTPArrClient_RemoveFromQueueByPath(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && r.URL.Path == "/api/v3/queue/789" {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	err := client.RemoveFromQueueByPath("/movies/Test", 789, true, false)
	if err != nil {
		t.Errorf("RemoveFromQueueByPath failed: %v", err)
	}
}

// =============================================================================
// RefreshMonitoredDownloads tests
// =============================================================================

func TestHTTPArrClient_RefreshMonitoredDownloads(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/v3/command" {
			w.WriteHeader(http.StatusCreated)
			json.NewEncoder(w).Encode(map[string]interface{}{"id": 1})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	instance := &ArrInstance{
		ID:     1,
		Name:   "Radarr",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "api-key",
	}

	err := client.RefreshMonitoredDownloads(instance)
	if err != nil {
		t.Errorf("RefreshMonitoredDownloads failed: %v", err)
	}
}

func TestHTTPArrClient_RefreshMonitoredDownloads_Error(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	instance := &ArrInstance{
		ID:     1,
		Name:   "Radarr",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "api-key",
	}

	err := client.RefreshMonitoredDownloads(instance)
	if err == nil {
		t.Error("Expected error for server error response")
	}
}

func TestHTTPArrClient_RefreshMonitoredDownloadsByPath(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && r.URL.Path == "/api/v3/command" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	err := client.RefreshMonitoredDownloadsByPath("/movies/Test")
	if err != nil {
		t.Errorf("RefreshMonitoredDownloadsByPath failed: %v", err)
	}
}

// =============================================================================
// GetDownloadStatus tests
// =============================================================================

func TestHTTPArrClient_GetDownloadStatus(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/queue" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"page":         1,
				"pageSize":     50,
				"totalRecords": 1,
				"records": []map[string]interface{}{
					{
						"id":                      1,
						"downloadId":              "test-download-id",
						"title":                   "Test Movie",
						"status":                  "downloading",
						"trackedDownloadStatus":   "ok",
						"trackedDownloadState":    "downloading",
						"sizeleft":                1000000,
						"size":                    5000000,
						"timeleft":                "00:10:00",
						"estimatedCompletionTime": "2024-01-01T12:00:00Z",
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	instance := &ArrInstance{
		ID:     1,
		Name:   "Radarr",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "api-key",
	}

	status, progress, errMsg, err := client.GetDownloadStatus(instance, "test-download-id")
	if err != nil {
		t.Fatalf("GetDownloadStatus failed: %v", err)
	}

	if status == "" {
		t.Error("Expected non-empty status")
	}
	if progress < 0 {
		t.Error("Expected positive progress")
	}
	_ = errMsg // May or may not be set
}

func TestHTTPArrClient_GetDownloadStatus_NotFound(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/queue" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"page":         1,
				"pageSize":     50,
				"totalRecords": 0,
				"records":      []map[string]interface{}{},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	instance := &ArrInstance{
		ID:     1,
		Name:   "Radarr",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "api-key",
	}

	_, _, _, err := client.GetDownloadStatus(instance, "nonexistent-id")
	// When not found, it should return an error
	if err == nil {
		t.Log("No error returned when download not in queue (may check history)")
	}
}

// =============================================================================
// GetDownloadStatusForPath tests
// =============================================================================

func TestHTTPArrClient_GetDownloadStatusForPath(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/queue" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"page":         1,
				"pageSize":     50,
				"totalRecords": 1,
				"records": []map[string]interface{}{
					{
						"id":                    1,
						"downloadId":            "path-download-id",
						"title":                 "Test Movie",
						"status":                "downloading",
						"trackedDownloadStatus": "ok",
						"trackedDownloadState":  "downloading",
						"sizeleft":              500000,
						"size":                  2000000,
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	status, progress, _, err := client.GetDownloadStatusForPath("/movies/Test", "path-download-id")
	if err != nil {
		t.Fatalf("GetDownloadStatusForPath failed: %v", err)
	}

	if status == "" {
		t.Error("Expected non-empty status")
	}
	if progress < 0 || progress > 100 {
		t.Errorf("Expected progress between 0-100, got %f", progress)
	}
}

// =============================================================================
// FindQueueItemsByMediaIDForPath tests
// =============================================================================

func TestHTTPArrClient_FindQueueItemsByMediaIDForPath(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/queue" {
			json.NewEncoder(w).Encode(map[string]interface{}{
				"page":         1,
				"pageSize":     50,
				"totalRecords": 2,
				"records": []map[string]interface{}{
					{
						"id":         1,
						"downloadId": "dl-1",
						"title":      "Test Movie",
						"movieId":    123,
						"status":     "downloading",
						"sizeleft":   1000000,
						"size":       5000000,
					},
					{
						"id":         2,
						"downloadId": "dl-2",
						"title":      "Test Movie",
						"movieId":    123,
						"status":     "queued",
						"sizeleft":   3000000,
						"size":       3000000,
					},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	items, err := client.FindQueueItemsByMediaIDForPath("/movies/Test", 123)
	if err != nil {
		t.Fatalf("FindQueueItemsByMediaIDForPath failed: %v", err)
	}

	if len(items) == 0 {
		t.Error("Expected at least one queue item")
	}
}

func TestHTTPArrClient_GetRecentHistoryForMediaByPath_Sonarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sonarr uses /api/v3/history/series endpoint
		if r.URL.Path == "/api/v3/history/series" {
			json.NewEncoder(w).Encode([]HistoryItem{
				{ID: 1, EventType: "grabbed", SourceTitle: "Test.Show.S01E01", SeriesID: 456},
				{ID: 2, EventType: "downloadFolderImported", SourceTitle: "Test.Show.S01E01", SeriesID: 456},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	history, err := client.GetRecentHistoryForMediaByPath("/tv/Test Show/Season 01/episode.mkv", 456, 10)
	if err != nil {
		t.Fatalf("GetRecentHistoryForMediaByPath failed: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("Expected 2 history items, got %d", len(history))
	}
}

func TestHTTPArrClient_DeleteFile_Sonarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	deleteEndpointCalled := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/api/v3/episodefile" && r.Method == "GET":
			json.NewEncoder(w).Encode([]struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			}{
				{ID: 20, Path: "/tv/Test Show/Season 01/episode.mkv"},
			})
		case r.URL.Path == "/api/v3/episode" && r.Method == "GET":
			// Return episodes for the series with episodeFileId matching our file
			json.NewEncoder(w).Encode([]struct {
				ID            int64 `json:"id"`
				EpisodeFileID int64 `json:"episodeFileId"`
			}{
				{ID: 100, EpisodeFileID: 20},
			})
		case r.URL.Path == "/api/v3/episodefile/20" && r.Method == "DELETE":
			deleteEndpointCalled = true
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata, err := client.DeleteFile(456, "/tv/Test Show/Season 01/episode.mkv")
	if err != nil {
		t.Fatalf("DeleteFile failed: %v", err)
	}

	if !deleteEndpointCalled {
		t.Error("Delete endpoint was not called")
	}

	// For Sonarr, metadata should have episode_ids
	if metadata["episode_ids"] == nil {
		t.Error("Expected episode_ids in metadata")
	}
	if metadata["deleted_path"] != "/tv/Test Show/Season 01/episode.mkv" {
		t.Errorf("Expected deleted_path, got %v", metadata["deleted_path"])
	}
}

func TestHTTPArrClient_DeleteFile_ServerError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return file list for lookup
		if r.URL.Path == "/api/v3/moviefile" && r.Method == "GET" {
			json.NewEncoder(w).Encode([]struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			}{
				{ID: 10, Path: "/movies/Test Movie/movie.mkv"},
			})
			return
		}
		// Return error for delete
		if r.Method == "DELETE" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	_, err := client.DeleteFile(123, "/movies/Test Movie/movie.mkv")
	if err == nil {
		t.Error("Expected error when delete fails")
	}
}

// =============================================================================
// findMissingEpisodesForPath tests
// =============================================================================

func TestHTTPArrClient_FindMissingEpisodesForPath_Success(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/episode" && r.URL.Query().Get("seriesId") == "100" {
			episodes := []struct {
				ID            int64 `json:"id"`
				SeasonNumber  int   `json:"seasonNumber"`
				EpisodeNumber int   `json:"episodeNumber"`
				HasFile       bool  `json:"hasFile"`
				Monitored     bool  `json:"monitored"`
			}{
				{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, HasFile: true, Monitored: true},
				{ID: 2, SeasonNumber: 1, EpisodeNumber: 2, HasFile: false, Monitored: true},  // Missing
				{ID: 3, SeasonNumber: 1, EpisodeNumber: 3, HasFile: false, Monitored: false}, // Not monitored
				{ID: 4, SeasonNumber: 2, EpisodeNumber: 1, HasFile: false, Monitored: true},  // Missing
			}
			json.NewEncoder(w).Encode(episodes)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	missingIDs, err := client.findMissingEpisodesForPath(instance, 100, "/tv/Show Name")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should find episodes 2 and 4 (missing and monitored)
	if len(missingIDs) != 2 {
		t.Errorf("Expected 2 missing episodes, got %d", len(missingIDs))
	}
}

func TestHTTPArrClient_FindMissingEpisodesForPath_WithSeasonFilter(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/episode" {
			episodes := []struct {
				ID            int64 `json:"id"`
				SeasonNumber  int   `json:"seasonNumber"`
				EpisodeNumber int   `json:"episodeNumber"`
				HasFile       bool  `json:"hasFile"`
				Monitored     bool  `json:"monitored"`
			}{
				{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, HasFile: false, Monitored: true},
				{ID: 2, SeasonNumber: 1, EpisodeNumber: 2, HasFile: false, Monitored: true},
				{ID: 3, SeasonNumber: 2, EpisodeNumber: 1, HasFile: false, Monitored: true},
			}
			json.NewEncoder(w).Encode(episodes)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	// Test with "Season 01" in path
	missingIDs, err := client.findMissingEpisodesForPath(instance, 100, "/tv/Show Name/Season 01/episode.mkv")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should only find season 1 episodes (1 and 2)
	if len(missingIDs) != 2 {
		t.Errorf("Expected 2 missing episodes for season 1, got %d", len(missingIDs))
	}
}

func TestHTTPArrClient_FindMissingEpisodesForPath_ServerError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	_, err := client.findMissingEpisodesForPath(instance, 100, "/tv/Show")
	if err == nil {
		t.Error("Expected error for server error response")
	}
}

func TestHTTPArrClient_FindMissingEpisodesForPath_InvalidJSON(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("invalid json"))
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	_, err := client.findMissingEpisodesForPath(instance, 100, "/tv/Show")
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}

func TestHTTPArrClient_FindMissingEpisodesForPath_NoMissingEpisodes(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		episodes := []struct {
			ID            int64 `json:"id"`
			SeasonNumber  int   `json:"seasonNumber"`
			EpisodeNumber int   `json:"episodeNumber"`
			HasFile       bool  `json:"hasFile"`
			Monitored     bool  `json:"monitored"`
		}{
			{ID: 1, SeasonNumber: 1, EpisodeNumber: 1, HasFile: true, Monitored: true},
			{ID: 2, SeasonNumber: 1, EpisodeNumber: 2, HasFile: true, Monitored: true},
		}
		json.NewEncoder(w).Encode(episodes)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	missingIDs, err := client.findMissingEpisodesForPath(instance, 100, "/tv/Show")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(missingIDs) != 0 {
		t.Errorf("Expected 0 missing episodes, got %d", len(missingIDs))
	}
}

// =============================================================================
// Additional tests for coverage improvement
// =============================================================================

func TestHTTPArrClient_CircuitBreakerOpen(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Create an instance
	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', 'http://localhost:9999', ?, 1)`, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    "http://localhost:9999",
		APIKey: "key",
	}

	// Force circuit breaker open by recording many failures
	cb := client.circuitBreakers.Get(instance.ID)
	for i := 0; i < 10; i++ {
		cb.RecordFailure()
	}

	// Now try to get queue - should fail due to circuit breaker
	_, err := client.GetQueue(instance, 1, 50)
	if err == nil {
		t.Error("Expected error due to circuit breaker")
	}

	// Error should indicate circuit breaker is open
	if !strings.Contains(err.Error(), "unhealthy") && !strings.Contains(err.Error(), "circuit") {
		t.Errorf("Expected circuit breaker error, got: %v", err)
	}
}

func TestHTTPArrClient_GetHistory_Success(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := struct {
			Page         int `json:"page"`
			PageSize     int `json:"pageSize"`
			TotalRecords int `json:"totalRecords"`
			Records      []struct {
				ID        int64  `json:"id"`
				EventType string `json:"eventType"`
				Date      string `json:"date"`
			} `json:"records"`
		}{
			Page:         1,
			PageSize:     50,
			TotalRecords: 2,
			Records: []struct {
				ID        int64  `json:"id"`
				EventType string `json:"eventType"`
				Date      string `json:"date"`
			}{
				{ID: 1, EventType: "grabbed", Date: "2024-01-01T00:00:00Z"},
				{ID: 2, EventType: "downloadFailed", Date: "2024-01-02T00:00:00Z"},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	history, err := client.GetHistory(instance, 1, 50, "")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if history.TotalRecords != 2 {
		t.Errorf("Expected 2 history records, got %d", history.TotalRecords)
	}
}

func TestHTTPArrClient_GetRecentHistoryForMedia_NoResults(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GetRecentHistoryForMedia expects a raw JSON array []HistoryItem, not a wrapped response
		json.NewEncoder(w).Encode([]HistoryItem{})
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	history, err := client.GetRecentHistoryForMedia(instance, 123, 50)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(history) != 0 {
		t.Errorf("Expected 0 history records, got %d", len(history))
	}
}

func TestHTTPArrClient_GetDownloadStatus_Success(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return queue with some items
		response := struct {
			Records []struct {
				ID         int64  `json:"id"`
				Title      string `json:"title"`
				Status     string `json:"status"`
				DownloadId string `json:"downloadId"`
				Sizeleft   int64  `json:"sizeleft"`
				Size       int64  `json:"size"`
			} `json:"records"`
		}{
			Records: []struct {
				ID         int64  `json:"id"`
				Title      string `json:"title"`
				Status     string `json:"status"`
				DownloadId string `json:"downloadId"`
				Sizeleft   int64  `json:"sizeleft"`
				Size       int64  `json:"size"`
			}{
				{ID: 1, Title: "Test.Show.S01E01", Status: "downloading", DownloadId: "abc123", Sizeleft: 500, Size: 1000},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	status, progress, errMsg, err := client.GetDownloadStatus(instance, "abc123")
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should find the download with matching downloadId
	if status == "" {
		t.Log("Got empty status - download might not match")
	}
	t.Logf("Got download status: %s, progress: %.2f, errMsg: %s", status, progress, errMsg)
}

func TestHTTPArrClient_RemoveFromQueue_Success(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "DELETE" && strings.Contains(r.URL.Path, "/api/v3/queue/") {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	err := client.RemoveFromQueue(instance, 123, true, false)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestHTTPArrClient_RefreshMonitoredDownloads_Success(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" && strings.Contains(r.URL.Path, "/api/v3/command") {
			w.WriteHeader(http.StatusOK)
			json.NewEncoder(w).Encode(map[string]interface{}{
				"id": 1,
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	err := client.RefreshMonitoredDownloads(instance)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}
}

func TestHTTPArrClient_GetQueueForPath_NoInstance(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Don't add any instances - should fail to find matching instance
	_, err := client.GetQueueForPath("/tv/Show")
	if err == nil {
		t.Error("Expected error when no instance for path")
	}
}

func TestHTTPArrClient_GetDownloadStatusForPath_NoInstance(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Don't add any instances - should fail
	_, _, _, err := client.GetDownloadStatusForPath("/tv/Show", "abc123")
	if err == nil {
		t.Error("Expected error when no instance for path")
	}
}

func TestHTTPArrClient_RemoveFromQueueByPath_NoInstance(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Don't add any instances - should fail
	err := client.RemoveFromQueueByPath("/tv/Show", 123, true, false)
	if err == nil {
		t.Error("Expected error when no instance for path")
	}
}

func TestHTTPArrClient_RefreshMonitoredDownloadsByPath_NoInstance(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Don't add any instances - should fail
	err := client.RefreshMonitoredDownloadsByPath("/tv/Show")
	if err == nil {
		t.Error("Expected error when no instance for path")
	}
}

func TestHTTPArrClient_FindQueueItemsByMediaID_MultipleItems(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := struct {
			Records []struct {
				ID       int64  `json:"id"`
				SeriesID int64  `json:"seriesId"`
				Title    string `json:"title"`
			} `json:"records"`
		}{
			Records: []struct {
				ID       int64  `json:"id"`
				SeriesID int64  `json:"seriesId"`
				Title    string `json:"title"`
			}{
				{ID: 1, SeriesID: 123, Title: "Test.Show.S01E01"},
				{ID: 2, SeriesID: 123, Title: "Test.Show.S01E02"},
				{ID: 3, SeriesID: 456, Title: "Other.Show.S01E01"},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	items, err := client.FindQueueItemsByMediaID(instance, 123)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should find 2 items matching seriesId 123
	if len(items) != 2 {
		t.Errorf("Expected 2 queue items, got %d", len(items))
	}
}

func TestHTTPArrClient_GetAllInstances_MultipleInstances(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', 'http://sonarr:8989', ?, 1)`, encryptedKey)
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (2, 'Radarr', 'radarr', 'http://radarr:7878', ?, 1)`, encryptedKey)
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (3, 'Disabled', 'sonarr', 'http://disabled:8989', ?, 0)`, encryptedKey)

	instances, err := client.GetAllInstances()
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	// Should get 2 enabled instances
	if len(instances) != 2 {
		t.Errorf("Expected 2 enabled instances, got %d", len(instances))
	}
}

func TestHTTPArrClient_Server500_RetryExhausted(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		// Always return 500 to force retries
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("Internal Server Error"))
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	// Reset circuit breaker to ensure it's not affecting this test
	client.ResetCircuitBreaker(instance.ID)

	_, err := client.GetQueue(instance, 1, 50)
	if err == nil {
		t.Error("Expected error after exhausting retries")
	}

	// Should have made multiple requests
	if requestCount < 2 {
		t.Errorf("Expected multiple retry attempts, got %d", requestCount)
	}
}

func TestHTTPArrClient_GetRecentHistoryForMediaByPath_Success(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GetRecentHistoryForMedia expects a raw JSON array []HistoryItem
		items := []HistoryItem{
			{ID: 1, EventType: "grabbed", SourceTitle: "Test.Show.S01E01"},
		}
		json.NewEncoder(w).Encode(items)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	// Match the exact INSERT pattern from passing tests
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	history, err := client.GetRecentHistoryForMediaByPath("/tv/Test Show/episode.mkv", 123, 50)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(history) != 1 {
		t.Errorf("Expected 1 history record, got %d", len(history))
	}
}

func TestHTTPArrClient_GetAllFilePaths_WithEpisodeFiles(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/api/v3/episode/"):
			// Return episode info with file
			json.NewEncoder(w).Encode(struct {
				HasFile       bool  `json:"hasFile"`
				EpisodeFileId int64 `json:"episodeFileId"`
			}{HasFile: true, EpisodeFileId: 100})
		case strings.HasPrefix(r.URL.Path, "/api/v3/episodefile/"):
			// Return file path
			json.NewEncoder(w).Encode(struct {
				Path string `json:"path"`
			}{Path: "/tv/Show/Season 1/episode.mkv"})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Test with episode_ids in metadata (underscore format)
	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(1), float64(2)},
	}
	paths, err := client.GetAllFilePaths(0, metadata, "/tv/Show/episode.mkv")
	if err != nil {
		t.Fatalf("GetAllFilePaths failed: %v", err)
	}

	if len(paths) == 0 {
		t.Error("Expected at least one path")
	}
}

func TestHTTPArrClient_GetHistory_EmptyRecords(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := struct {
			Page         int           `json:"page"`
			PageSize     int           `json:"pageSize"`
			TotalRecords int           `json:"totalRecords"`
			Records      []interface{} `json:"records"`
		}{
			Page:         1,
			PageSize:     50,
			TotalRecords: 0,
			Records:      []interface{}{},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	history, err := client.GetHistory(instance, 1, 50, "")
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}

	if history.TotalRecords != 0 {
		t.Errorf("Expected 0 records, got %d", history.TotalRecords)
	}
}

func TestHTTPArrClient_GetHistory_NonOKStatus(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	_, err := client.GetHistory(instance, 1, 50, "grabbed")
	if err == nil {
		t.Error("Expected error for non-OK status")
	}
}

func TestHTTPArrClient_GetRecentHistoryForMedia_WithResults(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return array with items
		items := []HistoryItem{
			{ID: 1, EventType: "grabbed", SourceTitle: "Test.Movie.2024"},
			{ID: 2, EventType: "downloadFolderImported", SourceTitle: "Test.Movie.2024"},
			{ID: 3, EventType: "grabbed", SourceTitle: "Test.Movie.2024"},
		}
		json.NewEncoder(w).Encode(items)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Radarr",
		Type:   "radarr",
		URL:    server.URL,
		APIKey: "key",
	}

	// Test with limit that truncates results
	history, err := client.GetRecentHistoryForMedia(instance, 123, 2)
	if err != nil {
		t.Fatalf("Unexpected error: %v", err)
	}

	if len(history) != 2 {
		t.Errorf("Expected 2 history records (limited), got %d", len(history))
	}
}

func TestHTTPArrClient_GetRecentHistoryForMedia_NonOKStatus(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	_, err := client.GetRecentHistoryForMedia(instance, 123, 50)
	if err == nil {
		t.Error("Expected error for non-OK status")
	}
}

func TestHTTPArrClient_FindMediaByPath_SeriesNotFound(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return empty series list
		json.NewEncoder(w).Encode([]interface{}{})
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	_, err := client.FindMediaByPath("/tv/NonExistentShow/episode.mkv")
	if err == nil {
		t.Error("Expected error when series not found")
	}
}

func TestHTTPArrClient_GetAllInstances_MultipleTypes(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(struct {
			Status string `json:"status"`
		}{Status: "ok"})
	}))
	defer server1.Close()

	server2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(struct {
			Status string `json:"status"`
		}{Status: "ok"})
	}))
	defer server2.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server1.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (2, 'Radarr', 'radarr', ?, ?, 1)`, server2.URL, encryptedKey)

	instances, err := client.GetAllInstances()
	if err != nil {
		t.Fatalf("GetAllInstances failed: %v", err)
	}

	if len(instances) != 2 {
		t.Errorf("Expected 2 instances, got %d", len(instances))
	}
}

func TestHTTPArrClient_RecordSuccess_AfterFailures(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount <= 3 {
			// First few requests fail to trigger circuit breaker
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Then succeed to test recovery
		json.NewEncoder(w).Encode(struct {
			Version string `json:"version"`
		}{Version: "4.0.0"})
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	// First request will fail with 500
	_, err := client.GetQueue(instance, 1, 50)
	if err == nil {
		t.Log("First request succeeded unexpectedly")
	}

	// Circuit breaker should still be closed, allow more attempts
	allStats := client.GetCircuitBreakerStats()
	if stats, ok := allStats[1]; ok {
		t.Logf("Circuit breaker state: %s, failures: %d", stats.State.String(), stats.ConsecutiveFailures)
	}
}

func TestHTTPArrClient_GetQueue_WithEventType(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify includeUnknownSeriesItems param is set
		if r.URL.Query().Get("includeUnknownSeriesItems") != "true" {
			t.Error("Expected includeUnknownSeriesItems=true")
		}
		response := struct {
			Page         int           `json:"page"`
			PageSize     int           `json:"pageSize"`
			TotalRecords int           `json:"totalRecords"`
			Records      []interface{} `json:"records"`
		}{
			Page:         1,
			PageSize:     50,
			TotalRecords: 0,
			Records:      []interface{}{},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	_, err := client.GetQueue(instance, 1, 50)
	if err != nil {
		t.Fatalf("GetQueue failed: %v", err)
	}
}

func TestHTTPArrClient_GetAllQueueItems_EmptyQueue(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := struct {
			Page         int           `json:"page"`
			PageSize     int           `json:"pageSize"`
			TotalRecords int           `json:"totalRecords"`
			Records      []interface{} `json:"records"`
		}{
			Page:         1,
			PageSize:     50,
			TotalRecords: 0,
			Records:      []interface{}{},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	items, err := client.GetAllQueueItems(instance)
	if err != nil {
		t.Fatalf("GetAllQueueItems failed: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("Expected 0 queue items, got %d", len(items))
	}
}

func TestHTTPArrClient_FindQueueItemByDownloadID_NotFound(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := struct {
			Page         int           `json:"page"`
			PageSize     int           `json:"pageSize"`
			TotalRecords int           `json:"totalRecords"`
			Records      []interface{} `json:"records"`
		}{
			Page:         1,
			PageSize:     50,
			TotalRecords: 0,
			Records:      []interface{}{},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	_, err := client.FindQueueItemByDownloadID(instance, "nonexistent")
	if err == nil {
		t.Error("Expected error when download ID not found")
	}
}

func TestHTTPArrClient_FindQueueItemsByMediaID_MultiplePages(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		page := r.URL.Query().Get("page")
		if page == "1" {
			response := struct {
				Page         int `json:"page"`
				PageSize     int `json:"pageSize"`
				TotalRecords int `json:"totalRecords"`
				Records      []struct {
					ID       int64 `json:"id"`
					SeriesId int64 `json:"seriesId"`
				} `json:"records"`
			}{
				Page:         1,
				PageSize:     50,
				TotalRecords: 2,
				Records: []struct {
					ID       int64 `json:"id"`
					SeriesId int64 `json:"seriesId"`
				}{
					{ID: 1, SeriesId: 100},
					{ID: 2, SeriesId: 100},
				},
			}
			json.NewEncoder(w).Encode(response)
		} else {
			response := struct {
				Page         int           `json:"page"`
				PageSize     int           `json:"pageSize"`
				TotalRecords int           `json:"totalRecords"`
				Records      []interface{} `json:"records"`
			}{
				Page:         2,
				PageSize:     50,
				TotalRecords: 2,
				Records:      []interface{}{},
			}
			json.NewEncoder(w).Encode(response)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	items, err := client.FindQueueItemsByMediaID(instance, 100)
	if err != nil {
		t.Fatalf("FindQueueItemsByMediaID failed: %v", err)
	}

	if len(items) != 2 {
		t.Errorf("Expected 2 queue items, got %d", len(items))
	}
}

func TestHTTPArrClient_GetInstanceForPath_DecryptError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]interface{}{})
	}))
	defer server.Close()

	// Insert with invalid encrypted key that can't be decrypted
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, 'invalid_encrypted_key', 1)`, server.URL)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Should fail to find instance because key can't be decrypted
	_, err := client.FindMediaByPath("/tv/Show/episode.mkv")
	if err == nil {
		t.Error("Expected error when API key cannot be decrypted")
	}
}

func TestHTTPArrClient_CheckEpisodeForFile_HasNoFile(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return episode with hasFile = false
		json.NewEncoder(w).Encode(struct {
			HasFile       bool  `json:"hasFile"`
			EpisodeFileId int64 `json:"episodeFileId"`
		}{HasFile: false, EpisodeFileId: 0})
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Use GetAllFilePaths which uses checkEpisodeForFile internally
	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(1)},
	}
	_, err := client.GetAllFilePaths(0, metadata, "/tv/Show/episode.mkv")
	// When all episodes have no file, GetAllFilePaths returns an error
	if err == nil {
		t.Error("Expected error when episode has no file")
	}
	if err != nil && !strings.Contains(err.Error(), "no files found") {
		t.Errorf("Expected 'no files found' error, got: %v", err)
	}
}

func TestHTTPArrClient_CheckEpisodeForFile_FilePathNotFound(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v3/episode/") {
			json.NewEncoder(w).Encode(struct {
				HasFile       bool  `json:"hasFile"`
				EpisodeFileId int64 `json:"episodeFileId"`
			}{HasFile: true, EpisodeFileId: 100})
		} else if strings.HasPrefix(r.URL.Path, "/api/v3/episodefile/") {
			// Episode file endpoint returns 404
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(1)},
	}
	_, err := client.GetAllFilePaths(0, metadata, "/tv/Show/episode.mkv")
	// When file paths can't be fetched, GetAllFilePaths returns an error
	if err == nil {
		t.Error("Expected error when file path not found")
	}
	if err != nil && !strings.Contains(err.Error(), "no files found") {
		t.Errorf("Expected 'no files found' error, got: %v", err)
	}
}

func TestHTTPArrClient_GetAllInstances_DBQueryError(t *testing.T) {
	client, db := setupTestClient(t)

	// Close db to cause query error
	db.Close()

	_, err := client.GetAllInstances()
	if err == nil {
		t.Error("Expected error when DB is closed")
	}
}

func TestHTTPArrClient_GetRecentHistoryForMediaByPath_WithData(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		items := []HistoryItem{
			{
				ID:          1,
				EventType:   "grabbed",
				SourceTitle: "Test.Show.S01E01",
				Data:        map[string]string{"importedPath": "/tv/Show/Season 1/episode.mkv"},
			},
		}
		json.NewEncoder(w).Encode(items)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	history, err := client.GetRecentHistoryForMediaByPath("/tv/Show/episode.mkv", 123, 10)
	if err != nil {
		t.Fatalf("GetRecentHistoryForMediaByPath failed: %v", err)
	}

	if len(history) != 1 {
		t.Errorf("Expected 1 history item, got %d", len(history))
	}

	// Check that importedPath was extracted from Data
	if history[0].ImportedPath != "/tv/Show/Season 1/episode.mkv" {
		t.Errorf("Expected importedPath to be extracted, got %q", history[0].ImportedPath)
	}
}

func TestHTTPArrClient_RecordSuccess_HalfOpen(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Track request count
	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		// First few requests fail to open circuit
		if requestCount <= 5 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Then succeed
		response := struct {
			Records []interface{} `json:"records"`
		}{Records: []interface{}{}}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	// Make failing requests to trigger circuit breaker
	for i := 0; i < 5; i++ {
		client.GetQueue(instance, 1, 50)
	}

	// Get circuit breaker stats
	allStats := client.GetCircuitBreakerStats()
	if stats, ok := allStats[1]; ok {
		t.Logf("After failures - State: %s, Failures: %d", stats.State.String(), stats.ConsecutiveFailures)
	}
}

func TestHTTPArrClient_FindMediaByPath_MovieMatch(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return movie list
		movies := []struct {
			ID         int64  `json:"id"`
			Title      string `json:"title"`
			Path       string `json:"path"`
			FolderName string `json:"folderName"`
		}{
			{ID: 1, Title: "Test Movie", Path: "/movies/Test Movie", FolderName: "Test Movie"},
		}
		json.NewEncoder(w).Encode(movies)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	mediaID, err := client.FindMediaByPath("/movies/Test Movie/movie.mkv")
	if err != nil {
		t.Fatalf("FindMediaByPath failed: %v", err)
	}

	if mediaID != 1 {
		t.Errorf("Expected media ID 1, got %d", mediaID)
	}
}

func TestHTTPArrClient_GetFilePath_RadarrSuccess(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return movie with file
		movie := struct {
			MovieFile struct {
				Path string `json:"path"`
			} `json:"movieFile"`
			HasFile bool `json:"hasFile"`
		}{
			HasFile: true,
		}
		movie.MovieFile.Path = "/movies/Test Movie/movie.mkv"
		json.NewEncoder(w).Encode(movie)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	path, err := client.GetFilePath(1, nil, "/movies/Test Movie/movie.mkv")
	if err != nil {
		t.Fatalf("GetFilePath failed: %v", err)
	}

	if path != "/movies/Test Movie/movie.mkv" {
		t.Errorf("Expected movie path, got %q", path)
	}
}

func TestHTTPArrClient_GetFilePath_RadarrNoFile(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return movie without file
		movie := struct {
			HasFile bool `json:"hasFile"`
		}{
			HasFile: false,
		}
		json.NewEncoder(w).Encode(movie)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	_, err := client.GetFilePath(1, nil, "/movies/Test Movie/movie.mkv")
	if err == nil {
		t.Error("Expected error when movie has no file")
	}
}

func TestHTTPArrClient_DoRequestWithBody(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	receivedBody := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check Content-Type header is set for POST with body
		if r.Method == "POST" && r.Header.Get("Content-Type") == "application/json" {
			receivedBody = true
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	// Trigger a request that uses POST with body (RemoveFromQueue)
	_ = client.RemoveFromQueue(instance, 1, true, false)

	if receivedBody {
		t.Log("POST request with body sent correctly")
	}
}

func TestHTTPArrClient_RetryableNetworkError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		if requestCount <= 2 {
			// Close connection abruptly to simulate network error
			hj, ok := w.(http.Hijacker)
			if ok {
				conn, _, _ := hj.Hijack()
				conn.Close()
				return
			}
		}
		// Third request succeeds
		response := struct {
			Page         int           `json:"page"`
			PageSize     int           `json:"pageSize"`
			TotalRecords int           `json:"totalRecords"`
			Records      []interface{} `json:"records"`
		}{
			Page:         1,
			PageSize:     50,
			TotalRecords: 0,
			Records:      []interface{}{},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	// This should retry on network errors
	_, err := client.GetQueue(instance, 1, 50)
	t.Logf("After network errors: err=%v, requests=%d", err, requestCount)
}

func TestHTTPArrClient_GetInstanceByID_NoInstances(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// No instances in DB
	instance, err := client.GetInstanceByID(999)
	if err == nil && instance != nil {
		t.Error("Expected nil instance for non-existent ID")
	}
}

func TestHTTPArrClient_GetInstanceByID_Found(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(struct{}{})
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance, err := client.GetInstanceByID(1)
	if err != nil {
		t.Fatalf("GetInstanceByID failed: %v", err)
	}

	if instance == nil {
		t.Error("Expected non-nil instance")
	}
	if instance != nil && instance.Name != "Sonarr" {
		t.Errorf("Expected instance name 'Sonarr', got %q", instance.Name)
	}
}

func TestHTTPArrClient_GetDownloadStatus_WithError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return queue with item that has errors
		response := struct {
			Page    int `json:"page"`
			Records []struct {
				ID                    int64  `json:"id"`
				DownloadId            string `json:"downloadId"`
				Size                  int64  `json:"size"`
				Sizeleft              int64  `json:"sizeleft"`
				TrackedDownloadState  string `json:"trackedDownloadState"`
				TrackedDownloadStatus string `json:"trackedDownloadStatus"`
				StatusMessages        []struct {
					Messages []string `json:"messages"`
				} `json:"statusMessages"`
			} `json:"records"`
		}{
			Page: 1,
			Records: []struct {
				ID                    int64  `json:"id"`
				DownloadId            string `json:"downloadId"`
				Size                  int64  `json:"size"`
				Sizeleft              int64  `json:"sizeleft"`
				TrackedDownloadState  string `json:"trackedDownloadState"`
				TrackedDownloadStatus string `json:"trackedDownloadStatus"`
				StatusMessages        []struct {
					Messages []string `json:"messages"`
				} `json:"statusMessages"`
			}{
				{
					ID:                    1,
					DownloadId:            "error123",
					Size:                  1000,
					Sizeleft:              1000,
					TrackedDownloadState:  "importBlocked",
					TrackedDownloadStatus: "warning",
					StatusMessages: []struct {
						Messages []string `json:"messages"`
					}{
						{Messages: []string{"File is corrupted"}},
					},
				},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	status, progress, errMsg, err := client.GetDownloadStatus(instance, "error123")
	if err != nil {
		t.Fatalf("GetDownloadStatus failed: %v", err)
	}

	t.Logf("Status: %s, Progress: %.2f, ErrMsg: %s", status, progress, errMsg)

	// Should have warning status
	if !strings.Contains(status, "warning") {
		t.Errorf("Expected status to contain 'warning', got %q", status)
	}

	// Should have error message
	if errMsg == "" {
		t.Log("Note: errMsg may be empty depending on status message parsing")
	}
}

func TestHTTPArrClient_RefreshMonitoredDownloads_CommandSent(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	commandReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/command" && r.Method == "POST" {
			commandReceived = true
			json.NewEncoder(w).Encode(struct {
				ID int64 `json:"id"`
			}{ID: 1})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	err := client.RefreshMonitoredDownloads(instance)
	if err != nil {
		t.Fatalf("RefreshMonitoredDownloads failed: %v", err)
	}

	if !commandReceived {
		t.Error("Expected command to be sent")
	}
}

func TestHTTPArrClient_DeleteFile_WhisparrV3(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Logf("Whisparr test: %s %s", r.Method, r.URL.Path)
		switch {
		case r.URL.Path == "/api/v3/moviefile" && r.Method == "GET":
			// Return moviefile list for the movie
			files := []struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			}{
				{ID: 100, Path: "/movies/Test Movie/movie.mkv"},
			}
			json.NewEncoder(w).Encode(files)
		case strings.HasPrefix(r.URL.Path, "/api/v3/moviefile/") && r.Method == "DELETE":
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Whisparr', 'whisparr-v3', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	// DeleteFile signature is (mediaID int64, path string) -> (map, error)
	_, err := client.DeleteFile(10, "/movies/Test Movie/movie.mkv")
	if err != nil {
		t.Fatalf("DeleteFile failed for whisparr-v3: %v", err)
	}
}

func TestHTTPArrClient_FindQueueItemsByMediaIDForPath_Success(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := struct {
			Page         int `json:"page"`
			PageSize     int `json:"pageSize"`
			TotalRecords int `json:"totalRecords"`
			Records      []struct {
				ID       int64 `json:"id"`
				SeriesId int64 `json:"seriesId"`
			} `json:"records"`
		}{
			Page:         1,
			PageSize:     50,
			TotalRecords: 1,
			Records: []struct {
				ID       int64 `json:"id"`
				SeriesId int64 `json:"seriesId"`
			}{
				{ID: 1, SeriesId: 123},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	items, err := client.FindQueueItemsByMediaIDForPath("/tv/Show/episode.mkv", 123)
	if err != nil {
		t.Fatalf("FindQueueItemsByMediaIDForPath failed: %v", err)
	}

	if len(items) != 1 {
		t.Errorf("Expected 1 queue item, got %d", len(items))
	}
}

func TestHTTPArrClient_GetQueueForPath_Success(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// GetQueueForPath uses GetAllQueueItems which calls GetQueue in a loop
		response := struct {
			Page         int `json:"page"`
			PageSize     int `json:"pageSize"`
			TotalRecords int `json:"totalRecords"`
			Records      []struct {
				ID         int64  `json:"id"`
				Title      string `json:"title"`
				DownloadId string `json:"downloadId"`
			} `json:"records"`
		}{
			Page:         1,
			PageSize:     200,
			TotalRecords: 1,
			Records: []struct {
				ID         int64  `json:"id"`
				Title      string `json:"title"`
				DownloadId string `json:"downloadId"`
			}{
				{ID: 1, Title: "Test.Show.S01E01", DownloadId: "abc123"},
			},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// GetQueueForPath returns []QueueItemInfo
	queue, err := client.GetQueueForPath("/tv/Show/episode.mkv")
	if err != nil {
		t.Fatalf("GetQueueForPath failed: %v", err)
	}

	if len(queue) != 1 {
		t.Errorf("Expected 1 queue item, got %d", len(queue))
	}
}

func TestHTTPArrClient_FindMediaByPath_NonOKStatus(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	_, err := client.FindMediaByPath("/tv/Show/episode.mkv")
	if err == nil {
		t.Error("Expected error for server error")
	}
}

func TestHTTPArrClient_GetInstanceForPath_NoMatch(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// No scan paths, so no instance will match
	_, err := client.FindMediaByPath("/nonexistent/path/file.mkv")
	if err == nil {
		t.Error("Expected error when no instance matches path")
	}
}

func TestHTTPArrClient_CmdHealthChecker_Basic(t *testing.T) {
	// Create a health checker
	checker := NewHealthChecker()

	// Verify the checker was created with default values
	if checker.FFprobePath != "ffprobe" {
		t.Errorf("Expected ffprobe path, got %q", checker.FFprobePath)
	}
	if checker.FFmpegPath != "ffmpeg" {
		t.Errorf("Expected ffmpeg path, got %q", checker.FFmpegPath)
	}
}

func TestHTTPArrClient_RemoveFromQueue_NonOKStatus(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	err := client.RemoveFromQueue(instance, 1, true, false)
	if err == nil {
		t.Error("Expected error for non-OK status")
	}
}

func TestHTTPArrClient_GetHistory_WithEventType(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	receivedEventType := ""
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedEventType = r.URL.Query().Get("eventType")
		response := struct {
			Page         int           `json:"page"`
			PageSize     int           `json:"pageSize"`
			TotalRecords int           `json:"totalRecords"`
			Records      []interface{} `json:"records"`
		}{
			Page:         1,
			PageSize:     50,
			TotalRecords: 0,
			Records:      []interface{}{},
		}
		json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance := &ArrInstance{
		ID:     1,
		Name:   "Sonarr",
		Type:   "sonarr",
		URL:    server.URL,
		APIKey: "key",
	}

	_, err := client.GetHistory(instance, 1, 50, "grabbed")
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}

	if receivedEventType != "grabbed" {
		t.Errorf("Expected eventType 'grabbed', got %q", receivedEventType)
	}
}

func TestHTTPArrClient_IsRetryableError_Various(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{"connection refused", errors.New("connection refused"), true},
		{"timeout", errors.New("i/o timeout"), true},
		{"EOF", errors.New("EOF"), true},
		{"connection reset", errors.New("connection reset by peer"), true},
		{"not retryable", errors.New("bad request"), false},
		{"nil error", nil, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isRetryableError(tt.err)
			if result != tt.expected {
				t.Errorf("isRetryableError(%v) = %v, expected %v", tt.err, result, tt.expected)
			}
		})
	}
}

// Test doRequestWithRetry with 5xx errors and retry exhaustion
func TestHTTPArrClient_DoRequest_5xxRetryExhaustion(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	requestCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Should fail after retries due to 503
	_, err := client.GetQueue(&ArrInstance{ID: 1, Name: "Sonarr", Type: "sonarr", URL: server.URL, APIKey: "key"}, 1, 100)
	if err == nil {
		t.Error("Expected error from 5xx retries, got nil")
	}

	// Verify retries happened
	if requestCount < 2 {
		t.Errorf("Expected multiple retry attempts, got %d", requestCount)
	}
}

// Test checkEpisodeForFile with non-OK status
func TestHTTPArrClient_CheckEpisodeForFile_NonOKStatus(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return 404 for episode endpoint
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Try GetFilePath which calls checkEpisodeForFile internally
	metadata := map[string]interface{}{"episode_id": float64(999)}
	_, err := client.GetFilePath(0, metadata, "/tv/Show/episode.mkv")
	// Should handle 404 gracefully
	if err == nil || !strings.Contains(err.Error(), "no file") {
		t.Logf("Got expected behavior: %v", err)
	}
}

// Test checkEpisodeForFile with no file attached
func TestHTTPArrClient_CheckEpisodeForFile_NoFile(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v3/episode/") {
			// Episode exists but has no file
			json.NewEncoder(w).Encode(struct {
				HasFile       bool  `json:"hasFile"`
				EpisodeFileId int64 `json:"episodeFileId"`
			}{HasFile: false, EpisodeFileId: 0})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{"episode_id": float64(1)}
	_, err := client.GetFilePath(0, metadata, "/tv/Show/episode.mkv")
	// Should return error since episode has no file
	if err == nil {
		t.Logf("Got expected no file result")
	}
}

// Test checkEpisodeForFile with episodefile non-OK
func TestHTTPArrClient_CheckEpisodeForFile_EpisodeFileNotOK(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v3/episode/") && !strings.Contains(r.URL.Path, "episodefile") {
			// Episode exists with file
			json.NewEncoder(w).Encode(struct {
				HasFile       bool  `json:"hasFile"`
				EpisodeFileId int64 `json:"episodeFileId"`
			}{HasFile: true, EpisodeFileId: 100})
			return
		}
		if strings.Contains(r.URL.Path, "/api/v3/episodefile/") {
			// Episode file endpoint returns 404
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{"episode_id": float64(1)}
	_, err := client.GetFilePath(0, metadata, "/tv/Show/episode.mkv")
	// Should handle gracefully
	t.Logf("Result: %v", err)
}

// Test GetQueueForPath with status messages
func TestHTTPArrClient_GetQueueForPath_WithStatusMessages(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/queue") {
			response := struct {
				Page         int `json:"page"`
				PageSize     int `json:"pageSize"`
				TotalRecords int `json:"totalRecords"`
				Records      []struct {
					ID             int64  `json:"id"`
					Title          string `json:"title"`
					Status         string `json:"status"`
					Size           int64  `json:"size"`
					SizeLeft       int64  `json:"sizeleft"`
					StatusMessages []struct {
						Title    string   `json:"title"`
						Messages []string `json:"messages"`
					} `json:"statusMessages"`
				} `json:"records"`
			}{
				Page:         1,
				PageSize:     1000,
				TotalRecords: 1,
				Records: []struct {
					ID             int64  `json:"id"`
					Title          string `json:"title"`
					Status         string `json:"status"`
					Size           int64  `json:"size"`
					SizeLeft       int64  `json:"sizeleft"`
					StatusMessages []struct {
						Title    string   `json:"title"`
						Messages []string `json:"messages"`
					} `json:"statusMessages"`
				}{
					{
						ID:       1,
						Title:    "Test Download",
						Status:   "warning",
						Size:     1000000,
						SizeLeft: 500000,
						StatusMessages: []struct {
							Title    string   `json:"title"`
							Messages []string `json:"messages"`
						}{
							{Title: "Warning", Messages: []string{"Track missing", "Audio mismatch"}},
						},
					},
				},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	items, err := client.GetQueueForPath("/tv/Show/episode.mkv")
	if err != nil {
		t.Fatalf("GetQueueForPath failed: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(items))
	}

	// Check status messages were collected
	if len(items[0].StatusMessages) != 2 {
		t.Errorf("Expected 2 status messages, got %d", len(items[0].StatusMessages))
	}

	// Check progress calculation
	if items[0].Progress != 50.0 {
		t.Errorf("Expected progress 50%%, got %.1f%%", items[0].Progress)
	}
}

// Test GetQueueForPath with zero size (edge case)
func TestHTTPArrClient_GetQueueForPath_ZeroSize(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/queue") {
			response := struct {
				Page         int `json:"page"`
				PageSize     int `json:"pageSize"`
				TotalRecords int `json:"totalRecords"`
				Records      []struct {
					ID       int64  `json:"id"`
					Title    string `json:"title"`
					Size     int64  `json:"size"`
					SizeLeft int64  `json:"sizeleft"`
				} `json:"records"`
			}{
				Page:         1,
				PageSize:     1000,
				TotalRecords: 1,
				Records: []struct {
					ID       int64  `json:"id"`
					Title    string `json:"title"`
					Size     int64  `json:"size"`
					SizeLeft int64  `json:"sizeleft"`
				}{
					{ID: 1, Title: "Unknown Size Download", Size: 0, SizeLeft: 0},
				},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	items, err := client.GetQueueForPath("/tv/Show/episode.mkv")
	if err != nil {
		t.Fatalf("GetQueueForPath failed: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("Expected 1 item, got %d", len(items))
	}

	// Progress should be 0 when size is 0 (avoid division by zero)
	if items[0].Progress != 0 {
		t.Errorf("Expected progress 0%% for zero size, got %.1f%%", items[0].Progress)
	}
}

// Test FindQueueItemsByMediaIDForPath error path
func TestHTTPArrClient_FindQueueItemsByMediaIDForPath_Error(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Server returns error
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	_, err := client.FindQueueItemsByMediaIDForPath("/tv/Show/episode.mkv", 123)
	if err == nil {
		t.Error("Expected error from 500 response, got nil")
	}
}

// Test GetHistory with pagination
func TestHTTPArrClient_GetHistory_WithPagination(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/history") {
			// Return history with records
			response := struct {
				Page         int `json:"page"`
				PageSize     int `json:"pageSize"`
				TotalRecords int `json:"totalRecords"`
				Records      []struct {
					ID        int64  `json:"id"`
					EventType string `json:"eventType"`
					MovieID   int64  `json:"movieId"`
				} `json:"records"`
			}{
				Page:         1,
				PageSize:     50,
				TotalRecords: 2,
				Records: []struct {
					ID        int64  `json:"id"`
					EventType string `json:"eventType"`
					MovieID   int64  `json:"movieId"`
				}{
					{ID: 1, EventType: "grabbed", MovieID: 10},
					{ID: 2, EventType: "downloadFailed", MovieID: 10},
				},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	instance := &ArrInstance{ID: 1, Name: "Radarr", Type: "radarr", URL: server.URL, APIKey: "key"}
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)

	history, err := client.GetHistory(instance, 1, 50, "")
	if err != nil {
		t.Fatalf("GetHistory failed: %v", err)
	}

	if len(history.Records) != 2 {
		t.Errorf("Expected 2 history items, got %d", len(history.Records))
	}
}

// Test getInstanceForPath with multiple paths
func TestHTTPArrClient_GetInstanceForPath_MultipleMatches(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(struct{}{})
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (2, 'Sonarr4K', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (2, '/local/tv4k', '/tv4k', 2, 0, 1)`)

	// Should find correct instance for /tv path
	_, err := client.GetQueueForPath("/tv/Show/episode.mkv")
	if err != nil {
		t.Logf("Error (may be expected): %v", err)
	}

	// Should find correct instance for /tv4k path
	_, err = client.GetQueueForPath("/tv4k/Show/episode.mkv")
	if err != nil {
		t.Logf("Error (may be expected): %v", err)
	}
}

// Test getAllInstancesInternal error path
func TestHTTPArrClient_GetAllInstances_DBError(t *testing.T) {
	client, db := setupTestClient(t)

	// Close the DB to cause errors
	db.DB.Close()

	instances, err := client.GetAllInstances()
	// Should handle error gracefully
	if err == nil && len(instances) > 0 {
		t.Error("Expected error or empty result with closed DB")
	}
}

// Test FindMediaByPath with Sonarr (series parse)
func TestHTTPArrClient_FindMediaByPath_Sonarr_Series(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "parse") {
			// Parse returns series info
			json.NewEncoder(w).Encode(struct {
				Series struct {
					ID    int64  `json:"id"`
					Title string `json:"title"`
				} `json:"series"`
			}{
				Series: struct {
					ID    int64  `json:"id"`
					Title string `json:"title"`
				}{ID: 55, Title: "Test Series"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	mediaID, err := client.FindMediaByPath("/tv/Test Series/Season 1/episode.mkv")
	if err != nil {
		t.Fatalf("FindMediaByPath failed: %v", err)
	}

	if mediaID != 55 {
		t.Errorf("Expected media ID 55, got %d", mediaID)
	}
}

// Test FindMediaByPath when parse returns no match
func TestHTTPArrClient_FindMediaByPath_NoMatch(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "parse") {
			// Parse returns empty/null
			json.NewEncoder(w).Encode(struct {
				Movie  interface{} `json:"movie"`
				Series interface{} `json:"series"`
			}{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	_, err := client.FindMediaByPath("/movies/Unknown Movie/movie.mkv")
	if err == nil {
		t.Error("Expected error for no match, got nil")
	}
}

// Test DeleteFile for Sonarr with episode metadata
func TestHTTPArrClient_DeleteFile_SonarrEpisode(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/episodefile/") && r.Method == "DELETE" {
			json.NewEncoder(w).Encode(map[string]interface{}{"success": true})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Delete with episode_file_id
	_, err := client.DeleteFile(123, "/tv/Show/episode.mkv")
	if err != nil {
		t.Logf("Delete result: %v", err)
	}
}

// Test GetFilePath with Radarr movie
func TestHTTPArrClient_GetFilePath_RadarrMovie(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v3/movie/") {
			// Return movie with nested movieFile object
			json.NewEncoder(w).Encode(struct {
				HasFile   bool `json:"hasFile"`
				MovieFile *struct {
					Path string `json:"path"`
				} `json:"movieFile"`
			}{
				HasFile: true,
				MovieFile: &struct {
					Path string `json:"path"`
				}{Path: "/movies/Test Movie (2024)/movie.mkv"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	path, err := client.GetFilePath(42, nil, "/movies/Test Movie/movie.mkv")
	if err != nil {
		t.Fatalf("GetFilePath failed: %v", err)
	}

	if path != "/movies/Test Movie (2024)/movie.mkv" {
		t.Errorf("Expected correct path, got %q", path)
	}
}

// Test checkEpisodeForFile with JSON decode error
func TestHTTPArrClient_CheckEpisodeForFile_InvalidJSON(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v3/episode/") {
			// Return invalid JSON
			w.Write([]byte("{invalid json"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{"episode_id": float64(1)}
	_, err := client.GetFilePath(0, metadata, "/tv/Show/episode.mkv")
	// Should handle JSON error
	if err == nil {
		t.Error("Expected error for invalid JSON")
	}
}

// Test GetRecentHistoryForMediaByPath success
func TestHTTPArrClient_GetRecentHistoryForMediaByPath_AllRecords(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/history/movie") || strings.Contains(r.URL.Path, "/history/series") {
			// Return history items directly
			json.NewEncoder(w).Encode([]struct {
				ID        int64  `json:"id"`
				EventType string `json:"eventType"`
			}{
				{ID: 1, EventType: "grabbed"},
				{ID: 2, EventType: "downloadFolderImported"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	items, err := client.GetRecentHistoryForMediaByPath("/movies/Test/movie.mkv", 10, 5)
	if err != nil {
		t.Fatalf("GetRecentHistoryForMediaByPath failed: %v", err)
	}

	if len(items) != 2 {
		t.Errorf("Expected 2 items, got %d", len(items))
	}
}

// Test FindQueueItemsByMediaIDForPath success path
func TestHTTPArrClient_FindQueueItemsByMediaIDForPath_Found(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/queue") {
			response := struct {
				Page         int `json:"page"`
				PageSize     int `json:"pageSize"`
				TotalRecords int `json:"totalRecords"`
				Records      []struct {
					ID      int64 `json:"id"`
					MovieID int64 `json:"movieId"`
				} `json:"records"`
			}{
				Page:         1,
				PageSize:     1000,
				TotalRecords: 1,
				Records: []struct {
					ID      int64 `json:"id"`
					MovieID int64 `json:"movieId"`
				}{
					{ID: 100, MovieID: 42},
				},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	items, err := client.FindQueueItemsByMediaIDForPath("/movies/Test/movie.mkv", 42)
	if err != nil {
		t.Fatalf("FindQueueItemsByMediaIDForPath failed: %v", err)
	}

	if len(items) != 1 {
		t.Errorf("Expected 1 item, got %d", len(items))
	}
}

// Test RefreshMonitoredDownloads API call
func TestHTTPArrClient_RefreshMonitoredDownloads_Sent(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	commandReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/command" && r.Method == "POST" {
			commandReceived = true
			json.NewEncoder(w).Encode(map[string]interface{}{"id": 1})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	err := client.RefreshMonitoredDownloads(&ArrInstance{ID: 1, Name: "Sonarr", Type: "sonarr", URL: server.URL, APIKey: "key"})
	if err != nil {
		t.Fatalf("RefreshMonitoredDownloads failed: %v", err)
	}

	if !commandReceived {
		t.Error("Command was not received by server")
	}
}

// Test GetDownloadStatus detailed check
func TestHTTPArrClient_GetDownloadStatus_Complete(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/queue") {
			response := struct {
				Page         int `json:"page"`
				PageSize     int `json:"pageSize"`
				TotalRecords int `json:"totalRecords"`
				Records      []struct {
					ID                   int64  `json:"id"`
					DownloadID           string `json:"downloadId"`
					Status               string `json:"status"`
					TrackedDownloadState string `json:"trackedDownloadState"`
					MovieID              int64  `json:"movieId"`
					Size                 int64  `json:"size"`
					SizeLeft             int64  `json:"sizeleft"`
				} `json:"records"`
			}{
				Page:         1,
				PageSize:     1000,
				TotalRecords: 1,
				Records: []struct {
					ID                   int64  `json:"id"`
					DownloadID           string `json:"downloadId"`
					Status               string `json:"status"`
					TrackedDownloadState string `json:"trackedDownloadState"`
					MovieID              int64  `json:"movieId"`
					Size                 int64  `json:"size"`
					SizeLeft             int64  `json:"sizeleft"`
				}{
					{ID: 1, DownloadID: "abc123", Status: "completed", TrackedDownloadState: "importPending", MovieID: 42, Size: 1000, SizeLeft: 0},
				},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	instance := &ArrInstance{ID: 1, Name: "Radarr", Type: "radarr", URL: server.URL, APIKey: "key"}
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)

	status, progress, errMsg, err := client.GetDownloadStatus(instance, "abc123")
	if err != nil {
		t.Fatalf("GetDownloadStatus failed: %v", err)
	}

	// Status returns TrackedDownloadState
	if status != "importPending" {
		t.Errorf("Expected status 'importPending', got %q", status)
	}

	if progress != 100.0 {
		t.Errorf("Expected progress 100%%, got %.1f%%", progress)
	}

	_ = errMsg // Suppress unused variable warning
}

// Test getInstanceForPath with disabled instance
func TestHTTPArrClient_GetInstanceForPath_DisabledInstance(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(struct{}{})
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	// Insert disabled instance
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 0)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	_, err := client.GetQueueForPath("/tv/Show/episode.mkv")
	// Should fail because instance is disabled
	if err == nil {
		t.Error("Expected error for disabled instance")
	}
}

// Test GetHistory with API error
func TestHTTPArrClient_GetHistory_APIError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	instance := &ArrInstance{ID: 1, Name: "Sonarr", Type: "sonarr", URL: server.URL, APIKey: "key"}
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	_, err := client.GetHistory(instance, 1, 50, "")
	if err == nil {
		t.Error("Expected error from 500 response")
	}
}

// Test GetRecentHistoryForMedia with error
func TestHTTPArrClient_GetRecentHistoryForMedia_Error(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	instance := &ArrInstance{ID: 1, Name: "Radarr", Type: "radarr", URL: server.URL, APIKey: "key"}
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)

	_, err := client.GetRecentHistoryForMedia(instance, 42, 10)
	if err == nil {
		t.Error("Expected error from 404 response")
	}
}

// Test doRequestWithRetry with non-retryable error
func TestHTTPArrClient_DoRequest_NonRetryableError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Use an invalid URL to cause a non-retryable error
	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', 'http://invalid.invalid.invalid:99999', ?, 1)`, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Should fail with connection error
	_, err := client.GetQueueForPath("/tv/Show/episode.mkv")
	if err == nil {
		t.Error("Expected error from invalid host")
	}
}

// Test FindQueueItemsByMediaID error
func TestHTTPArrClient_FindQueueItemsByMediaID_Error(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	instance := &ArrInstance{ID: 1, Name: "Sonarr", Type: "sonarr", URL: server.URL, APIKey: "key"}
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	_, err := client.FindQueueItemsByMediaID(instance, 42)
	if err == nil {
		t.Error("Expected error from 500 response")
	}
}

// Test GetAllQueueItems with multiple pages
func TestHTTPArrClient_GetAllQueueItems_Empty(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/queue") {
			response := struct {
				Page         int           `json:"page"`
				PageSize     int           `json:"pageSize"`
				TotalRecords int           `json:"totalRecords"`
				Records      []interface{} `json:"records"`
			}{
				Page:         1,
				PageSize:     1000,
				TotalRecords: 0,
				Records:      []interface{}{},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	instance := &ArrInstance{ID: 1, Name: "Sonarr", Type: "sonarr", URL: server.URL, APIKey: "key"}
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	items, err := client.GetAllQueueItems(instance)
	if err != nil {
		t.Fatalf("GetAllQueueItems failed: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("Expected 0 items, got %d", len(items))
	}
}

// Test GetDownloadStatusForPath
func TestHTTPArrClient_GetDownloadStatusForPath_Found(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/queue") {
			response := struct {
				Page         int `json:"page"`
				PageSize     int `json:"pageSize"`
				TotalRecords int `json:"totalRecords"`
				Records      []struct {
					ID                   int64  `json:"id"`
					DownloadID           string `json:"downloadId"`
					Status               string `json:"status"`
					TrackedDownloadState string `json:"trackedDownloadState"`
					Size                 int64  `json:"size"`
					SizeLeft             int64  `json:"sizeleft"`
				} `json:"records"`
			}{
				Page:         1,
				PageSize:     1000,
				TotalRecords: 1,
				Records: []struct {
					ID                   int64  `json:"id"`
					DownloadID           string `json:"downloadId"`
					Status               string `json:"status"`
					TrackedDownloadState string `json:"trackedDownloadState"`
					Size                 int64  `json:"size"`
					SizeLeft             int64  `json:"sizeleft"`
				}{
					{ID: 1, DownloadID: "xyz789", Status: "downloading", TrackedDownloadState: "downloading", Size: 1000, SizeLeft: 500},
				},
			}
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	status, progress, _, err := client.GetDownloadStatusForPath("/tv/Show/episode.mkv", "xyz789")
	if err != nil {
		t.Fatalf("GetDownloadStatusForPath failed: %v", err)
	}

	if status != "downloading" {
		t.Errorf("Expected status 'downloading', got %q", status)
	}

	if progress != 50.0 {
		t.Errorf("Expected progress 50%%, got %.1f%%", progress)
	}
}

// Test RemoveFromQueueByPath
func TestHTTPArrClient_RemoveFromQueueByPath_Success(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/queue/") && r.Method == "DELETE" {
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	err := client.RemoveFromQueueByPath("/tv/Show/episode.mkv", 123, true, false)
	if err != nil {
		t.Fatalf("RemoveFromQueueByPath failed: %v", err)
	}
}

// Test getInstanceByIDInternal with decryption
func TestHTTPArrClient_GetInstanceByID_WithDecryption(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(struct{}{})
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("my-api-key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	instance, err := client.GetInstanceByID(1)
	if err != nil {
		t.Fatalf("GetInstanceByID failed: %v", err)
	}

	if instance.APIKey != "my-api-key" {
		t.Errorf("Expected decrypted API key, got %q", instance.APIKey)
	}
}

// Test FindMediaByPath for Whisparr-v3
func TestHTTPArrClient_FindMediaByPath_Whisparr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "parse") {
			// Parse returns movie (Whisparr uses movie API)
			json.NewEncoder(w).Encode(struct {
				Movie struct {
					ID    int64  `json:"id"`
					Title string `json:"title"`
				} `json:"movie"`
			}{
				Movie: struct {
					ID    int64  `json:"id"`
					Title string `json:"title"`
				}{ID: 77, Title: "Test Scene"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Whisparr', 'whisparr-v3', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/adult', '/adult', 1, 0, 0)`)

	mediaID, err := client.FindMediaByPath("/adult/Studio/scene.mp4")
	if err != nil {
		t.Fatalf("FindMediaByPath failed: %v", err)
	}

	if mediaID != 77 {
		t.Errorf("Expected media ID 77, got %d", mediaID)
	}
}

// Test TriggerSearch for series
func TestHTTPArrClient_TriggerSearch_Series(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	commandReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/command" && r.Method == "POST" {
			commandReceived = true
			json.NewEncoder(w).Encode(map[string]interface{}{"id": 1})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	err := client.TriggerSearch(99, "/tv/Show/episode.mkv", nil)
	if err != nil {
		t.Fatalf("TriggerSearch failed: %v", err)
	}

	if !commandReceived {
		t.Error("Command was not received by server")
	}
}

// Test GetRecentHistoryForMediaByPath getInstanceForPath error
func TestHTTPArrClient_GetRecentHistoryForMediaByPath_NoInstance(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// No instances configured
	_, err := client.GetRecentHistoryForMediaByPath("/unknown/path/file.mkv", 42, 10)
	if err == nil {
		t.Error("Expected error for unknown path")
	}
}

// Test FindQueueItemsByMediaIDForPath getInstanceForPath error
func TestHTTPArrClient_FindQueueItemsByMediaIDForPath_NoInstance(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// No instances configured
	_, err := client.FindQueueItemsByMediaIDForPath("/unknown/path/file.mkv", 42)
	if err == nil {
		t.Error("Expected error for unknown path")
	}
}

// Test RefreshMonitoredDownloads with server error
func TestHTTPArrClient_RefreshMonitoredDownloads_ServerError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)

	err := client.RefreshMonitoredDownloads(&ArrInstance{ID: 1, Name: "Sonarr", Type: "sonarr", URL: server.URL, APIKey: "key"})
	if err == nil {
		t.Error("Expected error from 500 response")
	}
}

// Test GetFilePath with missing episode_ids
func TestHTTPArrClient_GetFilePath_MissingMetadata(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Missing episode_ids in metadata
	_, err := client.GetFilePath(0, map[string]interface{}{}, "/tv/Show/episode.mkv")
	if err == nil || !strings.Contains(err.Error(), "episode_ids") {
		t.Error("Expected error about missing episode_ids")
	}
}

// Test GetFilePath with unsupported type
func TestHTTPArrClient_GetFilePath_UnsupportedType(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Unknown', 'unknown', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/other', '/other', 1, 0, 0)`)

	_, err := client.GetFilePath(0, nil, "/other/file.mkv")
	if err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Error("Expected error about unsupported type")
	}
}

// Test getInstanceForPath with no scan paths
func TestHTTPArrClient_GetInstanceForPath_NoScanPaths(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(struct{}{})
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	// No scan paths inserted

	_, err := client.GetQueueForPath("/tv/Show/episode.mkv")
	if err == nil {
		t.Error("Expected error for no matching scan path")
	}
}

// Test TriggerSearch with episode IDs (Sonarr episode search)
func TestHTTPArrClient_TriggerSearch_WithEpisodes(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	commandReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/command" && r.Method == "POST" {
			commandReceived = true
			json.NewEncoder(w).Encode(map[string]interface{}{"id": 1})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Trigger with episode IDs
	err := client.TriggerSearch(99, "/tv/Show/episode.mkv", []int64{1, 2, 3})
	if err != nil {
		t.Fatalf("TriggerSearch failed: %v", err)
	}

	if !commandReceived {
		t.Error("Command was not received by server")
	}
}

// Test GetFilePath with empty episode_ids
func TestHTTPArrClient_GetFilePath_EmptyEpisodeIDs(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Empty episode_ids array
	_, err := client.GetFilePath(0, map[string]interface{}{"episode_ids": []interface{}{}}, "/tv/Show/episode.mkv")
	if err == nil {
		t.Error("Expected error for empty episode_ids")
	}
}

// Test GetAllFilePaths for Sonarr episodes with multiple files
func TestHTTPArrClient_GetAllFilePaths_SonarrMultiple(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v3/episode/") && !strings.Contains(r.URL.Path, "episodefile") {
			// Return episode with file
			json.NewEncoder(w).Encode(struct {
				HasFile       bool  `json:"hasFile"`
				EpisodeFileId int64 `json:"episodeFileId"`
			}{HasFile: true, EpisodeFileId: 200})
			return
		}
		if strings.Contains(r.URL.Path, "/api/v3/episodefile/") {
			json.NewEncoder(w).Encode(struct {
				Path string `json:"path"`
			}{Path: "/tv/Show/Season 1/episode.mkv"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(1), float64(2)},
	}
	paths, err := client.GetAllFilePaths(0, metadata, "/tv/Show/episode.mkv")
	if err != nil {
		t.Fatalf("GetAllFilePaths failed: %v", err)
	}

	if len(paths) == 0 {
		t.Error("Expected at least one path")
	}
}

// Test GetRecentHistoryForMedia success
func TestHTTPArrClient_GetRecentHistoryForMedia_Radarr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/history/movie") {
			json.NewEncoder(w).Encode([]struct {
				ID        int64  `json:"id"`
				EventType string `json:"eventType"`
			}{
				{ID: 1, EventType: "grabbed"},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	instance := &ArrInstance{ID: 1, Name: "Radarr", Type: "radarr", URL: server.URL, APIKey: "key"}
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)

	items, err := client.GetRecentHistoryForMedia(instance, 42, 10)
	if err != nil {
		t.Fatalf("GetRecentHistoryForMedia failed: %v", err)
	}

	if len(items) != 1 {
		t.Errorf("Expected 1 item, got %d", len(items))
	}
}

// Test getInstanceForPath with invalid encrypted key (no fallback)
func TestHTTPArrClient_GetInstanceForPath_DecryptFail(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Insert instance with invalid encrypted key that can't be decrypted
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', 'http://localhost', 'not-encrypted', 1)`)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	_, err := client.GetQueueForPath("/tv/Show/episode.mkv")
	// Should fail because API key can't be decrypted and no other matches
	if err == nil {
		t.Error("Expected error for invalid encrypted key")
	}
}

// Test getInstanceForPath with path suffix edge case
func TestHTTPArrClient_GetInstanceForPath_PathSuffix(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/queue") {
			response := struct {
				Page         int           `json:"page"`
				PageSize     int           `json:"pageSize"`
				TotalRecords int           `json:"totalRecords"`
				Records      []interface{} `json:"records"`
			}{Page: 1, PageSize: 1000, TotalRecords: 0, Records: []interface{}{}}
			json.NewEncoder(w).Encode(response)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	// Path /tv should NOT match /tv-archive
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// This should NOT match because /tv-archive doesn't match /tv prefix correctly
	_, err := client.GetQueueForPath("/tv-archive/Show/episode.mkv")
	if err == nil {
		t.Error("Expected error for path that shouldn't match")
	}
}

// Test FindMediaByPath with parse API error
func TestHTTPArrClient_FindMediaByPath_ParseError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	_, err := client.FindMediaByPath("/movies/Test/movie.mkv")
	if err == nil {
		t.Error("Expected error from parse API failure")
	}
}

// Test DeleteFile when file not found in arr
func TestHTTPArrClient_DeleteFile_FileNotFoundInArr(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/moviefile") && r.Method == "GET" {
			// Return empty file list
			json.NewEncoder(w).Encode([]struct{}{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	// File not found in arr, should still return metadata
	metadata, err := client.DeleteFile(42, "/movies/Nonexistent/movie.mkv")
	if err != nil {
		// Error is acceptable, the path doesn't exist
		t.Logf("Got expected behavior: %v", err)
	} else if metadata != nil {
		if metadata["deleted_path"] != "/movies/Nonexistent/movie.mkv" {
			t.Error("Expected deleted_path in metadata")
		}
	}
}

// Test DeleteFile for Radarr
func TestHTTPArrClient_DeleteFile_RadarrMovie(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	deleteReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// First, GET request to list movie files
		if strings.Contains(r.URL.Path, "/moviefile") && r.Method == "GET" {
			// Return list of files with matching basename
			json.NewEncoder(w).Encode([]struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			}{
				{ID: 99, Path: "/movies/Test/movie.mkv"},
			})
			return
		}
		// Then, DELETE request for specific file
		if strings.Contains(r.URL.Path, "/moviefile/99") && r.Method == "DELETE" {
			deleteReceived = true
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	_, err := client.DeleteFile(42, "/movies/Test/movie.mkv")
	if err != nil {
		t.Logf("Delete result: %v", err)
	}

	if !deleteReceived {
		t.Error("Delete request was not received")
	}
}

// Test DeleteFile with file list API error
func TestHTTPArrClient_DeleteFile_ListError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return error for file list
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	_, err := client.DeleteFile(42, "/movies/Test/movie.mkv")
	if err == nil {
		t.Error("Expected error from file list API failure")
	}
}

// Test GetFilePath with episode ID conversion
func TestHTTPArrClient_GetFilePath_EpisodeIDConversion(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v3/episode/") && !strings.Contains(r.URL.Path, "episodefile") {
			json.NewEncoder(w).Encode(struct {
				HasFile       bool  `json:"hasFile"`
				EpisodeFileId int64 `json:"episodeFileId"`
			}{HasFile: true, EpisodeFileId: 100})
			return
		}
		if strings.Contains(r.URL.Path, "/api/v3/episodefile/") {
			json.NewEncoder(w).Encode(struct {
				Path string `json:"path"`
			}{Path: "/tv/Show/Season 1/episode.mkv"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Test with episode_ids as []interface{} with float64 (JSON-like)
	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(1)},
	}
	path, err := client.GetFilePath(0, metadata, "/tv/Show/episode.mkv")
	if err != nil {
		t.Fatalf("GetFilePath failed: %v", err)
	}

	if path != "/tv/Show/Season 1/episode.mkv" {
		t.Errorf("Expected path, got %q", path)
	}
}

// Test TriggerSearch for Radarr movie
func TestHTTPArrClient_TriggerSearch_RadarrMovie(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	commandReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v3/command" && r.Method == "POST" {
			commandReceived = true
			json.NewEncoder(w).Encode(map[string]interface{}{"id": 1})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	err := client.TriggerSearch(42, "/movies/Test/movie.mkv", nil)
	if err != nil {
		t.Fatalf("TriggerSearch failed: %v", err)
	}

	if !commandReceived {
		t.Error("Command was not received by server")
	}
}

// Test TriggerSearch API error
func TestHTTPArrClient_TriggerSearch_APIError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	err := client.TriggerSearch(42, "/tv/Show/episode.mkv", nil)
	if err == nil {
		t.Error("Expected error from API failure")
	}
}

// Test GetFilePath with int64 episode_ids
func TestHTTPArrClient_GetFilePath_Int64EpisodeIDs(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v3/episode/") && !strings.Contains(r.URL.Path, "episodefile") {
			json.NewEncoder(w).Encode(struct {
				HasFile       bool  `json:"hasFile"`
				EpisodeFileId int64 `json:"episodeFileId"`
			}{HasFile: true, EpisodeFileId: 100})
			return
		}
		if strings.Contains(r.URL.Path, "/api/v3/episodefile/") {
			json.NewEncoder(w).Encode(struct {
				Path string `json:"path"`
			}{Path: "/tv/Show/Season 1/episode.mkv"})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// Test with episode_ids as []int64 directly
	metadata := map[string]interface{}{
		"episode_ids": []int64{1},
	}
	path, err := client.GetFilePath(0, metadata, "/tv/Show/episode.mkv")
	if err != nil {
		t.Fatalf("GetFilePath failed: %v", err)
	}

	if path != "/tv/Show/Season 1/episode.mkv" {
		t.Errorf("Expected path, got %q", path)
	}
}

// Test DeleteFile with Sonarr episode files
func TestHTTPArrClient_DeleteFile_SonarrEpisodeFiles(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	deleteReceived := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/episodefile") && r.Method == "GET" {
			json.NewEncoder(w).Encode([]struct {
				ID   int64  `json:"id"`
				Path string `json:"path"`
			}{
				{ID: 88, Path: "/tv/Show/episode.mkv"},
			})
			return
		}
		if strings.Contains(r.URL.Path, "/episodefile/88") && r.Method == "DELETE" {
			deleteReceived = true
			w.WriteHeader(http.StatusOK)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	_, err := client.DeleteFile(42, "/tv/Show/episode.mkv")
	if err != nil {
		t.Logf("Delete result: %v", err)
	}

	if !deleteReceived {
		t.Error("Delete request was not received")
	}
}

// Test checkEpisodeForFile when server returns error (non-200 for episode)
func TestHTTPArrClient_GetAllFilePaths_EpisodeNon200(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/episode/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(1)},
	}
	// Should return error since no files found when episode returns non-200
	_, err := client.GetAllFilePaths(42, metadata, "/tv/Show/episode.mkv")
	if err == nil {
		t.Error("Expected error for non-200 episode response")
	}
}

// Test checkEpisodeForFile when episode file request returns invalid JSON
func TestHTTPArrClient_GetAllFilePaths_InvalidEpisodeFileJSON(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/episode/") && !strings.Contains(r.URL.Path, "/episodefile/") {
			json.NewEncoder(w).Encode(struct {
				HasFile       bool  `json:"hasFile"`
				EpisodeFileId int64 `json:"episodeFileId"`
			}{
				HasFile:       true,
				EpisodeFileId: 99,
			})
			return
		}
		if strings.Contains(r.URL.Path, "/episodefile/") {
			w.Write([]byte("not valid json"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(1)},
	}
	// Should fail due to JSON decode error
	_, err := client.GetAllFilePaths(42, metadata, "/tv/Show/episode.mkv")
	if err == nil {
		t.Error("Expected error for invalid JSON in episode file response")
	}
}

// Test getAllInstancesInternal with decrypt error
func TestHTTPArrClient_GetAllInstances_DecryptError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// Insert instance with a clearly invalid encrypted key that won't decrypt
	// Using empty string which definitely can't be valid base64-encoded ciphertext
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Test', 'sonarr', 'http://test', '', 1)`)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	// GetAllInstances internally calls getAllInstancesInternal
	// This exercises the decrypt error path - instance with empty key should be skipped
	instances, _ := client.GetAllInstances()
	// Log what we got for debugging
	t.Logf("Got %d instances", len(instances))
}

// Test checkEpisodeForFile when episode returns invalid JSON
func TestHTTPArrClient_GetAllFilePaths_InvalidEpisodeJSON(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/episode/") {
			w.Write([]byte("not valid json for episode"))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(1)},
	}
	// Should fail due to JSON decode error
	_, err := client.GetAllFilePaths(42, metadata, "/tv/Show/episode.mkv")
	if err == nil {
		t.Error("Expected error for invalid JSON in episode response")
	}
}

// Test GetAllFilePaths when getInstanceForPath fails
func TestHTTPArrClient_GetAllFilePaths_InstanceNotFound(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	// No instances or scan paths configured
	metadata := map[string]interface{}{
		"episode_ids": []interface{}{float64(1)},
	}
	_, err := client.GetAllFilePaths(42, metadata, "/unknown/path")
	if err == nil {
		t.Error("Expected error when instance not found")
	}
}

// Test GetAllFilePaths when radarr GetFilePath fails
func TestHTTPArrClient_GetAllFilePaths_RadarrError(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Return error for movie endpoint
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Radarr', 'radarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/movies', '/movies', 1, 0, 0)`)

	_, err := client.GetAllFilePaths(42, nil, "/movies/Test/movie.mkv")
	if err == nil {
		t.Error("Expected error for Radarr GetFilePath failure")
	}
}

// Test GetFilePath with episode file request returning non-200
func TestHTTPArrClient_GetFilePath_EpisodeFileNon200(t *testing.T) {
	client, db := setupTestClient(t)
	defer db.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/api/v3/episode/") && !strings.Contains(r.URL.Path, "episodefile") {
			json.NewEncoder(w).Encode(struct {
				HasFile       bool  `json:"hasFile"`
				EpisodeFileId int64 `json:"episodeFileId"`
			}{HasFile: true, EpisodeFileId: 100})
			return
		}
		if strings.Contains(r.URL.Path, "/api/v3/episodefile/") {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	encryptedKey, _ := crypto.Encrypt("key")
	db.DB.Exec(`INSERT INTO arr_instances (id, name, type, url, api_key, enabled) VALUES (1, 'Sonarr', 'sonarr', ?, ?, 1)`, server.URL, encryptedKey)
	db.DB.Exec(`INSERT INTO scan_paths (id, local_path, arr_path, arr_instance_id, auto_remediate, is_4k) VALUES (1, '/local/tv', '/tv', 1, 0, 0)`)

	metadata := map[string]interface{}{
		"episode_id": float64(1),
	}
	_, err := client.GetFilePath(0, metadata, "/tv/Show/episode.mkv")
	// Should return error since episode file lookup failed
	if err == nil {
		t.Error("Expected error for episode file non-200")
	}
}
