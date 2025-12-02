package services

import (
	"database/sql"
	"math"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
)

type VerifierService struct {
	eventBus   *eventbus.EventBus
	detector   integration.HealthChecker
	pathMapper integration.PathMapper
	arrClient  integration.ArrClient
	db         *sql.DB

	// Graceful shutdown support
	shutdownCh chan struct{}
	wg         sync.WaitGroup
}

func NewVerifierService(eb *eventbus.EventBus, detector integration.HealthChecker, pm integration.PathMapper, arrClient integration.ArrClient, db *sql.DB) *VerifierService {
	return &VerifierService{
		eventBus:   eb,
		detector:   detector,
		pathMapper: pm,
		arrClient:  arrClient,
		db:         db,
		shutdownCh: make(chan struct{}),
	}
}

func (v *VerifierService) Start() {
	v.eventBus.Subscribe(domain.SearchCompleted, v.handleSearchCompleted)
}

// Shutdown gracefully stops all verification goroutines
func (v *VerifierService) Shutdown() {
	logger.Infof("Verifier: initiating graceful shutdown...")
	close(v.shutdownCh)
	v.wg.Wait()
	logger.Infof("Verifier: shutdown complete")
}

// isShuttingDown returns true if shutdown has been initiated
func (v *VerifierService) isShuttingDown() bool {
	select {
	case <-v.shutdownCh:
		return true
	default:
		return false
	}
}

func (v *VerifierService) handleSearchCompleted(event domain.Event) {
	corruptionID := event.AggregateID

	// Use type-safe event data parsing
	data, ok := event.ParseSearchCompletedEventData()
	if !ok {
		logger.Errorf("Missing file_path in SearchCompleted event for %s", corruptionID)
		return
	}

	filePath := data.FilePath
	mediaID := data.MediaID
	metadata := data.Metadata
	pathID := data.PathID

	// If media_id is missing, fall back to simple polling
	if mediaID == 0 {
		logger.Warnf("Missing media_id in SearchCompleted event for %s, falling back to file polling", corruptionID)
		v.wg.Add(1)
		go func() {
			defer v.wg.Done()
			v.pollForFileWithBackoff(corruptionID, filePath, 0, nil, 0)
		}()
		return
	}

	// Get arr_path for queue monitoring
	arrPath, err := v.pathMapper.ToArrPath(filePath)
	if err != nil {
		logger.Infof("[WARN] Could not map path %s to arr path: %v, falling back to file polling", filePath, err)
		v.wg.Add(1)
		go func() {
			defer v.wg.Done()
			v.pollForFileWithBackoff(corruptionID, filePath, mediaID, metadata, pathID)
		}()
		return
	}

	// Start queue-aware verification
	v.wg.Add(1)
	go func() {
		defer v.wg.Done()
		v.monitorDownloadProgress(corruptionID, filePath, arrPath, mediaID, metadata, pathID)
	}()
}

// monitorDownloadProgress uses the *arr queue and history APIs to track download progress
func (v *VerifierService) monitorDownloadProgress(corruptionID, filePath, arrPath string, mediaID int64, metadata map[string]interface{}, pathID int64) {
	cfg := config.Get()

	// Configuration
	pollInterval := cfg.VerificationInterval // Default 30s for queue checks
	timeout := v.getVerificationTimeout(pathID)

	startTime := time.Now()
	attempt := 0
	lastStatus := ""
	lastProgress := float64(0)
	wasInQueue := false // Track if we've seen item in queue before

	logger.Infof("Starting download monitoring for corruption %s (media ID: %d)", corruptionID, mediaID)

	for {
		// Check for shutdown
		if v.isShuttingDown() {
			logger.Infof("Verifier: stopping download monitoring for %s due to shutdown", corruptionID)
			return
		}

		// Check timeout
		elapsed := time.Since(startTime)
		if elapsed > timeout {
			logger.Infof("[WARN] Verification timeout for %s after %s", corruptionID, elapsed)
			if err := v.eventBus.Publish(domain.Event{
				AggregateID:   corruptionID,
				AggregateType: "corruption",
				EventType:     domain.DownloadTimeout,
				EventData: map[string]interface{}{
					"elapsed":     elapsed.String(),
					"attempts":    attempt,
					"last_status": lastStatus,
				},
			}); err != nil {
				logger.Errorf("Failed to publish DownloadTimeout event: %v", err)
			}
			return
		}

		attempt++

		// Step 1: Check queue for active download
		queueItems, err := v.arrClient.FindQueueItemsByMediaIDForPath(arrPath, mediaID)
		if err != nil {
			logger.Debugf("Queue check error for %s: %v", corruptionID, err)
		}

		if len(queueItems) > 0 {
			// Found in queue - track progress
			wasInQueue = true
			item := queueItems[0] // Use first matching item
			currentStatus := item.TrackedDownloadState

			// Check for failure states
			if item.TrackedDownloadState == "failed" || item.TrackedDownloadState == "failedPending" {
				logger.Infof("[WARN] Download failed for %s: %s", corruptionID, item.ErrorMessage)
				if err := v.eventBus.Publish(domain.Event{
					AggregateID:   corruptionID,
					AggregateType: "corruption",
					EventType:     domain.DownloadFailed,
					EventData: map[string]interface{}{
						"error":       item.ErrorMessage,
						"status":      item.TrackedDownloadStatus,
						"queue_id":    item.ID,
						"download_id": item.DownloadID,
					},
				}); err != nil {
					logger.Errorf("Failed to publish DownloadFailed event: %v", err)
				}
				return
			}

			// Check for importBlocked state - this requires manual intervention in *arr
			// Common causes: file already exists, disk full, permissions, corrupt download
			if item.TrackedDownloadState == "importBlocked" {
				errMsg := item.ErrorMessage
				if len(item.StatusMessages) > 0 {
					errMsg = strings.Join(item.StatusMessages, "; ")
				}

				logger.Warnf("Import blocked for %s: %s - requires manual intervention in *arr", corruptionID, errMsg)
				if err := v.eventBus.Publish(domain.Event{
					AggregateID:   corruptionID,
					AggregateType: "corruption",
					EventType:     domain.ImportBlocked,
					EventData: map[string]interface{}{
						"error":            errMsg,
						"status":           item.TrackedDownloadStatus,
						"state":            item.TrackedDownloadState,
						"queue_id":         item.ID,
						"download_id":      item.DownloadID,
						"title":            item.Title,
						"status_messages":  item.StatusMessages,
						"requires_manual":  true,
					},
				}); err != nil {
					logger.Errorf("Failed to publish ImportBlocked event: %v", err)
				}
				// Continue monitoring - user might fix the issue in *arr
			}

			// Check for warning/error status (stalled downloads, import issues, etc.)
			// These indicate problems that may not resolve on their own
			if item.TrackedDownloadStatus == "warning" || item.TrackedDownloadStatus == "error" {
				// Build error message from status messages
				errMsg := item.ErrorMessage
				if len(item.StatusMessages) > 0 {
					errMsg = strings.Join(item.StatusMessages, "; ")
				}

				// Log warning but continue monitoring - stalled downloads may recover
				logger.Infof("[WARN] Download has issues for %s: status=%s, state=%s, message=%s",
					corruptionID, item.TrackedDownloadStatus, item.TrackedDownloadState, errMsg)

				// Include warning info in progress status
				currentStatus = item.TrackedDownloadStatus + ":" + item.TrackedDownloadState
			}

			// Log progress changes
			if currentStatus != lastStatus || int(item.Progress) != int(lastProgress) {
				logger.Infof("Download progress for %s: %s (%.1f%%) - %s",
					corruptionID, currentStatus, item.Progress, item.TimeLeft)
				lastStatus = currentStatus
				lastProgress = item.Progress

				// Emit progress event for UI updates
				eventData := map[string]interface{}{
					"status":      currentStatus,
					"progress":    item.Progress,
					"time_left":   item.TimeLeft,
					"download_id": item.DownloadID,
					"title":       item.Title,
				}

				// Add warning info if present
				if item.TrackedDownloadStatus == "warning" || item.TrackedDownloadStatus == "error" {
					eventData["warning"] = true
					if len(item.StatusMessages) > 0 {
						eventData["warning_message"] = strings.Join(item.StatusMessages, "; ")
					} else if item.ErrorMessage != "" {
						eventData["warning_message"] = item.ErrorMessage
					}
				}

				if err := v.eventBus.Publish(domain.Event{
					AggregateID:   corruptionID,
					AggregateType: "corruption",
					EventType:     domain.DownloadProgress,
					EventData:     eventData,
				}); err != nil {
					logger.Debugf("Failed to publish DownloadProgress event: %v", err)
				}
			}

			// If import is pending or completed in queue, check history
			if item.TrackedDownloadState == "importPending" || item.TrackedDownloadState == "imported" {
				// Check history to see if import completed
				if v.checkHistoryForImport(corruptionID, arrPath, mediaID, filePath, metadata) {
					return // Import found and handled
				}
			}

			// Use shorter interval during active download
			time.Sleep(pollInterval)
			continue
		}

		// Step 2: Not in queue - check history for completed import
		if v.checkHistoryForImport(corruptionID, arrPath, mediaID, filePath, metadata) {
			return // Import found and handled
		}

		// Step 2.5: If item WAS in queue but now gone and not in history, it was manually removed
		if wasInQueue {
			logger.Warnf("Item for %s was in queue but is now gone without import history - manually removed from *arr", corruptionID)
			if err := v.eventBus.Publish(domain.Event{
				AggregateID:   corruptionID,
				AggregateType: "corruption",
				EventType:     domain.ManuallyRemoved,
				EventData: map[string]interface{}{
					"message":         "Download was manually removed from *arr queue without being imported",
					"requires_manual": true,
					"last_status":     lastStatus,
				},
			}); err != nil {
				logger.Errorf("Failed to publish ManuallyRemoved event: %v", err)
			}
			return // Stop monitoring - user needs to manually handle this
		}

		// Step 3: Fallback - check if file(s) exist via *arr API
		// Use GetAllFilePaths to handle multi-episode files that may be replaced with individual files
		if v.arrClient != nil {
			allPaths, err := v.arrClient.GetAllFilePaths(mediaID, metadata, filePath)
			if err == nil && len(allPaths) > 0 {
				// Verify all files exist on disk
				var existingPaths []string
				for _, p := range allPaths {
					localPath, mapErr := v.pathMapper.ToLocalPath(p)
					if mapErr != nil {
						localPath = p
					}
					if _, statErr := os.Stat(localPath); statErr == nil {
						existingPaths = append(existingPaths, localPath)
					}
				}

				if len(existingPaths) == len(allPaths) {
					// All files exist on disk
					if len(existingPaths) == 1 {
						logger.Infof("File detected for %s via *arr API: %s", corruptionID, existingPaths[0])
					} else {
						logger.Infof("Multi-episode files detected for %s via *arr API: %d files", corruptionID, len(existingPaths))
					}
					v.emitFilesDetected(corruptionID, existingPaths)
					return
				}
			}
		}

		// Use exponential backoff when not actively downloading
		backoff := calculateBackoffInterval(attempt, pollInterval, 10*time.Minute)
		if attempt%10 == 0 {
			logger.Debugf("Verification poll #%d for %s, no queue activity, next check in %s", attempt, corruptionID, backoff)
		}
		time.Sleep(backoff)
	}
}

// checkHistoryForImport checks *arr history for import completion
func (v *VerifierService) checkHistoryForImport(corruptionID, arrPath string, mediaID int64, referencePath string, metadata map[string]interface{}) bool {
	historyItems, err := v.arrClient.GetRecentHistoryForMediaByPath(arrPath, mediaID, 20)
	if err != nil {
		logger.Debugf("History check error for %s: %v", corruptionID, err)
		return false
	}

	// Look for recent import events
	hasImportEvent := false
	for _, item := range historyItems {
		if item.EventType == "downloadFolderImported" || item.EventType == "episodeFileImported" || item.EventType == "movieFileImported" {
			hasImportEvent = true
			break
		}
	}

	if !hasImportEvent {
		return false
	}

	// Import event found - use GetAllFilePaths to get all associated files
	// This handles multi-episode replacements where one file becomes multiple
	if v.arrClient != nil {
		allPaths, err := v.arrClient.GetAllFilePaths(mediaID, metadata, referencePath)
		if err == nil && len(allPaths) > 0 {
			// Convert all paths to local and verify they exist
			var existingPaths []string
			for _, p := range allPaths {
				localPath, mapErr := v.pathMapper.ToLocalPath(p)
				if mapErr != nil {
					localPath = p
				}
				if _, statErr := os.Stat(localPath); statErr == nil {
					existingPaths = append(existingPaths, localPath)
				}
			}

			if len(existingPaths) == len(allPaths) {
				// All files exist
				if len(existingPaths) == 1 {
					logger.Infof("Import detected for %s via history: %s", corruptionID, existingPaths[0])
				} else {
					logger.Infof("Multi-episode import detected for %s via history: %d files", corruptionID, len(existingPaths))
				}
				v.emitFilesDetected(corruptionID, existingPaths)
				return true
			}
		}
	}

	return false
}

// pollForFileWithBackoff is the fallback method when *arr tracking isn't available
func (v *VerifierService) pollForFileWithBackoff(corruptionID string, referencePath string, mediaID int64, metadata map[string]interface{}, pathID int64) {
	cfg := config.Get()

	initialInterval := cfg.VerificationInterval
	maxInterval := 24 * time.Hour
	timeout := v.getVerificationTimeout(pathID)

	useSmartVerification := mediaID > 0

	startTime := time.Now()
	attempt := 0

	for {
		// Check for shutdown
		if v.isShuttingDown() {
			logger.Infof("Verifier: stopping file polling for %s due to shutdown", corruptionID)
			return
		}

		if time.Since(startTime) > timeout {
			logger.Infof("Verification timeout for %s after %d attempts", corruptionID, attempt)
			if err := v.eventBus.Publish(domain.Event{
				AggregateID:   corruptionID,
				AggregateType: "corruption",
				EventType:     domain.DownloadTimeout,
				EventData: map[string]interface{}{
					"elapsed":  time.Since(startTime).String(),
					"attempts": attempt,
				},
			}); err != nil {
				logger.Errorf("Failed to publish DownloadTimeout event: %v", err)
			}
			return
		}

		currentInterval := calculateBackoffInterval(attempt, initialInterval, maxInterval)

		if attempt > 0 && (attempt%10 == 0 || currentInterval >= time.Hour) {
			logger.Debugf("Verification poll #%d for %s, next check in %s", attempt, corruptionID, currentInterval)
		}

		time.Sleep(currentInterval)
		attempt++

		var foundPaths []string

		if useSmartVerification && v.arrClient != nil {
			// Use GetAllFilePaths to handle multi-episode replacements
			allPaths, err := v.arrClient.GetAllFilePaths(mediaID, metadata, referencePath)
			if err == nil && len(allPaths) > 0 {
				for _, p := range allPaths {
					localPath, mapErr := v.pathMapper.ToLocalPath(p)
					if mapErr != nil {
						localPath = p
					}
					if _, statErr := os.Stat(localPath); statErr == nil {
						foundPaths = append(foundPaths, localPath)
					}
				}
				// Only consider found if ALL files exist
				if len(foundPaths) != len(allPaths) {
					foundPaths = nil
				}
			}
		} else {
			if _, err := os.Stat(referencePath); err == nil {
				foundPaths = []string{referencePath}
			}
		}

		if len(foundPaths) > 0 {
			if len(foundPaths) == 1 {
				logger.Infof("File detected for %s after %d attempts: %s", corruptionID, attempt, foundPaths[0])
			} else {
				logger.Infof("Multi-episode files detected for %s after %d attempts: %d files", corruptionID, attempt, len(foundPaths))
			}
			v.emitFilesDetected(corruptionID, foundPaths)
			return
		}
	}
}

// calculateBackoffInterval returns the next poll interval using exponential backoff
func calculateBackoffInterval(attempt int, initialInterval, maxInterval time.Duration) time.Duration {
	backoff := float64(initialInterval) * math.Pow(2, float64(attempt))
	// Check for overflow (Inf or negative after conversion) or exceeding max
	if math.IsInf(backoff, 1) || backoff < 0 || time.Duration(backoff) > maxInterval || time.Duration(backoff) < 0 {
		return maxInterval
	}
	return time.Duration(backoff)
}

// getVerificationTimeout returns the timeout for a given path_id
func (v *VerifierService) getVerificationTimeout(pathID int64) time.Duration {
	cfg := config.Get()
	defaultTimeout := cfg.VerificationTimeout

	if pathID == 0 || v.db == nil {
		return defaultTimeout
	}

	var timeoutHours sql.NullInt64
	err := v.db.QueryRow("SELECT verification_timeout_hours FROM scan_paths WHERE id = ?", pathID).Scan(&timeoutHours)
	if err != nil || !timeoutHours.Valid {
		return defaultTimeout
	}

	return time.Duration(timeoutHours.Int64) * time.Hour
}

// emitFilesDetected handles verification of one or more files (for multi-episode replacements)
func (v *VerifierService) emitFilesDetected(corruptionID string, filePaths []string) {
	if len(filePaths) == 0 {
		return
	}

	// If single file, use simple path in event
	if len(filePaths) == 1 {
		if err := v.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.FileDetected,
			EventData:     map[string]interface{}{"file_path": filePaths[0]},
		}); err != nil {
			logger.Errorf("Failed to publish FileDetected event: %v", err)
		}
	} else {
		// Multiple files (multi-episode replacement with individual episodes)
		if err := v.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.FileDetected,
			EventData: map[string]interface{}{
				"file_path":  filePaths[0], // Primary path for compatibility
				"file_paths": filePaths,    // All paths
				"file_count": len(filePaths),
			},
		}); err != nil {
			logger.Errorf("Failed to publish FileDetected event: %v", err)
		}
		logger.Infof("Multi-episode replacement detected for %s: %d files to verify", corruptionID, len(filePaths))
	}

	v.verifyHealthMultiple(corruptionID, filePaths)
}

// verifyHealthMultiple verifies the health of one or more files.
// All files must be healthy for verification to succeed.
func (v *VerifierService) verifyHealthMultiple(corruptionID string, filePaths []string) {
	if err := v.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.VerificationStarted,
	}); err != nil {
		logger.Errorf("Failed to publish VerificationStarted event: %v", err)
	}

	// Verify all files - all must be healthy for success
	var failedPaths []string
	var lastError string

	for _, filePath := range filePaths {
		healthy, err := v.detector.Check(filePath, "thorough")
		if !healthy {
			failedPaths = append(failedPaths, filePath)
			if err != nil {
				lastError = err.Message
			} else {
				lastError = "unknown error"
			}
			logger.Infof("Verification failed for %s: %s", filePath, lastError)
		}
	}

	if len(failedPaths) == 0 {
		// All files healthy
		if err := v.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.VerificationSuccess,
			EventData: map[string]interface{}{
				"verified_count": len(filePaths),
			},
		}); err != nil {
			logger.Errorf("Failed to publish VerificationSuccess event: %v", err)
		}
	} else {
		// At least one file failed verification
		if err := v.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.VerificationFailed,
			EventData: map[string]interface{}{
				"error":        lastError,
				"failed_paths": failedPaths,
				"failed_count": len(failedPaths),
				"total_count":  len(filePaths),
			},
		}); err != nil {
			logger.Errorf("Failed to publish VerificationFailed event: %v", err)
		}
	}
}
