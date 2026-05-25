package api

import (
	"context"
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
// Includes both granular technical filters (for API compatibility) and user-friendly combined filters.
var statusFilterClauses = map[string]string{
	// Granular technical filters (kept for API compatibility and detail views)
	"active":              "current_state != 'VerificationSuccess' AND current_state != 'MaxRetriesReached' AND current_state != 'CorruptionIgnored'",
	"pending":             "current_state = 'CorruptionDetected'",
	"in_progress":         "(current_state LIKE '%Started' OR current_state LIKE '%Queued' OR current_state LIKE '%Progress' OR current_state = 'RemediationQueued')",
	"resolved":            "current_state = 'VerificationSuccess'",
	"failed":              "current_state LIKE '%Failed'",
	"orphaned":            "current_state = 'MaxRetriesReached'",
	"ignored":             "current_state = 'CorruptionIgnored'",
	"manual_intervention": "(current_state = 'ImportBlocked' OR current_state = 'ManuallyRemoved')",

	// User-friendly combined filters (for simplified UI)
	"action_required": "(current_state = 'ImportBlocked' OR current_state = 'ManuallyRemoved' OR current_state = 'MaxRetriesReached')",
	"working":         "(current_state = 'CorruptionDetected' OR current_state LIKE '%Started' OR current_state LIKE '%Queued' OR current_state LIKE '%Progress' OR current_state = 'RemediationQueued' OR (current_state LIKE '%Failed' AND current_state != 'MaxRetriesReached'))",
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

	// Get total count with filter.
	// whereClause contains only fixed fragments with ? placeholders; user
	// values are passed via args. See CorruptionRepository.CountFiltered.
	total, err := s.corruptions.CountFiltered(ctx, whereClause, args...)
	if err != nil {
		respondDatabaseError(c, err)
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

	rows, err := s.corruptions.ListFiltered(ctx, whereClause, orderByClause, p.Limit, p.Offset, args...)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	corruptions := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		corruption := map[string]interface{}{
			"id":              row.CorruptionID,
			"state":           row.CurrentState,
			"retry_count":     row.RetryCount,
			"file_path":       row.FilePath,
			"last_error":      row.LastError.String,
			"detected_at":     row.DetectedAt,
			"last_updated_at": row.LastUpdatedAt,
			"corruption_type": row.CorruptionType.String,
		}
		if row.PathID.Valid {
			corruption["path_id"] = row.PathID.Int64
		}

		// Fetch enriched data from event_data (file_size from CorruptionDetected, media info from SearchCompleted)
		enriched := s.getEnrichedCorruptionData(ctx, row.CorruptionID)
		for k, v := range enriched {
			corruption[k] = v
		}

		corruptions = append(corruptions, corruption)
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
// The order parameter must be "ASC" or "DESC" - callers use hardcoded values.
func (s *RESTServer) fetchEventData(ctx context.Context, corruptionID, eventType, order string) map[string]interface{} {
	// Security: order is hardcoded as "ASC" or "DESC" by all callers (see enrichFrom* methods)
	// Validate anyway for defense in depth
	if order != "ASC" && order != "DESC" {
		order = "DESC"
	}
	raw, err := s.corruptions.LatestEventData(ctx, corruptionID, eventType, order)
	if err != nil {
		return nil
	}
	var data map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
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
	if v, ok := extractJSONString(data, "episode_title"); ok {
		enriched["episode_title"] = v
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
	resolvedState := string(domain.VerificationSuccess)
	total, err := s.corruptions.CountByState(ctx, resolvedState)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	// Get paginated data
	rows, err := s.corruptions.ListByState(ctx, resolvedState, p.Limit, p.Offset)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	remediations := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		remediations = append(remediations, map[string]interface{}{
			"id":           row.CorruptionID,
			"file_path":    row.FilePath,
			"status":       "resolved",
			"completed_at": row.LastUpdatedAt,
		})
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
	events, err := s.corruptions.ListEvents(ctx, id)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	history := make([]map[string]interface{}, 0, len(events))
	for _, ev := range events {
		var data map[string]interface{}
		if len(ev.EventData) > 0 {
			if err := json.Unmarshal(ev.EventData, &data); err != nil {
				logger.Debugf("Failed to unmarshal event data: %v", err)
			}
		}

		history = append(history, map[string]interface{}{
			"event_type": ev.EventType,
			"data":       data,
			"timestamp":  ev.CreatedAt,
		})
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
		respondBadRequest(c, err)
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgNoIDsProvided})
		return
	}

	retried := 0
	for _, id := range req.IDs {
		filePath, pathID, err := s.corruptions.CorruptionDetectedFileInfo(ctx, id)
		if err != nil {
			logger.Errorf("Failed to get file_path for corruption %s: %v", id, err)
			continue
		}

		if err := s.eventBus.Publish(domain.Event{
			AggregateID:   id,
			AggregateType: "corruption",
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path":      filePath,
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
		respondBadRequest(c, err)
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
		respondBadRequest(c, err)
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgNoIDsProvided})
		return
	}

	deleted := 0
	for _, id := range req.IDs {
		rows, err := s.corruptions.DeleteEvents(ctx, id)
		if err != nil {
			logger.Errorf("Failed to delete events for corruption %s: %v", id, err)
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
