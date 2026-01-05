package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
)

// recoveryQueryTimeout is the maximum time for database queries in recovery service.
const recoveryQueryTimeout = 30 * time.Second

// RecoveryService recovers stale in-progress items on startup.
// It runs once when the application starts to reconcile Healarr state
// with the actual state in *arr applications.
type RecoveryService struct {
	db         *sql.DB
	eventBus   *eventbus.EventBus
	arrClient  integration.ArrClient
	pathMapper integration.PathMapper
	detector   integration.HealthChecker

	staleThreshold time.Duration
}

// NewRecoveryService creates a new recovery service.
func NewRecoveryService(db *sql.DB, eb *eventbus.EventBus, arrClient integration.ArrClient, pathMapper integration.PathMapper, detector integration.HealthChecker, staleThreshold time.Duration) *RecoveryService {
	if staleThreshold <= 0 {
		staleThreshold = 24 * time.Hour
	}
	return &RecoveryService{
		db:             db,
		eventBus:       eb,
		arrClient:      arrClient,
		pathMapper:     pathMapper,
		detector:       detector,
		staleThreshold: staleThreshold,
	}
}

// staleItem represents an item stuck in an in-progress state.
type staleItem struct {
	CorruptionID string
	CurrentState string
	FilePath     string
	PathID       int64
	MediaID      int64
	LastUpdated  time.Time
}

// Run executes the recovery process once.
// This should be called on startup after all services are initialized.
func (r *RecoveryService) Run() {
	logger.Infof("Recovery: Checking for stale in-progress items (threshold: %s)", r.staleThreshold)

	items, err := r.findStaleItems()
	if err != nil {
		logger.Errorf("Recovery: Failed to find stale items: %v", err)
		return
	}

	if len(items) == 0 {
		logger.Infof("Recovery: No stale items found")
		return
	}

	logger.Infof("Recovery: Found %d stale items to recover", len(items))

	recovered := 0
	exhausted := 0
	skipped := 0

	for _, item := range items {
		action := r.recoverItem(item)
		switch action {
		case "recovered":
			recovered++
		case "exhausted":
			exhausted++
		case "skipped":
			skipped++
		}
	}

	logger.Infof("Recovery: Complete - recovered=%d, exhausted=%d, skipped=%d", recovered, exhausted, skipped)
}

// findStaleItems queries the database for items stuck in in-progress states.
func (r *RecoveryService) findStaleItems() ([]staleItem, error) {
	// Calculate the cutoff time based on stale threshold
	cutoffTime := time.Now().Add(-r.staleThreshold).Format("2006-01-02 15:04:05")

	query := `
		SELECT
			cs.corruption_id,
			cs.current_state,
			cs.file_path,
			COALESCE(cs.path_id, 0) as path_id,
			cs.last_updated_at,
			(
				SELECT json_extract(e.event_data, '$.media_id')
				FROM events e
				WHERE e.aggregate_id = cs.corruption_id
				AND e.event_type IN ('SearchCompleted', 'SearchStarted')
				ORDER BY e.id DESC
				LIMIT 1
			) as media_id
		FROM corruption_status cs
		WHERE cs.current_state IN ('DownloadProgress', 'SearchCompleted', 'SearchStarted', 'DownloadStarted', 'FileDetected')
		AND cs.last_updated_at < ?
	`

	ctx, cancel := context.WithTimeout(context.Background(), recoveryQueryTimeout)
	defer cancel()

	rows, err := r.db.QueryContext(ctx, query, cutoffTime)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []staleItem
	for rows.Next() {
		var item staleItem
		var lastUpdated string
		var mediaIDRaw sql.NullFloat64

		if err := rows.Scan(&item.CorruptionID, &item.CurrentState, &item.FilePath, &item.PathID, &lastUpdated, &mediaIDRaw); err != nil {
			logger.Debugf("Recovery: Failed to scan row: %v", err)
			continue
		}

		// Parse last updated time
		if t, err := time.Parse("2006-01-02 15:04:05", lastUpdated); err == nil {
			item.LastUpdated = t
		} else if t, err := time.Parse(time.RFC3339, lastUpdated); err == nil {
			item.LastUpdated = t
		}

		if mediaIDRaw.Valid {
			item.MediaID = int64(mediaIDRaw.Float64)
		}

		items = append(items, item)
	}

	return items, rows.Err()
}

// recoverItem attempts to recover a single stale item.
// Returns "recovered", "exhausted", or "skipped".
func (r *RecoveryService) recoverItem(item staleItem) string {
	logger.Debugf("Recovery: Processing %s (state: %s, media_id: %d)", item.FilePath, item.CurrentState, item.MediaID)

	// Step 1: Check if item is still in arr queue
	if item.MediaID > 0 {
		inQueue, err := r.isInArrQueue(item)
		if err != nil {
			logger.Debugf("Recovery: Failed to check queue for %s: %v", item.FilePath, err)
		} else if inQueue {
			logger.Debugf("Recovery: %s is still in arr queue, skipping", item.FilePath)
			return "skipped"
		}
	}

	// Step 2: Check if arr has the file (hasFile: true)
	if item.MediaID > 0 {
		hasFile, filePath, err := r.checkArrHasFile(item)
		if err != nil {
			logger.Debugf("Recovery: Failed to check arr file status for %s: %v", item.FilePath, err)
		} else if hasFile && filePath != "" {
			// File exists in arr - verify it's healthy
			logger.Infof("Recovery: %s has file in arr at %s, verifying health", item.FilePath, filePath)
			return r.verifyAndComplete(item, filePath)
		}
	}

	// Step 3: Check if file exists on disk at original path
	localPath := item.FilePath
	if r.pathMapper != nil {
		if mapped, err := r.pathMapper.ToLocalPath(item.FilePath); err == nil && mapped != "" {
			localPath = mapped
		}
	}

	if r.detector != nil {
		healthy, _ := r.detector.Check(localPath, "quick")
		if healthy {
			logger.Infof("Recovery: File exists and is healthy at %s", localPath)
			return r.emitVerificationSuccess(item, localPath)
		}
	}

	// Step 4: Item is gone - emit SearchExhausted
	logger.Infof("Recovery: No replacement found for %s, emitting SearchExhausted", item.FilePath)
	return r.emitSearchExhausted(item, "item_vanished")
}

// isInArrQueue checks if the item is still in the arr download queue.
func (r *RecoveryService) isInArrQueue(item staleItem) (bool, error) {
	if r.arrClient == nil {
		return false, nil
	}

	queueItems, err := r.arrClient.GetQueueForPath(item.FilePath)
	if err != nil {
		return false, err
	}

	for _, qi := range queueItems {
		if qi.Title != "" {
			// Simple check - if there's anything in queue for this path, consider it active
			return true, nil
		}
	}

	return false, nil
}

// checkArrHasFile checks if arr reports the media as having a file.
func (r *RecoveryService) checkArrHasFile(item staleItem) (hasFile bool, filePath string, err error) {
	if r.arrClient == nil {
		return false, "", nil
	}

	// Use GetAllFilePaths to check if arr has file(s) for this media
	// Pass nil metadata since we're just checking existence
	allPaths, err := r.arrClient.GetAllFilePaths(item.MediaID, nil, item.FilePath)
	if err != nil {
		return false, "", err
	}

	if len(allPaths) > 0 {
		// Return the first path found
		return true, allPaths[0], nil
	}

	return false, "", nil
}

// verifyAndComplete verifies the file health and emits success or exhausted.
func (r *RecoveryService) verifyAndComplete(item staleItem, filePath string) string {
	localPath := filePath
	if r.pathMapper != nil {
		if mapped, err := r.pathMapper.ToLocalPath(filePath); err == nil && mapped != "" {
			localPath = mapped
		}
	}

	if r.detector != nil {
		healthy, healthErr := r.detector.Check(localPath, "thorough")
		if healthy {
			return r.emitVerificationSuccess(item, localPath)
		}
		// File exists but is corrupt - emit SearchExhausted (user needs to retry)
		errMsg := "unknown error"
		if healthErr != nil {
			errMsg = healthErr.Message
		}
		logger.Warnf("Recovery: File at %s exists but failed health check: %s", localPath, errMsg)
		return r.emitSearchExhausted(item, "file_corrupt")
	}

	// No detector, assume healthy
	return r.emitVerificationSuccess(item, localPath)
}

// emitVerificationSuccess publishes a VerificationSuccess event.
func (r *RecoveryService) emitVerificationSuccess(item staleItem, filePath string) string {
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   item.CorruptionID,
		AggregateType: "corruption",
		EventType:     domain.VerificationSuccess,
		EventData: map[string]interface{}{
			"file_path":       filePath,
			"path_id":         item.PathID,
			"recovery_action": "auto_recovered",
			"original_state":  item.CurrentState,
		},
	}); err != nil {
		logger.Errorf("Recovery: Failed to publish VerificationSuccess for %s: %v", item.CorruptionID, err)
		return "skipped"
	}
	logger.Infof("Recovery: Marked %s as resolved (file verified)", item.FilePath)
	return "recovered"
}

// emitSearchExhausted publishes a SearchExhausted event.
func (r *RecoveryService) emitSearchExhausted(item staleItem, reason string) string {
	// Get retry count from database
	var retryCount int
	ctx, cancel := context.WithTimeout(context.Background(), recoveryQueryTimeout)
	defer cancel()

	if err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM events
		WHERE aggregate_id = ? AND event_type IN ('RetryScheduled', 'SearchStarted')
	`, item.CorruptionID).Scan(&retryCount); err != nil {
		logger.Debugf("Recovery: Failed to get retry count for %s: %v", item.CorruptionID, err)
	}

	eventData := map[string]interface{}{
		"file_path":        item.FilePath,
		"path_id":          item.PathID,
		"reason":           reason,
		"attempts":         retryCount,
		"last_search_time": item.LastUpdated.Format(time.RFC3339),
		"original_state":   item.CurrentState,
		"recovery_action":  "marked_exhausted",
	}

	// Include media_id if available
	if item.MediaID > 0 {
		eventData["media_id"] = item.MediaID
	}

	eventDataJSON, _ := json.Marshal(eventData)
	logger.Debugf("Recovery: SearchExhausted event data: %s", string(eventDataJSON))

	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   item.CorruptionID,
		AggregateType: "corruption",
		EventType:     domain.SearchExhausted,
		EventData:     eventData,
	}); err != nil {
		logger.Errorf("Recovery: Failed to publish SearchExhausted for %s: %v", item.CorruptionID, err)
		return "skipped"
	}
	logger.Infof("Recovery: Marked %s as exhausted (reason: %s)", item.FilePath, reason)
	return "exhausted"
}
