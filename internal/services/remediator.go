package services

import (
	"database/sql"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
)

// maxConcurrentRemediations limits how many remediations can run simultaneously
// to avoid overwhelming *arr APIs and download clients
const maxConcurrentRemediations = 5

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
	if !ok {
		logger.Errorf("Missing file_path in retry event for %s", corruptionID)
		r.publishError(corruptionID, domain.SearchFailed, "missing file_path in retry event")
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
		// Acquire semaphore to limit concurrent remediations
		r.semaphore <- struct{}{}
		defer func() { <-r.semaphore }()

		// Trigger search directly (skip deletion)
		r.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.SearchStarted,
			EventData: map[string]interface{}{
				"file_path": filePath,
				"media_id":  mediaID,
				"path_id":   pathID,
			},
		})

		// Extract episode IDs from metadata if available
		var episodeIDs []int64
		if metadata != nil {
			if episodeIDsRaw, ok := metadata["episode_ids"]; ok {
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
			}
		}

		err := r.arrClient.TriggerSearch(mediaID, arrPath, episodeIDs)
		if err != nil {
			logger.Errorf("Retry search failed for media %d: %v", mediaID, err)
			r.publishError(corruptionID, domain.SearchFailed, err.Error())
			return
		}

		logger.Infof("Retry search triggered successfully for %s (media ID: %d)", filePath, mediaID)

		r.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.SearchCompleted,
			EventData: map[string]interface{}{
				"file_path": filePath,
				"media_id":  mediaID,
				"metadata":  metadata,
				"path_id":   pathID,
				"is_retry":  true,
			},
		})
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

	filePath := data.FilePath
	corruptionType := data.CorruptionType
	pathID := data.PathID
	autoRemediate := data.AutoRemediate
	dryRun := data.DryRun

	// SAFETY CHECK: Verify this is a true corruption, not a recoverable error
	// This is a defense-in-depth check - the scanner should already filter these out,
	// but we double-check here before taking any destructive action
	switch corruptionType {
	case integration.ErrorTypeAccessDenied, integration.ErrorTypePathNotFound,
		integration.ErrorTypeMountLost, integration.ErrorTypeIOError,
		integration.ErrorTypeTimeout, integration.ErrorTypeInvalidConfig:
		// This is a recoverable error that somehow made it through - DO NOT remediate
		logger.Errorf("SAFETY: Refusing to remediate %s - error type '%s' indicates infrastructure issue, not corruption",
			filePath, corruptionType)
		r.publishError(corruptionID, domain.DeletionFailed,
			"remediation blocked: error type indicates infrastructure issue, not file corruption")
		return
	}

	logger.Infof("Handling corruption for file: %s", filePath)

	// Get path mapping
	arrPath, err := r.pathMapper.ToArrPath(filePath)
	if err != nil {
		logger.Errorf("Failed to map path %s: %v", filePath, err)
		r.publishError(corruptionID, domain.DeletionFailed, err.Error())
		return
	}

	// Emit queued event
	r.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.RemediationQueued,
	})

	// Check for auto-remediation
	if autoRemediate {
		// Check for global dry-run mode (environment variable) OR per-path dry-run mode
		// Global DryRunMode takes precedence - if set, ALL remediations are simulated
		if config.Get().DryRunMode {
			dryRun = true // Global override
		}

		if dryRun {
			logger.Infof("Auto-remediation enabled for %s, but DRY-RUN mode is set for this path", filePath)
		} else {
			logger.Infof("Auto-remediation enabled for %s, proceeding immediately", filePath)
		}

		// DRY-RUN MODE: Log what would happen without actually doing it
		if dryRun {
			go func() {
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
				r.eventBus.Publish(domain.Event{
					AggregateID:   corruptionID,
					AggregateType: "corruption",
					EventType:     domain.RemediationQueued, // Stay in queued state
					EventData: map[string]interface{}{
						"dry_run":  true,
						"media_id": mediaID,
						"message":  "Dry-run mode: remediation simulated but not executed",
					},
				})
			}()
			return
		}

		// Trigger remediation immediately
		go func() {
			// Acquire semaphore to limit concurrent remediations
			r.semaphore <- struct{}{}
			defer func() { <-r.semaphore }()

			// Delete file
			r.eventBus.Publish(domain.Event{
				AggregateID:   corruptionID,
				AggregateType: "corruption",
				EventType:     domain.DeletionStarted,
			})

			mediaID, err := r.arrClient.FindMediaByPath(arrPath)
			if err != nil {
				logger.Errorf("Failed to find media for path %s: %v", arrPath, err)
				r.publishError(corruptionID, domain.DeletionFailed, err.Error())
				return
			}

			metadata, err := r.arrClient.DeleteFile(mediaID, arrPath)
			if err != nil {
				logger.Errorf("Failed to delete file %s: %v", arrPath, err)
				r.publishError(corruptionID, domain.DeletionFailed, err.Error())
				return
			}

			// Success - emit deleted event
			r.eventBus.Publish(domain.Event{
				AggregateID:   corruptionID,
				AggregateType: "corruption",
				EventType:     domain.DeletionCompleted,
				EventData: map[string]interface{}{
					"media_id": mediaID,
					"metadata": metadata,
				},
			})

			// Trigger search
			r.eventBus.Publish(domain.Event{
				AggregateID:   corruptionID,
				AggregateType: "corruption",
				EventType:     domain.SearchStarted,
				EventData: map[string]interface{}{
					"file_path": filePath,
					"media_id":  mediaID,
					"path_id":   pathID,
				},
			})

			// Extract episode IDs from metadata for targeted search
			var episodeIDs []int64
			if episodeIDsRaw, ok := metadata["episode_ids"]; ok {
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
			}

			err = r.arrClient.TriggerSearch(mediaID, arrPath, episodeIDs)
			if err != nil {
				logger.Errorf("Failed to trigger search for media %d: %v", mediaID, err)
				r.publishError(corruptionID, domain.SearchFailed, err.Error())
				return
			}

			logger.Infof("Remediation completed successfully for %s", filePath)

			r.eventBus.Publish(domain.Event{
				AggregateID:   corruptionID,
				AggregateType: "corruption",
				EventType:     domain.SearchCompleted,
				EventData: map[string]interface{}{
					"file_path": filePath,
					"media_id":  mediaID,
					"metadata":  metadata,
					"path_id":   pathID,
				},
			})
		}()
	}
}

func (r *RemediatorService) publishError(id string, eventType domain.EventType, errMsg string) {
	r.eventBus.Publish(domain.Event{
		AggregateID:   id,
		AggregateType: "corruption",
		EventType:     eventType,
		EventData:     map[string]interface{}{"error": errMsg},
	})
}
