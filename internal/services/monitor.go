package services

import (
	"database/sql"
	"math"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/logger"
)

type MonitorService struct {
	eventBus *eventbus.EventBus
	db       *sql.DB
}

func NewMonitorService(eb *eventbus.EventBus, db *sql.DB) *MonitorService {
	return &MonitorService{
		eventBus: eb,
		db:       db,
	}
}

func (m *MonitorService) Start() {
	m.eventBus.Subscribe(domain.DeletionFailed, m.handleFailure)
	m.eventBus.Subscribe(domain.SearchFailed, m.handleFailure)
	m.eventBus.Subscribe(domain.VerificationFailed, m.handleFailure)
	m.eventBus.Subscribe(domain.DownloadTimeout, m.handleFailure)
}

func (m *MonitorService) handleFailure(event domain.Event) {
	corruptionID := event.AggregateID

	// Get retry count and max limit
	retryCount, maxRetries, err := m.getRetryCount(corruptionID)
	if err != nil {
		logger.Errorf("Failed to get retry count for %s: %v", corruptionID, err)
		return
	}

	if retryCount >= maxRetries {
		m.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.MaxRetriesReached,
		})
		return
	}

	// Fetch file_path and path_id from the original CorruptionDetected event
	// so the Remediator has the context it needs
	filePath, pathID, err := m.getCorruptionContext(corruptionID)
	if err != nil {
		logger.Errorf("Failed to get context for corruption %s: %v", corruptionID, err)
		return
	}

	// Exponential backoff: 15m, 30m, 60m
	delay := time.Duration(math.Pow(2, float64(retryCount))) * 15 * time.Minute

	time.AfterFunc(delay, func() {
		m.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path":      filePath,
				"path_id":        pathID,
				"auto_remediate": true, // Retries should always auto-remediate
			},
		})
	})
}

// getCorruptionContext retrieves the file_path and path_id from the original CorruptionDetected event
func (m *MonitorService) getCorruptionContext(corruptionID string) (string, int64, error) {
	var filePath sql.NullString
	var pathID sql.NullInt64

	err := m.db.QueryRow(`
		SELECT 
			json_extract(event_data, '$.file_path'),
			json_extract(event_data, '$.path_id')
		FROM events 
		WHERE aggregate_id = ? AND event_type = 'CorruptionDetected'
		LIMIT 1
	`, corruptionID).Scan(&filePath, &pathID)

	if err != nil {
		return "", 0, err
	}

	if !filePath.Valid || filePath.String == "" {
		return "", 0, sql.ErrNoRows
	}

	return filePath.String, pathID.Int64, nil
}

func (m *MonitorService) getRetryCount(corruptionID string) (int, int, error) {
	var count int
	var maxRetries sql.NullInt64
	defaultMaxRetries := config.Get().DefaultMaxRetries

	// Get retry count and max_retries from view and scan_paths
	// We use a LEFT JOIN to handle cases where path_id is missing or scan path is deleted
	query := `
		SELECT 
			cs.retry_count,
			sp.max_retries
		FROM corruption_status cs
		LEFT JOIN scan_paths sp ON sp.id = cs.path_id
		WHERE cs.corruption_id = ?
	`

	err := m.db.QueryRow(query, corruptionID).Scan(&count, &maxRetries)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, defaultMaxRetries, nil // Use configured default if not found
		}
		return 0, defaultMaxRetries, err
	}

	limit := defaultMaxRetries
	if maxRetries.Valid {
		limit = int(maxRetries.Int64)
	}
	// Ensure at least 1 retry if configured to 0 (unless 0 means no retries? Let's assume 0 is valid "no retry")
	// But usually 0 means "disable", but here it's max_retries.
	// If user sets 0, they want 0 retries.

	return count, limit, nil
}
