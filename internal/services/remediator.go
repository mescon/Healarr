package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"sync"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/pathutil"
	"github.com/mescon/Healarr/internal/repository"
	"github.com/mescon/Healarr/internal/safego"
)

// maxConcurrentRemediations limits how many remediations can run simultaneously
// to avoid overwhelming *arr APIs and download clients
const maxConcurrentRemediations = 5

// semaphoreAcquireTimeout is the maximum time to wait for a semaphore slot.
// This prevents indefinite blocking if all slots are stuck (Issue 5: deadlock prevention).
// Set to 2 minutes to allow time for HTTP timeouts (30s) plus processing.
const semaphoreAcquireTimeout = 2 * time.Minute

// RemediatorService handles corruption events by deleting files and triggering searches.
type RemediatorService struct {
	eventBus    eventbus.Publisher
	arrClient   integration.ArrClient
	pathMapper  integration.PathMapper
	db          *sql.DB
	corruptions *repository.CorruptionRepository
	scanPaths   *repository.ScanPathRepository
	semaphore   chan struct{} // limits concurrent remediations
	// Lifecycle management
	wg         sync.WaitGroup
	shutdownCh chan struct{}
	stopped    bool
	mu         sync.Mutex // protects stopped flag
}

// NewRemediatorService creates a new RemediatorService with the given dependencies.
func NewRemediatorService(eb eventbus.Publisher, arr integration.ArrClient, pm integration.PathMapper, db *sql.DB) *RemediatorService {
	r := &RemediatorService{
		eventBus:   eb,
		arrClient:  arr,
		pathMapper: pm,
		db:         db,
		semaphore:  make(chan struct{}, maxConcurrentRemediations),
		shutdownCh: make(chan struct{}),
	}
	if db != nil {
		r.corruptions = repository.NewCorruptionRepository(db)
		r.scanPaths = repository.NewScanPathRepository(db)
	}
	return r
}

// remediatorQueryTimeout bounds the scan-path config lookup done before remediating.
const remediatorQueryTimeout = 10 * time.Second

// Loop-breaker: once a file has been successfully remediated this many times
// within remediationLoopWindowDays and is corrupt again, re-downloading clearly
// is not fixing it (a transcode pipeline or failing storage keeps re-corrupting
// it), so auto-remediation pauses and surfaces the item instead of thrashing.
const (
	maxRemediationsBeforePause = 3
	remediationLoopWindowDays  = 30
)

// resolveRemediationPolicy returns the AUTHORITATIVE auto-remediate and dry-run
// settings for a corruption, read from the scan path's current config rather than
// the triggering event. The event payload is not trustworthy for this decision:
// recovery and monitor retries hardcode auto_remediate=true (and omit dry_run),
// and an operator may have flipped a path to manual or dry-run since the scan was
// queued. On any doubt (no path id, path deleted, lookup error) it refuses to
// auto-remediate, so we never delete a file without a clear, current opt-in.
func (r *RemediatorService) resolveRemediationPolicy(pathID int64, evtAuto, evtDry bool, filePath string) (autoRemediate, dryRun bool) {
	if r.scanPaths == nil {
		// Test fixtures without a repository: honor the event values.
		return evtAuto, evtDry
	}
	ctx, cancel := context.WithTimeout(context.Background(), remediatorQueryTimeout)
	defer cancel()

	if pathID <= 0 {
		// No path id on the event (legacy events, or producers that predate the
		// path_id-on-ScanFile fix). The event's own auto_remediate is NOT
		// trustworthy here: the recovery sweep, the stuck-remediation monitor,
		// and the manual-retry endpoint all hardcode auto_remediate=true and
		// drop dry_run, so honoring the event would invent consent the operator
		// never gave (and historically deleted files on dry-run paths). Resolve
		// the owning scan path from the file path instead; if no path matches,
		// refuse to auto-remediate.
		if sp, ok := r.findScanPathForFile(ctx, filePath); ok {
			return sp.AutoRemediate, sp.DryRun
		}
		logger.Warnf("Remediation: no path_id on event and no scan path matches %s; refusing to auto-remediate (safe default)", filePath)
		return false, evtDry
	}

	sp, err := r.scanPaths.GetByID(ctx, pathID)
	if err != nil {
		logger.Warnf("Remediation: cannot load scan path %d; refusing to auto-remediate (safe default): %v", pathID, err)
		return false, evtDry
	}
	return sp.AutoRemediate, sp.DryRun
}

// findScanPathForFile resolves the enabled scan path that owns filePath using
// the shared longest-prefix, separator-agnostic matching from pathutil. Used
// as the consent fallback when an event carries no path_id.
func (r *RemediatorService) findScanPathForFile(ctx context.Context, filePath string) (repository.ScanPath, bool) {
	if filePath == "" {
		return repository.ScanPath{}, false
	}
	rows, err := r.scanPaths.ListEnabled(ctx)
	if err != nil {
		logger.Warnf("Remediation: cannot list scan paths to resolve consent for %s: %v", filePath, err)
		return repository.ScanPath{}, false
	}
	best := -1
	bestLen := -1
	for i := range rows {
		if !pathutil.IsWithinRoot(rows[i].LocalPath, filePath) {
			continue
		}
		if l := pathutil.MatchedRootLen(rows[i].LocalPath); l > bestLen {
			bestLen = l
			best = i
		}
	}
	if best < 0 {
		return repository.ScanPath{}, false
	}
	return rows[best], true
}

// Start subscribes to corruption and retry events to begin remediation handling.
func (r *RemediatorService) Start() {
	r.eventBus.Subscribe(domain.CorruptionDetected, r.handleCorruptionDetected)
	r.eventBus.Subscribe(domain.RetryScheduled, r.handleRetry)
	// Restore an overridden monitored flag once remediation reaches a terminal
	// state (success or give-up), so we leave the library as we found it.
	r.eventBus.Subscribe(domain.VerificationSuccess, r.handleRemediationTerminal)
	r.eventBus.Subscribe(domain.MaxRetriesReached, r.handleRemediationTerminal)
	r.eventBus.Subscribe(domain.SearchExhausted, r.handleRemediationTerminal)
}

// Stop gracefully shuts down the RemediatorService.
// Waits for in-flight remediations to complete.
func (r *RemediatorService) Stop() {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return
	}
	r.stopped = true
	close(r.shutdownCh)
	r.mu.Unlock()

	r.wg.Wait()
	logger.Infof("RemediatorService stopped")
}

// isShuttingDown checks if shutdown was requested
func (r *RemediatorService) isShuttingDown() bool {
	select {
	case <-r.shutdownCh:
		return true
	default:
		return false
	}
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
	if r.corruptions == nil {
		return false, 0, nil
	}

	// Latest DeletionCompleted event for this corruption, if any.
	raw, err := r.corruptions.LatestEventData(context.Background(), corruptionID, string(domain.DeletionCompleted), "DESC")
	if err != nil {
		// No DeletionCompleted event found (ErrNotFound) or query error.
		return false, 0, nil
	}

	// Pull media_id from the (decrypted, unmarshaled) event data. JSON numbers
	// decode to float64; mirror the prior json_extract → int64 conversion.
	var data struct {
		MediaID float64 `json:"media_id"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return true, 0, nil
	}
	return true, int64(data.MediaID), nil
}

// retrySearchOnly triggers a new search without attempting deletion
func (r *RemediatorService) retrySearchOnly(event domain.Event, mediaID int64, metadata map[string]interface{}) {
	corruptionID := event.AggregateID

	// Use type-safe event data parsing
	data, err := event.ParseRetryEventData()
	if err != nil || data.FilePath == "" {
		logger.Warnf("Invalid retry event data for %s: %v", corruptionID, err)
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

	r.wg.Add(1)
	safego.Run("remediator-retry-search", func() {
		defer r.wg.Done()

		// Check if shutting down before starting work
		if r.isShuttingDown() {
			logger.Debugf("Remediator shutting down, skipping retry search for %s", corruptionID)
			return
		}

		// Acquire semaphore with timeout to limit concurrent remediations
		// and prevent indefinite blocking if slots are stuck
		select {
		case r.semaphore <- struct{}{}:
			defer func() { <-r.semaphore }()
		case <-r.shutdownCh:
			logger.Debugf("Remediator shutting down while waiting for semaphore for %s", corruptionID)
			return
		case <-time.After(semaphoreAcquireTimeout):
			logger.Warnf("Remediator: timeout acquiring semaphore for retry search %s after %v - all slots busy",
				corruptionID, semaphoreAcquireTimeout)
			r.publishError(corruptionID, domain.SearchFailed, "remediation queue full, will retry later")
			return
		}

		// If a verified-corrupt replacement is still on disk, delete it and
		// blocklist the release that produced it before re-searching, so the
		// *arr does not simply re-grab the same corrupt release. When a release
		// is blocklisted the *arr triggers its own re-download
		// (autoRedownloadFailed), so the explicit search below is skipped.
		blocklisted := r.handleCorruptReplacementBeforeSearch(corruptionID, pathID, mediaID, metadata)
		// Otherwise, if a download stalled in the *arr queue (timed out, never
		// imported), remove and blocklist it so the re-search grabs a different
		// release rather than waiting on the same dead one.
		if !blocklisted {
			blocklisted = r.handleStalledDownloadBeforeSearch(corruptionID, pathID, mediaID, arrPath, filePath)
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

		if !blocklisted {
			if err := r.arrClient.TriggerSearch(mediaID, arrPath, episodeIDs); err != nil {
				logger.Errorf("Retry search failed for media %d: %v", mediaID, err)
				r.publishError(corruptionID, domain.SearchFailed, err.Error())
				return
			}
			logger.Infof("Retry search triggered successfully for %s (media ID: %d)", filePath, mediaID)
		}

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
	})
}

// corruptReplacementPaths returns the local paths of a verified-corrupt
// replacement that is still on disk for this corruption, or nil. A corrupt
// replacement exists when the latest VerificationFailed event lists failed
// paths that still exist on disk. This distinguishes a corrupt re-download
// (which we must delete and blocklist) from a search or download failure where
// no replacement file is present (a plain search retry is correct there).
func (r *RemediatorService) corruptReplacementPaths(corruptionID string) []string {
	if r.corruptions == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), remediatorQueryTimeout)
	defer cancel()
	raw, err := r.corruptions.LatestEventData(ctx, corruptionID, string(domain.VerificationFailed), "DESC")
	if err != nil {
		return nil
	}
	var data struct {
		FailedPaths []string `json:"failed_paths"`
	}
	if err := json.Unmarshal([]byte(raw), &data); err != nil {
		return nil
	}
	var present []string
	for _, p := range data.FailedPaths {
		if p == "" {
			continue
		}
		if _, statErr := os.Stat(p); statErr == nil {
			present = append(present, p)
		}
	}
	return present
}

// latestGrabbedRelease returns the *arr history record ID and source title of
// the most recent grabbed release for a media item - the release that produced
// the file currently on disk. GetRecentHistoryForMediaByPath already filters to
// "grabbed" events; we pick the newest by date. Returns (0, "") if it cannot be
// resolved, which callers treat as "cannot blocklist, fall back to a search".
func (r *RemediatorService) latestGrabbedRelease(arrPath string, mediaID int64) (int64, string) {
	items, err := r.arrClient.GetRecentHistoryForMediaByPath(arrPath, mediaID, 10)
	if err != nil || len(items) == 0 {
		return 0, ""
	}
	newest := items[0]
	newestTime, _ := time.Parse(time.RFC3339, newest.Date)
	for _, item := range items[1:] {
		if t, perr := time.Parse(time.RFC3339, item.Date); perr == nil && t.After(newestTime) {
			newest, newestTime = item, t
		}
	}
	return newest.ID, newest.SourceTitle
}

// handleCorruptReplacementBeforeSearch deletes a verified-corrupt replacement
// that is still on disk and blocklists the *arr release that produced it, so the
// re-search does not grab the same corrupt release again. It returns true only
// when it blocklisted a release - in that case the *arr triggers its own
// re-download (autoRedownloadFailed) and the caller must NOT issue a duplicate
// search. On any inability to proceed (no corrupt replacement, non-auto path,
// dry-run, unresolvable release, delete/blocklist error) it returns false so the
// caller performs a normal search, never leaving a destructive half-state.
func (r *RemediatorService) handleCorruptReplacementBeforeSearch(corruptionID string, pathID, mediaID int64, _ map[string]interface{}) bool {
	paths := r.corruptReplacementPaths(corruptionID)
	if len(paths) == 0 {
		return false
	}

	// Re-read the authoritative auto-remediate / dry-run policy for this path.
	// The replacement's own local path doubles as the consent-resolution
	// fallback when the event chain carried no path_id.
	autoRemediate, dryRun := r.resolveRemediationPolicy(pathID, false, false, paths[0])
	if !autoRemediate {
		logger.Infof("Corrupt replacement present for %s but path is not auto-remediate; leaving for manual handling", corruptionID)
		return false
	}

	localPath := paths[0]
	arrPath, err := r.pathMapper.ToArrPath(localPath)
	if err != nil {
		logger.Errorf("Corrupt replacement for %s: cannot map path %s: %v", corruptionID, localPath, err)
		return false
	}

	grabbedID, sourceTitle := r.latestGrabbedRelease(arrPath, mediaID)
	if grabbedID <= 0 {
		logger.Warnf("Corrupt replacement for %s: could not resolve grabbed release for %s; falling back to a plain search", corruptionID, arrPath)
		return false
	}

	if dryRun {
		logger.Infof("[DRY-RUN] Would delete corrupt replacement %s and blocklist release %q (history %d) for %s", localPath, sourceTitle, grabbedID, corruptionID)
		return false
	}

	// Delete the corrupt replacement via the *arr API.
	if _, err := r.arrClient.DeleteFile(mediaID, arrPath); err != nil {
		logger.Errorf("Corrupt replacement for %s: failed to delete %s: %v", corruptionID, arrPath, err)
		r.publishError(corruptionID, domain.DeletionFailed, err.Error())
		return false
	}
	if err := r.eventBus.PublishWithRetry(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.DeletionCompleted,
		EventData: map[string]interface{}{
			"media_id":            mediaID,
			"file_path":           localPath,
			"source_title":        sourceTitle,
			"grabbed_history_id":  grabbedID,
			"corrupt_replacement": true,
		},
	}); err != nil {
		logger.Errorf("Failed to publish DeletionCompleted for corrupt replacement %s: %v", corruptionID, err)
	}

	// Blocklist the release so the *arr grabs a different one. markAsFailed also
	// triggers the *arr's own re-download when autoRedownloadFailed is enabled.
	if err := r.arrClient.MarkReleaseAsFailed(arrPath, grabbedID); err != nil {
		logger.Warnf("Corrupt replacement for %s: blocklist of release %q (history %d) failed: %v; falling back to a plain search", corruptionID, sourceTitle, grabbedID, err)
		return false
	}
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.ReleaseBlocklisted,
		EventData: map[string]interface{}{
			"media_id":     mediaID,
			"file_path":    localPath,
			"arr_path":     arrPath,
			"source_title": sourceTitle,
			"history_id":   grabbedID,
		},
	}); err != nil {
		logger.Errorf("Failed to publish ReleaseBlocklisted event for %s: %v", corruptionID, err)
	}
	logger.Infof("Blocklisted corrupt release %q (history %d) for %s; the *arr will grab the next-best release", sourceTitle, grabbedID, corruptionID)
	return true
}

// lastFailureWasDownloadTimeout reports whether the most recent failure-class
// event for an aggregate is a DownloadTimeout. This identifies a retry that
// was triggered because a download never completed (as opposed to a corrupt
// replacement or a search failure), so we only act on a genuinely stalled
// download and never disturb an in-progress one.
func (r *RemediatorService) lastFailureWasDownloadTimeout(corruptionID string) bool {
	if r.db == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), remediatorQueryTimeout)
	defer cancel()
	var eventType string
	err := r.db.QueryRowContext(ctx, `
		SELECT event_type FROM events
		WHERE aggregate_id = ?
		  AND event_type IN ('DownloadTimeout', 'VerificationFailed', 'SearchFailed', 'DeletionFailed', 'DownloadFailed')
		ORDER BY created_at DESC
		LIMIT 1
	`, corruptionID).Scan(&eventType)
	if err != nil {
		return false
	}
	return eventType == string(domain.DownloadTimeout)
}

// handleStalledDownloadBeforeSearch removes and blocklists a download that
// stalled in the *arr queue (a DownloadTimeout fired and the item is still
// queued, not imported), so the re-search grabs a different release instead of
// waiting on the same dead one. It returns true only when it blocklisted at
// least one queue item - the *arr then re-downloads the next-best release, so
// the caller must NOT issue a duplicate search. On any inability to proceed
// (not a timeout retry, nothing queued, non-auto path, dry-run, API error) it
// returns false so the caller performs a normal search.
func (r *RemediatorService) handleStalledDownloadBeforeSearch(corruptionID string, pathID, mediaID int64, arrPath, localPath string) bool {
	if !r.lastFailureWasDownloadTimeout(corruptionID) {
		return false
	}

	queueItems, err := r.arrClient.FindQueueItemsByMediaIDForPath(arrPath, mediaID)
	if err != nil || len(queueItems) == 0 {
		return false
	}

	// localPath is the consent-resolution fallback when the event chain
	// carried no path_id.
	autoRemediate, dryRun := r.resolveRemediationPolicy(pathID, false, false, localPath)
	if !autoRemediate {
		logger.Infof("Stalled download for %s but path is not auto-remediate; leaving for manual handling", corruptionID)
		return false
	}
	if dryRun {
		logger.Infof("[DRY-RUN] Would remove and blocklist %d stalled queue item(s) for %s", len(queueItems), corruptionID)
		return false
	}

	blocklistedAny := false
	for _, qi := range queueItems {
		if err := r.arrClient.RemoveFromQueueByPath(arrPath, qi.ID, true, true); err != nil {
			logger.Warnf("Stalled download for %s: failed to remove+blocklist queue item %d (%q): %v", corruptionID, qi.ID, qi.Title, err)
			continue
		}
		blocklistedAny = true
		if err := r.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.ReleaseBlocklisted,
			EventData: map[string]interface{}{
				"media_id":     mediaID,
				"arr_path":     arrPath,
				"queue_id":     qi.ID,
				"source_title": qi.Title,
				"reason":       "download_timeout",
			},
		}); err != nil {
			logger.Errorf("Failed to publish ReleaseBlocklisted event for %s: %v", corruptionID, err)
		}
		logger.Infof("Removed and blocklisted stalled download %q (queue %d) for %s; the *arr will grab the next-best release", qi.Title, qi.ID, corruptionID)
	}
	return blocklistedAny
}

// shouldPauseForRecurringCorruption reports whether this file has been
// successfully remediated too many times within the loop window to keep trying.
// On a query error it returns false - a transient DB issue must not change
// remediation behavior.
func (r *RemediatorService) shouldPauseForRecurringCorruption(filePath string) bool {
	if r.corruptions == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), remediatorQueryTimeout)
	defer cancel()
	n, err := r.corruptions.CountSuccessfulRemediationsForPath(ctx, filePath, remediationLoopWindowDays)
	if err != nil {
		logger.Warnf("Loop-breaker: cannot count prior remediations for %s; proceeding: %v", filePath, err)
		return false
	}
	return n >= maxRemediationsBeforePause
}

// ensureMonitoredForRemediation temporarily monitors an unmonitored item so the
// *arr will grab a replacement, recording the original state (a MonitorOverridden
// event) so handleRemediationTerminal can restore it later. Failing to read or
// set the flag is non-fatal: we log and proceed (the search may simply no-op,
// which is no worse than before this feature).
func (r *RemediatorService) ensureMonitoredForRemediation(corruptionID, arrPath string, mediaID int64) {
	monitored, err := r.arrClient.IsMonitored(arrPath, mediaID)
	if err != nil {
		logger.Warnf("Could not read monitored status for %s (media %d); proceeding without override: %v", corruptionID, mediaID, err)
		return
	}
	if monitored {
		return
	}
	if err := r.arrClient.SetMonitored(arrPath, mediaID, true); err != nil {
		logger.Warnf("Could not monitor %s (media %d) for remediation; proceeding anyway: %v", corruptionID, mediaID, err)
		return
	}
	if err := r.eventBus.Publish(domain.Event{
		AggregateID:   corruptionID,
		AggregateType: "corruption",
		EventType:     domain.MonitorOverridden,
		EventData: map[string]interface{}{
			"media_id":           mediaID,
			"arr_path":           arrPath,
			"original_monitored": false,
		},
	}); err != nil {
		logger.Errorf("Failed to publish MonitorOverridden event for %s: %v", corruptionID, err)
	}
	logger.Infof("Temporarily monitored media %d for %s so the *arr can grab a replacement (was unmonitored)", mediaID, corruptionID)
}

// monitorOverrideOriginal returns the arr path, media id, and original monitored
// flag recorded by a prior MonitorOverridden event for this aggregate, if any.
func (r *RemediatorService) monitorOverrideOriginal(corruptionID string) (arrPath string, mediaID int64, original, found bool) {
	if r.corruptions == nil {
		return "", 0, false, false
	}
	ctx, cancel := context.WithTimeout(context.Background(), remediatorQueryTimeout)
	defer cancel()
	raw, err := r.corruptions.LatestEventData(ctx, corruptionID, string(domain.MonitorOverridden), "DESC")
	if err != nil {
		return "", 0, false, false
	}
	var d struct {
		ArrPath  string  `json:"arr_path"`
		MediaID  float64 `json:"media_id"`
		Original bool    `json:"original_monitored"`
	}
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		return "", 0, false, false
	}
	return d.ArrPath, int64(d.MediaID), d.Original, true
}

// handleRemediationTerminal restores a monitored flag that remediation overrode,
// once the journey reaches a terminal state. No-op for items we never overrode.
// Restoring is idempotent (sets the flag back to its original value).
func (r *RemediatorService) handleRemediationTerminal(event domain.Event) {
	arrPath, mediaID, original, found := r.monitorOverrideOriginal(event.AggregateID)
	if !found {
		return
	}
	if err := r.arrClient.SetMonitored(arrPath, mediaID, original); err != nil {
		logger.Warnf("Failed to restore monitored=%t for %s after remediation: %v", original, event.AggregateID, err)
		return
	}
	logger.Infof("Restored monitored=%t for %s after remediation reached a terminal state", original, event.AggregateID)
}

func (r *RemediatorService) handleCorruptionDetected(event domain.Event) {
	corruptionID := event.AggregateID

	// Use type-safe event data parsing
	data, err := event.ParseCorruptionEventData()
	if err != nil {
		logger.Errorf("Invalid corruption event %s: %v", corruptionID, err)
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

	// Re-read the path's CURRENT auto-remediate / dry-run from the database. The
	// event payload is not authoritative (recovery/monitor retries hardcode
	// auto_remediate=true and drop dry_run, and the operator may have changed the
	// setting), so deciding deletion from it could delete files on a manual-mode
	// or dry-run path. This is the single enforcement point for both. When the
	// event has no path_id, the file path resolves the owning scan path instead.
	autoRemediate, dryRun := r.resolveRemediationPolicy(data.PathID, data.AutoRemediate, data.DryRun, data.FilePath)

	// Check for auto-remediation
	if !autoRemediate {
		return
	}

	// Check for global dry-run mode override
	dryRun = dryRun || config.Get().DryRunMode

	if dryRun {
		logger.Infof("Auto-remediation enabled for %s, but DRY-RUN mode is set for this path", data.FilePath)
		r.wg.Add(1)
		safego.Run("remediator-dry-run", func() {
			defer r.wg.Done()
			r.executeDryRun(corruptionID, data.FilePath, arrPath)
		})
	} else {
		logger.Infof("Auto-remediation enabled for %s, proceeding immediately", data.FilePath)
		r.wg.Add(1)
		safego.Run("remediator-execute", func() {
			defer r.wg.Done()
			r.executeRemediation(corruptionID, data.FilePath, arrPath, data.PathID)
		})
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
	// Check if shutting down before starting work
	if r.isShuttingDown() {
		logger.Debugf("Remediator shutting down, skipping remediation for %s", corruptionID)
		return
	}

	// Acquire semaphore with timeout to limit concurrent remediations
	// and prevent indefinite blocking if slots are stuck
	select {
	case r.semaphore <- struct{}{}:
		defer func() { <-r.semaphore }()
	case <-r.shutdownCh:
		logger.Debugf("Remediator shutting down while waiting for semaphore for %s", corruptionID)
		return
	case <-time.After(semaphoreAcquireTimeout):
		logger.Warnf("Remediator: timeout acquiring semaphore for %s after %v - all slots busy",
			corruptionID, semaphoreAcquireTimeout)
		r.publishError(corruptionID, domain.DeletionFailed, "remediation queue full, will retry later")
		return
	}

	// Loop-breaker: if this file keeps coming back corrupt even though we have
	// already restored it to health several times, re-downloading will not fix it
	// (a transcode pipeline or failing storage is re-corrupting it). Pause and
	// surface it rather than thrash - and crucially do NOT delete, since deleting
	// would just lose the file with no way to re-acquire a good one.
	if r.shouldPauseForRecurringCorruption(filePath) {
		logger.Warnf("Remediation paused for %s: still corrupt after %d successful restores in %d days", filePath, maxRemediationsBeforePause, remediationLoopWindowDays)
		if err := r.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.RemediationPaused,
			EventData: map[string]interface{}{
				"file_path": filePath,
				"reason":    "recurring_corruption",
			},
		}); err != nil {
			logger.Errorf("Failed to publish RemediationPaused event: %v", err)
		}
		return
	}

	// Find media first - validates we can proceed before publishing DeletionStarted
	mediaID, err := r.arrClient.FindMediaByPath(arrPath)
	if err != nil {
		logger.Errorf("Failed to find media for path %s: %v", arrPath, err)
		r.publishError(corruptionID, domain.DeletionFailed, err.Error())
		return
	}

	// If the item is unmonitored (e.g. a transcode pipeline like Tdarr unmonitored
	// it to protect its output), temporarily monitor it so the *arr will grab a
	// replacement. The original state is recorded and restored at a terminal state.
	r.ensureMonitoredForRemediation(corruptionID, arrPath, mediaID)

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

	// NOTE: Once deletion is successful, we MUST attempt the search even during shutdown.
	// Aborting here would leave the item in "DeletionCompleted" state without a search.
	// The retry mechanism (via MonitorService) will handle SearchFailed if search fails.

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
