package services

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/mescon/Healarr/internal/db"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
)

// Default media file extensions to scan
var defaultMediaExtensions = map[string]bool{
	".mkv":  true,
	".mp4":  true,
	".avi":  true,
	".mov":  true,
	".wmv":  true,
	".flv":  true,
	".webm": true,
	".m4v":  true,
	".mpg":  true,
	".mpeg": true,
	".ts":   true,
	".m2ts": true,
	".vob":  true,
	".3gp":  true,
	".ogv":  true,
	".divx": true,
	".xvid": true,
}

// isMediaFile checks if a file has a supported media extension
func isMediaFile(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return defaultMediaExtensions[ext]
}

// isHiddenOrTempFile checks if a file should be skipped (hidden, temp, fuse, etc.)
func isHiddenOrTempFile(path string) bool {
	name := filepath.Base(path)
	nameLower := strings.ToLower(name)

	// Skip hidden files (starting with .)
	if strings.HasPrefix(name, ".") {
		return true
	}
	// Skip FUSE temporary files
	if strings.HasPrefix(name, ".fuse_hidden") {
		return true
	}
	// Skip common temp file patterns
	if strings.HasSuffix(nameLower, ".tmp") || strings.HasSuffix(nameLower, ".temp") {
		return true
	}
	// Skip partial download files (various download clients)
	if strings.HasSuffix(nameLower, ".part") || strings.HasSuffix(nameLower, ".partial") {
		return true
	}
	// Skip qBittorrent incomplete files
	if strings.HasSuffix(nameLower, ".!qb") {
		return true
	}
	// Skip SABnzbd incomplete files
	if strings.HasPrefix(name, "__") || strings.Contains(nameLower, ".nzb") {
		return true
	}
	// Skip NZBGet temporary files
	if strings.HasSuffix(nameLower, ".nzbget") {
		return true
	}
	// Skip sample files (often corrupt/incomplete in releases)
	if strings.Contains(nameLower, "sample") && !strings.Contains(nameLower, "sampler") {
		return true
	}
	// Skip common extras that shouldn't trigger remediation
	if strings.Contains(nameLower, "-trailer") || strings.Contains(nameLower, ".trailer.") {
		return true
	}
	return false
}

// Batch throttling constants
const (
	// batchThrottleThreshold is the number of corruptions in a single scan that triggers throttling
	batchThrottleThreshold = 10
	// batchThrottleDelay is the delay between corruption events when throttling is active
	batchThrottleDelay = 30 * time.Second
)

type ScanProgress struct {
	ID               string             `json:"id"`
	Type             string             `json:"type"` // "path" or "file"
	Path             string             `json:"path"`
	PathID           int64              `json:"path_id,omitempty"` // Database path ID for resumable scans
	TotalFiles       int                `json:"total_files"`
	FilesDone        int                `json:"files_done"`
	CurrentFile      string             `json:"current_file"`
	Status           string             `json:"status"` // "enumerating", "scanning", "paused", "interrupted", "cancelled"
	StartTime        string             `json:"start_time"`
	ScanDBID         int64              `json:"-"` // Database scan record ID
	cancel           context.CancelFunc `json:"-"` // Don't export in JSON
	pauseChan        chan struct{}      `json:"-"` // Channel to signal pause
	resumeChan       chan struct{}      `json:"-"` // Channel to signal resume
	isPaused         bool               `json:"-"` // Track pause state
	corruptionCount  int                `json:"-"` // Track corruptions found in this scan for throttling
	isThrottled      bool               `json:"-"` // Whether this scan is being throttled
}

type ScannerService struct {
	db          *sql.DB
	eventBus    *eventbus.EventBus
	detector    integration.HealthChecker
	pathMapper  integration.PathMapper
	activeScans map[string]*ScanProgress
	mu          sync.Mutex
	// filesInProgress tracks individual files currently being scanned to prevent race conditions
	filesInProgress map[string]bool
	filesMu         sync.Mutex
	shutdownCh      chan struct{}
	wg              sync.WaitGroup
}

func NewScannerService(db *sql.DB, eb *eventbus.EventBus, detector integration.HealthChecker, pm integration.PathMapper) *ScannerService {
	return &ScannerService{
		db:              db,
		eventBus:        eb,
		detector:        detector,
		pathMapper:      pm,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}
}

// IsFileBeingScanned returns true if the given file is currently being scanned.
// This can be used by other services (like the verifier) to avoid race conditions.
func (s *ScannerService) IsFileBeingScanned(localPath string) bool {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	return s.filesInProgress[localPath]
}

// Shutdown gracefully stops all active scans by saving their state for later resumption
func (s *ScannerService) Shutdown() {
	logger.Infof("Scanner: initiating graceful shutdown...")
	close(s.shutdownCh)

	// Save state for all active scans and cancel them
	s.mu.Lock()
	for scanID, scan := range s.activeScans {
		if scan.Type == "path" && scan.ScanDBID > 0 {
			logger.Infof("Scanner: saving state for scan %s (file %d/%d)", scanID, scan.FilesDone, scan.TotalFiles)
			// Mark as interrupted in database - state is already saved during scanning
			_, err := s.db.Exec(`
				UPDATE scans SET status = 'interrupted', current_file_index = ?
				WHERE id = ?
			`, scan.FilesDone, scan.ScanDBID)
			if err != nil {
				logger.Errorf("Failed to save scan state for %s: %v", scanID, err)
			}
		}
		if scan.cancel != nil {
			scan.cancel()
		}
	}
	s.mu.Unlock()

	// Brief wait for goroutines to acknowledge cancellation (non-blocking)
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		logger.Infof("Scanner: all scans stopped")
	case <-time.After(2 * time.Second):
		logger.Infof("Scanner: timeout waiting for scans, state saved for resumption")
	}

	logger.Infof("Scanner: shutdown complete")
}

// ResumeInterruptedScans checks for scans that were interrupted by shutdown and resumes them
func (s *ScannerService) ResumeInterruptedScans() {
	rows, err := s.db.Query(`
		SELECT s.id, s.path_id, s.path, s.total_files, s.current_file_index, s.file_list, s.detection_config, s.auto_remediate, COALESCE(s.dry_run, 0)
		FROM scans s
		WHERE s.status = 'interrupted' AND s.file_list IS NOT NULL
		ORDER BY s.started_at DESC
	`)
	if err != nil {
		logger.Errorf("Failed to query interrupted scans: %v", err)
		return
	}
	defer rows.Close()

	var scansToResume []struct {
		scanDBID        int64
		pathID          int64
		path            string
		totalFiles      int
		currentIndex    int
		fileListJSON    string
		detectionConfig string
		autoRemediate   bool
		dryRun          bool
	}

	for rows.Next() {
		var scan struct {
			scanDBID        int64
			pathID          int64
			path            string
			totalFiles      int
			currentIndex    int
			fileListJSON    string
			detectionConfig string
			autoRemediate   bool
			dryRun          bool
		}
		var pathID sql.NullInt64
		var detectionConfigNull sql.NullString

		if err := rows.Scan(&scan.scanDBID, &pathID, &scan.path, &scan.totalFiles, &scan.currentIndex, &scan.fileListJSON, &detectionConfigNull, &scan.autoRemediate, &scan.dryRun); err != nil {
			logger.Errorf("Failed to scan interrupted scan row: %v", err)
			continue
		}
		if pathID.Valid {
			scan.pathID = pathID.Int64
		}
		if detectionConfigNull.Valid {
			scan.detectionConfig = detectionConfigNull.String
		}
		scansToResume = append(scansToResume, scan)
	}

	for _, scan := range scansToResume {
		logger.Infof("Resuming interrupted scan for %s (starting at file %d/%d)", scan.path, scan.currentIndex, scan.totalFiles)
		go s.resumeScan(scan.scanDBID, scan.pathID, scan.path, scan.totalFiles, scan.currentIndex, scan.fileListJSON, scan.detectionConfig, scan.autoRemediate, scan.dryRun)
	}
}

// resumeScan continues a previously interrupted scan
func (s *ScannerService) resumeScan(scanDBID, pathID int64, localPath string, totalFiles, startIndex int, fileListJSON, detectionConfigJSON string, autoRemediate bool, dryRun bool) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.wg.Add(1)
	defer s.wg.Done()

	// Parse file list
	var files []string
	if err := json.Unmarshal([]byte(fileListJSON), &files); err != nil {
		logger.Errorf("Failed to parse file list for resumed scan: %v", err)
		return
	}

	// Parse detection config
	var detectionConfig integration.DetectionConfig
	if detectionConfigJSON != "" {
		if err := json.Unmarshal([]byte(detectionConfigJSON), &detectionConfig); err != nil {
			logger.Errorf("Failed to parse detection config: %v", err)
			detectionConfig = integration.DetectionConfig{
				Method: integration.DetectionMethod("ffprobe"),
				Mode:   "quick",
			}
		}
	} else {
		detectionConfig = integration.DetectionConfig{
			Method: integration.DetectionMethod("ffprobe"),
			Mode:   "quick",
		}
	}

	scanID := uuid.New().String()
	progress := &ScanProgress{
		ID:          scanID,
		Type:        "path",
		Path:        localPath,
		PathID:      pathID,
		TotalFiles:  totalFiles,
		FilesDone:   startIndex,
		CurrentFile: "",
		Status:      "scanning",
		StartTime:   time.Now().Format(time.RFC3339),
		ScanDBID:    scanDBID,
		pauseChan:   make(chan struct{}),
		resumeChan:  make(chan struct{}),
		isPaused:    false,
	}
	progress.cancel = cancel

	s.mu.Lock()
	s.activeScans[scanID] = progress
	s.mu.Unlock()

	// Update scan status to running
	_, err := s.db.Exec(`UPDATE scans SET status = 'running' WHERE id = ?`, scanDBID)
	if err != nil {
		logger.Errorf("Failed to update scan status: %v", err)
	}

	defer func() {
		finalStatus := "completed"
		if progress.Status == "cancelled" || progress.Status == "interrupted" {
			finalStatus = progress.Status
		}
		_, err := s.db.Exec(`
			UPDATE scans SET status = ?, files_scanned = ?, completed_at = datetime('now')
			WHERE id = ?
		`, finalStatus, progress.FilesDone, scanDBID)
		if err != nil {
			logger.Errorf("Failed to update scan record: %v", err)
		}

		s.mu.Lock()
		delete(s.activeScans, scanID)
		s.mu.Unlock()

		s.eventBus.Publish(domain.Event{
			AggregateType: "scan",
			AggregateID:   scanID,
			EventType:     "ScanCompleted",
			EventData: map[string]interface{}{
				"scan_id": scanID,
				"status":  finalStatus,
				"resumed": true,
			},
		})
	}()

	s.emitProgress(progress)
	logger.Infof("Resumed scan %s for %s at file %d/%d", scanID, localPath, startIndex, totalFiles)

	// Continue scanning from where we left off
	s.scanFiles(ctx, progress, files, startIndex, detectionConfig, autoRemediate, dryRun, scanDBID)
}

// ScanFile scans a single file for corruption
func (s *ScannerService) ScanFile(localPath string) error {
	// RACE CONDITION PREVENTION: Check if this file is already being scanned
	// This prevents webhook race conditions where multiple events trigger scans for the same file
	s.filesMu.Lock()
	if s.filesInProgress[localPath] {
		s.filesMu.Unlock()
		logger.Debugf("Skipping scan for %s - already in progress", localPath)
		return nil
	}
	s.filesInProgress[localPath] = true
	s.filesMu.Unlock()

	// Ensure we clean up the in-progress flag when done
	defer func() {
		s.filesMu.Lock()
		delete(s.filesInProgress, localPath)
		s.filesMu.Unlock()
	}()

	scanID := uuid.New().String()
	progress := &ScanProgress{
		ID:          scanID,
		Type:        "file",
		Path:        localPath,
		TotalFiles:  1,
		FilesDone:   0,
		CurrentFile: localPath,
		Status:      "scanning",
		StartTime:   time.Now().Format(time.RFC3339),
	}

	s.mu.Lock()
	s.activeScans[scanID] = progress
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		delete(s.activeScans, scanID)
		s.mu.Unlock()
		// Emit completion event
		s.eventBus.Publish(domain.Event{
			AggregateType: "scan",
			AggregateID:   scanID,
			EventType:     "ScanCompleted", // Custom event type for now
			EventData: map[string]interface{}{
				"scan_id": scanID,
				"status":  "completed",
			},
		})
	}()

	// Emit start event
	s.emitProgress(progress)
	logger.Infof("Scan started for file: %s (ID: %s)", localPath, scanID)

	// Find scan path config for this file
	autoRemediate, dryRun, err := s.getScanPathConfig(localPath)
	if err != nil {
		// Log warning but proceed with defaults (false, false)
		// This is important for ops visibility - file scanned without matching path config
		logger.Warnf("Could not determine scan path config for %s: %v (using defaults: auto_remediate=false, dry_run=false)", localPath, err)
	}

	logger.Infof("Scanning single file: %s", localPath)

	// NOTE: We do NOT check for recently-modified files here because webhook scans
	// are triggered by Sonarr/Radarr AFTER import is complete - the file is done being written.
	// The recently-modified check only applies to path scans where we might find in-progress downloads.

	// Use quick mode for single file scans (called from webhooks)
	healthy, healthErr := s.detector.Check(localPath, "quick")

	progress.FilesDone = 1
	s.emitProgress(progress)

	if !healthy {
		// CRITICAL: Check if this is a recoverable error (mount lost, NAS offline, etc.)
		if healthErr.IsRecoverable() {
			logger.Infof("Recoverable error for file %s (Type: %s): %s - will NOT trigger remediation",
				localPath, healthErr.Type, healthErr.Message)
			// Don't emit corruption event for recoverable errors
			return nil
		}

		// This is TRUE corruption - emit event for remediation
		logger.Infof("Corruption detected in file: %s (Type: %s)", localPath, healthErr.Type)

		// DEDUPLICATION: Check if this file already has an active corruption record
		if s.hasActiveCorruption(localPath) {
			logger.Infof("Skipping duplicate corruption for file already being processed: %s", localPath)
			return nil
		}

		// Emit event
		err := s.eventBus.Publish(domain.Event{
			AggregateType: "corruption",
			AggregateID:   uuid.New().String(),
			EventType:     domain.CorruptionDetected,
			EventData: map[string]interface{}{
				"file_path":       localPath,
				"corruption_type": healthErr.Type,
				"error_details":   healthErr.Message,
				"source":          "webhook",
				"auto_remediate":  autoRemediate,
				"dry_run":         dryRun,
			},
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func (s *ScannerService) ScanPath(pathID int64, localPath string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s.wg.Add(1)
	defer s.wg.Done()

	scanID := uuid.New().String()
	progress := &ScanProgress{
		ID:          scanID,
		Type:        "path",
		Path:        localPath,
		PathID:      pathID,
		TotalFiles:  0,
		FilesDone:   0,
		CurrentFile: "",
		Status:      "enumerating",
		StartTime:   time.Now().Format(time.RFC3339),
		pauseChan:   make(chan struct{}),
		resumeChan:  make(chan struct{}),
		isPaused:    false,
	}

	progress.cancel = cancel

	s.mu.Lock()
	s.activeScans[scanID] = progress
	s.mu.Unlock()

	s.emitProgress(progress)

	// Get scan path configuration
	var autoRemediate bool
	var dryRun bool
	var detectionMethod, detectionMode string
	var detectionArgsJSON sql.NullString
	err := s.db.QueryRow(`
		SELECT auto_remediate, dry_run, detection_method, detection_args, detection_mode 
		FROM scan_paths WHERE id = ?
	`, pathID).Scan(&autoRemediate, &dryRun, &detectionMethod, &detectionArgsJSON, &detectionMode)
	if err != nil {
		// Log error but proceed with defaults
		logger.Errorf("Error querying scan path config: %v", err)
		detectionMethod = "ffprobe"
		detectionMode = "quick"
	}

	// Parse detection args if present
	var detectionArgs []string
	if detectionArgsJSON.Valid && detectionArgsJSON.String != "" {
		if err := json.Unmarshal([]byte(detectionArgsJSON.String), &detectionArgs); err != nil {
			logger.Errorf("Error parsing detection args: %v", err)
		}
	}

	detectionConfig := integration.DetectionConfig{
		Method: integration.DetectionMethod(detectionMethod),
		Args:   detectionArgs,
		Mode:   detectionMode,
	}

	logger.Infof("Starting scan for path ID %d: %s", pathID, localPath)

	// PRE-FLIGHT CHECK: Verify the path is accessible before starting enumeration
	// This prevents false positives when mounts are offline or NAS is down
	if err := s.verifyPathAccessible(localPath); err != nil {
		logger.Errorf("Pre-flight check failed for path %s: %v - scan aborted", localPath, err)

		s.mu.Lock()
		delete(s.activeScans, scanID)
		s.mu.Unlock()

		// Notify about the inaccessible path
		s.eventBus.Publish(domain.Event{
			AggregateType: "system",
			AggregateID:   scanID,
			EventType:     domain.SystemHealthDegraded,
			EventData: map[string]interface{}{
				"path":    localPath,
				"reason":  "Scan path is inaccessible",
				"details": err.Error(),
			},
		})

		return fmt.Errorf("scan path inaccessible: %w", err)
	}

	// Enumeration phase - only collect media files, skip hidden/temp files
	var files []string
	var skippedCount int
	var symlinkCount int
	err = filepath.Walk(localPath, func(filePath string, info os.FileInfo, err error) error {
		if err != nil {
			// Log permission errors but continue walking
			if os.IsPermission(err) {
				logger.Debugf("Permission denied: %s", filePath)
				return nil
			}
			return err
		}

		// Skip symlinks to avoid potential issues with hardlinked seeding files
		// and to prevent scanning the same file multiple times via different paths
		if info.Mode()&os.ModeSymlink != 0 {
			symlinkCount++
			return nil
		}

		if !info.IsDir() {
			// Skip hidden and temporary files
			if isHiddenOrTempFile(filePath) {
				skippedCount++
				return nil
			}
			// Only include media files
			if isMediaFile(filePath) {
				files = append(files, filePath)
			} else {
				skippedCount++
			}
		}
		return nil
	})
	if err != nil {
		s.mu.Lock()
		delete(s.activeScans, scanID)
		s.mu.Unlock()
		return err
	}

	if skippedCount > 0 || symlinkCount > 0 {
		logger.Debugf("Skipped %d non-media/hidden files and %d symlinks in %s", skippedCount, symlinkCount, localPath)
	}

	progress.TotalFiles = len(files)
	progress.Status = "scanning"

	// Serialize file list and detection config for resumability
	fileListJSON, err := json.Marshal(files)
	if err != nil {
		logger.Errorf("Failed to serialize file list: %v", err)
		fileListJSON = []byte("[]")
	}

	detectionConfigJSON, err := json.Marshal(detectionConfig)
	if err != nil {
		logger.Errorf("Failed to serialize detection config: %v", err)
		detectionConfigJSON = []byte("{}")
	}

	// Record scan start in database with resumability data
	var scanDBID int64
	result, err := s.db.Exec(`
		INSERT INTO scans (path, path_id, status, files_scanned, corruptions_found, total_files, current_file_index, file_list, detection_config, auto_remediate, dry_run, started_at)
		VALUES (?, ?, 'running', 0, 0, ?, 0, ?, ?, ?, ?, datetime('now'))
	`, localPath, pathID, len(files), string(fileListJSON), string(detectionConfigJSON), autoRemediate, dryRun)
	if err != nil {
		logger.Errorf("Failed to record scan start: %v", err)
	} else {
		scanDBID, _ = result.LastInsertId()
		progress.ScanDBID = scanDBID
	}

	s.emitProgress(progress)

	defer func() {
		// Final cleanup - only update if not interrupted (interrupted is handled by Shutdown)
		if progress.Status != "interrupted" {
			finalStatus := "completed"
			if progress.Status == "cancelled" {
				finalStatus = "cancelled"
			}
			if scanDBID > 0 {
				_, err := s.db.Exec(`
					UPDATE scans 
					SET status = ?, files_scanned = ?, completed_at = datetime('now')
					WHERE id = ?
				`, finalStatus, progress.FilesDone, scanDBID)
				if err != nil {
					logger.Errorf("Failed to update scan record: %v", err)
				}
			}
		}

		s.mu.Lock()
		delete(s.activeScans, scanID)
		s.mu.Unlock()
		s.eventBus.Publish(domain.Event{
			AggregateType: "scan",
			AggregateID:   scanID,
			EventType:     "ScanCompleted",
			EventData: map[string]interface{}{
				"scan_id": scanID,
				"status":  progress.Status,
			},
		})
	}()

	// Scan files starting from index 0
	s.scanFiles(ctx, progress, files, 0, detectionConfig, autoRemediate, dryRun, scanDBID)
	return nil
}

// =============================================================================
// scanFiles helpers - extracted for clarity and testability
// =============================================================================

// scanFileContext holds the context for scanning a single file.
// This reduces parameter passing and groups related data together.
type scanFileContext struct {
	filePath        string
	fileSize        int64
	fileMtime       time.Time
	pathID          int64
	scanDBID        int64
	autoRemediate   bool
	dryRun          bool
	detectionConfig integration.DetectionConfig
}

// scanLoopAction indicates what the scan loop should do after checking state.
type scanLoopAction int

const (
	scanContinue   scanLoopAction = iota // Continue to next file
	scanReturn                           // Return from the loop entirely
	scanSkipToNext                       // Skip current file, continue loop
)

// checkScanCancellation checks if the scan should be cancelled due to context cancellation or shutdown.
// Returns scanReturn if cancelled, scanContinue otherwise.
func (s *ScannerService) checkScanCancellation(ctx context.Context, progress *ScanProgress, localPath string, fileIndex, totalFiles int) scanLoopAction {
	select {
	case <-ctx.Done():
		logger.Infof("Scan cancelled: %s", localPath)
		progress.Status = "cancelled"
		s.emitProgress(progress)
		return scanReturn
	case <-s.shutdownCh:
		logger.Infof("Scan interrupted for graceful shutdown: %s (at file %d/%d)", localPath, fileIndex, totalFiles)
		progress.Status = "interrupted"
		s.emitProgress(progress)
		return scanReturn
	default:
		return scanContinue
	}
}

// handleScanPause handles pause/resume logic for the scan.
// Returns scanReturn if the scan should exit, scanContinue otherwise.
func (s *ScannerService) handleScanPause(ctx context.Context, progress *ScanProgress, localPath string, fileIndex int, scanDBID int64) scanLoopAction {
	s.mu.Lock()
	isPaused := progress.isPaused
	s.mu.Unlock()

	if !isPaused {
		return scanContinue
	}

	logger.Infof("Scan paused: %s (at file %d/%d)", localPath, fileIndex+1, progress.TotalFiles)

	// Save current position
	if scanDBID > 0 {
		if _, err := s.db.Exec(`UPDATE scans SET current_file_index = ?, status = 'paused' WHERE id = ?`, fileIndex, scanDBID); err != nil {
			logger.Warnf("Failed to update scan pause state for scan %d: %v", scanDBID, err)
		}
	}

	// Wait for resume or cancel
	select {
	case <-progress.resumeChan:
		logger.Infof("Scan resumed: %s", localPath)
		s.mu.Lock()
		progress.Status = "scanning"
		progress.isPaused = false
		s.mu.Unlock()
		if scanDBID > 0 {
			if _, err := s.db.Exec(`UPDATE scans SET status = 'running' WHERE id = ?`, scanDBID); err != nil {
				logger.Warnf("Failed to update scan resume state for scan %d: %v", scanDBID, err)
			}
		}
		s.emitProgress(progress)
		return scanContinue
	case <-ctx.Done():
		logger.Infof("Scan cancelled while paused: %s", localPath)
		progress.Status = "cancelled"
		s.emitProgress(progress)
		return scanReturn
	case <-s.shutdownCh:
		logger.Infof("Scan interrupted during pause: %s", localPath)
		progress.Status = "interrupted"
		s.emitProgress(progress)
		return scanReturn
	}
}

// shouldSkipRecentlyModified checks if a file was modified too recently and should be skipped.
// Returns true if file should be skipped (likely still being written).
func (s *ScannerService) shouldSkipRecentlyModified(sfc *scanFileContext) bool {
	if time.Since(sfc.fileMtime) < 2*time.Minute {
		logger.Infof("Skipping recently modified file (mtime %v ago): %s",
			time.Since(sfc.fileMtime).Round(time.Second), sfc.filePath)
		if sfc.scanDBID > 0 {
			db.ExecWithRetry(s.db, `
				INSERT INTO scan_files (scan_id, file_path, status, corruption_type, error_details, file_size)
				VALUES (?, ?, 'skipped', 'RecentlyModified', 'File modified within last 2 minutes - likely still being written', ?)
			`, sfc.scanDBID, sfc.filePath, sfc.fileSize)
		}
		return true
	}
	return false
}

// shouldSkipChangingSize checks if file size is actively changing (download in progress).
// Returns true if file should be skipped.
func (s *ScannerService) shouldSkipChangingSize(sfc *scanFileContext) bool {
	time.Sleep(500 * time.Millisecond)
	if info2, err := os.Stat(sfc.filePath); err == nil {
		if info2.Size() != sfc.fileSize {
			logger.Infof("Skipping file with changing size (download in progress?): %s", sfc.filePath)
			if sfc.scanDBID > 0 {
				db.ExecWithRetry(s.db, `
					INSERT INTO scan_files (scan_id, file_path, status, corruption_type, error_details, file_size)
					VALUES (?, ?, 'skipped', 'SizeChanging', 'File size changed during scan - active download/copy', ?)
				`, sfc.scanDBID, sfc.filePath, sfc.fileSize)
			}
			return true
		}
	}
	return false
}

// recordHealthyFile records a healthy file in the scan_files table.
func (s *ScannerService) recordHealthyFile(sfc *scanFileContext) {
	if sfc.scanDBID > 0 {
		_, err := db.ExecWithRetry(s.db, `
			INSERT INTO scan_files (scan_id, file_path, status, file_size)
			VALUES (?, ?, 'healthy', ?)
		`, sfc.scanDBID, sfc.filePath, sfc.fileSize)
		if err != nil {
			logger.Debugf("Failed to record healthy file: %v", err)
		}
	}
}

// handleRecoverableError processes an error that might be due to infrastructure issues.
// Returns scanReturn if scan should abort, scanSkipToNext to continue with next file.
func (s *ScannerService) handleRecoverableError(progress *ScanProgress, sfc *scanFileContext, healthErr *integration.HealthCheckError) scanLoopAction {
	logger.Infof("Recoverable error for file %s (Type: %s): %s - queued for rescan",
		sfc.filePath, healthErr.Type, healthErr.Message)

	// Record as "inaccessible" not "corrupt"
	if sfc.scanDBID > 0 {
		_, err := db.ExecWithRetry(s.db, `
			INSERT INTO scan_files (scan_id, file_path, status, corruption_type, error_details, file_size)
			VALUES (?, ?, 'inaccessible', ?, ?, ?)
		`, sfc.scanDBID, sfc.filePath, healthErr.Type, healthErr.Message, sfc.fileSize)
		if err != nil {
			logger.Debugf("Failed to record inaccessible file: %v", err)
		}
	}

	// Queue file for rescan when infrastructure is back
	s.queueForRescan(sfc.filePath, sfc.pathID, healthErr.Type, healthErr.Message)

	// Check if mount is lost - abort scan to prevent false positives
	if healthErr.Type == integration.ErrorTypeMountLost {
		logger.Errorf("Mount appears to be offline for path: %s - aborting scan to prevent false positives", progress.Path)
		progress.Status = "aborted"

		if sfc.scanDBID > 0 {
			if _, err := s.db.Exec(`UPDATE scans SET status = 'aborted', error_message = ? WHERE id = ?`,
				"Scan aborted: filesystem/mount became inaccessible", sfc.scanDBID); err != nil {
				logger.Warnf("Failed to update scan abort state for scan %d: %v", sfc.scanDBID, err)
			}
		}

		// Emit system health event
		s.eventBus.Publish(domain.Event{
			AggregateType: "system",
			AggregateID:   progress.ID,
			EventType:     domain.SystemHealthDegraded,
			EventData: map[string]interface{}{
				"path":    progress.Path,
				"reason":  "Mount or filesystem became inaccessible during scan",
				"details": healthErr.Message,
			},
		})
		return scanReturn
	}

	return scanSkipToNext
}

// handleTrueCorruption processes a file that is actually corrupted.
// Returns scanReturn if scan should stop, scanSkipToNext if file was duplicate, scanContinue otherwise.
func (s *ScannerService) handleTrueCorruption(ctx context.Context, progress *ScanProgress, sfc *scanFileContext, healthErr *integration.HealthCheckError) scanLoopAction {
	logger.Infof("Corruption detected in file: %s (Type: %s)", sfc.filePath, healthErr.Type)

	// DEDUPLICATION: Check if already being processed
	if s.hasActiveCorruption(sfc.filePath) {
		logger.Infof("Skipping duplicate corruption for file already being processed: %s", sfc.filePath)
		if sfc.scanDBID > 0 {
			db.ExecWithRetry(s.db, `
				INSERT INTO scan_files (scan_id, file_path, status, corruption_type, error_details, file_size)
				VALUES (?, ?, 'skipped', 'AlreadyProcessing', 'File already has active corruption record', ?)
			`, sfc.scanDBID, sfc.filePath, sfc.fileSize)
		}
		return scanSkipToNext
	}

	// Record corrupt file
	if sfc.scanDBID > 0 {
		_, err := db.ExecWithRetry(s.db, `
			INSERT INTO scan_files (scan_id, file_path, status, corruption_type, error_details, file_size)
			VALUES (?, ?, 'corrupt', ?, ?, ?)
		`, sfc.scanDBID, sfc.filePath, healthErr.Type, healthErr.Message, sfc.fileSize)
		if err != nil {
			logger.Debugf("Failed to record corrupt file: %v", err)
		}

		// Update corruptions count
		if _, err := db.ExecWithRetry(s.db, `UPDATE scans SET corruptions_found = corruptions_found + 1 WHERE id = ?`, sfc.scanDBID); err != nil {
			logger.Warnf("Failed to update corruptions count for scan %d: %v", sfc.scanDBID, err)
		}
	}

	// Track for throttling
	progress.corruptionCount++

	// Check if we need to apply batch throttling
	if action := s.applyBatchThrottling(ctx, progress); action != scanContinue {
		return action
	}

	// Emit corruption event for remediation
	err := s.eventBus.Publish(domain.Event{
		AggregateType: "corruption",
		AggregateID:   uuid.New().String(),
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path":       sfc.filePath,
			"path_id":         sfc.pathID,
			"corruption_type": healthErr.Type,
			"error_details":   healthErr.Message,
			"auto_remediate":  sfc.autoRemediate,
			"dry_run":         sfc.dryRun,
			"batch_throttled": progress.isThrottled,
		},
	})
	if err != nil {
		logger.Errorf("Failed to publish corruption event: %v", err)
	}

	return scanContinue
}

// applyBatchThrottling applies throttling when many corruptions are found.
// Returns scanReturn if cancelled during throttle delay, scanContinue otherwise.
func (s *ScannerService) applyBatchThrottling(ctx context.Context, progress *ScanProgress) scanLoopAction {
	// Activate throttling at threshold
	if progress.corruptionCount == batchThrottleThreshold {
		logger.Warnf("BATCH THROTTLING ACTIVATED: Found %d corruptions in scan %s - adding delays to avoid *arr overload",
			progress.corruptionCount, progress.ID)
		progress.isThrottled = true

		s.eventBus.Publish(domain.Event{
			AggregateType: "scan",
			AggregateID:   progress.ID,
			EventType:     domain.SystemHealthDegraded,
			EventData: map[string]interface{}{
				"type":             "batch_throttling",
				"corruption_count": progress.corruptionCount,
				"path":             progress.Path,
				"message":          fmt.Sprintf("High corruption count (%d) detected - throttling remediations", progress.corruptionCount),
			},
		})
	}

	// Apply delay if throttled
	if progress.isThrottled {
		logger.Debugf("Throttling: waiting %v before next corruption event (corruption #%d)",
			batchThrottleDelay, progress.corruptionCount)

		select {
		case <-ctx.Done():
			progress.Status = "cancelled"
			return scanReturn
		case <-s.shutdownCh:
			progress.Status = "interrupted"
			return scanReturn
		case <-time.After(batchThrottleDelay):
			// Continue after delay
		}
	}

	return scanContinue
}

// =============================================================================
// Main scan loop
// =============================================================================

// scanFiles is the shared file scanning loop used by both new and resumed scans.
// The main loop orchestrates helper methods that handle specific concerns.
func (s *ScannerService) scanFiles(ctx context.Context, progress *ScanProgress, files []string, startIndex int, detectionConfig integration.DetectionConfig, autoRemediate bool, dryRun bool, scanDBID int64) {
	localPath := progress.Path
	pathID := progress.PathID

	for i := startIndex; i < len(files); i++ {
		filePath := files[i]

		// Check for cancellation or shutdown
		if action := s.checkScanCancellation(ctx, progress, localPath, i, len(files)); action == scanReturn {
			return
		}

		// Handle pause/resume
		if action := s.handleScanPause(ctx, progress, localPath, i, scanDBID); action == scanReturn {
			return
		}

		// Update progress
		progress.FilesDone = i + 1
		progress.CurrentFile = filePath

		// Emit progress and save state periodically (every 10 files)
		if i%10 == 0 || i == len(files)-1 {
			s.emitProgress(progress)
			if scanDBID > 0 {
				if _, err := s.db.Exec(`UPDATE scans SET current_file_index = ?, files_scanned = ? WHERE id = ?`, i, progress.FilesDone, scanDBID); err != nil {
					logger.Warnf("Failed to save scan progress for scan %d: %v", scanDBID, err)
				}
			}
		}

		// Get file info for tracking and safety checks
		var fileSize int64
		var fileMtime time.Time
		if info, err := os.Stat(filePath); err == nil {
			fileSize = info.Size()
			fileMtime = info.ModTime()
		}

		// Build scan file context for helper methods
		sfc := &scanFileContext{
			filePath:        filePath,
			fileSize:        fileSize,
			fileMtime:       fileMtime,
			pathID:          pathID,
			scanDBID:        scanDBID,
			autoRemediate:   autoRemediate,
			dryRun:          dryRun,
			detectionConfig: detectionConfig,
		}

		// SAFETY: Skip recently modified files (likely being written)
		if s.shouldSkipRecentlyModified(sfc) {
			continue
		}

		// SAFETY: Skip files with changing size (download in progress)
		if s.shouldSkipChangingSize(sfc) {
			continue
		}

		// Run health check
		healthy, healthErr := s.detector.CheckWithConfig(filePath, detectionConfig)

		if healthy {
			s.recordHealthyFile(sfc)
			continue
		}

		// Handle based on error type
		if healthErr.IsRecoverable() {
			if action := s.handleRecoverableError(progress, sfc, healthErr); action == scanReturn {
				return
			}
			// scanSkipToNext - continue to next file
			continue
		}

		// Handle true corruption
		if action := s.handleTrueCorruption(ctx, progress, sfc, healthErr); action == scanReturn {
			return
		}
		// scanSkipToNext means duplicate, scanContinue means event was published
	}

	progress.Status = "completed"
}

func (s *ScannerService) emitProgress(p *ScanProgress) {
	s.eventBus.Publish(domain.Event{
		AggregateType: "scan",
		AggregateID:   p.ID,
		EventType:     "ScanProgress",
		EventData: map[string]interface{}{
			"id":           p.ID,
			"type":         p.Type,
			"path":         p.Path,
			"total_files":  p.TotalFiles,
			"files_done":   p.FilesDone,
			"current_file": p.CurrentFile,
			"status":       p.Status,
			"start_time":   p.StartTime,
		},
	})
}

func (s *ScannerService) GetActiveScans() []ScanProgress {
	s.mu.Lock()
	defer s.mu.Unlock()
	scans := make([]ScanProgress, 0, len(s.activeScans))
	for _, scan := range s.activeScans {
		// Return copies to avoid race conditions with background scan goroutines
		scans = append(scans, *scan)
	}
	return scans
}

// IsPathBeingScanned checks if a scan is already in progress for the given path
func (s *ScannerService) IsPathBeingScanned(path string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, scan := range s.activeScans {
		if scan.Path == path && scan.Type == "path" {
			return true
		}
	}
	return false
}

// CancelScan cancels an ongoing scan
func (s *ScannerService) CancelScan(scanID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	scan, exists := s.activeScans[scanID]
	if !exists {
		return fmt.Errorf("scan not found")
	}

	if scan.cancel != nil {
		scan.cancel()
	}
	return nil
}

// PauseScan pauses an ongoing scan
func (s *ScannerService) PauseScan(scanID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	scan, exists := s.activeScans[scanID]
	if !exists {
		return fmt.Errorf("scan not found: %s", scanID)
	}

	if scan.isPaused {
		return nil // Already paused
	}

	if scan.Status != "scanning" {
		return fmt.Errorf("scan is not in scanning state: %s", scan.Status)
	}

	scan.isPaused = true
	scan.Status = "paused"
	return nil
}

// ResumeScan resumes a paused scan
func (s *ScannerService) ResumeScan(scanID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	scan, exists := s.activeScans[scanID]
	if !exists {
		return fmt.Errorf("scan not found: %s", scanID)
	}

	if !scan.isPaused {
		return nil // Not paused
	}

	// Signal the scan goroutine to resume
	select {
	case scan.resumeChan <- struct{}{}:
		// Successfully sent resume signal
	default:
		// Channel not ready, scan might already be resuming
	}

	return nil
}

// getScanPathConfig finds the matching scan path configuration for a file path
// Returns auto_remediate, dry_run, and any error
func (s *ScannerService) getScanPathConfig(filePath string) (autoRemediate bool, dryRun bool, err error) {
	// Find the scan path that matches this file
	rows, err := s.db.Query("SELECT local_path, auto_remediate, COALESCE(dry_run, 0) FROM scan_paths WHERE enabled = 1")
	if err != nil {
		return false, false, err
	}
	defer rows.Close()

	var bestMatchLen int
	found := false

	for rows.Next() {
		var rootPath string
		var ar, dr bool
		if err := rows.Scan(&rootPath, &ar, &dr); err != nil {
			continue
		}

		// Check if filePath starts with rootPath AND is followed by / or end of string
		// This prevents /mnt/media/TV from matching /mnt/media/TV2
		if strings.HasPrefix(filePath, rootPath) {
			remainder := filePath[len(rootPath):]
			// Valid match only if remainder is empty or starts with /
			if remainder == "" || strings.HasPrefix(remainder, "/") {
				if len(rootPath) > bestMatchLen {
					bestMatchLen = len(rootPath)
					autoRemediate = ar
					dryRun = dr
					found = true
				}
			}
		}
	}

	if !found {
		return false, false, fmt.Errorf("no matching scan path found")
	}
	return autoRemediate, dryRun, nil
}

// verifyPathAccessible performs pre-flight checks to ensure a scan path is accessible
// before starting enumeration. This prevents false positives when mounts are offline.
func (s *ScannerService) verifyPathAccessible(path string) error {
	// 1. Check if path exists
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("path does not exist: %s", path)
		}
		if os.IsPermission(err) {
			return fmt.Errorf("permission denied: %s", path)
		}
		// Check for mount-related errors
		errStr := strings.ToLower(err.Error())
		if strings.Contains(errStr, "stale") ||
			strings.Contains(errStr, "transport endpoint") ||
			strings.Contains(errStr, "no such device") {
			return fmt.Errorf("mount appears offline: %v", err)
		}
		return fmt.Errorf("cannot access path: %v", err)
	}

	// 2. Verify it's a directory
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}

	// 3. Try to list the directory (verifies mount is functional)
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("cannot read directory (mount may be stale): %v", err)
	}

	// 4. Sanity check: a media directory should usually have some entries
	// (but we don't fail on empty - it could be intentionally empty)
	if len(entries) == 0 {
		logger.Infof("Warning: scan path %s is empty", path)
	}

	// 5. Try to access a random file to verify read capability (if entries exist)
	for _, entry := range entries {
		if !entry.IsDir() {
			testPath := filepath.Join(path, entry.Name())
			_, err := os.Stat(testPath)
			if err != nil {
				// Can't access files in the directory - suspicious
				return fmt.Errorf("can list directory but cannot access files (partial mount?): %v", err)
			}
			break // One successful test is enough
		}
	}

	return nil
}

// hasActiveCorruption checks if a file already has an unresolved corruption record
// This prevents duplicate processing from webhook replays, overlapping scans, etc.
func (s *ScannerService) hasActiveCorruption(filePath string) bool {
	// Check for any CorruptionDetected event for this file that hasn't been resolved
	// A corruption is "active" if it has no VerificationSuccess or has MaxRetriesReached
	var count int
	err := s.db.QueryRow(`
		SELECT COUNT(*) FROM events e1
		WHERE e1.event_type = 'CorruptionDetected'
		AND json_extract(e1.event_data, '$.file_path') = ?
		AND e1.created_at > datetime('now', '-7 days')
		AND NOT EXISTS (
			SELECT 1 FROM events e2 
			WHERE e2.aggregate_id = e1.aggregate_id 
			AND e2.event_type IN ('VerificationSuccess', 'MaxRetriesReached')
		)
	`, filePath).Scan(&count)

	if err != nil {
		logger.Debugf("Error checking for active corruption: %v", err)
		return false // Err on the side of processing
	}

	return count > 0
}

// queueForRescan adds a file to the pending_rescans table for later retry
// when infrastructure issues are resolved
func (s *ScannerService) queueForRescan(filePath string, pathID int64, errorType, errorMessage string) {
	// Calculate next retry time with exponential backoff
	// First retry: 5 minutes, then 15, 30, 60, 120 minutes
	_, err := s.db.Exec(`
		INSERT INTO pending_rescans (file_path, path_id, error_type, error_message, next_retry_at)
		VALUES (?, ?, ?, ?, datetime('now', '+5 minutes'))
		ON CONFLICT(file_path) DO UPDATE SET
			retry_count = retry_count + 1,
			last_attempt_at = CURRENT_TIMESTAMP,
			error_type = excluded.error_type,
			error_message = excluded.error_message,
			next_retry_at = datetime('now', '+' || (5 * (1 << MIN(retry_count, 5))) || ' minutes')
	`, filePath, pathID, errorType, errorMessage)

	if err != nil {
		logger.Errorf("Failed to queue file for rescan: %s: %v", filePath, err)
	} else {
		logger.Debugf("Queued for rescan: %s", filePath)
	}
}

// StartRescanWorker starts a background worker that periodically processes pending rescans
func (s *ScannerService) StartRescanWorker() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for {
			select {
			case <-s.shutdownCh:
				logger.Infof("Rescan worker shutting down")
				return
			case <-ticker.C:
				s.processPendingRescans()
			}
		}
	}()
	logger.Infof("Rescan worker started (checks every 5 minutes)")
}

// processPendingRescans checks files that previously had infrastructure errors
func (s *ScannerService) processPendingRescans() {
	// Get files ready for retry
	rows, err := s.db.Query(`
		SELECT id, file_path, path_id, retry_count, max_retries
		FROM pending_rescans
		WHERE status = 'pending'
		AND next_retry_at <= datetime('now')
		AND retry_count < max_retries
		ORDER BY next_retry_at ASC
		LIMIT 50
	`)
	if err != nil {
		logger.Errorf("Failed to query pending rescans: %v", err)
		return
	}
	defer rows.Close()

	var filesToProcess []struct {
		id         int64
		filePath   string
		pathID     int64
		retryCount int
		maxRetries int
	}

	for rows.Next() {
		var f struct {
			id         int64
			filePath   string
			pathID     int64
			retryCount int
			maxRetries int
		}
		var pathID sql.NullInt64
		if err := rows.Scan(&f.id, &f.filePath, &pathID, &f.retryCount, &f.maxRetries); err != nil {
			logger.Errorf("Failed to scan pending rescan row: %v", err)
			continue
		}
		if pathID.Valid {
			f.pathID = pathID.Int64
		}
		filesToProcess = append(filesToProcess, f)
	}

	if len(filesToProcess) == 0 {
		return
	}

	logger.Infof("Processing %d pending rescans", len(filesToProcess))

	for _, f := range filesToProcess {
		// Check for shutdown
		select {
		case <-s.shutdownCh:
			return
		default:
		}

		// Try to scan the file
		healthy, healthErr := s.detector.Check(f.filePath, "quick")

		if healthy {
			// File is now accessible and healthy - mark as resolved
			if _, err := s.db.Exec(`
				UPDATE pending_rescans
				SET status = 'resolved', resolved_at = CURRENT_TIMESTAMP, resolution = 'healthy'
				WHERE id = ?
			`, f.id); err != nil {
				logger.Warnf("Failed to mark pending rescan %d as resolved: %v", f.id, err)
			}
			logger.Infof("Pending rescan resolved as healthy: %s", f.filePath)
			continue
		}

		if healthErr.IsRecoverable() {
			// Still having infrastructure issues - update retry time
			if _, err := s.db.Exec(`
				UPDATE pending_rescans
				SET retry_count = retry_count + 1,
				    last_attempt_at = CURRENT_TIMESTAMP,
				    error_type = ?,
				    error_message = ?,
				    next_retry_at = datetime('now', '+' || (5 * (1 << MIN(retry_count + 1, 5))) || ' minutes')
				WHERE id = ?
			`, healthErr.Type, healthErr.Message, f.id); err != nil {
				logger.Warnf("Failed to update pending rescan %d retry state: %v", f.id, err)
			}

			// Check if we've exceeded max retries
			if f.retryCount+1 >= f.maxRetries {
				if _, err := s.db.Exec(`
					UPDATE pending_rescans
					SET status = 'abandoned', resolved_at = CURRENT_TIMESTAMP, resolution = 'abandoned'
					WHERE id = ?
				`, f.id); err != nil {
					logger.Warnf("Failed to mark pending rescan %d as abandoned: %v", f.id, err)
				}
				logger.Infof("Pending rescan abandoned after %d retries: %s", f.maxRetries, f.filePath)
			} else {
				logger.Debugf("Pending rescan still inaccessible, will retry: %s", f.filePath)
			}
			continue
		}

		// File is accessible but actually corrupt - emit corruption event
		logger.Infof("Pending rescan revealed corruption: %s (Type: %s)", f.filePath, healthErr.Type)

		// Mark as resolved with corruption
		if _, err := s.db.Exec(`
			UPDATE pending_rescans
			SET status = 'resolved', resolved_at = CURRENT_TIMESTAMP, resolution = 'corrupt'
			WHERE id = ?
		`, f.id); err != nil {
			logger.Warnf("Failed to mark pending rescan %d as corrupt: %v", f.id, err)
		}

		// Get scan path config for this path
		autoRemediate, dryRun, _ := s.getScanPathConfig(f.filePath)

		// Emit corruption event
		s.eventBus.Publish(domain.Event{
			AggregateType: "corruption",
			AggregateID:   uuid.New().String(),
			EventType:     domain.CorruptionDetected,
			EventData: map[string]interface{}{
				"file_path":       f.filePath,
				"path_id":         f.pathID,
				"corruption_type": healthErr.Type,
				"error_details":   healthErr.Message,
				"source":          "rescan_worker",
				"auto_remediate":  autoRemediate,
				"dry_run":         dryRun,
			},
		})
	}
}

// GetPendingRescanStats returns statistics about pending rescans
func (s *ScannerService) GetPendingRescanStats() (pending, abandoned, resolved int, err error) {
	err = s.db.QueryRow(`
		SELECT 
			COALESCE(SUM(CASE WHEN status = 'pending' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'abandoned' THEN 1 ELSE 0 END), 0),
			COALESCE(SUM(CASE WHEN status = 'resolved' THEN 1 ELSE 0 END), 0)
		FROM pending_rescans
	`).Scan(&pending, &abandoned, &resolved)
	return
}
