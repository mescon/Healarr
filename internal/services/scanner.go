package services

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/repository"
	"github.com/mescon/Healarr/internal/safego"
)

// scannerQueryTimeout is the maximum time for database queries in scanner service.
const scannerQueryTimeout = 10 * time.Second

// ScanStatus is the typed enum of scan lifecycle states. Same pattern as
// ArrType (#192) and DetectionMode (#194): typed string + Parse + Scan +
// Value for boundary validation. Constants are UNTYPED string consts so
// the existing comparison sites (~29 of them) keep compiling against bare
// string variables without explicit casts; the type matters at API and
// DB boundaries.
type ScanStatus string

const (
	ScanStatusEnumerating = "enumerating"
	ScanStatusScanning    = "scanning"
	ScanStatusPaused      = "paused"
	ScanStatusInterrupted = "interrupted"
	ScanStatusCancelled   = "cancelled"
	ScanStatusCompleted   = "completed"
	ScanStatusAborted     = "aborted"
	ScanStatusFailed      = "failed"
	ScanStatusRunning     = "running"
	ScanStatusPending     = "pending"
)

var validScanStatuses = map[ScanStatus]bool{
	ScanStatus(ScanStatusEnumerating): true,
	ScanStatus(ScanStatusScanning):    true,
	ScanStatus(ScanStatusPaused):      true,
	ScanStatus(ScanStatusInterrupted): true,
	ScanStatus(ScanStatusCancelled):   true,
	ScanStatus(ScanStatusCompleted):   true,
	ScanStatus(ScanStatusAborted):     true,
	ScanStatus(ScanStatusFailed):      true,
	ScanStatus(ScanStatusRunning):     true,
	ScanStatus(ScanStatusPending):     true,
}

// ParseScanStatus validates and converts a raw string to ScanStatus.
func ParseScanStatus(s string) (ScanStatus, error) {
	st := ScanStatus(s)
	if !validScanStatuses[st] {
		return "", fmt.Errorf("unknown scan status %q", s)
	}
	return st, nil
}

// Scan implements sql.Scanner so ScanStatus can be passed to rows.Scan.
func (s *ScanStatus) Scan(value any) error {
	if value == nil {
		return fmt.Errorf("ScanStatus: cannot scan NULL")
	}
	var str string
	switch v := value.(type) {
	case string:
		str = v
	case []byte:
		str = string(v)
	default:
		return fmt.Errorf("ScanStatus: expected string DB value, got %T", value)
	}
	parsed, err := ParseScanStatus(str)
	if err != nil {
		return fmt.Errorf("ScanStatus.Scan: %w", err)
	}
	*s = parsed
	return nil
}

// Value implements driver.Valuer for symmetric DB writes.
func (s ScanStatus) Value() (driver.Value, error) {
	return string(s), nil
}

// defaultShutdownTimeout is how long Shutdown waits for in-flight scan
// goroutines (mostly ffprobe/ffmpeg calls) to finish before declaring
// shutdown complete. Scan progress is persisted incrementally so a hard
// cutoff is safe, but a longer default reduces the chance that an
// in-flight thorough-mode ffmpeg decode is interrupted mid-file.
const defaultShutdownTimeout = 30 * time.Second

// scannerShutdownTimeout returns the operator-configured shutdown timeout
// from HEALARR_SCANNER_SHUTDOWN_TIMEOUT (parsed via time.ParseDuration),
// or defaultShutdownTimeout if unset/invalid.
func scannerShutdownTimeout() time.Duration {
	if v := os.Getenv("HEALARR_SCANNER_SHUTDOWN_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		logger.Warnf("Invalid HEALARR_SCANNER_SHUTDOWN_TIMEOUT %q; using default %s", v, defaultShutdownTimeout)
	}
	return defaultShutdownTimeout
}

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

	// Skip hidden files (starting with .) — also covers FUSE .fuse_hidden* files
	if strings.HasPrefix(name, ".") {
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

// ScanProgress represents the current state and progress of an active scan.
//
// Direct field access is permitted ONLY for code that holds mu — writers
// must Lock/Unlock around mutations, and any reader that crosses the
// service boundary (handlers, JSON marshaling, events) MUST go through
// Snapshot() to get a value-copy ScanProgressView rather than reading
// exported fields directly. That separation closes the data race the
// audit flagged on Status (T4).
type ScanProgress struct {
	mu              sync.Mutex         `json:"-"` // Protects mutable fields during concurrent access
	ID              string             `json:"-"`
	Type            string             `json:"-"` // "path" or "file"
	Path            string             `json:"-"`
	PathID          int64              `json:"-"` // Database path ID for resumable scans
	TotalFiles      int                `json:"-"`
	FilesDone       int                `json:"-"`
	CurrentFile     string             `json:"-"`
	Status          string             `json:"-"` // "enumerating", "scanning", "paused", "interrupted", "cancelled"
	StartTime       string             `json:"-"`
	ScanDBID        int64              `json:"-"` // Database scan record ID for navigation
	cancel          context.CancelFunc `json:"-"` // Don't export in JSON
	pauseChan       chan struct{}      `json:"-"` // Channel to signal pause
	resumeChan      chan struct{}      `json:"-"` // Channel to signal resume
	isPaused        bool               `json:"-"` // Track pause state
	corruptionCount int                `json:"-"` // Track corruptions found in this scan for throttling
	isThrottled     bool               `json:"-"` // Whether this scan is being throttled
}

// ScanProgressView is a race-free value-type snapshot of a ScanProgress.
// It carries no mutex and contains only the fields appropriate for JSON
// serialization — handlers and event publishers consume this type so
// readers never touch the live (and concurrently mutated) struct.
type ScanProgressView struct {
	ID          string `json:"id"`
	Type        string `json:"type"` // "path" or "file"
	Path        string `json:"path"`
	PathID      int64  `json:"path_id,omitempty"`
	TotalFiles  int    `json:"total_files"`
	FilesDone   int    `json:"files_done"`
	CurrentFile string `json:"current_file"`
	Status      string `json:"status"`
	StartTime   string `json:"start_time"`
	ScanDBID    int64  `json:"scan_db_id,omitempty"`
}

// Snapshot returns a ScanProgressView atomically: the lock is held for the
// duration of the field reads so the view captures a consistent state, but
// the returned value carries no mutex so callers can serialize it freely.
//
// This is the only sanctioned way to read ScanProgress fields from code
// that doesn't already hold the mutex.
func (p *ScanProgress) Snapshot() ScanProgressView {
	p.mu.Lock()
	defer p.mu.Unlock()
	return ScanProgressView{
		ID:          p.ID,
		Type:        p.Type,
		Path:        p.Path,
		PathID:      p.PathID,
		TotalFiles:  p.TotalFiles,
		FilesDone:   p.FilesDone,
		CurrentFile: p.CurrentFile,
		Status:      p.Status,
		StartTime:   p.StartTime,
		ScanDBID:    p.ScanDBID,
	}
}

// scanPathConfig holds cached scan path configuration
type scanPathConfig struct {
	LocalPath     string
	AutoRemediate bool
	DryRun        bool
}

// resumeScanConfig holds all parameters needed to resume an interrupted scan
type resumeScanConfig struct {
	ScanDBID            int64
	PathID              int64
	LocalPath           string
	TotalFiles          int
	StartIndex          int
	FileListJSON        string
	DetectionConfigJSON string
	AutoRemediate       bool
	DryRun              bool
}

// scanFilesConfig holds configuration for the main scan loop
type scanFilesConfig struct {
	Files           []string
	StartIndex      int
	DetectionConfig integration.DetectionConfig
	AutoRemediate   bool
	DryRun          bool
	ScanDBID        int64
}

// Scanner defines the interface for scan operations.
// This interface enables mocking in tests while allowing the concrete
// ScannerService to be used in production.
type Scanner interface {
	ScanFile(localPath string) error
	ScanPath(pathID int64, localPath string) error
	IsPathBeingScanned(path string) bool
	GetActiveScans() []ScanProgressView
	CancelScan(scanID string) error
	PauseScan(scanID string) error
	ResumeScan(scanID string) error
	Shutdown()
}

// ScannerService manages file scanning operations for corruption detection.
type ScannerService struct {
	db            *sql.DB
	scanPaths     *repository.ScanPathRepository
	rescans       *repository.RescanRepository
	scanFilesRepo *repository.ScanFileRepository
	scans         *repository.ScanRepository
	corruptions   *repository.CorruptionRepository
	eventBus      *eventbus.EventBus
	detector      integration.HealthChecker
	pathMapper    integration.PathMapper
	activeScans   map[string]*ScanProgress
	mu            sync.Mutex
	// filesInProgress tracks individual files currently being scanned to prevent race conditions
	filesInProgress map[string]bool
	filesMu         sync.Mutex
	shutdownCh      chan struct{}
	shuttingDown    bool // Prevents new scans from starting during shutdown
	wg              sync.WaitGroup

	// Cached scan path configs to avoid N+1 queries
	scanPathCache     []scanPathConfig
	scanPathCacheMu   sync.RWMutex
	scanPathCacheTime time.Time
}

// NewScannerService creates a new ScannerService with the given dependencies.
func NewScannerService(db *sql.DB, eb *eventbus.EventBus, detector integration.HealthChecker, pm integration.PathMapper) *ScannerService {
	s := &ScannerService{
		db:              db,
		eventBus:        eb,
		detector:        detector,
		pathMapper:      pm,
		activeScans:     make(map[string]*ScanProgress),
		filesInProgress: make(map[string]bool),
		shutdownCh:      make(chan struct{}),
	}
	s.initRepositories()
	return s
}

// initRepositories populates the domain repository fields from s.db. Safe
// to call multiple times; safe when s.db is nil. Called from
// NewScannerService and also lazily from scanPathRepo() so existing tests
// that construct &ScannerService{} directly don't need a fixture update
// just to satisfy a new repo field.
func (s *ScannerService) initRepositories() {
	if s.db == nil {
		return
	}
	if s.scanPaths == nil {
		s.scanPaths = repository.NewScanPathRepository(s.db)
	}
	if s.rescans == nil {
		s.rescans = repository.NewRescanRepository(s.db)
	}
	if s.scanFilesRepo == nil {
		s.scanFilesRepo = repository.NewScanFileRepository(s.db)
	}
	if s.scans == nil {
		s.scans = repository.NewScanRepository(s.db)
	}
	if s.corruptions == nil {
		s.corruptions = repository.NewCorruptionRepository(s.db)
	}
}

// corruptionRepo returns the lazy-initialized CorruptionRepository.
func (s *ScannerService) corruptionRepo() *repository.CorruptionRepository {
	s.initRepositories()
	return s.corruptions
}

// scanPathRepo returns the lazy-initialized ScanPathRepository.
func (s *ScannerService) scanPathRepo() *repository.ScanPathRepository {
	s.initRepositories()
	return s.scanPaths
}

// rescanRepo returns the lazy-initialized RescanRepository.
func (s *ScannerService) rescanRepo() *repository.RescanRepository {
	s.initRepositories()
	return s.rescans
}

// scanFileRepo returns the lazy-initialized ScanFileRepository.
func (s *ScannerService) scanFileRepo() *repository.ScanFileRepository {
	s.initRepositories()
	return s.scanFilesRepo
}

// scanRepo returns the lazy-initialized ScanRepository.
func (s *ScannerService) scanRepo() *repository.ScanRepository {
	s.initRepositories()
	return s.scans
}

// IsFileBeingScanned returns true if the given file is currently being scanned.
// This can be used by other services (like the verifier) to avoid race conditions.
func (s *ScannerService) IsFileBeingScanned(localPath string) bool {
	s.filesMu.Lock()
	defer s.filesMu.Unlock()
	return s.filesInProgress[localPath]
}

// Shutdown gracefully stops all active scans by saving their state for later resumption.
func (s *ScannerService) Shutdown() {
	logger.Infof("Scanner: initiating graceful shutdown...")

	// Set shutdown flag under lock to prevent new scans from starting
	s.mu.Lock()
	s.shuttingDown = true
	s.mu.Unlock()

	close(s.shutdownCh)

	// Save state for all active scans and cancel them
	s.mu.Lock()
	for scanID, scan := range s.activeScans {
		if scan.Type == "path" && scan.ScanDBID > 0 {
			// Lock to safely read mutable fields (fixes data race with markFileProcessed)
			scan.mu.Lock()
			filesDone := scan.FilesDone
			totalFiles := scan.TotalFiles
			scanDBID := scan.ScanDBID
			scan.mu.Unlock()

			logger.Infof("Scanner: saving state for scan %s (file %d/%d)", scanID, filesDone, totalFiles)
			// Mark as interrupted in database - state is already saved during scanning
			ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
			err := s.scanRepo().MarkInterrupted(ctx, scanDBID, filesDone)
			cancel()
			if err != nil {
				logger.Errorf("Failed to save scan state for %s: %v", scanID, err)
			}
		}
		if scan.cancel != nil {
			scan.cancel()
		}
	}
	s.mu.Unlock()

	// Wait for in-flight scan goroutines to finish. Scan-record state is
	// already persisted in the for-loop above, so an interrupted goroutine
	// only loses progress within its current per-file ffprobe call.
	done := make(chan struct{})
	safego.Run("scanner-shutdown-wait", func() {
		s.wg.Wait()
		close(done)
	})

	timeout := scannerShutdownTimeout()
	select {
	case <-done:
		logger.Infof("Scanner: all scans stopped")
	case <-time.After(timeout):
		logger.Warnf("Scanner: %s shutdown timeout reached; in-flight per-file work may have been interrupted (scan records already marked interrupted at file boundaries; resumption will pick up there)", timeout)
	}

	logger.Infof("Scanner: shutdown complete")
}

// ResumeInterruptedScans checks for scans that were interrupted by shutdown and resumes them.
func (s *ScannerService) ResumeInterruptedScans() {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	scansToResume, err := s.scanRepo().ListInterrupted(ctx)
	if err != nil {
		logger.Errorf("Failed to query interrupted scans: %v", err)
		return
	}

	for _, scan := range scansToResume {
		logger.Infof("Resuming interrupted scan for %s (starting at file %d/%d)", scan.Path, scan.CurrentFileIndex, scan.TotalFiles)
		s.wg.Add(1)
		cfg := resumeScanConfig{
			ScanDBID:            scan.ID,
			PathID:              scan.PathID.Int64,
			LocalPath:           scan.Path,
			TotalFiles:          scan.TotalFiles,
			StartIndex:          scan.CurrentFileIndex,
			FileListJSON:        scan.FileListJSON,
			DetectionConfigJSON: scan.DetectionConfigJSON.String,
			AutoRemediate:       scan.AutoRemediate,
			DryRun:              scan.DryRun,
		}
		safego.Run("scanner-resume", func() { s.resumeScan(cfg) })
	}
}

// resumeScan continues a previously interrupted scan
func (s *ScannerService) resumeScan(cfg resumeScanConfig) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	defer s.wg.Done()

	// Parse file list
	var files []string
	if err := json.Unmarshal([]byte(cfg.FileListJSON), &files); err != nil {
		logger.Errorf("Failed to parse file list for resumed scan: %v", err)
		return
	}

	// Parse detection config
	var detectionConfig integration.DetectionConfig
	if cfg.DetectionConfigJSON != "" {
		if err := json.Unmarshal([]byte(cfg.DetectionConfigJSON), &detectionConfig); err != nil {
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
		Path:        cfg.LocalPath,
		PathID:      cfg.PathID,
		TotalFiles:  cfg.TotalFiles,
		FilesDone:   cfg.StartIndex,
		CurrentFile: "",
		Status:      ScanStatusScanning,
		StartTime:   time.Now().Format(time.RFC3339),
		ScanDBID:    cfg.ScanDBID,
		pauseChan:   make(chan struct{}),
		resumeChan:  make(chan struct{}),
		isPaused:    false,
	}
	progress.cancel = cancel

	s.mu.Lock()
	s.activeScans[scanID] = progress
	s.mu.Unlock()

	// Update scan status to running
	statusCtx, statusCancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	err := s.scanRepo().SetStatus(statusCtx, cfg.ScanDBID, string(ScanStatusRunning))
	statusCancel()
	if err != nil {
		logger.Errorf("Failed to update scan status: %v", err)
	}

	defer func() {
		finalStatus := ScanStatusCompleted
		if progress.Status == ScanStatusCancelled || progress.Status == ScanStatusInterrupted {
			finalStatus = progress.Status
		}
		deferCtx, deferCancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
		err := s.scanRepo().Finalize(deferCtx, cfg.ScanDBID, finalStatus, progress.FilesDone)
		deferCancel()
		if err != nil {
			logger.Errorf("Failed to update scan record: %v", err)
		}

		s.mu.Lock()
		delete(s.activeScans, scanID)
		s.mu.Unlock()

		if err := s.eventBus.Publish(domain.Event{
			AggregateType: "scan",
			AggregateID:   scanID,
			EventType:     "ScanCompleted",
			EventData: map[string]interface{}{
				"scan_id": scanID,
				"status":  finalStatus,
				"resumed": true,
			},
		}); err != nil {
			logger.Errorf("Failed to publish ScanCompleted event for resumed scan %s: %v", scanID, err)
		}
	}()

	s.emitProgress(progress)
	logger.Infof("Resumed scan %s for %s at file %d/%d", scanID, cfg.LocalPath, cfg.StartIndex, cfg.TotalFiles)

	// Continue scanning from where we left off
	s.scanFiles(ctx, progress, scanFilesConfig{
		Files:           files,
		StartIndex:      cfg.StartIndex,
		DetectionConfig: detectionConfig,
		AutoRemediate:   cfg.AutoRemediate,
		DryRun:          cfg.DryRun,
		ScanDBID:        cfg.ScanDBID,
	})
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
		Status:      ScanStatusScanning,
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
		if err := s.eventBus.Publish(domain.Event{
			AggregateType: "scan",
			AggregateID:   scanID,
			EventType:     "ScanCompleted", // Custom event type for now
			EventData: map[string]interface{}{
				"scan_id": scanID,
				"status":  ScanStatusCompleted,
			},
		}); err != nil {
			logger.Errorf("Failed to publish ScanCompleted event for file scan %s: %v", scanID, err)
		}
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

	// Capture file size before health check (for enriched corruption data)
	var fileSize int64
	if info, err := os.Stat(localPath); err == nil {
		fileSize = info.Size()
	}

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

		// Emit event - critical entry point for remediation journey, use retry
		err := s.eventBus.PublishWithRetry(domain.Event{
			AggregateType: "corruption",
			AggregateID:   uuid.New().String(),
			EventType:     domain.CorruptionDetected,
			EventData: map[string]interface{}{
				"file_path":       localPath,
				"file_size":       fileSize,
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

// =============================================================================
// ScanPath helpers - extracted to reduce cognitive complexity
// =============================================================================

// scanPathSettings holds the configuration for a scan path (detection settings, remediation flags)
type scanPathSettings struct {
	AutoRemediate   bool
	DryRun          bool
	DetectionConfig integration.DetectionConfig
}

// loadScanPathSettings loads the scan configuration from the database
func (s *ScannerService) loadScanPathSettings(pathID int64) scanPathSettings {
	var autoRemediate, dryRun bool
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	path, err := s.scanPathRepo().GetByID(ctx, pathID)
	detectionMethod := "ffprobe"
	detectionMode := "quick"
	if err != nil {
		logger.Errorf("Error querying scan path config: %v", err)
	} else {
		autoRemediate = path.AutoRemediate
		dryRun = path.DryRun
		detectionMethod = path.DetectionMethod
		detectionMode = path.DetectionMode
	}

	var detectionArgs []string
	if err == nil && path.DetectionArgs.Valid && path.DetectionArgs.String != "" {
		if err := json.Unmarshal([]byte(path.DetectionArgs.String), &detectionArgs); err != nil {
			logger.Errorf("Error parsing detection args: %v", err)
		}
	}

	return scanPathSettings{
		AutoRemediate: autoRemediate,
		DryRun:        dryRun,
		DetectionConfig: integration.DetectionConfig{
			Method: integration.DetectionMethod(detectionMethod),
			Args:   detectionArgs,
			Mode:   detectionMode,
		},
	}
}

// walkStats tracks statistics during directory enumeration
type walkStats struct {
	files        []string
	skippedCount int
	symlinkCount int
}

// classifyEntry determines whether a file should be included as a media file.
// Uses fs.DirEntry to correctly detect symlinks (unlike os.FileInfo from filepath.Walk).
// Returns: (isMedia, isSkipped, isSymlink)
func classifyEntry(filePath string, d fs.DirEntry) (isMedia, isSkipped, isSymlink bool) {
	// DirEntry.Type() correctly returns ModeSymlink for symlinks
	if d.Type()&os.ModeSymlink != 0 {
		return false, false, true
	}
	if d.IsDir() {
		return false, false, false
	}
	if isHiddenOrTempFile(filePath) {
		return false, true, false
	}
	if isMediaFile(filePath) {
		return true, false, false
	}
	return false, true, false
}

// enumerateMediaFiles walks the directory and returns a list of media files.
// Uses filepath.WalkDir to correctly detect symlinks.
func (s *ScannerService) enumerateMediaFiles(localPath string) ([]string, error) {
	stats := walkStats{}

	err := filepath.WalkDir(localPath, func(filePath string, d fs.DirEntry, err error) error {
		if err != nil {
			return s.handleWalkError(filePath, err)
		}
		isMedia, isSkipped, isSymlink := classifyEntry(filePath, d)
		switch {
		case isSymlink:
			stats.symlinkCount++
		case isSkipped:
			stats.skippedCount++
		case isMedia:
			stats.files = append(stats.files, filePath)
		}
		return nil
	})

	if err == nil && (stats.skippedCount > 0 || stats.symlinkCount > 0) {
		logger.Debugf("Skipped %d non-media/hidden files and %d symlinks in %s", stats.skippedCount, stats.symlinkCount, localPath)
	}

	return stats.files, err
}

// handleWalkError handles errors during file system traversal
func (s *ScannerService) handleWalkError(filePath string, err error) error {
	if os.IsPermission(err) {
		logger.Debugf("Permission denied: %s", filePath)
		return nil
	}
	return err
}

// recordScanStart inserts the scan record into the database and returns the scan ID
func (s *ScannerService) recordScanStart(localPath string, pathID int64, files []string, cfg scanPathSettings) int64 {
	fileListJSON, err := json.Marshal(files)
	if err != nil {
		logger.Errorf("Failed to serialize file list: %v", err)
		fileListJSON = []byte("[]")
	}

	detectionConfigJSON, err := json.Marshal(cfg.DetectionConfig)
	if err != nil {
		logger.Errorf("Failed to serialize detection config: %v", err)
		detectionConfigJSON = []byte("{}")
	}

	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	scanDBID, err := s.scanRepo().Create(ctx, repository.CreateScanParams{
		Path:                localPath,
		PathID:              pathID,
		TotalFiles:          len(files),
		FileListJSON:        string(fileListJSON),
		DetectionConfigJSON: string(detectionConfigJSON),
		AutoRemediate:       cfg.AutoRemediate,
		DryRun:              cfg.DryRun,
	})
	if err != nil {
		logger.Errorf("Failed to record scan start: %v", err)
		return 0
	}
	return scanDBID
}

// handlePathInaccessible reports that a path is not accessible
func (s *ScannerService) handlePathInaccessible(scanID, localPath string, accessErr error) error {
	s.mu.Lock()
	delete(s.activeScans, scanID)
	s.mu.Unlock()

	if pubErr := s.eventBus.Publish(domain.Event{
		AggregateType: "system",
		AggregateID:   scanID,
		EventType:     domain.SystemHealthDegraded,
		EventData: map[string]interface{}{
			"path":    localPath,
			"reason":  "Scan path is inaccessible",
			"details": accessErr.Error(),
		},
	}); pubErr != nil {
		logger.Errorf("Failed to publish SystemHealthDegraded event: %v", pubErr)
	}

	return fmt.Errorf("scan path inaccessible: %w", accessErr)
}

// finalizeScan handles the cleanup when a scan completes
func (s *ScannerService) finalizeScan(scanID string, progress *ScanProgress, scanDBID int64) {
	if progress.Status != ScanStatusInterrupted {
		finalStatus := ScanStatusCompleted
		if progress.Status == ScanStatusCancelled {
			finalStatus = ScanStatusCancelled
		}
		if scanDBID > 0 {
			ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
			err := s.scanRepo().Finalize(ctx, scanDBID, finalStatus, progress.FilesDone)
			cancel()
			if err != nil {
				logger.Errorf("Failed to update scan record: %v", err)
			}
		}
	}

	s.mu.Lock()
	delete(s.activeScans, scanID)
	s.mu.Unlock()

	if err := s.eventBus.Publish(domain.Event{
		AggregateType: "scan",
		AggregateID:   scanID,
		EventType:     "ScanCompleted",
		EventData: map[string]interface{}{
			"scan_id": scanID,
			"status":  progress.Status,
		},
	}); err != nil {
		logger.Errorf("Failed to publish ScanCompleted event for path scan %s: %v", scanID, err)
	}
}

// ScanPath scans all media files in the given directory path for corruption.
func (s *ScannerService) ScanPath(pathID int64, localPath string) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Check if shutdown is in progress before starting scan
	s.mu.Lock()
	if s.shuttingDown {
		s.mu.Unlock()
		return fmt.Errorf("scanner is shutting down")
	}
	s.wg.Add(1)
	s.mu.Unlock()
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
		Status:      ScanStatusEnumerating,
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

	// Load configuration
	cfg := s.loadScanPathSettings(pathID)
	logger.Infof("Starting scan for path ID %d: %s", pathID, localPath)

	// Pre-flight check
	if err := s.verifyPathAccessible(localPath); err != nil {
		logger.Errorf("Pre-flight check failed for path %s: %v - scan aborted", localPath, err)
		return s.handlePathInaccessible(scanID, localPath, err)
	}

	// Enumerate files
	files, err := s.enumerateMediaFiles(localPath)
	if err != nil {
		s.mu.Lock()
		delete(s.activeScans, scanID)
		s.mu.Unlock()
		return err
	}

	progress.TotalFiles = len(files)
	progress.Status = ScanStatusScanning

	// Record scan start
	scanDBID := s.recordScanStart(localPath, pathID, files, cfg)
	progress.ScanDBID = scanDBID
	s.emitProgress(progress)

	defer s.finalizeScan(scanID, progress, scanDBID)

	// Scan files starting from index 0
	s.scanFiles(ctx, progress, scanFilesConfig{
		Files:           files,
		StartIndex:      0,
		DetectionConfig: cfg.DetectionConfig,
		AutoRemediate:   cfg.AutoRemediate,
		DryRun:          cfg.DryRun,
		ScanDBID:        scanDBID,
	})
	return nil
}

// =============================================================================
// scanFiles helpers - extracted for clarity and testability
// =============================================================================

// scanFileContext holds the context for scanning a single file.
// This reduces parameter passing and groups related data together.
type scanFileContext struct {
	filePath          string
	fileSize          int64
	fileMtime         time.Time
	pathID            int64
	scanDBID          int64
	autoRemediate     bool
	dryRun            bool
	detectionConfig   integration.DetectionConfig
	activeCorruptions map[string]bool // Preloaded map of file paths with active corruptions
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
		progress.Status = ScanStatusCancelled
		s.emitProgress(progress)
		return scanReturn
	case <-s.shutdownCh:
		logger.Infof("Scan interrupted for graceful shutdown: %s (at file %d/%d)", localPath, fileIndex, totalFiles)
		progress.Status = ScanStatusInterrupted
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
		pauseCtx, pauseCancel := context.WithTimeout(ctx, scannerQueryTimeout)
		if err := s.scanRepo().MarkPaused(pauseCtx, scanDBID, fileIndex); err != nil {
			logger.Warnf("Failed to update scan pause state for scan %d: %v", scanDBID, err)
		}
		pauseCancel()
	}

	// Wait for resume or cancel
	select {
	case <-progress.resumeChan:
		logger.Infof("Scan resumed: %s", localPath)
		s.mu.Lock()
		progress.Status = ScanStatusScanning
		progress.isPaused = false
		s.mu.Unlock()
		if scanDBID > 0 {
			resumeCtx, resumeCancel := context.WithTimeout(ctx, scannerQueryTimeout)
			if err := s.scanRepo().SetStatus(resumeCtx, scanDBID, string(ScanStatusRunning)); err != nil {
				logger.Warnf("Failed to update scan resume state for scan %d: %v", scanDBID, err)
			}
			resumeCancel()
		}
		s.emitProgress(progress)
		return scanContinue
	case <-ctx.Done():
		logger.Infof("Scan cancelled while paused: %s", localPath)
		progress.Status = ScanStatusCancelled
		s.emitProgress(progress)
		return scanReturn
	case <-s.shutdownCh:
		logger.Infof("Scan interrupted during pause: %s", localPath)
		progress.Status = ScanStatusInterrupted
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
			if err := s.scanFileRepo().Record(context.Background(), sfc.scanDBID, repository.ScanFileRecord{
				FilePath:       sfc.filePath,
				Status:         "skipped",
				CorruptionType: "RecentlyModified",
				ErrorDetails:   "File modified within last 2 minutes - likely still being written",
				FileSize:       sfc.fileSize,
			}); err != nil {
				logger.Debugf("Failed to record skipped file (recently modified): %v", err)
			}
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
				if err := s.scanFileRepo().Record(context.Background(), sfc.scanDBID, repository.ScanFileRecord{
					FilePath:       sfc.filePath,
					Status:         "skipped",
					CorruptionType: "SizeChanging",
					ErrorDetails:   "File size changed during scan - active download/copy",
					FileSize:       sfc.fileSize,
				}); err != nil {
					// scan_files rows drive the UI scan-detail screen; losing
					// writes silently produces empty scan reports. Log loud.
					logger.Errorf("Failed to record skipped file (size changing): %v", err)
				}
			}
			return true
		}
	}
	return false
}

// recordHealthyFile records a healthy file in the scan_files table.
func (s *ScannerService) recordHealthyFile(sfc *scanFileContext) {
	if sfc.scanDBID > 0 {
		err := s.scanFileRepo().Record(context.Background(), sfc.scanDBID, repository.ScanFileRecord{
			FilePath: sfc.filePath,
			Status:   "healthy",
			FileSize: sfc.fileSize,
		})
		if err != nil {
			// scan_files rows drive the UI scan-detail screen; losing writes
			// silently produces "0 healthy, 0 corrupt" reports.
			logger.Errorf("Failed to record healthy file: %v", err)
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
		err := s.scanFileRepo().Record(context.Background(), sfc.scanDBID, repository.ScanFileRecord{
			FilePath:       sfc.filePath,
			Status:         "inaccessible",
			CorruptionType: healthErr.Type,
			ErrorDetails:   healthErr.Message,
			FileSize:       sfc.fileSize,
		})
		if err != nil {
			logger.Errorf("Failed to record inaccessible file: %v", err)
		}
	}

	// Queue file for rescan when infrastructure is back
	s.queueForRescan(sfc.filePath, sfc.pathID, healthErr.Type, healthErr.Message)

	// Check if mount is lost - abort scan to prevent false positives
	if healthErr.Type == integration.ErrorTypeMountLost {
		logger.Errorf("Mount appears to be offline for path: %s - aborting scan to prevent false positives", progress.Path)
		progress.Status = ScanStatusAborted

		if sfc.scanDBID > 0 {
			if err := s.scanRepo().MarkAborted(context.Background(), sfc.scanDBID,
				"Scan aborted: filesystem/mount became inaccessible"); err != nil {
				logger.Warnf("Failed to update scan abort state for scan %d: %v", sfc.scanDBID, err)
			}
		}

		// Emit system health event
		if err := s.eventBus.Publish(domain.Event{
			AggregateType: "system",
			AggregateID:   progress.ID,
			EventType:     domain.SystemHealthDegraded,
			EventData: map[string]interface{}{
				"path":    progress.Path,
				"reason":  "Mount or filesystem became inaccessible during scan",
				"details": healthErr.Message,
			},
		}); err != nil {
			logger.Errorf("Failed to publish SystemHealthDegraded event: %v", err)
		}
		return scanReturn
	}

	return scanSkipToNext
}

// handleTrueCorruption processes a file that is actually corrupted.
// Returns scanReturn if scan should stop, scanSkipToNext if file was duplicate, scanContinue otherwise.
func (s *ScannerService) handleTrueCorruption(ctx context.Context, progress *ScanProgress, sfc *scanFileContext, healthErr *integration.HealthCheckError) scanLoopAction {
	logger.Infof("Corruption detected in file: %s (Type: %s)", sfc.filePath, healthErr.Type)

	// DEDUPLICATION: Check if already being processed
	// Use preloaded map for path scans (O(1) lookup), fall back to query for single-file scans
	hasActive := false
	if sfc.activeCorruptions != nil {
		hasActive = sfc.activeCorruptions[sfc.filePath]
	} else {
		hasActive = s.hasActiveCorruption(sfc.filePath)
	}
	if hasActive {
		logger.Infof("Skipping duplicate corruption for file already being processed: %s", sfc.filePath)
		if sfc.scanDBID > 0 {
			if err := s.scanFileRepo().Record(context.Background(), sfc.scanDBID, repository.ScanFileRecord{
				FilePath:       sfc.filePath,
				Status:         "skipped",
				CorruptionType: "AlreadyProcessing",
				ErrorDetails:   "File already has active corruption record",
				FileSize:       sfc.fileSize,
			}); err != nil {
				logger.Debugf("Failed to record skipped file (already processing): %v", err)
			}
		}
		return scanSkipToNext
	}

	// Record corrupt file
	if sfc.scanDBID > 0 {
		err := s.scanFileRepo().Record(context.Background(), sfc.scanDBID, repository.ScanFileRecord{
			FilePath:       sfc.filePath,
			Status:         "corrupt",
			CorruptionType: healthErr.Type,
			ErrorDetails:   healthErr.Message,
			FileSize:       sfc.fileSize,
		})
		if err != nil {
			logger.Debugf("Failed to record corrupt file: %v", err)
		}

		// Update corruptions count
		if err := s.scanRepo().IncrementCorruptions(sfc.scanDBID); err != nil {
			logger.Warnf("Failed to update corruptions count for scan %d: %v", sfc.scanDBID, err)
		}
	}

	// Track for throttling
	progress.corruptionCount++

	// Check if we need to apply batch throttling
	if action := s.applyBatchThrottling(ctx, progress); action != scanContinue {
		return action
	}

	// Emit corruption event for remediation - critical entry point, use retry
	err := s.eventBus.PublishWithRetry(domain.Event{
		AggregateType: "corruption",
		AggregateID:   uuid.New().String(),
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path":       sfc.filePath,
			"file_size":       sfc.fileSize,
			"path_id":         sfc.pathID,
			"corruption_type": healthErr.Type,
			"error_details":   healthErr.Message,
			"auto_remediate":  sfc.autoRemediate,
			"dry_run":         sfc.dryRun,
			"batch_throttled": progress.isThrottled,
		},
	})
	if err != nil {
		logger.Errorf("Failed to publish corruption event after retries: %v", err)
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

		if err := s.eventBus.Publish(domain.Event{
			AggregateType: "scan",
			AggregateID:   progress.ID,
			EventType:     domain.SystemHealthDegraded,
			EventData: map[string]interface{}{
				"type":             "batch_throttling",
				"corruption_count": progress.corruptionCount,
				"path":             progress.Path,
				"message":          fmt.Sprintf("High corruption count (%d) detected - throttling remediations", progress.corruptionCount),
			},
		}); err != nil {
			logger.Errorf("Failed to publish batch throttling event: %v", err)
		}
	}

	// Apply delay if throttled
	if progress.isThrottled {
		logger.Debugf("Throttling: waiting %v before next corruption event (corruption #%d)",
			batchThrottleDelay, progress.corruptionCount)

		select {
		case <-ctx.Done():
			progress.Status = ScanStatusCancelled
			return scanReturn
		case <-s.shutdownCh:
			progress.Status = ScanStatusInterrupted
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
func (s *ScannerService) scanFiles(ctx context.Context, progress *ScanProgress, cfg scanFilesConfig) {
	// PERFORMANCE: Preload active corruptions in a single query to avoid N+1 problem
	activeCorruptions := s.LoadActiveCorruptionsForPath(progress.Path)

	for i := cfg.StartIndex; i < len(cfg.Files); i++ {
		action := s.processFileInScan(ctx, progress, cfg, i, activeCorruptions)
		if action == scanReturn {
			return
		}
	}

	progress.Status = "completed"
}

// processFileInScan handles all processing for a single file during a scan.
// Returns scanReturn if the scan should stop, scanContinue to proceed to the next file.
func (s *ScannerService) processFileInScan(
	ctx context.Context,
	progress *ScanProgress,
	cfg scanFilesConfig,
	fileIndex int,
	activeCorruptions map[string]bool,
) scanLoopAction {
	filePath := cfg.Files[fileIndex]

	// RACE PREVENTION: Check if file is being scanned by another goroutine (e.g., webhook)
	// This prevents duplicate scans when a bulk ScanPath and individual ScanFile overlap.
	s.filesMu.Lock()
	if s.filesInProgress[filePath] {
		s.filesMu.Unlock()
		logger.Debugf("Skipping file already being scanned: %s", filePath)
		s.markFileProcessed(progress, fileIndex, cfg.ScanDBID)
		return scanContinue
	}
	s.filesInProgress[filePath] = true
	s.filesMu.Unlock()

	// Ensure cleanup when done with this file
	defer func() {
		s.filesMu.Lock()
		delete(s.filesInProgress, filePath)
		s.filesMu.Unlock()
	}()

	// Check for cancellation or shutdown
	if s.checkScanCancellation(ctx, progress, progress.Path, fileIndex, len(cfg.Files)) == scanReturn {
		return scanReturn
	}

	// Handle pause/resume
	if s.handleScanPause(ctx, progress, progress.Path, fileIndex, cfg.ScanDBID) == scanReturn {
		return scanReturn
	}

	// Update progress
	progress.mu.Lock()
	progress.CurrentFile = filePath
	progress.mu.Unlock()
	s.emitProgress(progress)

	// Build scan file context
	sfc := s.buildScanFileContext(filePath, progress.PathID, cfg, activeCorruptions)

	// Process the file and return result
	return s.checkAndHandleFile(ctx, progress, cfg, fileIndex, sfc)
}

// buildScanFileContext creates the context struct for file processing.
func (s *ScannerService) buildScanFileContext(
	filePath string,
	pathID int64,
	cfg scanFilesConfig,
	activeCorruptions map[string]bool,
) *scanFileContext {
	var fileSize int64
	var fileMtime time.Time
	if info, err := os.Stat(filePath); err == nil {
		fileSize = info.Size()
		fileMtime = info.ModTime()
	}

	return &scanFileContext{
		filePath:          filePath,
		fileSize:          fileSize,
		fileMtime:         fileMtime,
		pathID:            pathID,
		scanDBID:          cfg.ScanDBID,
		autoRemediate:     cfg.AutoRemediate,
		dryRun:            cfg.DryRun,
		detectionConfig:   cfg.DetectionConfig,
		activeCorruptions: activeCorruptions,
	}
}

// checkAndHandleFile performs safety checks and health verification for a file.
func (s *ScannerService) checkAndHandleFile(
	ctx context.Context,
	progress *ScanProgress,
	cfg scanFilesConfig,
	fileIndex int,
	sfc *scanFileContext,
) scanLoopAction {
	// SAFETY: Skip recently modified files (likely being written)
	if s.shouldSkipRecentlyModified(sfc) {
		s.markFileProcessed(progress, fileIndex, cfg.ScanDBID)
		return scanContinue
	}

	// SAFETY: Skip files with changing size (download in progress)
	if s.shouldSkipChangingSize(sfc) {
		s.markFileProcessed(progress, fileIndex, cfg.ScanDBID)
		return scanContinue
	}

	// Run health check
	healthy, healthErr := s.detector.CheckWithConfig(sfc.filePath, cfg.DetectionConfig)

	if healthy {
		// In thorough mode, run content analysis on structurally healthy files
		if cfg.DetectionConfig.Mode == integration.ModeThorough {
			healthy, healthErr = s.detector.AnalyzeContent(sfc.filePath)
			if !healthy {
				return s.handleHealthCheckResult(ctx, progress, cfg, fileIndex, sfc, healthErr)
			}
		}
		s.recordHealthyFile(sfc)
		s.markFileProcessed(progress, fileIndex, cfg.ScanDBID)
		return scanContinue
	}

	// Handle the health check result
	return s.handleHealthCheckResult(ctx, progress, cfg, fileIndex, sfc, healthErr)
}

// handleHealthCheckResult processes the result of a failed health check.
func (s *ScannerService) handleHealthCheckResult(
	ctx context.Context,
	progress *ScanProgress,
	cfg scanFilesConfig,
	fileIndex int,
	sfc *scanFileContext,
	healthErr *integration.HealthCheckError,
) scanLoopAction {
	// Handle recoverable errors (infrastructure issues)
	if healthErr.IsRecoverable() {
		if s.handleRecoverableError(progress, sfc, healthErr) == scanReturn {
			return scanReturn
		}
		s.markFileProcessed(progress, fileIndex, cfg.ScanDBID)
		return scanContinue
	}

	// Handle true corruption
	if s.handleTrueCorruption(ctx, progress, sfc, healthErr) == scanReturn {
		return scanReturn
	}
	s.markFileProcessed(progress, fileIndex, cfg.ScanDBID)
	return scanContinue
}

// markFileProcessed increments the file counter and saves progress periodically
func (s *ScannerService) markFileProcessed(progress *ScanProgress, fileIndex int, scanDBID int64) {
	// Lock to safely update mutable fields (fixes data race with GetActiveScans/Shutdown)
	progress.mu.Lock()
	progress.FilesDone++
	filesDone := progress.FilesDone
	progress.mu.Unlock()

	// Save state to database periodically (every 10 files) to avoid excessive I/O
	if fileIndex%10 == 0 {
		if scanDBID > 0 {
			progressCtx, progressCancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
			if err := s.scanRepo().UpdateProgress(progressCtx, scanDBID, fileIndex, filesDone); err != nil {
				logger.Warnf("Failed to save scan progress for scan %d: %v", scanDBID, err)
			}
			progressCancel()
		}
	}
}

func (s *ScannerService) emitProgress(p *ScanProgress) {
	// Snapshot captures fields atomically under the per-scan mutex so the
	// event sees a consistent state; the publish happens outside the lock
	// because Publish does I/O.
	view := p.Snapshot()
	if err := s.eventBus.Publish(domain.Event{
		AggregateType: "scan",
		AggregateID:   view.ID,
		EventType:     domain.ScanProgress,
		EventData: map[string]interface{}{
			"id":           view.ID,
			"type":         view.Type,
			"path":         view.Path,
			"total_files":  view.TotalFiles,
			"files_done":   view.FilesDone,
			"current_file": view.CurrentFile,
			"status":       view.Status,
			"start_time":   view.StartTime,
			"scan_db_id":   view.ScanDBID, // Database ID for frontend navigation
		},
	}); err != nil {
		logger.Debugf("Failed to emit scan progress: %v", err)
	}
}

// GetActiveScans returns race-free snapshots of all currently active scans.
// Each view is captured under the per-scan mutex so the snapshot is
// internally consistent; the returned slice itself is safe to mutate by
// the caller.
func (s *ScannerService) GetActiveScans() []ScanProgressView {
	s.mu.Lock()
	defer s.mu.Unlock()
	views := make([]ScanProgressView, 0, len(s.activeScans))
	for _, scan := range s.activeScans {
		views = append(views, scan.Snapshot())
	}
	return views
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

const scanPathCacheTTL = 60 * time.Second

// refreshScanPathCache loads all enabled scan paths into memory cache.
// Cache expires after scanPathCacheTTL to pick up config changes.
func (s *ScannerService) refreshScanPathCache() error {
	s.scanPathCacheMu.Lock()
	defer s.scanPathCacheMu.Unlock()

	if time.Since(s.scanPathCacheTime) < scanPathCacheTTL && len(s.scanPathCache) > 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	rows, err := s.scanPathRepo().ListEnabled(ctx)
	if err != nil {
		return err
	}

	cache := make([]scanPathConfig, 0, len(rows))
	for _, p := range rows {
		cache = append(cache, scanPathConfig{
			LocalPath:     p.LocalPath,
			AutoRemediate: p.AutoRemediate,
			DryRun:        p.DryRun,
		})
	}

	s.scanPathCache = cache
	s.scanPathCacheTime = time.Now()
	return nil
}

// InvalidateScanPathCache clears the scan path cache, forcing a reload on next access.
// Call this when scan paths are added/modified/deleted.
func (s *ScannerService) InvalidateScanPathCache() {
	s.scanPathCacheMu.Lock()
	defer s.scanPathCacheMu.Unlock()
	s.scanPathCacheTime = time.Time{} // Zero time forces refresh
}

// getScanPathConfig finds the matching scan path configuration for a file path.
// Uses cached scan paths to avoid N+1 query problem (was: 1 query per file).
// Returns auto_remediate, dry_run, and any error.
func (s *ScannerService) getScanPathConfig(filePath string) (autoRemediate bool, dryRun bool, err error) {
	// Ensure cache is fresh
	if err := s.refreshScanPathCache(); err != nil {
		return false, false, err
	}

	s.scanPathCacheMu.RLock()
	defer s.scanPathCacheMu.RUnlock()

	var bestMatchLen int
	found := false

	for _, cfg := range s.scanPathCache {
		// Check if filePath starts with rootPath AND is followed by / or end of string
		// This prevents /mnt/media/TV from matching /mnt/media/TV2
		if strings.HasPrefix(filePath, cfg.LocalPath) {
			remainder := filePath[len(cfg.LocalPath):]
			// Valid match only if remainder is empty or starts with /
			if remainder == "" || strings.HasPrefix(remainder, "/") {
				if len(cfg.LocalPath) > bestMatchLen {
					bestMatchLen = len(cfg.LocalPath)
					autoRemediate = cfg.AutoRemediate
					dryRun = cfg.DryRun
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
		return s.classifyStatError(path, err)
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
	return s.testFileAccess(path, entries)
}

// classifyStatError returns an appropriate error based on the type of stat failure
func (s *ScannerService) classifyStatError(path string, err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("path does not exist: %s", path)
	}
	if os.IsPermission(err) {
		return fmt.Errorf("permission denied: %s", path)
	}
	if s.isMountError(err) {
		return fmt.Errorf("mount appears offline: %v", err)
	}
	return fmt.Errorf("cannot access path: %v", err)
}

// isMountError checks if an error indicates a mount-related problem
func (s *ScannerService) isMountError(err error) bool {
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "stale") ||
		strings.Contains(errStr, "transport endpoint") ||
		strings.Contains(errStr, "no such device")
}

// testFileAccess tries to access a file in the directory to verify read capability
func (s *ScannerService) testFileAccess(path string, entries []os.DirEntry) error {
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		testPath := filepath.Join(path, entry.Name())
		if _, err := os.Stat(testPath); err != nil {
			return fmt.Errorf("can list directory but cannot access files (partial mount?): %v", err)
		}
		return nil // One successful test is enough
	}
	return nil
}

// hasActiveCorruption checks if a file already has an unresolved corruption record
// This prevents duplicate processing from webhook replays, overlapping scans, etc.
func (s *ScannerService) hasActiveCorruption(filePath string) bool {
	// Check for any CorruptionDetected event for this file that hasn't been resolved
	// A corruption is "active" if it has no VerificationSuccess or has MaxRetriesReached
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	active, err := s.corruptionRepo().HasActive(ctx, filePath)
	if err != nil {
		logger.Debugf("Error checking for active corruption: %v", err)
		return false // Err on the side of processing
	}
	return active
}

// LoadActiveCorruptionsForPath preloads all active corruptions for a given root path.
// This fixes the N+1 query problem during path scans by doing a single query upfront.
// Returns a map of file_path -> true for files with active corruptions.
func (s *ScannerService) LoadActiveCorruptionsForPath(rootPath string) map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	// Get all active corruptions for files under this path in a single query
	result, err := s.corruptionRepo().ListActiveFilePathsUnderRoot(ctx, rootPath)
	if err != nil {
		logger.Debugf("Error loading active corruptions for path %s: %v", rootPath, err)
		return make(map[string]bool)
	}

	logger.Debugf("Preloaded %d active corruptions for path %s", len(result), rootPath)
	return result
}

// queueForRescan adds a file to the pending_rescans table for later retry
// when infrastructure issues are resolved
func (s *ScannerService) queueForRescan(filePath string, pathID int64, errorType, errorMessage string) {
	// Exponential backoff: 5 minutes initially, doubling each retry, capped at 160 minutes (5 * 2^5)
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	if err := s.rescanRepo().Queue(ctx, filePath, pathID, errorType, errorMessage); err != nil {
		logger.Errorf("Failed to queue file for rescan: %s: %v", filePath, err)
	} else {
		logger.Debugf("Queued for rescan: %s", filePath)
	}
}

// StartRescanWorker starts a background worker that periodically processes pending rescans
func (s *ScannerService) StartRescanWorker() {
	s.wg.Add(1)
	safego.Run("scanner-rescan-worker", func() {
		defer s.wg.Done()
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
	})
	logger.Infof("Rescan worker started (checks every 5 minutes)")
}

// =============================================================================
// processPendingRescans helpers - extracted to reduce cognitive complexity
// =============================================================================

// pendingRescanFile represents a file pending rescan
type pendingRescanFile struct {
	ID         int64
	FilePath   string
	PathID     int64
	RetryCount int
	MaxRetries int
}

// loadPendingRescanFiles loads files that are ready for retry
func (s *ScannerService) loadPendingRescanFiles() ([]pendingRescanFile, error) {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	rows, err := s.rescanRepo().ListReady(ctx, 50)
	if err != nil {
		return nil, err
	}

	files := make([]pendingRescanFile, 0, len(rows))
	for _, row := range rows {
		f := pendingRescanFile{
			ID:         row.ID,
			FilePath:   row.FilePath,
			RetryCount: row.RetryCount,
			MaxRetries: row.MaxRetries,
		}
		if row.PathID.Valid {
			f.PathID = row.PathID.Int64
		}
		files = append(files, f)
	}

	return files, nil
}

// markRescanResolved marks a pending rescan as resolved with the given resolution
func (s *ScannerService) markRescanResolved(id int64, resolution string) {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	// For 'abandoned' resolution, set status to 'abandoned'; otherwise 'resolved'
	status := "resolved"
	if resolution == "abandoned" {
		status = "abandoned"
	}

	if err := s.rescanRepo().MarkResolved(ctx, id, status, resolution); err != nil {
		logger.Warnf("Failed to mark pending rescan %d as %s: %v", id, resolution, err)
	}
}

// updateRescanRetry updates the retry state for a pending rescan
func (s *ScannerService) updateRescanRetry(f pendingRescanFile, healthErr *integration.HealthCheckError) {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	if err := s.rescanRepo().BumpRetry(ctx, f.ID, healthErr.Type, healthErr.Message); err != nil {
		logger.Warnf("Failed to update pending rescan %d retry state: %v", f.ID, err)
	}

	// Check if we've exceeded max retries
	if f.RetryCount+1 >= f.MaxRetries {
		s.markRescanResolved(f.ID, "abandoned")
		logger.Infof("Pending rescan abandoned after %d retries: %s", f.MaxRetries, f.FilePath)
	} else {
		logger.Debugf("Pending rescan still inaccessible, will retry: %s", f.FilePath)
	}
}

// emitRescanCorruption emits a corruption event for a rescan that found actual corruption
func (s *ScannerService) emitRescanCorruption(f pendingRescanFile, healthErr *integration.HealthCheckError) {
	autoRemediate, dryRun, _ := s.getScanPathConfig(f.FilePath)

	var fileSize int64
	if info, err := os.Stat(f.FilePath); err == nil {
		fileSize = info.Size()
	}

	// Critical entry point for remediation journey, use retry
	if err := s.eventBus.PublishWithRetry(domain.Event{
		AggregateType: "corruption",
		AggregateID:   uuid.New().String(),
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path":       f.FilePath,
			"file_size":       fileSize,
			"path_id":         f.PathID,
			"corruption_type": healthErr.Type,
			"error_details":   healthErr.Message,
			"source":          "rescan_worker",
			"auto_remediate":  autoRemediate,
			"dry_run":         dryRun,
		},
	}); err != nil {
		logger.Errorf("Failed to publish corruption event for rescan after retries: %v", err)
	}
}

// processPendingRescans checks files that previously had infrastructure errors
func (s *ScannerService) processPendingRescans() {
	files, err := s.loadPendingRescanFiles()
	if err != nil {
		logger.Errorf("Failed to query pending rescans: %v", err)
		return
	}

	if len(files) == 0 {
		return
	}

	logger.Infof("Processing %d pending rescans", len(files))

	for _, f := range files {
		// Check for shutdown
		select {
		case <-s.shutdownCh:
			return
		default:
		}

		healthy, healthErr := s.detector.Check(f.FilePath, "quick")

		if healthy {
			s.markRescanResolved(f.ID, "healthy")
			logger.Infof("Pending rescan resolved as healthy: %s", f.FilePath)
			continue
		}

		if healthErr.IsRecoverable() {
			s.updateRescanRetry(f, healthErr)
			continue
		}

		// File is accessible but actually corrupt
		logger.Infof("Pending rescan revealed corruption: %s (Type: %s)", f.FilePath, healthErr.Type)
		s.markRescanResolved(f.ID, "corrupt")
		s.emitRescanCorruption(f, healthErr)
	}
}

// GetPendingRescanStats returns statistics about pending rescans
func (s *ScannerService) GetPendingRescanStats() (pending, abandoned, resolved int, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	stats, err := s.rescanRepo().Stats(ctx)
	if err != nil {
		return 0, 0, 0, err
	}
	return stats.Pending, stats.Abandoned, stats.Resolved, nil
}
