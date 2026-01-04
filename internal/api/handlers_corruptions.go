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

	// Status filter
	if statusFilter != "all" {
		switch statusFilter {
		case "active":
			whereClauses = append(whereClauses, "current_state != 'VerificationSuccess' AND current_state != 'MaxRetriesReached' AND current_state != 'CorruptionIgnored'")
		case "pending":
			// Pending = detected but not yet being processed (just CorruptionDetected state)
			whereClauses = append(whereClauses, "current_state = 'CorruptionDetected'")
		case "in_progress":
			// In progress = currently being remediated (Queued, Started, Progress states)
			whereClauses = append(whereClauses, "(current_state LIKE '%Started' OR current_state LIKE '%Queued' OR current_state LIKE '%Progress' OR current_state = 'RemediationQueued')")
		case "resolved":
			whereClauses = append(whereClauses, "current_state = 'VerificationSuccess'")
		case "failed":
			whereClauses = append(whereClauses, "current_state LIKE '%Failed'")
		case "orphaned":
			whereClauses = append(whereClauses, "current_state = 'MaxRetriesReached'")
		case "ignored":
			whereClauses = append(whereClauses, "current_state = 'CorruptionIgnored'")
		case "manual_intervention":
			// Items that require manual intervention in *arr (import blocked, manually removed from queue)
			whereClauses = append(whereClauses, "(current_state = 'ImportBlocked' OR current_state = 'ManuallyRemoved')")
		}
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
	// Map frontend sort keys to DB columns
	dbSortField := p.SortBy
	if p.SortBy == "state" {
		dbSortField = "current_state"
	}

	query := fmt.Sprintf("SELECT corruption_id, current_state, retry_count, file_path, path_id, last_error, detected_at, last_updated_at, corruption_type %s%s ORDER BY %s %s LIMIT ? OFFSET ?", baseQuery, whereClause, dbSortField, strings.ToUpper(p.SortOrder))
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

	// Get file_size from CorruptionDetected event
	var corruptionEventData sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT event_data FROM events
		WHERE aggregate_id = ? AND event_type = 'CorruptionDetected'
		ORDER BY created_at ASC LIMIT 1
	`, corruptionID).Scan(&corruptionEventData)
	if err == nil && corruptionEventData.Valid {
		var data map[string]interface{}
		if json.Unmarshal([]byte(corruptionEventData.String), &data) == nil {
			if fs, ok := data["file_size"].(float64); ok && fs > 0 {
				enriched["file_size"] = int64(fs)
			}
		}
	}

	// Get media info from SearchCompleted event (latest one if multiple)
	var searchEventData sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT event_data FROM events
		WHERE aggregate_id = ? AND event_type = 'SearchCompleted'
		ORDER BY created_at DESC LIMIT 1
	`, corruptionID).Scan(&searchEventData)
	if err == nil && searchEventData.Valid {
		var data map[string]interface{}
		if json.Unmarshal([]byte(searchEventData.String), &data) == nil {
			if title, ok := data["media_title"].(string); ok && title != "" {
				enriched["media_title"] = title
			}
			if year, ok := data["media_year"].(float64); ok {
				enriched["media_year"] = int(year)
			}
			if mediaType, ok := data["media_type"].(string); ok {
				enriched["media_type"] = mediaType
			}
			if season, ok := data["season_number"].(float64); ok {
				enriched["season_number"] = int(season)
			}
			if episode, ok := data["episode_number"].(float64); ok {
				enriched["episode_number"] = int(episode)
			}
			if arrType, ok := data["arr_type"].(string); ok {
				enriched["arr_type"] = arrType
			}
			if instanceName, ok := data["instance_name"].(string); ok {
				enriched["instance_name"] = instanceName
			}
		}
	}

	// Get quality and duration from VerificationSuccess event (if resolved)
	var verifyEventData sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT event_data FROM events
		WHERE aggregate_id = ? AND event_type = 'VerificationSuccess'
		ORDER BY created_at DESC LIMIT 1
	`, corruptionID).Scan(&verifyEventData)
	if err == nil && verifyEventData.Valid {
		var data map[string]interface{}
		if json.Unmarshal([]byte(verifyEventData.String), &data) == nil {
			if quality, ok := data["quality"].(string); ok && quality != "" {
				enriched["quality"] = quality
			}
			if releaseGroup, ok := data["release_group"].(string); ok && releaseGroup != "" {
				enriched["release_group"] = releaseGroup
			}
			if totalDur, ok := data["total_duration_seconds"].(float64); ok {
				enriched["total_duration_seconds"] = int64(totalDur)
			}
			if downloadDur, ok := data["download_duration_seconds"].(float64); ok {
				enriched["download_duration_seconds"] = int64(downloadDur)
			}
			if newFilePath, ok := data["new_file_path"].(string); ok {
				enriched["new_file_path"] = newFilePath
			}
			if newFileSize, ok := data["new_file_size"].(float64); ok {
				enriched["new_file_size"] = int64(newFileSize)
			}
		}
	}

	// Get latest download progress info (for in-progress items)
	var progressEventData sql.NullString
	err = s.db.QueryRowContext(ctx, `
		SELECT event_data FROM events
		WHERE aggregate_id = ? AND event_type = 'DownloadProgress'
		ORDER BY created_at DESC LIMIT 1
	`, corruptionID).Scan(&progressEventData)
	if err == nil && progressEventData.Valid {
		var data map[string]interface{}
		if json.Unmarshal([]byte(progressEventData.String), &data) == nil {
			if progress, ok := data["progress"].(float64); ok {
				enriched["download_progress"] = progress
			}
			if sizeBytes, ok := data["size_bytes"].(float64); ok {
				enriched["download_size"] = int64(sizeBytes)
			}
			if sizeLeft, ok := data["size_remaining_bytes"].(float64); ok {
				enriched["download_remaining"] = int64(sizeLeft)
			}
			if protocol, ok := data["protocol"].(string); ok {
				enriched["download_protocol"] = protocol
			}
			if client, ok := data["download_client"].(string); ok {
				enriched["download_client"] = client
			}
			if indexer, ok := data["indexer"].(string); ok {
				enriched["indexer"] = indexer
			}
			if timeLeft, ok := data["time_left"].(string); ok {
				enriched["download_time_left"] = timeLeft
			}
		}
	}

	return enriched
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "No IDs provided"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "No IDs provided"})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": "No IDs provided"})
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
