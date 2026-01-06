package integration

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
)

// RateLimiter implements a token bucket rate limiter for API calls
type RateLimiter struct {
	mu         sync.Mutex
	tokens     float64
	maxTokens  float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// NewRateLimiter creates a rate limiter with specified RPS and burst size
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	return &RateLimiter{
		tokens:     float64(burst),
		maxTokens:  float64(burst),
		refillRate: rps,
		lastRefill: time.Now(),
	}
}

// Wait blocks until a token is available or context is cancelled
func (r *RateLimiter) Wait(ctx context.Context) error {
	for {
		r.mu.Lock()
		// Refill tokens based on elapsed time
		now := time.Now()
		elapsed := now.Sub(r.lastRefill).Seconds()
		r.tokens += elapsed * r.refillRate
		if r.tokens > r.maxTokens {
			r.tokens = r.maxTokens
		}
		r.lastRefill = now

		if r.tokens >= 1 {
			r.tokens--
			r.mu.Unlock()
			return nil
		}

		// Calculate wait time for next token
		waitTime := time.Duration((1 - r.tokens) / r.refillRate * float64(time.Second))
		r.mu.Unlock()

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitTime):
			// Continue to next iteration
		}
	}
}

type HTTPArrClient struct {
	db              *sql.DB
	httpClient      *http.Client
	rateLimiter     *RateLimiter
	circuitBreakers *CircuitBreakerRegistry
}

func NewArrClient(db *sql.DB) *HTTPArrClient {
	cfg := config.Get()
	return &HTTPArrClient{
		db: db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
		rateLimiter:     NewRateLimiter(cfg.ArrRateLimitRPS, cfg.ArrRateLimitBurst),
		circuitBreakers: NewCircuitBreakerRegistry(DefaultCircuitBreakerConfig()),
	}
}

// GetCircuitBreakerStats returns statistics for all circuit breakers.
// This is useful for monitoring the health of *arr instances.
func (c *HTTPArrClient) GetCircuitBreakerStats() map[int64]CircuitBreakerStats {
	return c.circuitBreakers.AllStats()
}

// ResetCircuitBreaker resets the circuit breaker for a specific instance.
func (c *HTTPArrClient) ResetCircuitBreaker(instanceID int64) {
	c.circuitBreakers.Get(instanceID).Reset()
}

// ResetAllCircuitBreakers resets all circuit breakers.
func (c *HTTPArrClient) ResetAllCircuitBreakers() {
	c.circuitBreakers.ResetAll()
}

type ArrInstance struct {
	ID     int64
	Name   string
	Type   string
	URL    string
	APIKey string
}

// MediaItem represents a movie or TV show in *arr
type MediaItem struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	Path  string `json:"path"`
}

// ParseResult represents the response from /api/v3/parse endpoint
type ParseResult struct {
	Movie  *MediaItem `json:"movie"`  // For Radarr
	Series *MediaItem `json:"series"` // For Sonarr
}

// MovieFile represents a movie file in Radarr
type MovieFile struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

// EpisodeFile represents an episode file in Sonarr
type EpisodeFile struct {
	ID   int64  `json:"id"`
	Path string `json:"path"`
}

// QueueItem represents an item in the *arr download queue
type QueueItem struct {
	ID                    int64           `json:"id"`
	DownloadID            string          `json:"downloadId"`
	Title                 string          `json:"title"`
	Status                string          `json:"status"`                // downloading, completed, delay, etc.
	TrackedDownloadState  string          `json:"trackedDownloadState"`  // downloading, importPending, imported, failedPending, failed
	TrackedDownloadStatus string          `json:"trackedDownloadStatus"` // ok, warning, error
	ErrorMessage          string          `json:"errorMessage"`
	StatusMessages        []StatusMessage `json:"statusMessages"`
	Protocol              string          `json:"protocol"` // usenet, torrent
	DownloadClient        string          `json:"downloadClient"`
	Indexer               string          `json:"indexer"`
	OutputPath            string          `json:"outputPath"`
	Size                  int64           `json:"size"`
	SizeLeft              int64           `json:"sizeleft"`
	TimeLeft              string          `json:"timeleft"`
	EstimatedCompletion   string          `json:"estimatedCompletionTime"`
	Added                 string          `json:"added"`
	// Movie/Episode specific
	MovieID   int64 `json:"movieId,omitempty"`
	SeriesID  int64 `json:"seriesId,omitempty"`
	EpisodeID int64 `json:"episodeId,omitempty"`
}

// StatusMessage contains warning/error details from *arr
type StatusMessage struct {
	Title    string   `json:"title"`
	Messages []string `json:"messages"`
}

// QueueResponse is the paginated response from /api/v3/queue
type QueueResponse struct {
	Page         int         `json:"page"`
	PageSize     int         `json:"pageSize"`
	TotalRecords int         `json:"totalRecords"`
	Records      []QueueItem `json:"records"`
}

// HistoryItem represents an item from *arr history
type HistoryItem struct {
	ID          int64             `json:"id"`
	EventType   string            `json:"eventType"` // grabbed, downloadFolderImported, episodeFileDeleted, etc.
	Date        string            `json:"date"`
	DownloadID  string            `json:"downloadId"`
	SourceTitle string            `json:"sourceTitle"`
	MovieID     int64             `json:"movieId,omitempty"`
	SeriesID    int64             `json:"seriesId,omitempty"`
	EpisodeID   int64             `json:"episodeId,omitempty"`
	Data        map[string]string `json:"data"`
}

// HistoryResponse is the paginated response from /api/v3/history
type HistoryResponse struct {
	Page         int           `json:"page"`
	PageSize     int           `json:"pageSize"`
	TotalRecords int           `json:"totalRecords"`
	Records      []HistoryItem `json:"records"`
}

func (c *HTTPArrClient) getInstanceForPath(arrPath string) (*ArrInstance, error) {
	rows, err := c.db.Query("SELECT i.id, i.name, i.type, i.url, i.api_key, sp.arr_path FROM arr_instances i JOIN scan_paths sp ON sp.arr_instance_id = i.id WHERE i.enabled = 1")
	if err != nil {
		return nil, fmt.Errorf("failed to query instances: %w", err)
	}
	defer rows.Close()

	var bestMatch *ArrInstance
	var longestPrefixLen int

	for rows.Next() {
		var i ArrInstance
		var rootPath string
		var encryptedKey string
		if err := rows.Scan(&i.ID, &i.Name, &i.Type, &i.URL, &encryptedKey, &rootPath); err != nil {
			continue
		}

		// Decrypt API key
		decryptedKey, err := crypto.Decrypt(encryptedKey)
		if err != nil {
			logger.Errorf("Failed to decrypt API key for instance %d: %v", i.ID, err)
			continue
		}
		i.APIKey = decryptedKey

		// Normalize rootPath by removing trailing slash for consistent matching
		rootPath = strings.TrimRight(rootPath, "/")

		// Check if arrPath starts with rootPath AND is followed by / or end of string
		// This prevents /data/movies from matching /data/movies-archive
		if strings.HasPrefix(arrPath, rootPath) {
			remainder := arrPath[len(rootPath):]
			// Valid match only if remainder is empty or starts with /
			if remainder == "" || strings.HasPrefix(remainder, "/") {
				if len(rootPath) > longestPrefixLen {
					longestPrefixLen = len(rootPath)
					bestMatch = &i
				}
			}
		}
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating instances for path: %w", err)
	}

	if bestMatch == nil {
		return nil, fmt.Errorf("no instance found for path: %s", arrPath)
	}

	return bestMatch, nil
}

// isRetryableError checks if an error is a transient network error worth retrying
func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	// Check for timeout
	if os.IsTimeout(err) {
		return true
	}

	// Check for common network errors in the error string
	errStr := err.Error()
	retryablePatterns := []string{
		"connection refused",
		"connection reset",
		"no such host",
		"network is unreachable",
		"i/o timeout",
		"EOF",
		"connection timed out",
		"temporary failure",
	}

	for _, pattern := range retryablePatterns {
		if strings.Contains(strings.ToLower(errStr), strings.ToLower(pattern)) {
			return true
		}
	}

	return false
}

func (c *HTTPArrClient) doRequest(instance *ArrInstance, method, endpoint string, bodyData interface{}) (*http.Response, error) {
	return c.doRequestWithRetry(instance, method, endpoint, bodyData, 3)
}

// doRequestWithRetry performs an HTTP request with automatic retry for transient errors.
// Integrates with circuit breaker to prevent hammering unhealthy instances.
func (c *HTTPArrClient) doRequestWithRetry(instance *ArrInstance, method, endpoint string, bodyData interface{}, maxRetries int) (*http.Response, error) {
	// Check circuit breaker before making any requests
	cb := c.circuitBreakers.Get(instance.ID)
	if !cb.Allow() {
		logger.Warnf("Circuit breaker OPEN for %s (%s) - rejecting request to %s", instance.Name, instance.Type, endpoint)
		return nil, fmt.Errorf("%w: %s is unhealthy", ErrCircuitOpen, instance.Name)
	}

	var lastErr error

	for attempt := 0; attempt < maxRetries; attempt++ {
		// Apply rate limiting before making the request
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

		if err := c.rateLimiter.Wait(ctx); err != nil {
			cancel()
			cb.RecordFailure()
			return nil, fmt.Errorf("rate limiter timeout: %w", err)
		}
		cancel()

		apiURL := fmt.Sprintf("%s%s", strings.TrimRight(instance.URL, "/"), endpoint)

		var body io.Reader
		if bodyData != nil {
			jsonBytes, err := json.Marshal(bodyData)
			if err != nil {
				return nil, err
			}
			body = bytes.NewBuffer(jsonBytes)
		}

		req, err := http.NewRequest(method, apiURL, body)
		if err != nil {
			return nil, err
		}

		req.Header.Set("X-Api-Key", instance.APIKey)
		if bodyData != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err == nil {
			// Check for server errors (5xx) that might be retryable
			if resp.StatusCode >= 500 && resp.StatusCode < 600 {
				// 5xx errors count as failures for circuit breaker
				if attempt >= maxRetries-1 {
					cb.RecordFailure()
					logger.Warnf("*arr API %s returned %d after %d attempts - recording circuit breaker failure", instance.Name, resp.StatusCode, maxRetries)
				}
				// Drain and close body to allow connection reuse
				if _, discardErr := io.Copy(io.Discard, resp.Body); discardErr != nil {
					logger.Debugf("Failed to drain response body during retry: %v", discardErr)
				}
				if closeErr := resp.Body.Close(); closeErr != nil {
					logger.Debugf("Failed to close response body during retry: %v", closeErr)
				}
				if attempt < maxRetries-1 {
					logger.Infof("*arr API returned %d, retrying (%d/%d)...", resp.StatusCode, attempt+1, maxRetries)
					time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
					continue
				}
				return nil, fmt.Errorf("*arr API returned %d after %d attempts", resp.StatusCode, maxRetries)
			}
			// Success - record it
			cb.RecordSuccess()
			return resp, nil
		}

		lastErr = err

		// Check if error is retryable
		if !isRetryableError(err) {
			cb.RecordFailure()
			return nil, err
		}

		if attempt < maxRetries-1 {
			logger.Infof("*arr API request failed (attempt %d/%d): %v, retrying...", attempt+1, maxRetries, err)
			time.Sleep(time.Duration(attempt+1) * 2 * time.Second)
		}
	}

	// All retries exhausted - record failure
	cb.RecordFailure()
	return nil, fmt.Errorf("*arr API unavailable after %d attempts: %w", maxRetries, lastErr)
}

func (c *HTTPArrClient) FindMediaByPath(path string) (int64, error) {
	instance, err := c.getInstanceForPath(path)
	if err != nil {
		return 0, err
	}

	// 1. Try /api/v3/parse
	logger.Debugf("Parsing path with %s: %s", instance.Type, path)
	encodedPath := url.QueryEscape(path)
	endpoint := fmt.Sprintf("/api/v3/parse?path=%s", encodedPath)

	resp, err := c.doRequest(instance, "GET", endpoint, nil)
	if err == nil && resp.StatusCode == http.StatusOK {
		defer resp.Body.Close()
		var result ParseResult
		if err := json.NewDecoder(resp.Body).Decode(&result); err == nil {
			if (instance.Type == "radarr" || instance.Type == "whisparr-v3") && result.Movie != nil {
				logger.Infof("Found movie via parse: %s (ID: %d)", result.Movie.Title, result.Movie.ID)
				return result.Movie.ID, nil
			} else if (instance.Type == "sonarr" || instance.Type == "whisparr-v2") && result.Series != nil {
				logger.Infof("Found series via parse: %s (ID: %d)", result.Series.Title, result.Series.ID)
				return result.Series.ID, nil
			}
		}
	} else if resp != nil {
		// Drain and close body even on non-OK responses to allow connection reuse
		if _, discardErr := io.Copy(io.Discard, resp.Body); discardErr != nil {
			logger.Debugf("Failed to drain response body: %v", discardErr)
		}
		if closeErr := resp.Body.Close(); closeErr != nil {
			logger.Debugf("Failed to close response body: %v", closeErr)
		}
	}

	// 2. Fallback: List all and match by folder
	logger.Infof("Parse failed, falling back to listing all media for %s", instance.Type)
	// This is expensive but necessary if parse fails
	var listEndpoint string
	if instance.Type == "radarr" || instance.Type == "whisparr-v3" {
		listEndpoint = "/api/v3/movie"
	} else {
		listEndpoint = "/api/v3/series"
	}

	resp, err = c.doRequest(instance, "GET", listEndpoint, nil)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("failed to list media: %s", resp.Status)
	}

	var items []MediaItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return 0, err
	}

	// Match logic: check if file is inside media path
	// We match by folder name to be safe against mapping differences
	fileDir := filepath.Dir(path)
	fileDirBase := filepath.Base(fileDir)

	// For TV shows, the parent directory is Season XX, so we need the grandparent (show folder)
	// Example: /data/media/TV/Colony/Season 01/file.mkv -> Colony
	showDir := filepath.Dir(fileDir) // This would be "Colony" for a season folder
	showDirBase := filepath.Base(showDir)

	for _, item := range items {
		mediaFolder := filepath.Base(item.Path)
		// Exact folder match (case-insensitive)
		if strings.EqualFold(mediaFolder, fileDirBase) {
			logger.Infof("Matched media by folder name: %s (ID: %d)", item.Title, item.ID)
			return item.ID, nil
		}
		// For TV shows, also check the parent folder (show name)
		if strings.EqualFold(mediaFolder, showDirBase) {
			logger.Infof("Matched media by show folder: %s (ID: %d)", item.Title, item.ID)
			return item.ID, nil
		}
		// Check if the media path is a prefix of the file path (exact path containment)
		// Normalize paths for comparison
		normalizedMediaPath := strings.ToLower(strings.TrimSuffix(item.Path, "/"))
		normalizedFilePath := strings.ToLower(path)
		if strings.HasPrefix(normalizedFilePath, normalizedMediaPath+"/") {
			logger.Infof("Matched media by path prefix: %s (ID: %d)", item.Title, item.ID)
			return item.ID, nil
		}
	}

	return 0, fmt.Errorf("media not found for path: %s", path)
}

func (c *HTTPArrClient) DeleteFile(mediaID int64, path string) (map[string]interface{}, error) {
	instance, err := c.getInstanceForPath(path)
	if err != nil {
		return nil, err
	}

	// 1. Get files for media
	var endpoint string
	if instance.Type == "radarr" || instance.Type == "whisparr-v3" {
		endpoint = fmt.Sprintf("/api/v3/moviefile?movieId=%d", mediaID)
	} else {
		endpoint = fmt.Sprintf("/api/v3/episodefile?seriesId=%d", mediaID)
	}

	resp, err := c.doRequest(instance, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get files: %s", resp.Status)
	}

	// Decode into a generic struct that has ID and Path
	// For Sonarr, EpisodeFile has "episodes" list, but we might just need the ID for now?
	// Actually, we need to know which episodes this file belongs to, so we can check them later.
	// Sonarr v3: EpisodeFile has 'id', 'path'. Episodes are linked via 'episodeFileId' on the Episode object.
	// But the /episodefile endpoint might not return the linked episodes directly?
	// Let's check if we can get it.
	// Actually, for Sonarr, we might need to query episodes first?
	// Let's stick to finding the file ID first.
	type GenericFile struct {
		ID   int64  `json:"id"`
		Path string `json:"path"`
	}
	var files []GenericFile
	if err := json.NewDecoder(resp.Body).Decode(&files); err != nil {
		return nil, err
	}

	// 2. Match file by basename
	targetBase := filepath.Base(path)
	var fileID int64

	for _, f := range files {
		if filepath.Base(f.Path) == targetBase {
			fileID = f.ID
			break
		}
	}

	// Capture metadata before deletion (or if file already gone)
	metadata := make(map[string]interface{})
	metadata["deleted_path"] = path

	if fileID == 0 {
		// File not found in *arr - it may have been deleted externally or by *arr itself.
		// Check if the file still exists on disk to determine if this is expected.
		if _, statErr := os.Stat(path); os.IsNotExist(statErr) {
			// File is gone from both *arr and disk - treat as already deleted
			logger.Infof("File already deleted (not in %s and not on disk): %s", instance.Type, path)

			// Still need to gather metadata for the search phase
			if instance.Type == "sonarr" || instance.Type == "whisparr-v2" {
				// For Sonarr, find episodes that are now missing files
				episodeIDs, err := c.findMissingEpisodesForPath(instance, mediaID, path)
				if err == nil && len(episodeIDs) > 0 {
					metadata["episode_ids"] = episodeIDs
				} else {
					// Fallback: search all missing episodes for this series
					logger.Infof("Could not determine specific episodes, will search all missing for series %d", mediaID)
					metadata["search_all_missing"] = true
				}
			} else {
				metadata["movie_id"] = mediaID
			}

			metadata["already_deleted"] = true
			return metadata, nil
		}
		// File exists on disk but not in *arr - this is unexpected
		return nil, fmt.Errorf("file not found in %s but exists on disk: %s", instance.Type, path)
	}

	if instance.Type == "sonarr" || instance.Type == "whisparr-v2" {
		// We need to find which episodes use this file.
		// Since we can't easily get it from /episodefile response (it varies),
		// let's query episodes for the series and match episodeFileId.
		epEndpoint := fmt.Sprintf("/api/v3/episode?seriesId=%d", mediaID)
		epResp, err := c.doRequest(instance, "GET", epEndpoint, nil)
		if err == nil && epResp.StatusCode == http.StatusOK {
			defer epResp.Body.Close()
			type Episode struct {
				ID            int64 `json:"id"`
				EpisodeFileID int64 `json:"episodeFileId"`
			}
			var episodes []Episode
			if err := json.NewDecoder(epResp.Body).Decode(&episodes); err == nil {
				var episodeIDs []int64
				for _, ep := range episodes {
					if ep.EpisodeFileID == fileID {
						episodeIDs = append(episodeIDs, ep.ID)
					}
				}
				metadata["episode_ids"] = episodeIDs
			}
		}
	} else {
		metadata["movie_id"] = mediaID
	}

	// 3. Delete
	logger.Infof("Deleting file ID %d from %s", fileID, instance.Type)
	var deleteEndpoint string
	if instance.Type == "radarr" || instance.Type == "whisparr-v3" {
		deleteEndpoint = fmt.Sprintf("/api/v3/moviefile/%d", fileID)
	} else {
		deleteEndpoint = fmt.Sprintf("/api/v3/episodefile/%d", fileID)
	}

	resp, err = c.doRequest(instance, "DELETE", deleteEndpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return nil, fmt.Errorf("failed to delete file: %s", resp.Status)
	}

	logger.Infof("Successfully deleted file %s from %s", path, instance.Type)
	return metadata, nil
}

// findMissingEpisodesForPath finds episodes that should have files in the given path but don't.
// This is used when a file was externally deleted to determine which episodes need searching.
func (c *HTTPArrClient) findMissingEpisodesForPath(instance *ArrInstance, seriesID int64, path string) ([]int64, error) {
	// Get all episodes for the series
	epEndpoint := fmt.Sprintf("/api/v3/episode?seriesId=%d", seriesID)
	resp, err := c.doRequest(instance, "GET", epEndpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get episodes: %s", resp.Status)
	}

	type Episode struct {
		ID            int64 `json:"id"`
		SeasonNumber  int   `json:"seasonNumber"`
		EpisodeNumber int   `json:"episodeNumber"`
		HasFile       bool  `json:"hasFile"`
		Monitored     bool  `json:"monitored"`
	}
	var episodes []Episode
	if err := json.NewDecoder(resp.Body).Decode(&episodes); err != nil {
		return nil, err
	}

	// Try to determine which season from the path (e.g., "Season 01" or "S01")
	pathLower := strings.ToLower(path)
	seasonNum := -1

	// Look for "season XX" pattern
	if idx := strings.Index(pathLower, "season "); idx != -1 {
		remaining := pathLower[idx+7:]
		if len(remaining) >= 2 {
			if n, err := strconv.Atoi(remaining[:2]); err == nil {
				seasonNum = n
			}
		}
	}

	// Find missing episodes (optionally filtered by season)
	var missingEpisodeIDs []int64
	for _, ep := range episodes {
		if !ep.HasFile && ep.Monitored {
			if seasonNum == -1 || ep.SeasonNumber == seasonNum {
				missingEpisodeIDs = append(missingEpisodeIDs, ep.ID)
			}
		}
	}

	return missingEpisodeIDs, nil
}

func (c *HTTPArrClient) GetFilePath(mediaID int64, metadata map[string]interface{}, referencePath string) (string, error) {
	instance, err := c.getInstanceForPath(referencePath)
	if err != nil {
		return "", err
	}

	switch instance.Type {
	case "radarr", "whisparr-v3":
		// For Radarr/Whisparr v3, just get the movie and check if it has a file
		endpoint := fmt.Sprintf("/api/v3/movie/%d", mediaID)
		resp, err := c.doRequest(instance, "GET", endpoint, nil)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("failed to get movie: %s", resp.Status)
		}

		var movie struct {
			HasFile   bool `json:"hasFile"`
			MovieFile *struct {
				Path string `json:"path"`
			} `json:"movieFile"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&movie); err != nil {
			return "", err
		}

		if !movie.HasFile || movie.MovieFile == nil {
			return "", fmt.Errorf("movie has no file yet")
		}
		return movie.MovieFile.Path, nil

	case "sonarr", "whisparr-v2":
		// For Sonarr/Whisparr v2, we need to check the episodes we tracked
		episodeIDsRaw, ok := metadata["episode_ids"]
		if !ok {
			return "", fmt.Errorf("missing episode_ids in metadata")
		}

		// Handle JSON unmarshaling where numbers might be float64
		var episodeIDs []int64
		switch v := episodeIDsRaw.(type) {
		case []int64:
			episodeIDs = v
		case []interface{}:
			for _, item := range v {
				if f, ok := item.(float64); ok {
					episodeIDs = append(episodeIDs, int64(f))
				}
			}
		}

		if len(episodeIDs) == 0 {
			return "", fmt.Errorf("no episode IDs found in metadata")
		}

		// Check each episode to see if it has a file now
		// Use helper function to ensure response bodies are closed after each iteration
		for _, epID := range episodeIDs {
			filePath, found, err := c.checkEpisodeForFile(instance, epID)
			if err != nil {
				continue
			}
			if found {
				return filePath, nil
			}
		}
		return "", fmt.Errorf("no new file found for episodes")

	default:
		return "", fmt.Errorf("unsupported instance type: %s", instance.Type)
	}
}

// checkEpisodeForFile checks if an episode has a file and returns its path.
// This is a helper to avoid defer-in-loop resource leaks.
func (c *HTTPArrClient) checkEpisodeForFile(instance *ArrInstance, epID int64) (string, bool, error) {
	endpoint := fmt.Sprintf("/api/v3/episode/%d", epID)
	resp, err := c.doRequest(instance, "GET", endpoint, nil)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, nil
	}

	var episode struct {
		HasFile       bool  `json:"hasFile"`
		EpisodeFileID int64 `json:"episodeFileId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&episode); err != nil {
		return "", false, err
	}

	if !episode.HasFile || episode.EpisodeFileID == 0 {
		return "", false, nil
	}

	// Get the file path
	fileEndpoint := fmt.Sprintf("/api/v3/episodefile/%d", episode.EpisodeFileID)
	fileResp, err := c.doRequest(instance, "GET", fileEndpoint, nil)
	if err != nil {
		return "", false, err
	}
	defer fileResp.Body.Close()

	if fileResp.StatusCode != http.StatusOK {
		return "", false, nil
	}

	var file struct {
		Path string `json:"path"`
	}
	if err := json.NewDecoder(fileResp.Body).Decode(&file); err != nil {
		return "", false, err
	}

	return file.Path, true, nil
}

// GetAllFilePaths returns all unique file paths for the tracked episodes/movie.
// For multi-episode files that were replaced with individual episode files, this returns multiple paths.
func (c *HTTPArrClient) GetAllFilePaths(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error) {
	instance, err := c.getInstanceForPath(referencePath)
	if err != nil {
		return nil, err
	}

	switch instance.Type {
	case "radarr", "whisparr-v3":
		// For movies, there's only one file
		path, err := c.GetFilePath(mediaID, metadata, referencePath)
		if err != nil {
			return nil, err
		}
		return []string{path}, nil

	case "sonarr", "whisparr-v2":
		// For Sonarr, check all tracked episodes and collect unique file paths
		episodeIDsRaw, ok := metadata["episode_ids"]
		if !ok {
			return nil, fmt.Errorf("missing episode_ids in metadata")
		}

		var episodeIDs []int64
		switch v := episodeIDsRaw.(type) {
		case []int64:
			episodeIDs = v
		case []interface{}:
			for _, item := range v {
				if f, ok := item.(float64); ok {
					episodeIDs = append(episodeIDs, int64(f))
				}
			}
		}

		if len(episodeIDs) == 0 {
			return nil, fmt.Errorf("no episode IDs found in metadata")
		}

		// Collect all unique file paths
		uniquePaths := make(map[string]bool)
		var paths []string

		for _, epID := range episodeIDs {
			filePath, found, err := c.checkEpisodeForFile(instance, epID)
			if err != nil {
				continue
			}
			if found && !uniquePaths[filePath] {
				uniquePaths[filePath] = true
				paths = append(paths, filePath)
			}
		}

		if len(paths) == 0 {
			return nil, fmt.Errorf("no files found for episodes")
		}

		return paths, nil

	default:
		return nil, fmt.Errorf("unsupported instance type: %s", instance.Type)
	}
}

func (c *HTTPArrClient) TriggerSearch(mediaID int64, path string, episodeIDs []int64) error {
	instance, err := c.getInstanceForPath(path)
	if err != nil {
		return err
	}

	logger.Infof("Triggering search for media ID %d on %s", mediaID, instance.Type)
	var payload map[string]interface{}
	if instance.Type == "radarr" || instance.Type == "whisparr-v3" {
		// For movies, search for the specific movie only
		payload = map[string]interface{}{
			"name":     "MoviesSearch",
			"movieIds": []int{int(mediaID)},
		}
	} else {
		// For Sonarr/Whisparr v2, use EpisodeSearch with specific episode IDs
		// This ensures we only search for the specific corrupted episode(s), not the entire series
		if len(episodeIDs) > 0 {
			// Convert int64 to int for the API
			intEpisodeIDs := make([]int, len(episodeIDs))
			for i, id := range episodeIDs {
				intEpisodeIDs[i] = int(id)
			}
			logger.Infof("Using EpisodeSearch for specific episode IDs: %v", intEpisodeIDs)
			payload = map[string]interface{}{
				"name":       "EpisodeSearch",
				"episodeIds": intEpisodeIDs,
			}
		} else {
			// Fallback to MissingEpisodeSearch only if we don't have episode IDs
			logger.Errorf("WARNING: No episode IDs provided, falling back to MissingEpisodeSearch for series %d - this may trigger more downloads than expected", mediaID)
			payload = map[string]interface{}{
				"name":     "MissingEpisodeSearch",
				"seriesId": int(mediaID),
			}
		}
	}

	resp, err := c.doRequest(instance, "POST", "/api/v3/command", payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to trigger search: %s", resp.Status)
	}

	return nil
}

// getAllInstancesInternal returns all enabled *arr instances (internal use)
func (c *HTTPArrClient) getAllInstancesInternal() ([]*ArrInstance, error) {
	rows, err := c.db.Query("SELECT id, name, type, url, api_key FROM arr_instances WHERE enabled = 1")
	if err != nil {
		return nil, fmt.Errorf("failed to query instances: %w", err)
	}
	defer rows.Close()

	var instances []*ArrInstance
	for rows.Next() {
		var i ArrInstance
		var encryptedKey string
		if err := rows.Scan(&i.ID, &i.Name, &i.Type, &i.URL, &encryptedKey); err != nil {
			continue
		}
		decryptedKey, err := crypto.Decrypt(encryptedKey)
		if err != nil {
			logger.Errorf("Failed to decrypt API key for instance %d: %v", i.ID, err)
			continue
		}
		i.APIKey = decryptedKey
		instances = append(instances, &i)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating instances: %w", err)
	}

	return instances, nil
}

// getInstanceByIDInternal returns a specific *arr instance by ID (internal use)
func (c *HTTPArrClient) getInstanceByIDInternal(id int64) (*ArrInstance, error) {
	var i ArrInstance
	var encryptedKey string
	err := c.db.QueryRow("SELECT id, name, type, url, api_key FROM arr_instances WHERE id = ?", id).
		Scan(&i.ID, &i.Name, &i.Type, &i.URL, &encryptedKey)
	if err != nil {
		return nil, err
	}
	decryptedKey, err := crypto.Decrypt(encryptedKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt API key: %w", err)
	}
	i.APIKey = decryptedKey
	return &i, nil
}

// GetQueue retrieves the download queue for an *arr instance
func (c *HTTPArrClient) GetQueue(instance *ArrInstance, page, pageSize int) (*QueueResponse, error) {
	endpoint := fmt.Sprintf("/api/v3/queue?page=%d&pageSize=%d&includeUnknownSeriesItems=true&includeUnknownMovieItems=true", page, pageSize)

	resp, err := c.doRequest(instance, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get queue: %s", resp.Status)
	}

	var queue QueueResponse
	if err := json.NewDecoder(resp.Body).Decode(&queue); err != nil {
		return nil, err
	}
	return &queue, nil
}

// GetAllQueueItems retrieves all items in the download queue (handles pagination)
func (c *HTTPArrClient) GetAllQueueItems(instance *ArrInstance) ([]QueueItem, error) {
	var allItems []QueueItem
	page := 1
	pageSize := 100

	for {
		queue, err := c.GetQueue(instance, page, pageSize)
		if err != nil {
			return nil, err
		}
		allItems = append(allItems, queue.Records...)
		if len(allItems) >= queue.TotalRecords {
			break
		}
		page++
	}
	return allItems, nil
}

// FindQueueItemByDownloadID finds a queue item by its download client ID
func (c *HTTPArrClient) FindQueueItemByDownloadID(instance *ArrInstance, downloadID string) (*QueueItem, error) {
	items, err := c.GetAllQueueItems(instance)
	if err != nil {
		return nil, err
	}
	for _, item := range items {
		if item.DownloadID == downloadID {
			return &item, nil
		}
	}
	return nil, fmt.Errorf("queue item not found for download ID: %s", downloadID)
}

// FindQueueItemsByMediaID finds queue items for a specific movie or series
func (c *HTTPArrClient) FindQueueItemsByMediaID(instance *ArrInstance, mediaID int64) ([]QueueItem, error) {
	items, err := c.GetAllQueueItems(instance)
	if err != nil {
		return nil, err
	}
	var matches []QueueItem
	for _, item := range items {
		if item.MovieID == mediaID || item.SeriesID == mediaID {
			matches = append(matches, item)
		}
	}
	return matches, nil
}

// GetHistory retrieves the history for an *arr instance
func (c *HTTPArrClient) GetHistory(instance *ArrInstance, page, pageSize int, eventType string) (*HistoryResponse, error) {
	endpoint := fmt.Sprintf("/api/v3/history?page=%d&pageSize=%d&sortKey=date&sortDirection=descending", page, pageSize)
	if eventType != "" {
		endpoint += "&eventType=" + eventType
	}

	resp, err := c.doRequest(instance, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get history: %s", resp.Status)
	}

	var history HistoryResponse
	if err := json.NewDecoder(resp.Body).Decode(&history); err != nil {
		return nil, err
	}
	return &history, nil
}

// GetRecentHistoryForMedia retrieves recent history events for a specific media item
func (c *HTTPArrClient) GetRecentHistoryForMedia(instance *ArrInstance, mediaID int64, limit int) ([]HistoryItem, error) {
	var endpoint string
	if instance.Type == "radarr" || instance.Type == "whisparr-v3" {
		endpoint = fmt.Sprintf("/api/v3/history/movie?movieId=%d&eventType=grabbed", mediaID)
	} else {
		endpoint = fmt.Sprintf("/api/v3/history/series?seriesId=%d&eventType=grabbed", mediaID)
	}

	resp, err := c.doRequest(instance, "GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get media history: %s", resp.Status)
	}

	var items []HistoryItem
	if err := json.NewDecoder(resp.Body).Decode(&items); err != nil {
		return nil, err
	}

	if len(items) > limit {
		items = items[:limit]
	}
	return items, nil
}

// GetDownloadStatus checks the status of a download by tracking it through the queue
// Returns: status, progress (0-100), error message, and error
func (c *HTTPArrClient) GetDownloadStatus(instance *ArrInstance, downloadID string) (status string, progress float64, errMsg string, err error) {
	item, err := c.FindQueueItemByDownloadID(instance, downloadID)
	if err != nil {
		// Item might have been imported already - check history
		return "unknown", 0, "", err
	}

	// Calculate progress
	if item.Size > 0 {
		progress = float64(item.Size-item.SizeLeft) / float64(item.Size) * 100
	}

	// Build status string
	status = item.TrackedDownloadState
	if item.TrackedDownloadStatus == "warning" || item.TrackedDownloadStatus == "error" {
		status = item.TrackedDownloadStatus + ":" + item.TrackedDownloadState
	}

	// Collect error messages
	var msgs []string
	if item.ErrorMessage != "" {
		msgs = append(msgs, item.ErrorMessage)
	}
	for _, sm := range item.StatusMessages {
		msgs = append(msgs, sm.Messages...)
	}
	errMsg = strings.Join(msgs, "; ")

	return status, progress, errMsg, nil
}

// RemoveFromQueue removes an item from the download queue
func (c *HTTPArrClient) RemoveFromQueue(instance *ArrInstance, queueID int64, removeFromClient, blocklist bool) error {
	endpoint := fmt.Sprintf("/api/v3/queue/%d?removeFromClient=%t&blocklist=%t", queueID, removeFromClient, blocklist)

	resp, err := c.doRequest(instance, "DELETE", endpoint, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("failed to remove from queue: %s", resp.Status)
	}
	return nil
}

// RefreshMonitoredDownloads triggers a refresh of monitored downloads
func (c *HTTPArrClient) RefreshMonitoredDownloads(instance *ArrInstance) error {
	payload := map[string]interface{}{
		"name": "RefreshMonitoredDownloads",
	}

	resp, err := c.doRequest(instance, "POST", "/api/v3/command", payload)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to refresh downloads: %s", resp.Status)
	}
	return nil
}

// CheckInstanceHealth checks if an *arr instance is reachable by calling its system status endpoint
func (c *HTTPArrClient) CheckInstanceHealth(instanceID int64) error {
	instance, err := c.getInstanceByIDInternal(instanceID)
	if err != nil {
		return err
	}

	resp, err := c.doRequest(instance, "GET", "/api/v3/system/status", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unhealthy: %s", resp.Status)
	}
	return nil
}

// =============================================================================
// Interface-compatible wrapper methods (take arrPath, find instance internally)
// =============================================================================

// GetAllInstances implements ArrClient interface
func (c *HTTPArrClient) GetAllInstances() ([]*ArrInstanceInfo, error) {
	instances, err := c.getAllInstancesInternal()
	if err != nil {
		return nil, err
	}
	var infos []*ArrInstanceInfo
	for _, inst := range instances {
		infos = append(infos, &ArrInstanceInfo{
			ID:     inst.ID,
			Name:   inst.Name,
			Type:   inst.Type,
			URL:    inst.URL,
			APIKey: inst.APIKey,
		})
	}
	return infos, nil
}

// GetInstanceByID implements ArrClient interface
func (c *HTTPArrClient) GetInstanceByID(id int64) (*ArrInstanceInfo, error) {
	inst, err := c.getInstanceByIDInternal(id)
	if err != nil {
		return nil, err
	}
	return &ArrInstanceInfo{
		ID:     inst.ID,
		Name:   inst.Name,
		Type:   inst.Type,
		URL:    inst.URL,
		APIKey: inst.APIKey,
	}, nil
}

// GetQueueForPath implements ArrClient interface - gets queue for a path's instance
func (c *HTTPArrClient) GetQueueForPath(arrPath string) ([]QueueItemInfo, error) {
	instance, err := c.getInstanceForPath(arrPath)
	if err != nil {
		return nil, err
	}

	items, err := c.GetAllQueueItems(instance)
	if err != nil {
		return nil, err
	}

	var infos []QueueItemInfo
	for _, item := range items {
		progress := float64(0)
		if item.Size > 0 {
			progress = float64(item.Size-item.SizeLeft) / float64(item.Size) * 100
		}

		// Collect status messages from the nested StatusMessages structure
		var statusMsgs []string
		for _, sm := range item.StatusMessages {
			statusMsgs = append(statusMsgs, sm.Messages...)
		}

		infos = append(infos, QueueItemInfo{
			ID:                    item.ID,
			DownloadID:            item.DownloadID,
			Title:                 item.Title,
			Status:                item.Status,
			TrackedDownloadState:  item.TrackedDownloadState,
			TrackedDownloadStatus: item.TrackedDownloadStatus,
			ErrorMessage:          item.ErrorMessage,
			StatusMessages:        statusMsgs,
			Protocol:              item.Protocol,
			DownloadClient:        item.DownloadClient,
			Indexer:               item.Indexer,
			Size:                  item.Size,
			SizeLeft:              item.SizeLeft,
			Progress:              progress,
			TimeLeft:              item.TimeLeft,
			EstimatedCompletion:   item.EstimatedCompletion,
			AddedAt:               item.Added,
			MovieID:               item.MovieID,
			SeriesID:              item.SeriesID,
			EpisodeID:             item.EpisodeID,
		})
	}
	return infos, nil
}

// FindQueueItemsByMediaIDForPath implements ArrClient interface
func (c *HTTPArrClient) FindQueueItemsByMediaIDForPath(arrPath string, mediaID int64) ([]QueueItemInfo, error) {
	instance, err := c.getInstanceForPath(arrPath)
	if err != nil {
		return nil, err
	}

	items, err := c.FindQueueItemsByMediaID(instance, mediaID)
	if err != nil {
		return nil, err
	}

	var infos []QueueItemInfo
	for _, item := range items {
		progress := float64(0)
		if item.Size > 0 {
			progress = float64(item.Size-item.SizeLeft) / float64(item.Size) * 100
		}

		// Collect status messages from the nested StatusMessages structure
		var statusMsgs []string
		for _, sm := range item.StatusMessages {
			statusMsgs = append(statusMsgs, sm.Messages...)
		}

		infos = append(infos, QueueItemInfo{
			ID:                    item.ID,
			DownloadID:            item.DownloadID,
			Title:                 item.Title,
			Status:                item.Status,
			TrackedDownloadState:  item.TrackedDownloadState,
			TrackedDownloadStatus: item.TrackedDownloadStatus,
			ErrorMessage:          item.ErrorMessage,
			StatusMessages:        statusMsgs,
			Protocol:              item.Protocol,
			DownloadClient:        item.DownloadClient,
			Indexer:               item.Indexer,
			Size:                  item.Size,
			SizeLeft:              item.SizeLeft,
			Progress:              progress,
			TimeLeft:              item.TimeLeft,
			EstimatedCompletion:   item.EstimatedCompletion,
			AddedAt:               item.Added,
			MovieID:               item.MovieID,
			SeriesID:              item.SeriesID,
			EpisodeID:             item.EpisodeID,
		})
	}
	return infos, nil
}

// GetDownloadStatusForPath implements ArrClient interface
func (c *HTTPArrClient) GetDownloadStatusForPath(arrPath string, downloadID string) (status string, progress float64, errMsg string, err error) {
	instance, err := c.getInstanceForPath(arrPath)
	if err != nil {
		return "", 0, "", err
	}
	return c.GetDownloadStatus(instance, downloadID)
}

// GetRecentHistoryForMediaByPath implements ArrClient interface
func (c *HTTPArrClient) GetRecentHistoryForMediaByPath(arrPath string, mediaID int64, limit int) ([]HistoryItemInfo, error) {
	instance, err := c.getInstanceForPath(arrPath)
	if err != nil {
		return nil, err
	}

	items, err := c.GetRecentHistoryForMedia(instance, mediaID, limit)
	if err != nil {
		return nil, err
	}

	var infos []HistoryItemInfo
	for _, item := range items {
		info := HistoryItemInfo{
			ID:          item.ID,
			EventType:   item.EventType,
			Date:        item.Date,
			DownloadID:  item.DownloadID,
			SourceTitle: item.SourceTitle,
			MovieID:     item.MovieID,
			SeriesID:    item.SeriesID,
			EpisodeID:   item.EpisodeID,
		}
		// Extract imported path from data if available
		if path, ok := item.Data["importedPath"]; ok {
			info.ImportedPath = path
		}
		// Extract quality and release info from data
		if quality, ok := item.Data["quality"]; ok {
			info.Quality = quality
		}
		if releaseGroup, ok := item.Data["releaseGroup"]; ok {
			info.ReleaseGroup = releaseGroup
		}
		if indexer, ok := item.Data["indexer"]; ok {
			info.Indexer = indexer
		}
		if downloadClient, ok := item.Data["downloadClient"]; ok {
			info.DownloadClient = downloadClient
		}
		infos = append(infos, info)
	}
	return infos, nil
}

// RemoveFromQueueByPath implements ArrClient interface
func (c *HTTPArrClient) RemoveFromQueueByPath(arrPath string, queueID int64, removeFromClient, blocklist bool) error {
	instance, err := c.getInstanceForPath(arrPath)
	if err != nil {
		return err
	}
	return c.RemoveFromQueue(instance, queueID, removeFromClient, blocklist)
}

// RefreshMonitoredDownloadsByPath implements ArrClient interface
func (c *HTTPArrClient) RefreshMonitoredDownloadsByPath(arrPath string) error {
	instance, err := c.getInstanceForPath(arrPath)
	if err != nil {
		return err
	}
	return c.RefreshMonitoredDownloads(instance)
}

// GetMediaDetails implements ArrClient interface - fetches friendly media titles for display.
// For movies: returns title and year
// For TV: returns series name, year, and episode details
// Returns nil (not error) if media details can't be fetched, allowing graceful degradation.
func (c *HTTPArrClient) GetMediaDetails(mediaID int64, arrPath string) (*MediaDetails, error) {
	instance, err := c.getInstanceForPath(arrPath)
	if err != nil {
		return nil, nil // Graceful degradation - return nil, not error
	}

	switch instance.Type {
	case "radarr", "whisparr-v3":
		return c.getMovieDetails(instance, mediaID)
	case "sonarr", "whisparr-v2":
		return c.getSeriesDetails(instance, mediaID)
	default:
		return nil, nil
	}
}

// getMovieDetails fetches movie title and year from Radarr/Whisparr
func (c *HTTPArrClient) getMovieDetails(instance *ArrInstance, movieID int64) (*MediaDetails, error) {
	endpoint := fmt.Sprintf("/api/v3/movie/%d", movieID)
	resp, err := c.doRequest(instance, "GET", endpoint, nil)
	if err != nil {
		logger.Debugf("Failed to fetch movie details for ID %d: %v", movieID, err)
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Debugf("Movie %d not found in %s (status: %s)", movieID, instance.Name, resp.Status)
		return nil, nil
	}

	var movie struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&movie); err != nil {
		return nil, nil
	}

	return &MediaDetails{
		Title:        movie.Title,
		Year:         movie.Year,
		MediaType:    "movie",
		ArrType:      instance.Type,
		InstanceName: instance.Name,
	}, nil
}

// getSeriesDetails fetches series and episode details from Sonarr/Whisparr
func (c *HTTPArrClient) getSeriesDetails(instance *ArrInstance, seriesID int64) (*MediaDetails, error) {
	// First, get series info
	seriesEndpoint := fmt.Sprintf("/api/v3/series/%d", seriesID)
	resp, err := c.doRequest(instance, "GET", seriesEndpoint, nil)
	if err != nil {
		logger.Debugf("Failed to fetch series details for ID %d: %v", seriesID, err)
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Debugf("Series %d not found in %s (status: %s)", seriesID, instance.Name, resp.Status)
		return nil, nil
	}

	var series struct {
		Title string `json:"title"`
		Year  int    `json:"year"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&series); err != nil {
		return nil, nil
	}

	return &MediaDetails{
		Title:        series.Title,
		Year:         series.Year,
		MediaType:    "series",
		ArrType:      instance.Type,
		InstanceName: instance.Name,
	}, nil
}

// GetEpisodeDetails fetches episode-specific details (season, episode number, title).
// This is a separate call because we often have the episode ID from queue/history data.
func (c *HTTPArrClient) GetEpisodeDetails(episodeID int64, arrPath string) (*MediaDetails, error) {
	instance, err := c.getInstanceForPath(arrPath)
	if err != nil {
		return nil, nil
	}

	if instance.Type != "sonarr" && instance.Type != "whisparr-v2" {
		return nil, nil // Only valid for Sonarr
	}

	// Get episode details
	epEndpoint := fmt.Sprintf("/api/v3/episode/%d", episodeID)
	resp, err := c.doRequest(instance, "GET", epEndpoint, nil)
	if err != nil {
		return nil, nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, nil
	}

	var episode struct {
		Title         string `json:"title"`
		SeasonNumber  int    `json:"seasonNumber"`
		EpisodeNumber int    `json:"episodeNumber"`
		SeriesID      int64  `json:"seriesId"`
		Series        struct {
			Title string `json:"title"`
			Year  int    `json:"year"`
		} `json:"series"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&episode); err != nil {
		return nil, nil
	}

	return &MediaDetails{
		Title:         episode.Series.Title,
		Year:          episode.Series.Year,
		MediaType:     "series",
		SeasonNumber:  episode.SeasonNumber,
		EpisodeNumber: episode.EpisodeNumber,
		EpisodeTitle:  episode.Title,
		ArrType:       instance.Type,
		InstanceName:  instance.Name,
	}, nil
}
