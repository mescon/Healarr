package services

import (
	"context"
	"database/sql"
	"fmt"
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

// verifierQueryTimeout is the maximum time for database queries in verifier service.
const verifierQueryTimeout = 10 * time.Second

// logMsgDownloadMonitorShutdown is the log message format for shutdown during download monitoring.
const logMsgDownloadMonitorShutdown = "Verifier: stopping download monitoring for %s due to shutdown"

// VerificationMeta stores quality/release info captured from history for VerificationSuccess events
type VerificationMeta struct {
	Quality        string
	ReleaseGroup   string
	Indexer        string
	DownloadClient string
	NewFilePath    string // Primary file path (for single files)
	NewFilePaths   []string
	NewFileSize    int64
}

type VerifierService struct {
	eventBus   *eventbus.EventBus
	detector   integration.HealthChecker
	pathMapper integration.PathMapper
	arrClient  integration.ArrClient
	db         *sql.DB

	// Graceful shutdown support
	shutdownCh chan struct{}
	wg         sync.WaitGroup

	// State tracking for event deduplication
	lastStateMu sync.RWMutex
	lastState   map[string]string // corruptionID -> last known state

	// Verification metadata - stores quality/release info from history for enriching VerificationSuccess
	verifyMetaMu sync.RWMutex
	verifyMeta   map[string]*VerificationMeta // corruptionID -> metadata
}

func NewVerifierService(eb *eventbus.EventBus, detector integration.HealthChecker, pm integration.PathMapper, arrClient integration.ArrClient, db *sql.DB) *VerifierService {
	return &VerifierService{
		eventBus:   eb,
		detector:   detector,
		pathMapper: pm,
		arrClient:  arrClient,
		db:         db,
		shutdownCh: make(chan struct{}),
		lastState:  make(map[string]string),
		verifyMeta: make(map[string]*VerificationMeta),
	}
}

// setLastState updates the last known state for a corruption (thread-safe)
func (v *VerifierService) setLastState(corruptionID, state string) {
	v.lastStateMu.Lock()
	v.lastState[corruptionID] = state
	v.lastStateMu.Unlock()
}

// getLastState returns the last known state for a corruption (thread-safe)
func (v *VerifierService) getLastState(corruptionID string) string {
	v.lastStateMu.RLock()
	defer v.lastStateMu.RUnlock()
	return v.lastState[corruptionID]
}

// clearLastState removes state tracking for a corruption (call on completion)
func (v *VerifierService) clearLastState(corruptionID string) {
	v.lastStateMu.Lock()
	delete(v.lastState, corruptionID)
	v.lastStateMu.Unlock()
}

// setVerifyMeta stores verification metadata for enriching VerificationSuccess
func (v *VerifierService) setVerifyMeta(corruptionID string, meta *VerificationMeta) {
	v.verifyMetaMu.Lock()
	v.verifyMeta[corruptionID] = meta
	v.verifyMetaMu.Unlock()
}

// getVerifyMeta retrieves verification metadata (returns nil if not set)
func (v *VerifierService) getVerifyMeta(corruptionID string) *VerificationMeta {
	v.verifyMetaMu.RLock()
	defer v.verifyMetaMu.RUnlock()
	return v.verifyMeta[corruptionID]
}

// clearVerifyMeta removes verification metadata (call on completion)
func (v *VerifierService) clearVerifyMeta(corruptionID string) {
	v.verifyMetaMu.Lock()
	delete(v.verifyMeta, corruptionID)
	v.verifyMetaMu.Unlock()
}

// queueAction represents the result of processing a queue item.
type queueAction int

const (
	queueActionContinue queueAction = iota // Continue monitoring
	queueActionStop                        // Stop monitoring (terminal state)
)

// handleQueueItemFailed handles failed/failedPending download states.
func (v *VerifierService) handleQueueItemFailed(corruptionID string, item integration.QueueItemInfo) queueAction {
	if item.TrackedDownloadState != "failed" && item.TrackedDownloadState != "failedPending" {
		return queueActionContinue
	}

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
	return queueActionStop
}

// handleQueueItemBlocked handles importBlocked state.
func (v *VerifierService) handleQueueItemBlocked(corruptionID, filePath string, item integration.QueueItemInfo) {
	if item.TrackedDownloadState != "importBlocked" {
		// Clear tracked state if we transitioned FROM importBlocked
		if v.getLastState(corruptionID) == "importBlocked" {
			v.clearLastState(corruptionID)
		}
		return
	}

	// Only emit event on state change to prevent spam
	if v.getLastState(corruptionID) == "importBlocked" {
		return
	}

	v.setLastState(corruptionID, "importBlocked")

	errMsg := item.ErrorMessage
	if len(item.StatusMessages) > 0 {
		errMsg = strings.Join(item.StatusMessages, "; ")
	}

	logger.Warnf("Import blocked for %s (%s): %s - requires manual intervention in *arr", item.Title, filePath, errMsg)
	if err := v.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.ImportBlocked,
		EventData: map[string]interface{}{
			"error":           errMsg,
			"status":          item.TrackedDownloadStatus,
			"state":           item.TrackedDownloadState,
			"queue_id":        item.ID,
			"download_id":     item.DownloadID,
			"title":           item.Title,
			"status_messages": item.StatusMessages,
			"requires_manual": true,
		},
	}); err != nil {
		logger.Errorf("Failed to publish ImportBlocked event: %v", err)
	}
}

// handleQueueItemIgnored handles user-ignored downloads.
func (v *VerifierService) handleQueueItemIgnored(corruptionID string, item integration.QueueItemInfo) queueAction {
	if item.TrackedDownloadState != "ignored" {
		return queueActionContinue
	}

	logger.Warnf("Download ignored by user in *arr for %s: %s", corruptionID, item.Title)
	if err := v.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DownloadIgnored,
		EventData: map[string]interface{}{
			"reason":          "User ignored download in *arr",
			"queue_id":        item.ID,
			"download_id":     item.DownloadID,
			"title":           item.Title,
			"requires_manual": true,
		},
	}); err != nil {
		logger.Errorf("Failed to publish DownloadIgnored event: %v", err)
	}
	return queueActionStop
}

// getQueueItemStatus returns the current status string and any warning message.
func getQueueItemStatus(item integration.QueueItemInfo) (string, string) {
	currentStatus := item.TrackedDownloadState

	if item.TrackedDownloadStatus != "warning" && item.TrackedDownloadStatus != "error" {
		return currentStatus, ""
	}

	errMsg := item.ErrorMessage
	if len(item.StatusMessages) > 0 {
		errMsg = strings.Join(item.StatusMessages, "; ")
	}

	return item.TrackedDownloadStatus + ":" + item.TrackedDownloadState, errMsg
}

// buildProgressEventData creates the event data map for a DownloadProgress event.
func buildProgressEventData(item integration.QueueItemInfo, currentStatus, warningMsg string) map[string]interface{} {
	eventData := map[string]interface{}{
		"status":               currentStatus,
		"progress":             item.Progress,
		"time_left":            item.TimeLeft,
		"download_id":          item.DownloadID,
		"title":                item.Title,
		"protocol":             item.Protocol,
		"download_client":      item.DownloadClient,
		"indexer":              item.Indexer,
		"size_bytes":           item.Size,
		"size_remaining_bytes": item.SizeLeft,
		"estimated_completion": item.EstimatedCompletion,
		"added_at":             item.AddedAt,
	}

	if warningMsg != "" {
		eventData["warning"] = true
		eventData["warning_message"] = warningMsg
	}

	return eventData
}

// enrichVerificationEventData adds metadata fields to the event data map.
func enrichVerificationEventData(eventData map[string]interface{}, meta *VerificationMeta) {
	if meta == nil {
		return
	}
	if meta.NewFilePath != "" {
		eventData["new_file_path"] = meta.NewFilePath
	}
	if meta.NewFileSize > 0 {
		eventData["new_file_size"] = meta.NewFileSize
	}
	if meta.Quality != "" {
		eventData["quality"] = meta.Quality
	}
	if meta.ReleaseGroup != "" {
		eventData["release_group"] = meta.ReleaseGroup
	}
	if meta.Indexer != "" {
		eventData["indexer"] = meta.Indexer
	}
	if meta.DownloadClient != "" {
		eventData["download_client"] = meta.DownloadClient
	}
}

// convertAndVerifyPaths converts arr paths to local paths and returns only those that exist.
func (v *VerifierService) convertAndVerifyPaths(arrPaths []string) []string {
	var existingPaths []string
	for _, p := range arrPaths {
		localPath, mapErr := v.pathMapper.ToLocalPath(p)
		if mapErr != nil {
			localPath = p
		}
		if _, statErr := os.Stat(localPath); statErr == nil {
			existingPaths = append(existingPaths, localPath)
		}
	}
	return existingPaths
}

// waitWithShutdown performs an interruptible sleep that returns true if shutdown was requested.
func (v *VerifierService) waitWithShutdown(d time.Duration) bool {
	select {
	case <-v.shutdownCh:
		return true
	case <-time.After(d):
		return false
	}
}

// storeImportMetadata stores quality/release info from a history import item.
func (v *VerifierService) storeImportMetadata(corruptionID string, existingPaths []string, importItem *integration.HistoryItemInfo) {
	meta := &VerificationMeta{
		Quality:        importItem.Quality,
		ReleaseGroup:   importItem.ReleaseGroup,
		Indexer:        importItem.Indexer,
		DownloadClient: importItem.DownloadClient,
		NewFilePaths:   existingPaths,
	}
	if len(existingPaths) == 1 {
		meta.NewFilePath = existingPaths[0]
		if fi, err := os.Stat(existingPaths[0]); err == nil {
			meta.NewFileSize = fi.Size()
		}
		logger.Infof("Import detected for %s via history: %s", corruptionID, existingPaths[0])
	} else {
		logger.Infof("Multi-episode import detected for %s via history: %d files", corruptionID, len(existingPaths))
	}
	v.setVerifyMeta(corruptionID, meta)
}

// publishDownloadTimeout publishes a timeout event for the given corruption.
func (v *VerifierService) publishDownloadTimeout(corruptionID string, elapsed time.Duration, attempt int, lastStatus string) {
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
}

// publishManuallyRemoved publishes an event when an item is removed from queue without import.
func (v *VerifierService) publishManuallyRemoved(corruptionID, lastStatus string) {
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
}

// getDurationMetrics calculates how long the remediation took.
// Returns: (total_duration_seconds, download_duration_seconds)
// total_duration = CorruptionDetected → now
// download_duration = first DownloadProgress → now
func (v *VerifierService) getDurationMetrics(corruptionID string) (int64, int64) {
	now := time.Now()

	ctx, cancel := context.WithTimeout(context.Background(), verifierQueryTimeout)
	defer cancel()

	// Get CorruptionDetected timestamp
	var corruptionTime time.Time
	err := v.db.QueryRowContext(ctx, `
		SELECT created_at FROM events
		WHERE aggregate_id = ? AND event_type = 'CorruptionDetected'
		ORDER BY created_at ASC LIMIT 1
	`, corruptionID).Scan(&corruptionTime)
	if err != nil {
		return 0, 0
	}

	totalDuration := int64(now.Sub(corruptionTime).Seconds())

	// Get first DownloadProgress timestamp (if any)
	var downloadStartTime time.Time
	err = v.db.QueryRowContext(ctx, `
		SELECT created_at FROM events
		WHERE aggregate_id = ? AND event_type = 'DownloadProgress'
		ORDER BY created_at ASC LIMIT 1
	`, corruptionID).Scan(&downloadStartTime)
	if err != nil {
		// No DownloadProgress event - maybe it completed very quickly
		return totalDuration, 0
	}

	downloadDuration := int64(now.Sub(downloadStartTime).Seconds())
	return totalDuration, downloadDuration
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

// monitorState tracks the state during download monitoring
type monitorState struct {
	corruptionID string
	filePath     string
	arrPath      string
	mediaID      int64
	metadata     map[string]interface{}
	pollInterval time.Duration
	timeout      time.Duration
	startTime    time.Time
	attempt      int
	lastStatus   string
	lastProgress float64
	wasInQueue   bool
}

// monitorAction represents actions from monitoring steps
type monitorAction int

const (
	monitorContinue monitorAction = iota // Continue monitoring loop
	monitorStop                          // Stop monitoring
)

// handleQueueItem processes a single queue item and returns the appropriate action
func (v *VerifierService) handleQueueItem(state *monitorState, item integration.QueueItemInfo) monitorAction {
	state.wasInQueue = true

	// Handle terminal states
	if v.handleQueueItemFailed(state.corruptionID, item) == queueActionStop {
		return monitorStop
	}
	if v.handleQueueItemIgnored(state.corruptionID, item) == queueActionStop {
		return monitorStop
	}

	// Handle importBlocked state
	v.handleQueueItemBlocked(state.corruptionID, state.filePath, item)

	// Log and emit progress changes
	currentStatus, warningMsg := getQueueItemStatus(item)
	if warningMsg != "" {
		logger.Infof("[WARN] Download has issues for %s: status=%s, state=%s, message=%s",
			state.corruptionID, item.TrackedDownloadStatus, item.TrackedDownloadState, warningMsg)
	}

	if currentStatus != state.lastStatus || int(item.Progress) != int(state.lastProgress) {
		logger.Infof("Download progress for %s: %s (%.1f%%) - %s",
			state.corruptionID, currentStatus, item.Progress, item.TimeLeft)
		state.lastStatus = currentStatus
		state.lastProgress = item.Progress

		eventData := buildProgressEventData(item, currentStatus, warningMsg)
		_ = v.eventBus.Publish(domain.Event{
			AggregateID:   state.corruptionID,
			AggregateType: "corruption",
			EventType:     domain.DownloadProgress,
			EventData:     eventData,
		})
	}

	// Check history if import is in progress/completed
	if isImportState(item.TrackedDownloadState) {
		if v.checkHistoryForImport(state.corruptionID, state.arrPath, state.mediaID, state.filePath, state.metadata) {
			return monitorStop
		}
	}

	return monitorContinue
}

// isImportState checks if the download state indicates import activity
func isImportState(state string) bool {
	return state == "importPending" || state == "importing" || state == "imported"
}

// handleNoQueueItems handles the case when no items are in the download queue
func (v *VerifierService) handleNoQueueItems(state *monitorState, elapsed time.Duration) monitorAction {
	// Check history for completed import
	if v.checkHistoryForImport(state.corruptionID, state.arrPath, state.mediaID, state.filePath, state.metadata) {
		return monitorStop
	}

	// If item was in queue but now gone, it was manually removed
	if state.wasInQueue {
		v.publishManuallyRemoved(state.corruptionID, state.lastStatus)
		return monitorStop
	}

	// Fallback - check if files exist via *arr API
	if v.checkAndEmitFilesFromArrAPI(state.corruptionID, state.filePath, state.mediaID, state.metadata, elapsed, state.timeout) {
		return monitorStop
	}

	return monitorContinue
}

// monitorDownloadProgress uses the *arr queue and history APIs to track download progress
func (v *VerifierService) monitorDownloadProgress(corruptionID, filePath, arrPath string, mediaID int64, metadata map[string]interface{}, pathID int64) {
	defer v.clearLastState(corruptionID)

	cfg := config.Get()
	state := &monitorState{
		corruptionID: corruptionID,
		filePath:     filePath,
		arrPath:      arrPath,
		mediaID:      mediaID,
		metadata:     metadata,
		pollInterval: cfg.VerificationInterval,
		timeout:      v.getVerificationTimeout(pathID),
		startTime:    time.Now(),
	}

	logger.Infof("Starting download monitoring for corruption %s (media ID: %d)", corruptionID, mediaID)

	for {
		action := v.executeMonitorIteration(state)
		if action == monitorStop {
			return
		}
	}
}

// executeMonitorIteration performs a single iteration of download monitoring.
// Returns monitorStop if monitoring should end, monitorContinue to keep polling.
func (v *VerifierService) executeMonitorIteration(state *monitorState) monitorAction {
	if v.isShuttingDown() {
		logger.Infof(logMsgDownloadMonitorShutdown, state.corruptionID)
		return monitorStop
	}

	elapsed := time.Since(state.startTime)
	if elapsed > state.timeout {
		v.publishDownloadTimeout(state.corruptionID, elapsed, state.attempt, state.lastStatus)
		return monitorStop
	}

	state.attempt++

	// Check queue for active download
	queueItems, err := v.arrClient.FindQueueItemsByMediaIDForPath(state.arrPath, state.mediaID)
	if err != nil {
		logger.Debugf("Queue check error for %s: %v", state.corruptionID, err)
	}

	if len(queueItems) > 0 {
		return v.handleActiveDownload(state, queueItems[0])
	}

	return v.handleInactiveDownload(state, elapsed)
}

// handleActiveDownload processes the case where a download is in the queue.
func (v *VerifierService) handleActiveDownload(state *monitorState, queueItem integration.QueueItemInfo) monitorAction {
	if v.handleQueueItem(state, queueItem) == monitorStop {
		return monitorStop
	}
	if v.waitWithShutdown(state.pollInterval) {
		logger.Infof(logMsgDownloadMonitorShutdown, state.corruptionID)
		return monitorStop
	}
	return monitorContinue
}

// handleInactiveDownload processes the case where no download is in the queue.
func (v *VerifierService) handleInactiveDownload(state *monitorState, elapsed time.Duration) monitorAction {
	if v.handleNoQueueItems(state, elapsed) == monitorStop {
		return monitorStop
	}

	// Exponential backoff when not actively downloading
	backoff := calculateBackoffInterval(state.attempt, state.pollInterval, 10*time.Minute)
	if state.attempt%10 == 0 {
		logger.Debugf("Verification poll #%d for %s, no queue activity, next check in %s", state.attempt, state.corruptionID, backoff)
	}
	if v.waitWithShutdown(backoff) {
		logger.Infof(logMsgDownloadMonitorShutdown, state.corruptionID)
		return monitorStop
	}
	return monitorContinue
}

// checkAndEmitFilesFromArrAPI checks if files exist via *arr API and emits appropriate events.
// Returns true if files were found and handled, false otherwise.
func (v *VerifierService) checkAndEmitFilesFromArrAPI(corruptionID, filePath string, mediaID int64, metadata map[string]interface{}, elapsed, timeout time.Duration) bool {
	if v.arrClient == nil {
		return false
	}

	allPaths, err := v.arrClient.GetAllFilePaths(mediaID, metadata, filePath)
	if err != nil || len(allPaths) == 0 {
		return false
	}

	existingPaths := v.convertAndVerifyPaths(allPaths)
	if len(existingPaths) == len(allPaths) {
		// All files exist on disk
		v.logFileDetection(corruptionID, existingPaths)
		v.emitFilesDetected(corruptionID, existingPaths)
		return true
	}

	// Partial replacement detection
	if len(existingPaths) > 0 && elapsed > timeout/2 {
		logger.Warnf("Partial replacement detected for %s: %d of %d files exist after %s",
			corruptionID, len(existingPaths), len(allPaths), elapsed)
		v.emitPartialReplacement(corruptionID, existingPaths, len(allPaths))
		return true
	}

	return false
}

// logFileDetection logs the appropriate message for detected files.
func (v *VerifierService) logFileDetection(corruptionID string, paths []string) {
	if len(paths) == 1 {
		logger.Infof("File detected for %s via *arr API: %s", corruptionID, paths[0])
	} else {
		logger.Infof("Multi-episode files detected for %s via *arr API: %d files", corruptionID, len(paths))
	}
}

// findImportEvent searches history items for import completion events.
func findImportEvent(historyItems []integration.HistoryItemInfo) *integration.HistoryItemInfo {
	importTypes := map[string]bool{
		"downloadFolderImported": true,
		"episodeFileImported":    true,
		"movieFileImported":      true,
	}
	for i, item := range historyItems {
		if importTypes[item.EventType] {
			return &historyItems[i]
		}
	}
	return nil
}

// checkHistoryForImport checks *arr history for import completion
func (v *VerifierService) checkHistoryForImport(corruptionID, arrPath string, mediaID int64, referencePath string, metadata map[string]interface{}) bool {
	historyItems, err := v.getHistoryWithRetry(arrPath, mediaID, 20, 3)
	if err != nil {
		logger.Debugf("History check error for %s after retries: %v", corruptionID, err)
		return false
	}

	importItem := findImportEvent(historyItems)
	if importItem == nil || v.arrClient == nil {
		return false
	}

	allPaths, err := v.arrClient.GetAllFilePaths(mediaID, metadata, referencePath)
	if err != nil || len(allPaths) == 0 {
		return false
	}

	existingPaths := v.convertAndVerifyPaths(allPaths)
	return v.handleImportPaths(corruptionID, existingPaths, allPaths, importItem)
}

// handleImportPaths processes import paths and emits appropriate events.
func (v *VerifierService) handleImportPaths(corruptionID string, existingPaths, allPaths []string, importItem *integration.HistoryItemInfo) bool {
	if len(existingPaths) == len(allPaths) {
		v.storeImportMetadata(corruptionID, existingPaths, importItem)
		v.emitFilesDetected(corruptionID, existingPaths)
		return true
	}
	if len(existingPaths) > 0 {
		logger.Warnf("Partial import detected for %s via history: %d of %d files exist",
			corruptionID, len(existingPaths), len(allPaths))
		v.emitPartialReplacement(corruptionID, existingPaths, len(allPaths))
		return true
	}
	return false
}

// getHistoryWithRetry attempts to fetch history with exponential backoff retries
// This handles transient API failures that could cause missed import detections
func (v *VerifierService) getHistoryWithRetry(arrPath string, mediaID int64, limit, maxRetries int) ([]integration.HistoryItemInfo, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		// Check for shutdown between retries
		if v.isShuttingDown() {
			return nil, fmt.Errorf("shutdown in progress")
		}

		historyItems, err := v.arrClient.GetRecentHistoryForMediaByPath(arrPath, mediaID, limit)
		if err == nil {
			return historyItems, nil
		}

		lastErr = err
		if attempt < maxRetries-1 {
			// Exponential backoff: 1s, 2s, 4s - interruptible for graceful shutdown
			backoff := time.Duration(1<<uint(attempt)) * time.Second
			logger.Debugf("History API failed (attempt %d/%d), retrying in %v: %v", attempt+1, maxRetries, backoff, err)
			select {
			case <-v.shutdownCh:
				return nil, fmt.Errorf("shutdown in progress")
			case <-time.After(backoff):
			}
		}
	}
	return nil, fmt.Errorf("history API failed after %d attempts: %w", maxRetries, lastErr)
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

		elapsed := time.Since(startTime)
		if elapsed > timeout {
			v.publishDownloadTimeout(corruptionID, elapsed, attempt, "")
			return
		}

		currentInterval := calculateBackoffInterval(attempt, initialInterval, maxInterval)

		if attempt > 0 && (attempt%10 == 0 || currentInterval >= time.Hour) {
			logger.Debugf("Verification poll #%d for %s, next check in %s", attempt, corruptionID, currentInterval)
		}

		// Interruptible sleep for graceful shutdown
		if v.waitWithShutdown(currentInterval) {
			logger.Infof("Verifier: stopping file polling for %s due to shutdown", corruptionID)
			return
		}
		attempt++

		foundPaths := v.findFilesForVerification(mediaID, metadata, referencePath, useSmartVerification)

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

// findFilesForVerification looks for files via *arr API or direct path check.
func (v *VerifierService) findFilesForVerification(mediaID int64, metadata map[string]interface{}, referencePath string, useSmartVerification bool) []string {
	if useSmartVerification && v.arrClient != nil {
		allPaths, err := v.arrClient.GetAllFilePaths(mediaID, metadata, referencePath)
		if err == nil && len(allPaths) > 0 {
			foundPaths := v.convertAndVerifyPaths(allPaths)
			// Only return if ALL files exist
			if len(foundPaths) == len(allPaths) {
				return foundPaths
			}
		}
	}

	// Fallback: check if reference path exists directly
	if _, err := os.Stat(referencePath); err == nil {
		return []string{referencePath}
	}

	return nil
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

	ctx, cancel := context.WithTimeout(context.Background(), verifierQueryTimeout)
	defer cancel()

	var timeoutHours sql.NullInt64
	err := v.db.QueryRowContext(ctx, "SELECT verification_timeout_hours FROM scan_paths WHERE id = ?", pathID).Scan(&timeoutHours)
	if err != nil || !timeoutHours.Valid {
		return defaultTimeout
	}

	return time.Duration(timeoutHours.Int64) * time.Hour
}

// emitPartialReplacement handles the case where only some files were replaced
// This prevents waiting forever when *arr only finds/grabs some of the expected files
func (v *VerifierService) emitPartialReplacement(corruptionID string, existingPaths []string, expectedCount int) {
	logger.Infof("Processing partial replacement for %s: %d of %d files found",
		corruptionID, len(existingPaths), expectedCount)

	// Emit a warning event about partial replacement
	if err := v.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.FileDetected,
		EventData: map[string]interface{}{
			"file_path":           existingPaths[0], // Primary path for compatibility
			"file_paths":          existingPaths,
			"file_count":          len(existingPaths),
			"expected_count":      expectedCount,
			"partial_replacement": true,
			"missing_count":       expectedCount - len(existingPaths),
		},
	}); err != nil {
		logger.Errorf("Failed to publish FileDetected event for partial replacement: %v", err)
	}

	// Verify the files that DO exist
	v.verifyHealthMultiple(corruptionID, existingPaths)
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

// verifyFilesHealth checks all files and returns failed paths and last error.
func (v *VerifierService) verifyFilesHealth(filePaths []string) (failedPaths []string, lastError string) {
	for _, filePath := range filePaths {
		healthy, err := v.detector.Check(filePath, "thorough")
		if healthy {
			continue
		}
		failedPaths = append(failedPaths, filePath)
		if err != nil {
			lastError = err.Message
		} else {
			lastError = "unknown error"
		}
		logger.Infof("Verification failed for %s: %s", filePath, lastError)
	}
	return failedPaths, lastError
}

// buildSuccessEventData builds event data for a successful verification.
func (v *VerifierService) buildSuccessEventData(corruptionID string, fileCount int) map[string]interface{} {
	eventData := map[string]interface{}{"verified_count": fileCount}

	totalDuration, downloadDuration := v.getDurationMetrics(corruptionID)
	if totalDuration > 0 {
		eventData["total_duration_seconds"] = totalDuration
	}
	if downloadDuration > 0 {
		eventData["download_duration_seconds"] = downloadDuration
	}

	enrichVerificationEventData(eventData, v.getVerifyMeta(corruptionID))
	return eventData
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

	failedPaths, lastError := v.verifyFilesHealth(filePaths)
	v.clearVerifyMeta(corruptionID)

	if len(failedPaths) == 0 {
		eventData := v.buildSuccessEventData(corruptionID, len(filePaths))
		if err := v.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.VerificationSuccess,
			EventData:     eventData,
		}); err != nil {
			logger.Errorf("Failed to publish VerificationSuccess event: %v", err)
		}
		return
	}

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
