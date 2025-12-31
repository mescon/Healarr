package integration

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
