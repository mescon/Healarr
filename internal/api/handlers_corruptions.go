package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/logger"
)

func (s *RESTServer) getCorruptions(c *gin.Context) {
	// Parse pagination parameters
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 1000 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Parse sorting and filtering parameters
	sortBy := c.DefaultQuery("sort_by", "last_updated_at")
	sortOrder := c.DefaultQuery("sort_order", "desc")
	statusFilter := c.DefaultQuery("status", "all")

	// Validate sort parameters to prevent SQL injection
	allowedSortFields := map[string]bool{
		"detected_at":     true,
		"last_updated_at": true,
		"file_path":       true,
		"state":           true,
		"corruption_type": true,
	}
	if !allowedSortFields[sortBy] {
		sortBy = "last_updated_at"
	}
	if sortOrder != "asc" && sortOrder != "desc" {
		sortOrder = "desc"
	}

	// Build query
	baseQuery := "FROM corruption_status"
	whereClause := ""
	args := []interface{}{}

	if statusFilter != "all" {
		whereClause = " WHERE "
		switch statusFilter {
		case "active":
			whereClause += "current_state != 'VerificationSuccess' AND current_state != 'MaxRetriesReached' AND current_state != 'CorruptionIgnored'"
		case "pending":
			// Pending = detected but not yet being processed (just CorruptionDetected state)
			whereClause += "current_state = 'CorruptionDetected'"
		case "in_progress":
			// In progress = currently being remediated (Queued, Started, Progress states)
			whereClause += "(current_state LIKE '%Started' OR current_state LIKE '%Queued' OR current_state LIKE '%Progress' OR current_state = 'RemediationQueued')"
		case "resolved":
			whereClause += "current_state = 'VerificationSuccess'"
		case "failed":
			whereClause += "current_state LIKE '%Failed'"
		case "orphaned":
			whereClause += "current_state = 'MaxRetriesReached'"
		case "ignored":
			whereClause += "current_state = 'CorruptionIgnored'"
		default:
			whereClause = "" // Ignore invalid filter
		}
	}

	// Get total count with filter
	var total int
	countQuery := "SELECT COUNT(*) " + baseQuery + whereClause
	err := s.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Get paginated data with filter and sort
	// Map frontend sort keys to DB columns
	dbSortField := sortBy
	if sortBy == "state" {
		dbSortField = "current_state"
	}

	query := fmt.Sprintf("SELECT corruption_id, current_state, retry_count, file_path, last_error, detected_at, last_updated_at, corruption_type %s%s ORDER BY %s %s LIMIT ? OFFSET ?", baseQuery, whereClause, dbSortField, strings.ToUpper(sortOrder))
	args = append(args, limit, offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	corruptions := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id, state, filePath string
		var lastError, corruptionType sql.NullString
		var retryCount int
		var detectedAt, lastUpdatedAt string

		if err := rows.Scan(&id, &state, &retryCount, &filePath, &lastError, &detectedAt, &lastUpdatedAt, &corruptionType); err != nil {
			continue
		}

		corruptions = append(corruptions, map[string]interface{}{
			"id":              id,
			"state":           state,
			"retry_count":     retryCount,
			"file_path":       filePath,
			"last_error":      lastError.String,
			"detected_at":     detectedAt,
			"last_updated_at": lastUpdatedAt,
			"corruption_type": corruptionType.String,
		})
	}

	totalPages := (total + limit - 1) / limit
	c.JSON(http.StatusOK, gin.H{
		"data": corruptions,
		"pagination": gin.H{
			"page":        page,
			"limit":       limit,
			"total":       total,
			"total_pages": totalPages,
		},
	})
}

func (s *RESTServer) getRemediations(c *gin.Context) {
	// Parse pagination parameters
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	if page < 1 {
		page = 1
	}
	if limit < 1 || limit > 500 {
		limit = 50
	}
	offset := (page - 1) * limit

	// Get total count
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM corruption_status WHERE current_state = 'resolved'").Scan(&total)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Get paginated data
	rows, err := s.db.Query("SELECT corruption_id, file_path, last_updated_at FROM corruption_status WHERE current_state = 'resolved' ORDER BY last_updated_at DESC LIMIT ? OFFSET ?", limit, offset)
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

	totalPages := (total + limit - 1) / limit
	c.JSON(http.StatusOK, gin.H{
		"data": remediations,
		"pagination": gin.H{
			"page":        page,
			"limit":       limit,
			"total":       total,
			"total_pages": totalPages,
		},
	})
}

func (s *RESTServer) getCorruptionHistory(c *gin.Context) {
	id := c.Param("id")
	rows, err := s.db.Query("SELECT event_type, event_data, created_at FROM events WHERE aggregate_id = ? ORDER BY created_at ASC", id)
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
			json.Unmarshal(eventData, &data)
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
		err := s.db.QueryRow(`
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

		s.eventBus.Publish(domain.Event{
			AggregateID:   id,
			AggregateType: "corruption",
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path":      filePath.String,
				"path_id":        pathID.Int64,
				"auto_remediate": true,
				"manual_retry":   true,
			},
		})
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
		s.eventBus.Publish(domain.Event{
			AggregateID:   id,
			AggregateType: "corruption",
			EventType:     domain.CorruptionIgnored,
			EventData:     map[string]interface{}{"reason": "Manually ignored by user"},
		})
		ignored++
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Ignored %d corruption(s)", ignored),
		"ignored": ignored,
	})
}

// deleteCorruptions removes corruption entries from the database
func (s *RESTServer) deleteCorruptions(c *gin.Context) {
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
		result, err := s.db.Exec(`DELETE FROM events WHERE aggregate_id = ?`, id)
		if err != nil {
			logger.Errorf("Failed to delete events for corruption %s: %v", id, err)
			continue
		}
		rows, _ := result.RowsAffected()
		if rows > 0 {
			deleted++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": fmt.Sprintf("Deleted %d corruption(s)", deleted),
		"deleted": deleted,
	})
}
