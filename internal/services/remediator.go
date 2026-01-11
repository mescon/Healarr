package services

import (
	"database/sql"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
)

// maxConcurrentRemediations limits how many remediations can run simultaneously
// to avoid overwhelming *arr APIs and download clients
const maxConcurrentRemediations = 5

// semaphoreAcquireTimeout is the maximum time to wait for a semaphore slot.
// This prevents indefinite blocking if all slots are stuck (Issue 5: deadlock prevention).
// Set to 2 minutes to allow time for HTTP timeouts (30s) plus processing.
const semaphoreAcquireTimeout = 2 * time.Minute

type RemediatorService struct {
	eventBus   eventbus.Publisher
	arrClient  integration.ArrClient
	pathMapper integration.PathMapper
	db         *sql.DB
	semaphore  chan struct{} // limits concurrent remediations
}

func NewRemediatorService(eb eventbus.Publisher, arr integration.ArrClient, pm integration.PathMapper, db *sql.DB) *RemediatorService {
	r := &RemediatorService{
		eventBus:   eb,
		arrClient:  arr,
		pathMapper: pm,
		db:         db,
		semaphore:  make(chan struct{}, maxConcurrentRemediations),
	}
	return r
}

func (r *RemediatorService) Start() {
	r.eventBus.Subscribe(domain.CorruptionDetected, r.handleCorruptionDetected)
	r.eventBus.Subscribe(domain.RetryScheduled, r.handleRetry)
}

func (r *RemediatorService) handleRetry(event domain.Event) {
	corruptionID := event.AggregateID

	// Check if deletion was already completed for this corruption
	// If so, we skip deletion and go directly to search
	deletionCompleted, mediaID, metadata := r.checkDeletionCompleted(corruptionID)

	if deletionCompleted {
		logger.Infof("Retry for %s: deletion already completed, skipping to search phase", corruptionID)
		r.retrySearchOnly(event, mediaID, metadata)
		return
	}

	// Deletion not yet completed - run full remediation flow
	r.handleCorruptionDetected(event)
}

// checkDeletionCompleted checks if a DeletionCompleted event exists for this corruption
// and returns the media_id and metadata from that event
func (r *RemediatorService) checkDeletionCompleted(corruptionID string) (bool, int64, map[string]interface{}) {
	if r.db == nil {
		return false, 0, nil
	}

	var mediaIDFloat sql.NullFloat64
	var metadataJSON sql.NullString

	err := r.db.QueryRow(`
		SELECT
			json_extract(event_data, '$.media_id'),
			json_extract(event_data, '$.metadata')
		FROM events
		WHERE aggregate_id = ? AND event_type = 'DeletionCompleted'
		ORDER BY created_at DESC
		LIMIT 1
	`, corruptionID).Scan(&mediaIDFloat, &metadataJSON)

	if err != nil {
		// No DeletionCompleted event found
		return false, 0, nil
	}

	mediaID := int64(0)
	if mediaIDFloat.Valid {
		mediaID = int64(mediaIDFloat.Float64)
	}

	// Parse metadata if available
	var metadata map[string]interface{}
	if metadataJSON.Valid && metadataJSON.String != "" {
		// The metadata is stored as a JSON object, we need to extract it
		// For simplicity, we'll just return nil and let the search use mediaID
		metadata = nil
	}

	return true, mediaID, metadata
}

// retrySearchOnly triggers a new search without attempting deletion
func (r *RemediatorService) retrySearchOnly(event domain.Event, mediaID int64, metadata map[string]interface{}) {
	corruptionID := event.AggregateID

	// Use type-safe event data parsing
	data, ok := event.ParseRetryEventData()
	if !ok || data.FilePath == "" {
		logger.Warnf("Invalid retry event data for %s: missing or empty file path", corruptionID)
		r.publishError(corruptionID, domain.SearchFailed, "missing or empty file_path in retry event")
		return
	}

	filePath := data.FilePath
	pathID := data.PathID

	// Get arr path for the search
	arrPath, err := r.pathMapper.ToArrPath(filePath)
	if err != nil {
		logger.Errorf("Failed to map path %s during retry: %v", filePath, err)
		r.publishError(corruptionID, domain.SearchFailed, err.Error())
		return
	}

	// If we don't have mediaID from previous deletion, look it up
	if mediaID == 0 {
		mediaID, err = r.arrClient.FindMediaByPath(arrPath)
		if err != nil {
			logger.Errorf("Failed to find media for retry search %s: %v", arrPath, err)
			r.publishError(corruptionID, domain.SearchFailed, err.Error())
			return
		}
	}

	go func() {
		// Acquire semaphore with timeout to limit concurrent remediations
		// and prevent indefinite blocking if slots are stuck
		select {
		case r.semaphore <- struct{}{}:
			defer func() { <-r.semaphore }()
		case <-time.After(semaphoreAcquireTimeout):
			logger.Warnf("Remediator: timeout acquiring semaphore for retry search %s after %v - all slots busy",
				corruptionID, semaphoreAcquireTimeout)
			r.publishError(corruptionID, domain.SearchFailed, "remediation queue full, will retry later")
			return
		}

		// Extract episode IDs from metadata first - validates data before announcing search
		episodeIDs := extractEpisodeIDs(metadata)

		// Publish search started with episode context (skip deletion in retry)
		if err := r.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.SearchStarted,
			EventData: map[string]interface{}{
				"file_path":   filePath,
				"media_id":    mediaID,
				"path_id":     pathID,
				"episode_ids": episodeIDs,
			},
		}); err != nil {
			logger.Errorf("Failed to publish SearchStarted event: %v", err)
		}

		err := r.arrClient.TriggerSearch(mediaID, arrPath, episodeIDs)
		if err != nil {
			logger.Errorf("Retry search failed for media %d: %v", mediaID, err)
			r.publishError(corruptionID, domain.SearchFailed, err.Error())
			return
		}

		logger.Infof("Retry search triggered successfully for %s (media ID: %d)", filePath, mediaID)

		// Publish search completed with enriched event data - critical event, use retry
		eventData := r.buildSearchEventData(filePath, arrPath, mediaID, pathID, metadata, true)
		if err := r.eventBus.PublishWithRetry(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.SearchCompleted,
			EventData:     eventData,
		}); err != nil {
			logger.Errorf("Failed to publish SearchCompleted event after retries: %v", err)
		}
	}()
}

func (r *RemediatorService) handleCorruptionDetected(event domain.Event) {
	corruptionID := event.AggregateID

	// Use type-safe event data parsing
	data, ok := event.ParseCorruptionEventData()
	if !ok {
		logger.Errorf("Missing file_path in event data for corruption %s", corruptionID)
		r.publishError(corruptionID, domain.DeletionFailed, "missing file_path in event data")
		return
	}

	// SAFETY CHECK: Verify this is a true corruption, not a recoverable error
	if r.isInfrastructureError(data.CorruptionType) {
		logger.Errorf("SAFETY: Refusing to remediate %s - error type '%s' indicates infrastructure issue, not corruption",
			data.FilePath, data.CorruptionType)
		r.publishError(corruptionID, domain.DeletionFailed,
			"remediation blocked: error type indicates infrastructure issue, not file corruption")
		return
	}

	logger.Infof("Handling corruption for file: %s", data.FilePath)

	// Get path mapping
	arrPath, err := r.pathMapper.ToArrPath(data.FilePath)
	if err != nil {
		logger.Errorf("Failed to map path %s: %v", data.FilePath, err)
		r.publishError(corruptionID, domain.DeletionFailed, err.Error())
		return
	}

	// Emit queued event
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.RemediationQueued,
	}); err != nil {
		logger.Errorf("Failed to publish RemediationQueued event: %v", err)
	}

	// Check for auto-remediation
	if !data.AutoRemediate {
		return
	}

	// Check for global dry-run mode override
	dryRun := data.DryRun || config.Get().DryRunMode

	if dryRun {
		logger.Infof("Auto-remediation enabled for %s, but DRY-RUN mode is set for this path", data.FilePath)
		go r.executeDryRun(corruptionID, data.FilePath, arrPath)
	} else {
		logger.Infof("Auto-remediation enabled for %s, proceeding immediately", data.FilePath)
		go r.executeRemediation(corruptionID, data.FilePath, arrPath, data.PathID)
	}
}

// isInfrastructureError checks if the error type indicates an infrastructure issue
// rather than actual file corruption
func (r *RemediatorService) isInfrastructureError(corruptionType string) bool {
	switch corruptionType {
	case integration.ErrorTypeAccessDenied, integration.ErrorTypePathNotFound,
		integration.ErrorTypeMountLost, integration.ErrorTypeIOError,
		integration.ErrorTypeTimeout, integration.ErrorTypeInvalidConfig:
		return true
	}
	return false
}

// executeDryRun simulates the remediation without making changes
func (r *RemediatorService) executeDryRun(corruptionID, filePath, arrPath string) {
	mediaID, err := r.arrClient.FindMediaByPath(arrPath)
	if err != nil {
		logger.Infof("[DRY-RUN] Would fail to find media for path %s: %v", arrPath, err)
		return
	}
	logger.Infof("[DRY-RUN] Would delete file and trigger search:")
	logger.Infof("[DRY-RUN]   - File: %s", filePath)
	logger.Infof("[DRY-RUN]   - *arr Path: %s", arrPath)
	logger.Infof("[DRY-RUN]   - Media ID: %d", mediaID)
	logger.Infof("[DRY-RUN]   - Action: DELETE file via *arr API, then trigger search")
	logger.Infof("[DRY-RUN] Set HEALARR_DRY_RUN=false to enable actual remediation")

	// Emit a special event for dry-run completion
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.RemediationQueued, // Stay in queued state
		EventData: map[string]interface{}{
			"dry_run":  true,
			"media_id": mediaID,
			"message":  "Dry-run mode: remediation simulated but not executed",
		},
	}); err != nil {
		logger.Errorf("Failed to publish dry-run event: %v", err)
	}
}

// executeRemediation performs the actual deletion and search trigger
func (r *RemediatorService) executeRemediation(corruptionID, filePath, arrPath string, pathID int64) {
	// Acquire semaphore with timeout to limit concurrent remediations
	// and prevent indefinite blocking if slots are stuck
	select {
	case r.semaphore <- struct{}{}:
		defer func() { <-r.semaphore }()
	case <-time.After(semaphoreAcquireTimeout):
		logger.Warnf("Remediator: timeout acquiring semaphore for %s after %v - all slots busy",
			corruptionID, semaphoreAcquireTimeout)
		r.publishError(corruptionID, domain.DeletionFailed, "remediation queue full, will retry later")
		return
	}

	// Find media first - validates we can proceed before publishing DeletionStarted
	mediaID, err := r.arrClient.FindMediaByPath(arrPath)
	if err != nil {
		logger.Errorf("Failed to find media for path %s: %v", arrPath, err)
		r.publishError(corruptionID, domain.DeletionFailed, err.Error())
		return
	}

	// Publish deletion started - now that we've validated we can proceed
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionStarted,
		EventData: map[string]interface{}{
			"file_path": filePath,
			"arr_path":  arrPath,
			"media_id":  mediaID,
		},
	}); err != nil {
		logger.Errorf("Failed to publish DeletionStarted event: %v", err)
	}

	// Delete file
	metadata, err := r.arrClient.DeleteFile(mediaID, arrPath)
	if err != nil {
		logger.Errorf("Failed to delete file %s: %v", arrPath, err)
		r.publishError(corruptionID, domain.DeletionFailed, err.Error())
		return
	}

	// Publish deletion completed - critical event, use retry
	if err := r.eventBus.PublishWithRetry(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionCompleted,
		EventData: map[string]interface{}{
			"media_id": mediaID,
			"metadata": metadata,
		},
	}); err != nil {
		logger.Errorf("Failed to publish DeletionCompleted event after retries: %v", err)
	}

	// Trigger search
	r.triggerSearch(corruptionID, filePath, arrPath, pathID, mediaID, metadata)
}

// triggerSearch initiates the search for a replacement file
func (r *RemediatorService) triggerSearch(corruptionID, filePath, arrPath string, pathID, mediaID int64, metadata map[string]interface{}) {
	// Extract episode IDs from metadata first - validates data before announcing search
	episodeIDs := extractEpisodeIDs(metadata)

	// Publish search started with episode context
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.SearchStarted,
		EventData: map[string]interface{}{
			"file_path":   filePath,
			"media_id":    mediaID,
			"path_id":     pathID,
			"episode_ids": episodeIDs,
		},
	}); err != nil {
		logger.Errorf("Failed to publish SearchStarted event: %v", err)
	}

	err := r.arrClient.TriggerSearch(mediaID, arrPath, episodeIDs)
	if err != nil {
		logger.Errorf("Failed to trigger search for media %d: %v", mediaID, err)
		r.publishError(corruptionID, domain.SearchFailed, err.Error())
		return
	}

	logger.Infof("Remediation completed successfully for %s", filePath)

	// Publish search completed with enriched event data - critical event, use retry
	eventData := r.buildSearchEventData(filePath, arrPath, mediaID, pathID, metadata, false)
	if err := r.eventBus.PublishWithRetry(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.SearchCompleted,
		EventData:     eventData,
	}); err != nil {
		logger.Errorf("Failed to publish SearchCompleted event after retries: %v", err)
	}
}

// extractEpisodeIDs extracts episode IDs from metadata for targeted search
func extractEpisodeIDs(metadata map[string]interface{}) []int64 {
	var episodeIDs []int64
	episodeIDsRaw, ok := metadata["episode_ids"]
	if !ok {
		return episodeIDs
	}

	switch v := episodeIDsRaw.(type) {
	case []int64:
		episodeIDs = v
	case []interface{}:
		for _, item := range v {
			if f, ok := item.(float64); ok {
				episodeIDs = append(episodeIDs, int64(f))
			} else if i, ok := item.(int64); ok {
				episodeIDs = append(episodeIDs, i)
			}
		}
	}
	return episodeIDs
}

// buildSearchEventData creates the event data map for search events with media details
func (r *RemediatorService) buildSearchEventData(filePath, arrPath string, mediaID, pathID int64, metadata map[string]interface{}, isRetry bool) map[string]interface{} {
	eventData := map[string]interface{}{
		"file_path": filePath,
		"media_id":  mediaID,
		"metadata":  metadata,
		"path_id":   pathID,
	}
	if isRetry {
		eventData["is_retry"] = true
	}

	// Fetch media details for rich display (gracefully degrades if unavailable)
	details, err := r.arrClient.GetMediaDetails(mediaID, arrPath)
	if err != nil || details == nil {
		return eventData
	}

	eventData["media_title"] = details.Title
	eventData["media_year"] = details.Year
	eventData["media_type"] = details.MediaType
	eventData["arr_type"] = details.ArrType
	eventData["instance_name"] = details.InstanceName
	if details.SeasonNumber > 0 {
		eventData["season_number"] = details.SeasonNumber
	}
	if details.EpisodeNumber > 0 {
		eventData["episode_number"] = details.EpisodeNumber
	}
	if details.EpisodeTitle != "" {
		eventData["episode_title"] = details.EpisodeTitle
	}
	return eventData
}

func (r *RemediatorService) publishError(id string, eventType domain.EventType, errMsg string) {
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   id,
		AggregateType: "corruption",
		EventType:     eventType,
		EventData:     map[string]interface{}{"error": errMsg},
	}); err != nil {
		logger.Errorf("Failed to publish error event %s: %v", eventType, err)
	}
}
