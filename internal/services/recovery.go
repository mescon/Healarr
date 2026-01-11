package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
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
	Metadata     map[string]interface{} // Additional metadata from events
	RetryCount   int                    // Number of retry attempts so far
	MaxRetries   int                    // Max retries for this path
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

// Early remediation states that may need recovery if interrupted
var earlyRemediationStates = []string{
	"RemediationQueued",
	"DeletionStarted",
	"DeletionCompleted",
}

// Post-search states that need verification recovery
var postSearchStates = []string{
	"DownloadProgress",
	"SearchCompleted",
	"SearchStarted",
	"DownloadStarted",
	"FileDetected",
}

// Failed states that may need retry recovery (Issue 2: lost retry timers)
var failedStates = []string{
	"DeletionFailed",
	"SearchFailed",
	"VerificationFailed",
	"DownloadTimeout",
	"DownloadFailed",
}

// findStaleItems queries the database for items stuck in in-progress states.
func (r *RecoveryService) findStaleItems() ([]staleItem, error) {
	// Calculate the cutoff time based on stale threshold
	cutoffTime := time.Now().Add(-r.staleThreshold).Format("2006-01-02 15:04:05")

	// Build combined state list for query
	allStates := make([]string, 0, len(earlyRemediationStates)+len(postSearchStates)+len(failedStates))
	allStates = append(allStates, earlyRemediationStates...)
	allStates = append(allStates, postSearchStates...)
	allStates = append(allStates, failedStates...)

	// Build placeholders for IN clause using parameterized query pattern.
	// Security: placeholders only contains "?" characters, actual values passed via args.
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(allStates)), ", ")
	args := make([]interface{}, 0, len(allStates)+1)
	for _, state := range allStates {
		args = append(args, state)
	}
	args = append(args, cutoffTime)

	query := `
		SELECT
			cs.corruption_id,
			cs.current_state,
			cs.file_path,
			COALESCE(cs.path_id, 0) as path_id,
			cs.last_updated_at,
			cs.retry_count,
			COALESCE(sp.max_retries, 3) as max_retries,
			(
				SELECT json_extract(e.event_data, '$.media_id')
				FROM events e
				WHERE e.aggregate_id = cs.corruption_id
				AND e.event_type IN ('SearchCompleted', 'SearchStarted', 'DeletionCompleted')
				ORDER BY e.id DESC
				LIMIT 1
			) as media_id,
			(
				SELECT e.event_data
				FROM events e
				WHERE e.aggregate_id = cs.corruption_id
				AND e.event_type = 'DeletionCompleted'
				ORDER BY e.id DESC
				LIMIT 1
			) as deletion_metadata
		FROM corruption_status cs
		LEFT JOIN scan_paths sp ON sp.id = cs.path_id
		WHERE cs.current_state IN (` + placeholders + `)
		AND cs.last_updated_at < ?
	`

	ctx, cancel := context.WithTimeout(context.Background(), recoveryQueryTimeout)
	defer cancel()

	rows, err := r.db.QueryContext(ctx, query, args...) //NOSONAR - placeholders are "?" only, values passed via args
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var items []staleItem
	for rows.Next() {
		var item staleItem
		var lastUpdated string
		var mediaIDRaw sql.NullFloat64
		var deletionMetadataRaw sql.NullString

		if err := rows.Scan(&item.CorruptionID, &item.CurrentState, &item.FilePath, &item.PathID,
			&lastUpdated, &item.RetryCount, &item.MaxRetries, &mediaIDRaw, &deletionMetadataRaw); err != nil {
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

		// Parse deletion metadata if available
		if deletionMetadataRaw.Valid && deletionMetadataRaw.String != "" {
			var metadata map[string]interface{}
			if err := json.Unmarshal([]byte(deletionMetadataRaw.String), &metadata); err == nil {
				item.Metadata = metadata
			}
		}

		items = append(items, item)
	}

	return items, rows.Err()
}

// checkArrStatus checks queue and file status in arr for the item.
// Returns: "skipped" if in queue, "recovered" if file found, "" to continue.
func (r *RecoveryService) checkArrStatus(item staleItem) string {
	if item.MediaID <= 0 {
		return ""
	}

	// Check if item is still in arr queue
	if inQueue, err := r.isInArrQueue(item); err != nil {
		logger.Debugf("Recovery: Failed to check queue for %s: %v", item.FilePath, err)
	} else if inQueue {
		logger.Debugf("Recovery: %s is still in arr queue, skipping", item.FilePath)
		return "skipped"
	}

	// Check if arr has the file
	if hasFile, filePath, err := r.checkArrHasFile(item); err != nil {
		logger.Debugf("Recovery: Failed to check arr file status for %s: %v", item.FilePath, err)
	} else if hasFile && filePath != "" {
		logger.Infof("Recovery: %s has file in arr at %s, verifying health", item.FilePath, filePath)
		return r.verifyAndComplete(item, filePath)
	}

	return ""
}

// getLocalPath returns the mapped local path for the item's file path.
func (r *RecoveryService) getLocalPath(item staleItem) string {
	if r.pathMapper == nil {
		return item.FilePath
	}
	if mapped, err := r.pathMapper.ToLocalPath(item.FilePath); err == nil && mapped != "" {
		return mapped
	}
	return item.FilePath
}

// recoverItem attempts to recover a single stale item.
// Returns "recovered", "exhausted", or "skipped".
func (r *RecoveryService) recoverItem(item staleItem) string {
	logger.Debugf("Recovery: Processing %s (state: %s, media_id: %d, retries: %d/%d)",
		item.FilePath, item.CurrentState, item.MediaID, item.RetryCount, item.MaxRetries)

	// Route to appropriate recovery handler based on state category
	if r.isEarlyRemediationState(item.CurrentState) {
		return r.recoverEarlyRemediationState(item)
	}

	if r.isFailedState(item.CurrentState) {
		return r.recoverFailedState(item)
	}

	// Post-search states: use existing verification logic
	return r.recoverPostSearchState(item)
}

// isEarlyRemediationState checks if state is an early remediation state
func (r *RecoveryService) isEarlyRemediationState(state string) bool {
	for _, s := range earlyRemediationStates {
		if s == state {
			return true
		}
	}
	return false
}

// isFailedState checks if state is a failed state
func (r *RecoveryService) isFailedState(state string) bool {
	for _, s := range failedStates {
		if s == state {
			return true
		}
	}
	return false
}

// recoverEarlyRemediationState handles recovery for RemediationQueued, DeletionStarted, DeletionCompleted
func (r *RecoveryService) recoverEarlyRemediationState(item staleItem) string {
	switch item.CurrentState {
	case "RemediationQueued":
		// Remediation was queued but never started - re-trigger via RetryScheduled
		logger.Infof("Recovery: %s stuck in RemediationQueued, re-triggering remediation", item.FilePath)
		return r.emitRetryScheduled(item)

	case "DeletionStarted":
		// Deletion was started but we don't know if it completed
		// Check if file still exists on disk
		localPath := r.getLocalPath(item)
		if r.detector != nil {
			healthy, _ := r.detector.Check(localPath, "quick")
			if healthy {
				// File still exists and is healthy - corruption may have been a false positive
				logger.Infof("Recovery: %s in DeletionStarted but file is healthy, marking resolved", item.FilePath)
				return r.emitVerificationSuccess(item, localPath)
			}
		}
		// File is gone or still corrupt - re-trigger remediation to continue from search
		logger.Infof("Recovery: %s stuck in DeletionStarted, re-triggering remediation", item.FilePath)
		return r.emitRetryScheduled(item)

	case "DeletionCompleted":
		// File was deleted but search was never triggered - trigger search now
		logger.Infof("Recovery: %s stuck in DeletionCompleted, triggering search", item.FilePath)
		return r.emitSearchNeeded(item)

	default:
		return "skipped"
	}
}

// recoverFailedState handles recovery for failed states (DeletionFailed, SearchFailed, etc.)
// This fixes Issue 2: lost retry timers on restart
func (r *RecoveryService) recoverFailedState(item staleItem) string {
	// Check if max retries reached
	if item.RetryCount >= item.MaxRetries {
		logger.Infof("Recovery: %s in %s has reached max retries (%d/%d), marking exhausted",
			item.FilePath, item.CurrentState, item.RetryCount, item.MaxRetries)
		return r.emitMaxRetriesReached(item)
	}

	// Schedule a retry - the in-memory timer was lost on restart
	logger.Infof("Recovery: %s in %s, scheduling retry (%d/%d)",
		item.FilePath, item.CurrentState, item.RetryCount+1, item.MaxRetries)
	return r.emitRetryScheduled(item)
}

// recoverPostSearchState handles recovery for post-search verification states
func (r *RecoveryService) recoverPostSearchState(item staleItem) string {
	// Step 1 & 2: Check arr queue and file status
	if result := r.checkArrStatus(item); result != "" {
		return result
	}

	// Step 3: Check if file exists on disk at original path
	localPath := r.getLocalPath(item)
	if r.detector != nil {
		if healthy, _ := r.detector.Check(localPath, "quick"); healthy {
			logger.Infof("Recovery: File exists and is healthy at %s", localPath)
			return r.emitVerificationSuccess(item, localPath)
		}
	}

	// Step 4: Item is gone - emit SearchExhausted
	logger.Infof("Recovery: No replacement found for %s, emitting SearchExhausted", item.FilePath)
	return r.emitSearchExhausted(item, "item_vanished")
}

// emitRetryScheduled publishes a RetryScheduled event to re-trigger remediation
func (r *RecoveryService) emitRetryScheduled(item staleItem) string {
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   item.CorruptionID,
		AggregateType: "corruption",
		EventType:     domain.RetryScheduled,
		EventData: map[string]interface{}{
			"file_path":       item.FilePath,
			"path_id":         item.PathID,
			"auto_remediate":  true,
			"recovery_action": "startup_recovery",
			"original_state":  item.CurrentState,
		},
	}); err != nil {
		logger.Errorf("Recovery: Failed to publish RetryScheduled for %s: %v", item.CorruptionID, err)
		return "skipped"
	}
	logger.Infof("Recovery: Scheduled retry for %s (was in %s)", item.FilePath, item.CurrentState)
	return "recovered"
}

// emitSearchNeeded publishes a SearchStarted event for items stuck after deletion
func (r *RecoveryService) emitSearchNeeded(item staleItem) string {
	// We need media_id to trigger search - if not available, fall back to retry
	if item.MediaID <= 0 {
		logger.Warnf("Recovery: %s in DeletionCompleted but no media_id, falling back to retry", item.FilePath)
		return r.emitRetryScheduled(item)
	}

	// Extract episode IDs from metadata if available
	var episodeIDs []int64
	if item.Metadata != nil {
		if metadataInner, ok := item.Metadata["metadata"].(map[string]interface{}); ok {
			if eps, ok := metadataInner["episodeIds"].([]interface{}); ok {
				for _, ep := range eps {
					if epID, ok := ep.(float64); ok {
						episodeIDs = append(episodeIDs, int64(epID))
					}
				}
			}
		}
	}

	eventData := map[string]interface{}{
		"file_path":       item.FilePath,
		"media_id":        item.MediaID,
		"path_id":         item.PathID,
		"recovery_action": "startup_recovery",
		"original_state":  item.CurrentState,
	}
	if len(episodeIDs) > 0 {
		eventData["episode_ids"] = episodeIDs
	}

	// Publish SearchStarted to trigger the remediator to call TriggerSearch
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   item.CorruptionID,
		AggregateType: "corruption",
		EventType:     domain.SearchStarted,
		EventData:     eventData,
	}); err != nil {
		logger.Errorf("Recovery: Failed to publish SearchStarted for %s: %v", item.CorruptionID, err)
		return "skipped"
	}

	// Now we need to actually trigger the search in arr
	if r.arrClient != nil {
		arrPath := item.FilePath
		if r.pathMapper != nil {
			if mapped, err := r.pathMapper.ToArrPath(item.FilePath); err == nil && mapped != "" {
				arrPath = mapped
			}
		}

		if err := r.arrClient.TriggerSearch(item.MediaID, arrPath, episodeIDs); err != nil {
			logger.Errorf("Recovery: Failed to trigger search for %s: %v", item.FilePath, err)
			// Publish SearchFailed so the normal retry flow can handle it
			r.eventBus.Publish(domain.Event{
				AggregateID:   item.CorruptionID,
				AggregateType: "corruption",
				EventType:     domain.SearchFailed,
				EventData: map[string]interface{}{
					"file_path":       item.FilePath,
					"path_id":         item.PathID,
					"error":           err.Error(),
					"recovery_action": "startup_recovery",
				},
			})
			return "skipped"
		}

		// Publish SearchCompleted
		if err := r.eventBus.Publish(domain.Event{
			AggregateID:   item.CorruptionID,
			AggregateType: "corruption",
			EventType:     domain.SearchCompleted,
			EventData:     eventData,
		}); err != nil {
			logger.Errorf("Recovery: Failed to publish SearchCompleted for %s: %v", item.CorruptionID, err)
		}
	}

	logger.Infof("Recovery: Triggered search for %s (was stuck in DeletionCompleted)", item.FilePath)
	return "recovered"
}

// emitMaxRetriesReached publishes a MaxRetriesReached event
func (r *RecoveryService) emitMaxRetriesReached(item staleItem) string {
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   item.CorruptionID,
		AggregateType: "corruption",
		EventType:     domain.MaxRetriesReached,
		EventData: map[string]interface{}{
			"file_path":       item.FilePath,
			"path_id":         item.PathID,
			"retry_count":     item.RetryCount,
			"max_retries":     item.MaxRetries,
			"original_state":  item.CurrentState,
			"recovery_action": "startup_recovery",
		},
	}); err != nil {
		logger.Errorf("Recovery: Failed to publish MaxRetriesReached for %s: %v", item.CorruptionID, err)
		return "skipped"
	}
	logger.Infof("Recovery: Marked %s as max retries reached", item.FilePath)
	return "exhausted"
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
