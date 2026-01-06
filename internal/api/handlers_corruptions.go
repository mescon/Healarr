package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/logger"
)

// dbTimeout is the maximum time to wait for database operations
const dbTimeout = 5 * time.Second

// statusFilterClauses maps status filter values to SQL WHERE clauses.
var statusFilterClauses = map[string]string{
	"active":              "current_state != 'VerificationSuccess' AND current_state != 'MaxRetriesReached' AND current_state != 'CorruptionIgnored'",
	"pending":             "current_state = 'CorruptionDetected'",
	"in_progress":         "(current_state LIKE '%Started' OR current_state LIKE '%Queued' OR current_state LIKE '%Progress' OR current_state = 'RemediationQueued')",
	"resolved":            "current_state = 'VerificationSuccess'",
	"failed":              "current_state LIKE '%Failed'",
	"orphaned":            "current_state = 'MaxRetriesReached'",
	"ignored":             "current_state = 'CorruptionIgnored'",
	"manual_intervention": "(current_state = 'ImportBlocked' OR current_state = 'ManuallyRemoved')",
}

// extractJSONString extracts a string value from a map if it exists and is non-empty.
func extractJSONString(data map[string]interface{}, key string) (string, bool) {
	if v, ok := data[key].(string); ok && v != "" {
		return v, true
	}
	return "", false
}

// extractJSONInt extracts an integer value from a map (stored as float64 in JSON).
func extractJSONInt(data map[string]interface{}, key string) (int, bool) {
	if v, ok := data[key].(float64); ok {
		return int(v), true
	}
	return 0, false
}

// extractJSONInt64 extracts an int64 value from a map (stored as float64 in JSON).
func extractJSONInt64(data map[string]interface{}, key string) (int64, bool) {
	if v, ok := data[key].(float64); ok {
		return int64(v), true
	}
	return 0, false
}

// extractJSONFloat extracts a float64 value from a map.
func extractJSONFloat(data map[string]interface{}, key string) (float64, bool) {
	if v, ok := data[key].(float64); ok {
		return v, true
	}
	return 0, false
}

func (s *RESTServer) getCorruptions(c *gin.Context) {
	// Create context with timeout to prevent blocking on DB locks
	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	// Parse pagination with config
	cfg := PaginationConfig{
		DefaultLimit:     50,
		MaxLimit:         1000,
		DefaultSortBy:    "last_updated_at",
		DefaultSortOrder: "desc",
		AllowedSortBy: map[string]bool{
			"detected_at":     true,
			"last_updated_at": true,
			"file_path":       true,
			"state":           true,
			"corruption_type": true,
		},
	}
	p := ParsePagination(c, cfg)
	statusFilter := c.DefaultQuery("status", "all")
	pathIDFilter := c.Query("path_id")

	// Build query
	baseQuery := "FROM corruption_status"
	whereClauses := []string{}
	args := []interface{}{}

	// Status filter - use map lookup instead of switch
	if clause, ok := statusFilterClauses[statusFilter]; ok {
		whereClauses = append(whereClauses, clause)
	}

	// Path ID filter (for filtering by scan path)
	if pathIDFilter != "" {
		if pathID, err := strconv.ParseInt(pathIDFilter, 10, 64); err == nil {
			whereClauses = append(whereClauses, "path_id = ?")
			args = append(args, pathID)
		}
	}

	// Build WHERE clause
	whereClause := ""
	if len(whereClauses) > 0 {
		whereClause = " WHERE " + strings.Join(whereClauses, " AND ")
	}

	// Get total count with filter
	var total int
	countQuery := "SELECT COUNT(*) " + baseQuery + whereClause
	err := s.db.QueryRowContext(ctx, countQuery, args...).Scan(&total)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Get paginated data with filter and sort
	// Map frontend sort keys to DB columns (key = API param, value = DB column)
	allowedSortColumns := map[string]string{
		"detected_at":     "detected_at",
		"last_updated_at": "last_updated_at",
		"file_path":       "file_path",
		"state":           "current_state",
		"corruption_type": "corruption_type",
	}
	orderByClause := SafeOrderByClause(p.SortBy, p.SortOrder, allowedSortColumns, "last_updated_at", "desc")

	query := fmt.Sprintf("SELECT corruption_id, current_state, retry_count, file_path, path_id, last_error, detected_at, last_updated_at, corruption_type %s%s %s LIMIT ? OFFSET ?", baseQuery, whereClause, orderByClause)
	args = append(args, p.Limit, p.Offset)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	corruptions := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, state, filePath string
		var pathID sql.NullInt64
		var lastError, corruptionType sql.NullString
		var retryCount int
		var detectedAt, lastUpdatedAt string

		if err := rows.Scan(&id, &state, &retryCount, &filePath, &pathID, &lastError, &detectedAt, &lastUpdatedAt, &corruptionType); err != nil {
			continue
		}

		corruption := map[string]interface{}{
			"id":              id,
			"state":           state,
			"retry_count":     retryCount,
			"file_path":       filePath,
			"last_error":      lastError.String,
			"detected_at":     detectedAt,
			"last_updated_at": lastUpdatedAt,
			"corruption_type": corruptionType.String,
		}
		if pathID.Valid {
			corruption["path_id"] = pathID.Int64
		}

		// Fetch enriched data from event_data (file_size from CorruptionDetected, media info from SearchCompleted)
		enriched := s.getEnrichedCorruptionData(ctx, id)
		for k, v := range enriched {
			corruption[k] = v
		}

		corruptions = append(corruptions, corruption)
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading corruptions"})
		logger.Errorf("Error iterating corruptions: %v", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":       corruptions,
		"pagination": NewPaginationResponse(p, total),
	})
}

// getEnrichedCorruptionData extracts enriched display data from event_data:
// - file_size from CorruptionDetected
// - media_title, media_type, arr_type from SearchCompleted
// - quality, release_group, total_duration_seconds from VerificationSuccess
// - download progress info from latest DownloadProgress
func (s *RESTServer) getEnrichedCorruptionData(ctx context.Context, corruptionID string) map[string]interface{} {
	enriched := make(map[string]interface{})
	s.enrichFromCorruptionDetected(ctx, corruptionID, enriched)
	s.enrichFromSearchCompleted(ctx, corruptionID, enriched)
	s.enrichFromVerificationSuccess(ctx, corruptionID, enriched)
	s.enrichFromDownloadProgress(ctx, corruptionID, enriched)
	return enriched
}

// fetchEventData fetches and unmarshals event data for a specific event type.
func (s *RESTServer) fetchEventData(ctx context.Context, corruptionID, eventType, order string) map[string]interface{} {
	var eventData sql.NullString
	query := fmt.Sprintf(`SELECT event_data FROM events WHERE aggregate_id = ? AND event_type = ? ORDER BY created_at %s LIMIT 1`, order)
	if err := s.db.QueryRowContext(ctx, query, corruptionID, eventType).Scan(&eventData); err != nil {
		return nil
	}
	if !eventData.Valid {
		return nil
	}
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(eventData.String), &data); err != nil {
		logger.Debugf("Failed to unmarshal %s event data for %s: %v", eventType, corruptionID, err)
		return nil
	}
	return data
}

// enrichFromCorruptionDetected extracts file_size from CorruptionDetected event.
func (s *RESTServer) enrichFromCorruptionDetected(ctx context.Context, corruptionID string, enriched map[string]interface{}) {
	data := s.fetchEventData(ctx, corruptionID, "CorruptionDetected", "ASC")
	if data == nil {
		return
	}
	if fs, ok := extractJSONInt64(data, "file_size"); ok && fs > 0 {
		enriched["file_size"] = fs
	}
}

// enrichFromSearchCompleted extracts media info from SearchCompleted event.
func (s *RESTServer) enrichFromSearchCompleted(ctx context.Context, corruptionID string, enriched map[string]interface{}) {
	data := s.fetchEventData(ctx, corruptionID, "SearchCompleted", "DESC")
	if data == nil {
		return
	}
	if v, ok := extractJSONString(data, "media_title"); ok {
		enriched["media_title"] = v
	}
	if v, ok := extractJSONInt(data, "media_year"); ok {
		enriched["media_year"] = v
	}
	if v, ok := extractJSONString(data, "media_type"); ok {
		enriched["media_type"] = v
	}
	if v, ok := extractJSONInt(data, "season_number"); ok {
		enriched["season_number"] = v
	}
	if v, ok := extractJSONInt(data, "episode_number"); ok {
		enriched["episode_number"] = v
	}
	if v, ok := extractJSONString(data, "arr_type"); ok {
		enriched["arr_type"] = v
	}
	if v, ok := extractJSONString(data, "instance_name"); ok {
		enriched["instance_name"] = v
	}
}

// enrichFromVerificationSuccess extracts quality/duration info from VerificationSuccess event.
func (s *RESTServer) enrichFromVerificationSuccess(ctx context.Context, corruptionID string, enriched map[string]interface{}) {
	data := s.fetchEventData(ctx, corruptionID, "VerificationSuccess", "DESC")
	if data == nil {
		return
	}
	if v, ok := extractJSONString(data, "quality"); ok {
		enriched["quality"] = v
	}
	if v, ok := extractJSONString(data, "release_group"); ok {
		enriched["release_group"] = v
	}
	if v, ok := extractJSONInt64(data, "total_duration_seconds"); ok {
		enriched["total_duration_seconds"] = v
	}
	if v, ok := extractJSONInt64(data, "download_duration_seconds"); ok {
		enriched["download_duration_seconds"] = v
	}
	if v, ok := extractJSONString(data, "new_file_path"); ok {
		enriched["new_file_path"] = v
	}
	if v, ok := extractJSONInt64(data, "new_file_size"); ok {
		enriched["new_file_size"] = v
	}
}

// enrichFromDownloadProgress extracts download progress info from DownloadProgress event.
func (s *RESTServer) enrichFromDownloadProgress(ctx context.Context, corruptionID string, enriched map[string]interface{}) {
	data := s.fetchEventData(ctx, corruptionID, "DownloadProgress", "DESC")
	if data == nil {
		return
	}
	if v, ok := extractJSONFloat(data, "progress"); ok {
		enriched["download_progress"] = v
	}
	if v, ok := extractJSONInt64(data, "size_bytes"); ok {
		enriched["download_size"] = v
	}
	if v, ok := extractJSONInt64(data, "size_remaining_bytes"); ok {
		enriched["download_remaining"] = v
	}
	if v, ok := extractJSONString(data, "protocol"); ok {
		enriched["download_protocol"] = v
	}
	if v, ok := extractJSONString(data, "download_client"); ok {
		enriched["download_client"] = v
	}
	if v, ok := extractJSONString(data, "indexer"); ok {
		enriched["indexer"] = v
	}
	if v, ok := extractJSONString(data, "time_left"); ok {
		enriched["download_time_left"] = v
	}
}

func (s *RESTServer) getRemediations(c *gin.Context) {
	// Create context with timeout to prevent blocking on DB locks
	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	// Parse pagination (no sorting - fixed order by last_updated_at DESC)
	p := ParsePagination(c, DefaultPaginationConfig())

	// Get total count
	var total int
	err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM corruption_status WHERE current_state = ?", string(domain.VerificationSuccess)).Scan(&total)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Get paginated data
	rows, err := s.db.QueryContext(ctx, "SELECT corruption_id, file_path, last_updated_at FROM corruption_status WHERE current_state = ? ORDER BY last_updated_at DESC LIMIT ? OFFSET ?", string(domain.VerificationSuccess), p.Limit, p.Offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	remediations := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, filePath, completedAt string
		if err := rows.Scan(&id, &filePath, &completedAt); err != nil {
			continue
		}
		remediations = append(remediations, map[string]interface{}{
			"id":           id,
			"file_path":    filePath,
			"status":       "resolved",
			"completed_at": completedAt,
		})
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading remediations"})
		logger.Errorf("Error iterating remediations: %v", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":       remediations,
		"pagination": NewPaginationResponse(p, total),
	})
}

func (s *RESTServer) getCorruptionHistory(c *gin.Context) {
	// Create context with timeout to prevent blocking on DB locks
	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	id := c.Param("id")
	rows, err := s.db.QueryContext(ctx, "SELECT event_type, event_data, created_at FROM events WHERE aggregate_id = ? ORDER BY created_at ASC", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	history := make([]map[string]interface{}, 0)
	for rows.Next() {
		var eventType, createdAt string
		var eventData []byte // event_data is JSON stored as text/blob
		if err := rows.Scan(&eventType, &eventData, &createdAt); err != nil {
			continue
		}

		var data map[string]interface{}
		if len(eventData) > 0 {
			if err := json.Unmarshal(eventData, &data); err != nil {
				logger.Debugf("Failed to unmarshal event data: %v", err)
			}
		}

		history = append(history, map[string]interface{}{
			"event_type": eventType,
			"data":       data,
			"timestamp":  createdAt,
		})
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading history"})
		logger.Errorf("Error iterating corruption history: %v", err)
		return
	}

	c.JSON(http.StatusOK, history)
}

// retryCorruptions triggers a manual retry for selected corruptions
func (s *RESTServer) retryCorruptions(c *gin.Context) {
	// Create context with timeout to prevent blocking on DB locks
	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgNoIDsProvided})
		return
	}

	retried := 0
	for _, id := range req.IDs {
		var filePath sql.NullString
		var pathID sql.NullInt64
		err := s.db.QueryRowContext(ctx, `
			SELECT
				json_extract(event_data, '$.file_path'),
				json_extract(event_data, '$.path_id')
			FROM events
			WHERE aggregate_id = ? AND event_type = 'CorruptionDetected'
			LIMIT 1
		`, id).Scan(&filePath, &pathID)
		if err != nil || !filePath.Valid || filePath.String == "" {
			logger.Errorf("Failed to get file_path for corruption %s: %v", id, err)
			continue
		}

		if err := s.eventBus.Publish(domain.Event{
			AggregateID:   id,
			AggregateType: "corruption",
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path":      filePath.String,
				"path_id":        pathID.Int64,
				"auto_remediate": true,
				"manual_retry":   true,
			},
		}); err != nil {
			logger.Errorf("Failed to publish RetryScheduled event for %s: %v", id, err)
			continue
		}
		retried++
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Retried %d corruption(s)", retried),
		"retried": retried,
	})
}

// ignoreCorruptions marks corruptions as ignored (excluded from stats)
func (s *RESTServer) ignoreCorruptions(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgNoIDsProvided})
		return
	}

	ignored := 0
	for _, id := range req.IDs {
		if err := s.eventBus.Publish(domain.Event{
			AggregateID:   id,
			AggregateType: "corruption",
			EventType:     domain.CorruptionIgnored,
			EventData:     map[string]interface{}{"reason": "Manually ignored by user"},
		}); err != nil {
			logger.Errorf("Failed to publish CorruptionIgnored event for %s: %v", id, err)
			continue
		}
		ignored++
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Ignored %d corruption(s)", ignored),
		"ignored": ignored,
	})
}

// deleteCorruptions removes corruption entries from the database
func (s *RESTServer) deleteCorruptions(c *gin.Context) {
	// Create context with timeout to prevent blocking on DB locks
	ctx, cancel := context.WithTimeout(c.Request.Context(), dbTimeout)
	defer cancel()

	var req struct {
		IDs []string `json:"ids"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgNoIDsProvided})
		return
	}

	deleted := 0
	for _, id := range req.IDs {
		result, err := s.db.ExecContext(ctx, `DELETE FROM events WHERE aggregate_id = ?`, id)
		if err != nil {
			logger.Errorf("Failed to delete events for corruption %s: %v", id, err)
			continue
		}
		rows, rowsErr := result.RowsAffected()
		if rowsErr != nil {
			logger.Debugf("Failed to get rows affected for corruption %s: %v", id, rowsErr)
			continue
		}
		if rows > 0 {
			deleted++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Deleted %d corruption(s)", deleted),
		"deleted": deleted,
	})
}
