package services

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/pathutil"
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

// defaultEnumerationTimeout bounds the directory-walk phase of a scan. A
// media library on a healthy local disk enumerates tens of thousands of
// files in well under a second, but a degraded network mount (mergerfs over
// CIFS/NFS, a stalled rclone FUSE backend, etc.) can make each stat block for
// seconds, turning enumeration into an effectively-infinite hang. Without a
// bound the scan would sit forever with no file list and no way to recover
// short of restarting the container. 30 minutes is generous enough for very
// large libraries on slow-but-working storage while still capping a genuine
// hang.
const defaultEnumerationTimeout = 30 * time.Minute

// scannerEnumerationTimeout returns the operator-configured enumeration
// timeout from HEALARR_SCANNER_ENUMERATION_TIMEOUT (time.ParseDuration), or
// defaultEnumerationTimeout if unset/invalid.
func scannerEnumerationTimeout() time.Duration {
	if v := os.Getenv("HEALARR_SCANNER_ENUMERATION_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			return d
		}
		logger.Warnf("Invalid HEALARR_SCANNER_ENUMERATION_TIMEOUT %q; using default %s", v, defaultEnumerationTimeout)
	}
	return defaultEnumerationTimeout
}

// enumerationProgressInterval throttles the "enumerated N files so far" log
// line emitted during the directory walk so a slow mount produces visible
// heartbeat output without flooding the log on a fast one.
const enumerationProgressInterval = 5 * time.Second

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

	// usesWatermark + resumeIndex carry the parallel scan's contiguous-done
	// watermark (maintained by the watermark goroutine in scanFilesParallel,
	// under mu). When usesWatermark is true, shutdown/pause persistence MUST
	// use resumeIndex: workers complete out of order, so both FilesDone (a
	// count) and the dispatch frontier OVERSHOOT the highest index for which
	// everything before it is done — persisting either would make a resumed
	// scan silently skip the unfinished gap.
	usesWatermark bool `json:"-"`
	resumeIndex   int  `json:"-"`
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
	ID            int64
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

	// persistProgressInline gates markFileProcessed's every-10-files
	// UpdateProgress write. True only on the serial path, where files
	// complete in order and the worker's own index IS the resume point. In
	// parallel mode the watermark goroutine owns all progress persistence:
	// an out-of-order worker writing its own index would overshoot the
	// contiguous watermark and make resume skip unfinished files. Set by
	// scanFiles when it picks the execution mode.
	persistProgressInline bool
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

	// sizeStabilityDelay is how long isSizeChanging waits before
	// re-stat'ing a file to detect an in-progress download. A field (default
	// set by NewScannerService) so tests — which construct &ScannerService{}
	// directly and leave it at the 0 zero-value — don't pay 500ms per file.
	sizeStabilityDelay time.Duration

	// scanWorkers is the number of files whose detection (ffprobe / stat
	// stability check) runs concurrently within a scan. Set by
	// NewScannerService from HEALARR_SCANNER_WORKERS. The 0 zero-value left by
	// &ScannerService{}-direct test fixtures means "sequential", so those tests
	// keep the original single-file-at-a-time path.
	scanWorkers int
}

// defaultSizeStabilityDelay is the production re-stat interval for
// detecting files whose size is still changing (active download/copy).
const defaultSizeStabilityDelay = 500 * time.Millisecond

// fallbackScanWorkers is used only when available memory can't be determined.
const fallbackScanWorkers = 4

// maxScanWorkers caps operator-configured concurrency to avoid thrashing
// storage or spawning an unreasonable number of ffprobe processes.
const maxScanWorkers = 32

// perWorkerMemoryBudget is the RAM we assume each concurrent detection worker may
// need. ffprobe (quick mode) is light, but a thorough-mode ffmpeg decode of a
// large file can use a few hundred MB. Budgeting generously means a small
// container won't be pushed into an OOM kill by the default concurrency — each
// extra worker is an extra subprocess counted against the container's cgroup limit.
const perWorkerMemoryBudget = 512 * 1024 * 1024 // 512 MB

// scannerWorkers returns the detection concurrency. HEALARR_SCANNER_WORKERS wins
// if set (clamped to [1, maxScanWorkers]); otherwise the default is tuned to the
// memory available to the process so a memory-constrained container doesn't OOM
// from running several ffmpeg/ffprobe subprocesses at once.
func scannerWorkers() int {
	if v := os.Getenv("HEALARR_SCANNER_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 1 {
			if n > maxScanWorkers {
				return maxScanWorkers
			}
			return n
		}
		logger.Warnf("Invalid HEALARR_SCANNER_WORKERS %q; using a memory-aware default", v)
	}
	return workersForMemory(availableMemoryBytes(), runtime.NumCPU())
}

// workersForMemory derives the worker count from a memory budget and CPU count.
// Roughly memBytes/perWorkerMemoryBudget, capped by CPU count and maxScanWorkers,
// floored at 1. If memBytes is 0 (unknown) it returns fallbackScanWorkers. Pure
// function for testability.
func workersForMemory(memBytes uint64, cpus int) int {
	if cpus < 1 {
		cpus = 1
	}
	if memBytes == 0 {
		n := fallbackScanWorkers
		if n > cpus {
			n = cpus
		}
		return n
	}
	n := int(memBytes / perWorkerMemoryBudget)
	if n > cpus {
		n = cpus
	}
	if n > maxScanWorkers {
		n = maxScanWorkers
	}
	if n < 1 {
		n = 1
	}
	return n
}

// availableMemoryBytes best-effort reports the memory ceiling the process runs
// under: the container's cgroup memory limit when present, else total system
// memory. Returns 0 when it can't be determined (e.g. non-Linux), so callers
// fall back to a fixed default. Linux-specific paths simply don't exist
// elsewhere, so this stays portable without build tags.
func availableMemoryBytes() uint64 {
	// cgroup v2: a single limit file, "max" means unlimited.
	if b, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "max" {
			if v, err := strconv.ParseUint(s, 10, 64); err == nil && v > 0 {
				return v
			}
		}
	}
	// cgroup v1: "unlimited" is a near-max sentinel, so ignore implausibly large values.
	if b, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		if v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil && v > 0 && v < (1<<62) {
			return v
		}
	}
	// Fallback: total system memory from /proc/meminfo (kB).
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if kb, ok := strings.CutPrefix(line, "MemTotal:"); ok {
				fields := strings.Fields(kb)
				if len(fields) >= 1 {
					if v, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
						return v * 1024
					}
				}
			}
		}
	}
	return 0
}

// NewScannerService creates a new ScannerService with the given dependencies.
func NewScannerService(db *sql.DB, eb *eventbus.EventBus, detector integration.HealthChecker, pm integration.PathMapper) *ScannerService {
	s := &ScannerService{
		db:                 db,
		eventBus:           eb,
		detector:           detector,
		pathMapper:         pm,
		activeScans:        make(map[string]*ScanProgress),
		filesInProgress:    make(map[string]bool),
		shutdownCh:         make(chan struct{}),
		sizeStabilityDelay: defaultSizeStabilityDelay,
		scanWorkers:        scannerWorkers(),
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
			resumeAt := filesDone
			if scan.usesWatermark {
				// Parallel scan: FilesDone is a completion COUNT, not an
				// index, and workers finish out of order — persisting it
				// would overshoot the contiguous watermark and make resume
				// skip unfinished files. The watermark goroutine keeps
				// resumeIndex at the highest safe replay point.
				resumeAt = scan.resumeIndex
			}
			scan.mu.Unlock()

			logger.Infof("Scanner: saving state for scan %s (resume at %d, %d/%d files done)", scanID, resumeAt, filesDone, totalFiles)
			// Mark as interrupted in database - state is already saved during scanning
			ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
			err := s.scanRepo().MarkInterrupted(ctx, scanDBID, resumeAt)
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
		// Mirror finalizeScan: an INTERRUPTED scan must NOT be finalized.
		// Finalize unconditionally stamps completed_at, and ListInterrupted
		// requires completed_at IS NULL — so finalizing here made any scan
		// that was resumed once and interrupted again permanently
		// unresumable. Shutdown's MarkInterrupted already persisted the
		// resume state; leave the row alone.
		if progress.Status == ScanStatusInterrupted {
			s.mu.Lock()
			delete(s.activeScans, scanID)
			s.mu.Unlock()
			return
		}
		finalStatus := ScanStatusCompleted
		if progress.Status == ScanStatusCancelled || progress.Status == ScanStatusAborted {
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
	autoRemediate, dryRun, pathID, err := s.getScanPathConfig(localPath)
	if err != nil {
		// Log warning but proceed with defaults (false, false)
		// This is important for ops visibility - file scanned without matching path config
		logger.Warnf("Could not determine scan path config for %s: %v (using defaults: auto_remediate=false, dry_run=false)", localPath, err)
	}

	logger.Infof("Scanning single file: %s", localPath)

	// The *arr fires the import webhook the moment ITS view of the move
	// completes — but when *arr and Healarr see the storage through
	// different mounts (NFS attribute caching, SMB, rclone/mergerfs),
	// Healarr's view can lag by seconds: a stat here may show 0 bytes or a
	// partial file, and a quick probe would flag a healthy, just-imported
	// file as corrupt — deleting it and possibly blocklisting a good
	// release. Gate on size stability first; an unstable file goes to the
	// rescan queue (retried in ~5 min, when the view has settled).
	var fileSize int64
	if info, err := os.Stat(localPath); err == nil {
		fileSize = info.Size()
	}
	time.Sleep(s.sizeStabilityDelay)
	if info, err := os.Stat(localPath); err == nil {
		if info.Size() != fileSize {
			logger.Infof("Webhook scan deferred: %s is still changing size (%d -> %d bytes); queued for rescan", localPath, fileSize, info.Size())
			s.queueForRescan(localPath, pathID, "SizeChanging", "file size still changing at webhook time")
			return nil
		}
		fileSize = info.Size()
	}

	// Use quick mode for single file scans (called from webhooks)
	healthy, healthErr := s.detector.Check(localPath, "quick")

	// Two-probe confirmation: a TRUE-corruption verdict on a just-imported
	// file gets one re-probe after a short delay before any remediation is
	// triggered. A stale-mount artifact (0-byte stat, truncated view)
	// clears on the second probe; genuine corruption does not.
	if !healthy && healthErr != nil && healthErr.IsTrueCorruption() {
		time.Sleep(s.sizeStabilityDelay)
		healthy, healthErr = s.detector.Check(localPath, "quick")
		if healthy {
			logger.Infof("Webhook scan: %s healthy on re-probe (initial verdict was a stale-view artifact)", localPath)
		}
	}

	progress.FilesDone = 1
	s.emitProgress(progress)

	if healthy {
		// Explicit success line so a single-file (webhook) scan visibly
		// confirms completion in the log, instead of ending silently after
		// "Scanning single file". Requested in #305.
		logger.Infof("Scan completed for file: %s (healthy)", localPath)
		return nil
	}

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

	// Emit event - critical entry point for remediation journey, use retry.
	// path_id is REQUIRED for the remediator's consent re-read: without it the
	// recovery/retry paths cannot resolve the path's current auto_remediate /
	// dry_run and historically invented consent (deleted files on dry-run paths).
	if err := s.eventBus.PublishWithRetry(domain.Event{
		AggregateType: "corruption",
		AggregateID:   uuid.New().String(),
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path":       localPath,
			"file_size":       fileSize,
			"corruption_type": healthErr.Type,
			"error_details":   healthErr.Message,
			"source":          "webhook",
			"path_id":         pathID,
			"auto_remediate":  autoRemediate,
			"dry_run":         dryRun,
		},
	}); err != nil {
		return err
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

	// Bundle the per-path overrides for the global scan tunables (Phase 2 of
	// the /config redesign). nil-valued fields inside mean "inherit the
	// global"; the resolver in health_checker handles the precedence.
	overrides := buildScanOverrides(path)

	return scanPathSettings{
		AutoRemediate: autoRemediate,
		DryRun:        dryRun,
		DetectionConfig: integration.DetectionConfig{
			Method:    integration.DetectionMethod(detectionMethod),
			Args:      detectionArgs,
			Mode:      detectionMode,
			Overrides: overrides,
		},
	}
}

// buildScanOverrides translates the nullable per-path override columns on
// a scan_paths row into the integration package's ScanOverrides bundle.
// Returns nil when no override is set so the resolver short-circuits
// straight to the live globals - cheaper than constructing a bundle of
// nil pointers per file in a 25k-file library.
func buildScanOverrides(path repository.ScanPath) *integration.ScanOverrides {
	if !path.ThoroughDurationSeconds.Valid && !path.ThoroughTimeoutSeconds.Valid && !path.Hwaccel.Valid {
		return nil
	}
	ov := &integration.ScanOverrides{}
	if path.ThoroughDurationSeconds.Valid {
		v := path.ThoroughDurationSeconds.Int64
		ov.ThoroughDurationSeconds = &v
	}
	if path.ThoroughTimeoutSeconds.Valid {
		v := path.ThoroughTimeoutSeconds.Int64
		ov.ThoroughTimeoutSeconds = &v
	}
	if path.Hwaccel.Valid {
		v := path.Hwaccel.String
		ov.Hwaccel = &v
	}
	return ov
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

// enumerateMediaFiles walks localPath and returns the media files found. It
// honors ctx so a cancelled scan or a blown enumeration timeout aborts the
// walk promptly (each directory entry checks ctx.Err()), and it emits a
// throttled "enumerated N files" heartbeat so a slow mount is visibly making
// progress in the logs instead of looking like a hang. Returns ctx.Err()
// (context.Canceled / context.DeadlineExceeded) if the walk was aborted.
func (s *ScannerService) enumerateMediaFiles(ctx context.Context, localPath string) ([]string, error) {
	stats := walkStats{}
	lastHeartbeat := time.Now()
	entriesSeen := 0

	err := filepath.WalkDir(localPath, func(filePath string, d fs.DirEntry, err error) error {
		// Abort promptly on cancel / enumeration timeout. Returning the
		// context error stops WalkDir and propagates out.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		if err != nil {
			return s.handleWalkError(filePath, err)
		}
		entriesSeen++
		isMedia, isSkipped, isSymlink := classifyEntry(filePath, d)
		switch {
		case isSymlink:
			stats.symlinkCount++
		case isSkipped:
			stats.skippedCount++
		case isMedia:
			stats.files = append(stats.files, filePath)
		}
		// Heartbeat: a degraded mount makes each stat block for seconds, so
		// without this the only log line is "Starting scan" until the walk
		// finishes (or never does). Throttled to avoid flooding fast walks.
		if time.Since(lastHeartbeat) >= enumerationProgressInterval {
			logger.Infof("Enumerating %s: %d media files found so far (%d entries scanned)", localPath, len(stats.files), entriesSeen)
			lastHeartbeat = time.Now()
		}
		return nil
	})

	if err != nil {
		return stats.files, err
	}
	if stats.skippedCount > 0 || stats.symlinkCount > 0 {
		logger.Debugf("Skipped %d non-media/hidden files and %d symlinks in %s", stats.skippedCount, stats.symlinkCount, localPath)
	}
	return stats.files, nil
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
		switch progress.Status {
		case ScanStatusCancelled:
			finalStatus = ScanStatusCancelled
		case ScanStatusAborted:
			// A mount-failure abort must stay ABORTED: finalizing it as
			// completed made a failed scan masquerade as a successful one in
			// "Last Scan" and the dashboard.
			finalStatus = ScanStatusAborted
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

	// Create the scan row up front in the 'enumerating' state so the scan is
	// visible in /scans and the dashboard during the directory walk, instead
	// of materializing only after enumeration finishes (which on a slow or
	// degraded mount can take minutes and looks indistinguishable from a hang).
	scanDBID := s.recordEnumerationStart(localPath, pathID, cfg)
	progress.ScanDBID = scanDBID
	s.emitProgress(progress)

	// Enumerate files under a bounded, cancellable context: a user cancel
	// (which cancels the parent ctx) or a blown enumeration timeout aborts the
	// walk promptly instead of hanging the scan forever on stalled I/O.
	enumCtx, enumCancel := context.WithTimeout(ctx, scannerEnumerationTimeout())
	files, err := s.enumerateMediaFiles(enumCtx, localPath)
	enumCancel()
	if err != nil {
		return s.handleEnumerationFailure(scanID, scanDBID, localPath, ctx, err)
	}

	progress.TotalFiles = len(files)
	progress.Status = ScanStatusScanning

	// Persist the discovered file list + total and transition to 'scanning'.
	// If the up-front insert failed (scanDBID == 0), fall back to the legacy
	// create-after-enumeration path so the scan is still recorded.
	if scanDBID > 0 {
		s.finishEnumerationRecord(scanDBID, files)
	} else {
		scanDBID = s.recordScanStart(localPath, pathID, files, cfg)
		progress.ScanDBID = scanDBID
	}
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

// recordEnumerationStart inserts the scan row in the 'enumerating' state
// before the directory walk, returning the scan id (0 on failure, which the
// caller treats as "fall back to recording after enumeration").
func (s *ScannerService) recordEnumerationStart(localPath string, pathID int64, cfg scanPathSettings) int64 {
	detectionConfigJSON, err := json.Marshal(cfg.DetectionConfig)
	if err != nil {
		logger.Errorf("Failed to serialize detection config: %v", err)
		detectionConfigJSON = []byte("{}")
	}
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()
	scanDBID, err := s.scanRepo().CreateEnumerating(ctx, repository.CreateScanParams{
		Path:                localPath,
		PathID:              pathID,
		DetectionConfigJSON: string(detectionConfigJSON),
		AutoRemediate:       cfg.AutoRemediate,
		DryRun:              cfg.DryRun,
	})
	if err != nil {
		logger.Errorf("Failed to record enumeration start: %v", err)
		return 0
	}
	return scanDBID
}

// finishEnumerationRecord stores the enumerated file list + total on the scan
// row and flips it from 'enumerating' to 'scanning'.
func (s *ScannerService) finishEnumerationRecord(scanDBID int64, files []string) {
	fileListJSON, err := json.Marshal(files)
	if err != nil {
		logger.Errorf("Failed to serialize file list: %v", err)
		fileListJSON = []byte("[]")
	}
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()
	if err := s.scanRepo().FinishEnumeration(ctx, scanDBID, len(files), string(fileListJSON)); err != nil {
		logger.Errorf("Failed to persist enumerated file list for scan %d: %v", scanDBID, err)
	}
}

// handleEnumerationFailure maps a failed/aborted directory walk to a terminal
// scan state, removes the scan from the in-memory active set, and returns the
// appropriate error. It is called instead of (not in addition to)
// finalizeScan, because the deferred finalizeScan is only registered after a
// successful enumeration.
func (s *ScannerService) handleEnumerationFailure(scanID string, scanDBID int64, localPath string, ctx context.Context, err error) error {
	s.mu.Lock()
	delete(s.activeScans, scanID)
	s.mu.Unlock()

	qctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	switch {
	case errors.Is(err, context.Canceled) || ctx.Err() != nil:
		// User cancel or scanner shutdown during the walk.
		logger.Infof("Enumeration cancelled for %s", localPath)
		if scanDBID > 0 {
			if _, mErr := s.scanRepo().MarkCancelled(qctx, scanDBID, "cancelled during enumeration"); mErr != nil {
				logger.Errorf("Failed to mark scan %d cancelled: %v", scanDBID, mErr)
			}
		}
		return nil
	case errors.Is(err, context.DeadlineExceeded):
		msg := fmt.Sprintf("enumeration timed out after %s; path %s may be on a slow or unresponsive mount", scannerEnumerationTimeout(), localPath)
		logger.Errorf("%s", msg)
		if scanDBID > 0 {
			if mErr := s.scanRepo().MarkAborted(qctx, scanDBID, msg); mErr != nil {
				logger.Errorf("Failed to mark scan %d aborted: %v", scanDBID, mErr)
			}
		}
		return fmt.Errorf("%s", msg)
	default:
		logger.Errorf("Enumeration failed for %s: %v", localPath, err)
		if scanDBID > 0 {
			if mErr := s.scanRepo().MarkAborted(qctx, scanDBID, err.Error()); mErr != nil {
				logger.Errorf("Failed to mark scan %d aborted: %v", scanDBID, mErr)
			}
		}
		return err
	}
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
	// Check shutdown FIRST and on its own: Shutdown closes shutdownCh, marks
	// the scan interrupted in the DB, then cancels the ctx — so by the time
	// a goroutine sees ctx.Done(), shutdownCh is also closed. A combined
	// select picks pseudo-randomly between two ready channels, which made
	// roughly half of all graceful shutdowns finalize the scan as
	// "cancelled" (terminal, never resumed) instead of "interrupted",
	// silently discarding hours of progress per docker restart.
	select {
	case <-s.shutdownCh:
		logger.Infof("Scan interrupted for graceful shutdown: %s (at file %d/%d)", localPath, fileIndex, totalFiles)
		progress.Status = ScanStatusInterrupted
		s.emitProgress(progress)
		return scanReturn
	default:
	}
	select {
	case <-ctx.Done():
		logger.Infof("Scan cancelled: %s", localPath)
		progress.Status = ScanStatusCancelled
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
	resumeCh := progress.resumeChan
	s.mu.Unlock()

	if !isPaused {
		return scanContinue
	}

	logger.Infof("Scan paused: %s (at file %d/%d)", localPath, fileIndex+1, progress.TotalFiles)

	// Save current position. For parallel scans, fileIndex is the DISPATCH
	// frontier (files handed to workers), which overshoots the contiguous
	// watermark; persist the watermark so a resume after pause+restart
	// cannot skip files still in flight at pause time.
	resumeAt := fileIndex
	progress.mu.Lock()
	if progress.usesWatermark {
		resumeAt = progress.resumeIndex
	}
	progress.mu.Unlock()
	if scanDBID > 0 {
		pauseCtx, pauseCancel := context.WithTimeout(ctx, scannerQueryTimeout)
		if err := s.scanRepo().MarkPaused(pauseCtx, scanDBID, resumeAt); err != nil {
			logger.Warnf("Failed to update scan pause state for scan %d: %v", scanDBID, err)
		}
		pauseCancel()
	}

	// Wait for resume or cancel. resumeCh was captured under the lock above;
	// ResumeScan closes it to wake every waiter at once.
	select {
	case <-resumeCh:
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

// recentlyModifiedWindow is how fresh a file's mtime must be for the scanner to
// treat it as "likely still being written" and skip it.
const recentlyModifiedWindow = 2 * time.Minute

// isRecentlyModified reports whether the file was modified within
// recentlyModifiedWindow (likely still being written). Pure: no side effects.
func isRecentlyModified(sfc *scanFileContext) bool {
	return time.Since(sfc.fileMtime) < recentlyModifiedWindow
}

// isSizeChanging reports whether the file's size changes across sizeStabilityDelay
// — an active download/copy, even when the mtime is preserved (e.g. rsync
// --times). Aside from the delay and a re-stat it has no side effects: no
// database writes, no progress mutation, so it is safe to run concurrently.
func (s *ScannerService) isSizeChanging(sfc *scanFileContext) bool {
	time.Sleep(s.sizeStabilityDelay)
	info2, err := os.Stat(sfc.filePath)
	return err == nil && info2.Size() != sfc.fileSize
}

// recordSkipped writes a "skipped" row to scan_files. It is separated from the
// skip decision (isRecentlyModified / isSizeChanging) so detection can run
// concurrently — workers decide, the coordinator records sequentially. scan_files
// rows drive the UI scan-detail screen, so a failed write is logged loudly.
func (s *ScannerService) recordSkipped(sfc *scanFileContext, corruptionType, details string) {
	if sfc.scanDBID <= 0 {
		return
	}
	if err := s.scanFileRepo().Record(context.Background(), sfc.scanDBID, repository.ScanFileRecord{
		FilePath:       sfc.filePath,
		Status:         "skipped",
		CorruptionType: corruptionType,
		ErrorDetails:   details,
		FileSize:       sfc.fileSize,
	}); err != nil {
		logger.Errorf("Failed to record skipped file (%s): %v", corruptionType, err)
	}
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

	// DEDUPLICATION: Check if already being processed.
	// The preloaded snapshot is taken once at scan START; on a long scan it
	// can be hours stale, and a webhook may have opened (and still be
	// running) a remediation journey for this same file in the meantime.
	// Two concurrent journeys for one file can end with the loser deleting
	// the winner's healthy replacement. The snapshot remains the fast path
	// for the common case (file already active at scan start); a snapshot
	// MISS gets a live re-check before we open a new journey — corrupt
	// files are rare, so the extra query is negligible.
	hasActive := sfc.activeCorruptions != nil && sfc.activeCorruptions[sfc.filePath]
	if !hasActive {
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

	// With a worker pool configured, run detection concurrently. The 0/1 case
	// (default for &ScannerService{}-direct test fixtures) keeps the original
	// single-file-at-a-time path untouched.
	if s.scanWorkers > 1 {
		// Parallel mode: the watermark goroutine owns ALL progress
		// persistence; workers must not write their own out-of-order index.
		cfg.persistProgressInline = false
		s.scanFilesParallel(ctx, progress, cfg, activeCorruptions)
		return
	}

	// Serial mode: files complete in order, so the inline every-10-files
	// checkpoint in markFileProcessed is the correct resume point.
	cfg.persistProgressInline = true
	for i := cfg.StartIndex; i < len(cfg.Files); i++ {
		action := s.processFileInScan(ctx, progress, cfg, i, activeCorruptions)
		if action == scanReturn {
			return
		}
	}

	progress.Status = "completed"
}

// detectJobState is the outcome of a worker's attempt to detect one file.
type detectJobState int

const (
	jobPending      detectJobState = iota // worker did not complete (e.g. panic)
	jobDone                               // detection ran; result is populated
	jobDedupSkipped                       // another scan already holds this file
	jobStopped                            // scan was canceling/shutting down
)

// detectJob carries one file's detection work across the fan-out/fan-in boundary.
type detectJob struct {
	sfc    *scanFileContext
	result detectionResult
	state  detectJobState
}

// watermarkPersistInterval is how often the watermark goroutine flushes the
// contiguous-completion watermark to scans.current_file_index. Bounded so
// per-file write amplification stays low and resume granularity stays tight.
const watermarkPersistInterval = 2 * time.Second

// scanFilesParallel processes files with a semaphore-bounded worker pool. The
// previous design used ordered fan-out/fan-in batches of scanWorkers files
// with a wg.Wait barrier between batches: one slow file held back every
// other worker in the batch, and all completions clustered into the same
// CURRENT_TIMESTAMP second. The new design dispatches one worker per file up
// to scanWorkers in flight, lets each commit its scan_files row as it
// finishes, and tracks completion in a per-index bitmap so the persisted
// current_file_index can still advance monotonically as a contiguous-done
// watermark. Idempotency on (scan_id, file_path) (migration 010) covers the
// resume replay window where the watermark may lag behind the actual
// highest-completed index. Cancellation and pause are checked at every
// dispatch iteration instead of only at batch boundaries.
func (s *ScannerService) scanFilesParallel(
	ctx context.Context,
	progress *ScanProgress,
	cfg scanFilesConfig,
	activeCorruptions map[string]bool,
) {
	files := cfg.Files
	pathID := progress.PathID

	// done[i] flips to true once index i has been fully processed (recorded,
	// counter bumped, or deliberately skipped). The watermark advances over
	// the contiguous prefix of trues.
	done := make([]atomic.Bool, len(files))

	// stopped flips when a worker or the dispatch loop decides the scan must
	// stop (cancel, pause, fatal detection error). Workers that haven't
	// started yet bail without consuming a semaphore slot; the dispatch loop
	// breaks on the next iteration.
	var stopped atomic.Bool

	sem := make(chan struct{}, s.scanWorkers)
	var wg sync.WaitGroup

	// Mark this scan watermark-managed: shutdown/pause persistence must use
	// progress.resumeIndex (the contiguous-done watermark) instead of
	// FilesDone or the dispatch frontier, both of which overshoot under
	// out-of-order completion.
	progress.mu.Lock()
	progress.usesWatermark = true
	progress.resumeIndex = cfg.StartIndex
	progress.mu.Unlock()

	// Watermark persister: walks `done` from cfg.StartIndex forward over
	// consecutive trues, advances an in-memory watermark, and flushes it to
	// scans.current_file_index every ~watermarkPersistInterval. Owning the
	// DB write here means workers can finish out of order without
	// corrupting the resume floor.
	//
	// workersDone closes after wg.Wait() so the goroutine always terminates
	// even when the scan stops early WITHOUT a ctx cancel (worker panic,
	// mount-lost abort): before this exit path existed, scanFilesParallel
	// blocked forever on <-watermarkDone, the deferred ctx cancel in
	// ScanPath never ran, and the path stayed "being scanned" until restart.
	watermarkDone := make(chan struct{})
	workersDone := make(chan struct{})
	safego.Run("scan-watermark", func() {
		defer close(watermarkDone)
		watermark := cfg.StartIndex
		lastPersisted := -1
		ticker := time.NewTicker(watermarkPersistInterval)
		defer ticker.Stop()

		advance := func() {
			for watermark < len(files) && done[watermark].Load() {
				watermark++
			}
			progress.mu.Lock()
			progress.resumeIndex = watermark
			progress.mu.Unlock()
		}
		persistIfAdvanced := func() {
			if cfg.ScanDBID <= 0 || watermark == lastPersisted {
				return
			}
			progress.mu.Lock()
			filesDone := progress.FilesDone
			progress.mu.Unlock()
			progressCtx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
			if err := s.scanRepo().UpdateProgress(progressCtx, cfg.ScanDBID, watermark, filesDone); err != nil {
				// Best-effort: the watermark is a resume optimization, not
				// correctness-critical (the next tick re-attempts, and
				// scan_files rows are the source of truth). Debug-level so a
				// transient lock doesn't spam the log viewer as if it were an
				// error — the symptom reported in #321.
				logger.Debugf("watermark persist for scan %d (idx=%d) deferred: %v", cfg.ScanDBID, watermark, err)
			}
			cancel()
			lastPersisted = watermark
		}

		for {
			advance()
			if watermark >= len(files) {
				persistIfAdvanced()
				return
			}
			select {
			case <-ticker.C:
				persistIfAdvanced()
			case <-ctx.Done():
				persistIfAdvanced()
				return
			case <-workersDone:
				// All workers finished but the watermark did not reach the
				// end (early stop). Persist the final contiguous position
				// and exit so scanFilesParallel can return.
				advance()
				persistIfAdvanced()
				return
			}
		}
	})

	// Dispatch loop: spawn one worker per file, capped at scanWorkers
	// in-flight via the semaphore. Cancellation and pause are checked per
	// iteration; if either fires we set `stopped` so already-dispatched
	// workers can bail and we break out of the loop.
	for i := cfg.StartIndex; i < len(files); i++ {
		if s.checkScanCancellation(ctx, progress, progress.Path, i, len(files)) == scanReturn {
			stopped.Store(true)
			break
		}
		if s.handleScanPause(ctx, progress, progress.Path, i, cfg.ScanDBID) == scanReturn {
			stopped.Store(true)
			break
		}
		if stopped.Load() {
			break
		}

		// Acquire a slot (blocks if scanWorkers are already in flight). Use
		// a select so we honor cancellation while waiting for a slot.
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			stopped.Store(true)
		}
		if stopped.Load() {
			break
		}

		idx := i
		wg.Add(1)
		safego.Run("scan-detect-worker", func() {
			defer wg.Done()
			defer func() { <-sem }()
			// A panic inside detection/handling must not wedge the scan:
			// without this recover, done[idx] stayed false forever, the
			// watermark never reached len(files), and the whole scan
			// deadlocked on <-watermarkDone with the path locked until
			// restart. Recover locally, count the file as processed
			// (skipped), and let the scan continue. (safego's top-level
			// recover never fires for this closure anymore, which is fine —
			// it exists as the generic backstop.)
			defer func() {
				if r := recover(); r != nil {
					logger.Errorf("Scan worker panicked on %s (file skipped): %v", files[idx], r)
					s.markFileProcessedNoSync(progress)
					done[idx].Store(true)
				}
			}()

			// Late-bail: another worker may have flipped `stopped` between
			// our dispatch and our start. Don't mark done — resume must
			// re-attempt this file.
			if stopped.Load() {
				return
			}

			var job detectJob
			s.detectInWorker(ctx, &job, files[idx], pathID, cfg, activeCorruptions)

			switch job.state {
			case jobDedupSkipped:
				logger.Debugf("Skipping file already being scanned: %s", files[idx])
				s.markFileProcessedNoSync(progress)
				done[idx].Store(true)
			case jobStopped:
				// Worker observed ctx.Done() mid-detection. Signal so the
				// dispatch loop stops, but leave done[idx]=false so resume
				// retries this file.
				stopped.Store(true)
				return
			case jobDone:
				progress.mu.Lock()
				progress.CurrentFile = files[idx]
				progress.mu.Unlock()
				s.emitProgress(progress)
				if s.handleDetection(ctx, progress, cfg, idx, job.sfc, job.result) == scanReturn {
					// handleDetection already persisted the terminal scan
					// state for this file via its own write paths. Signal
					// shutdown but mark done so the watermark can pass this
					// index (we don't want a stuck-forever watermark on a
					// file the scanner has decided to terminate from).
					stopped.Store(true)
					done[idx].Store(true)
					return
				}
				done[idx].Store(true)
			default: // jobPending — worker panicked mid-file.
				logger.Warnf("Scan worker did not complete for %s; skipping", files[idx])
				s.markFileProcessedNoSync(progress)
				done[idx].Store(true)
			}
		})
	}

	wg.Wait()
	close(workersDone)
	<-watermarkDone

	if !stopped.Load() {
		progress.Status = "completed"
	}
}

// markFileProcessedNoSync bumps the in-memory FilesDone counter without
// writing to the database. The parallel path uses this because the
// watermark goroutine owns DB progress writes; the original
// markFileProcessed (with its every-10-files persist of the worker's
// fileIndex) is unsafe under out-of-order completion since it can write a
// non-monotonic current_file_index.
func (s *ScannerService) markFileProcessedNoSync(progress *ScanProgress) {
	progress.mu.Lock()
	progress.FilesDone++
	progress.mu.Unlock()
}

// detectInWorker runs the read-only detection for a single file inside a worker
// goroutine, recording the outcome into job. It holds the file in
// filesInProgress only for the duration of detection (the expensive part) so an
// overlapping scan won't double-detect it; recording happens later in the fan-in.
func (s *ScannerService) detectInWorker(
	ctx context.Context,
	job *detectJob,
	filePath string,
	pathID int64,
	cfg scanFilesConfig,
	activeCorruptions map[string]bool,
) {
	// Dedup against overlapping scans (e.g. a webhook scan and a bulk scan).
	s.filesMu.Lock()
	if s.filesInProgress[filePath] {
		s.filesMu.Unlock()
		job.state = jobDedupSkipped
		return
	}
	s.filesInProgress[filePath] = true
	s.filesMu.Unlock()
	defer func() {
		s.filesMu.Lock()
		delete(s.filesInProgress, filePath)
		s.filesMu.Unlock()
	}()

	// Don't start an expensive detection if the scan is already stopping.
	select {
	case <-ctx.Done():
		job.state = jobStopped
		return
	case <-s.shutdownCh:
		job.state = jobStopped
		return
	default:
	}

	job.sfc = s.buildScanFileContext(filePath, pathID, cfg, activeCorruptions)
	job.result = s.detectFile(job.sfc, cfg)
	job.state = jobDone
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
		s.markFileProcessed(progress, fileIndex, cfg.ScanDBID, cfg.persistProgressInline)
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
// fileOutcome is the read-only verdict produced by detectFile.
type fileOutcome int

const (
	outcomeHealthy fileOutcome = iota
	outcomeUnhealthy
	outcomeSkippedRecentlyModified
	outcomeSkippedSizeChanging
)

// detectionResult is the read-only result of detecting one file.
type detectionResult struct {
	outcome   fileOutcome
	healthErr *integration.HealthCheckError // populated only when outcomeUnhealthy
}

// detectFile performs all READ-ONLY detection for a single file: the stat-based
// safety skips (recently modified, actively changing size) and health
// verification (ffprobe, plus a content-analysis pass in thorough mode). It
// performs no database writes and mutates no scan progress, so it is safe to run
// concurrently across files — the seam the scan worker pool will parallelize.
// The caller records the outcome and advances progress sequentially.
func (s *ScannerService) detectFile(sfc *scanFileContext, cfg scanFilesConfig) detectionResult {
	// SAFETY: recently modified files are likely still being written.
	if isRecentlyModified(sfc) {
		logger.Infof("Skipping recently modified file (mtime %v ago): %s",
			time.Since(sfc.fileMtime).Round(time.Second), sfc.filePath)
		return detectionResult{outcome: outcomeSkippedRecentlyModified}
	}

	// SAFETY: a file whose size is still changing is an active download/copy.
	if s.isSizeChanging(sfc) {
		logger.Infof("Skipping file with changing size (download in progress?): %s", sfc.filePath)
		return detectionResult{outcome: outcomeSkippedSizeChanging}
	}

	healthy, healthErr := s.detector.CheckWithConfig(sfc.filePath, cfg.DetectionConfig)
	if healthy && cfg.DetectionConfig.Mode == integration.ModeThorough {
		// Thorough mode: structurally-healthy files get a content-analysis pass.
		healthy, healthErr = s.detector.AnalyzeContent(sfc.filePath, cfg.DetectionConfig.Overrides)
	}
	if healthy {
		return detectionResult{outcome: outcomeHealthy}
	}
	return detectionResult{outcome: outcomeUnhealthy, healthErr: healthErr}
}

// checkAndHandleFile runs detection for a file and applies the outcome: it
// records the scan_files row (or routes corruption to remediation) and advances
// progress. Detection (detectFile) is read-only; the recording/handling here is
// the sequential, stateful half.
func (s *ScannerService) checkAndHandleFile(
	ctx context.Context,
	progress *ScanProgress,
	cfg scanFilesConfig,
	fileIndex int,
	sfc *scanFileContext,
) scanLoopAction {
	return s.handleDetection(ctx, progress, cfg, fileIndex, sfc, s.detectFile(sfc, cfg))
}

// handleDetection applies a detection outcome: it records the scan_files row (or
// routes corruption to remediation) and advances progress. It is the stateful,
// sequential half of file processing — detectFile is the read-only half. Kept
// separate so the worker pool can run detection concurrently and feed results
// here in scan order, preserving the single-threaded semantics resume relies on.
func (s *ScannerService) handleDetection(
	ctx context.Context,
	progress *ScanProgress,
	cfg scanFilesConfig,
	fileIndex int,
	sfc *scanFileContext,
	result detectionResult,
) scanLoopAction {
	switch result.outcome {
	case outcomeSkippedRecentlyModified:
		s.recordSkipped(sfc, "RecentlyModified", "File modified within last 2 minutes - likely still being written")
	case outcomeSkippedSizeChanging:
		s.recordSkipped(sfc, "SizeChanging", "File size changed during scan - active download/copy")
	case outcomeHealthy:
		s.recordHealthyFile(sfc)
	case outcomeUnhealthy:
		// handleHealthCheckResult marks the file processed itself and returns the action.
		return s.handleHealthCheckResult(ctx, progress, cfg, fileIndex, sfc, result.healthErr)
	}

	s.markFileProcessed(progress, fileIndex, cfg.ScanDBID, cfg.persistProgressInline)
	return scanContinue
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
		s.markFileProcessed(progress, fileIndex, cfg.ScanDBID, cfg.persistProgressInline)
		return scanContinue
	}

	// Handle true corruption
	if s.handleTrueCorruption(ctx, progress, sfc, healthErr) == scanReturn {
		return scanReturn
	}
	s.markFileProcessed(progress, fileIndex, cfg.ScanDBID, cfg.persistProgressInline)
	return scanContinue
}

// markFileProcessed increments the file counter and, on the serial path
// (persistInline), saves progress periodically. Parallel-mode callers pass
// persistInline=false: their fileIndex is out of order and the watermark
// goroutine owns progress persistence.
func (s *ScannerService) markFileProcessed(progress *ScanProgress, fileIndex int, scanDBID int64, persistInline bool) {
	// Lock to safely update mutable fields (fixes data race with GetActiveScans/Shutdown)
	progress.mu.Lock()
	progress.FilesDone++
	filesDone := progress.FilesDone
	progress.mu.Unlock()

	// Save state to database periodically (every 10 files) to avoid excessive I/O
	if persistInline && fileIndex%10 == 0 {
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
	// Volatile: ScanProgress is an ephemeral UI signal consumed only by the
	// WebSocket hub and the Prometheus gauge (both in-memory subscribers). It is
	// never replayed or queried back, so we skip the per-file event-store INSERT.
	// Durable progress is persisted separately via the scans row (UpdateProgress).
	s.eventBus.PublishVolatile(domain.Event{
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
	})
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

// ReconcileOrphanScans is the startup hook: any DB row left in an active
// status ("running"/"enumerating"/"scanning") at this point cannot belong to
// this process (activeScans is empty until the first scan starts), so it must
// be the residue of a previous hard restart that prevented MarkInterrupted
// from running.
//
// Rows with real persisted progress (file_list saved, current_file_index > 0)
// are demoted to 'interrupted' so the subsequent ResumeInterruptedScans pass
// picks them up and continues from the saved checkpoint. Rows without
// resumable state (mid-enumerate, or zero progress) are marked 'cancelled'.
//
// "paused" and "interrupted" are deliberately spared (legitimate resumable
// states). Idempotent: a fresh DB or a previously-reconciled DB results in
// 0 updates.
func (s *ScannerService) ReconcileOrphanScans() {
	if s.scanRepo() == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	res, err := s.scanRepo().ReconcileOrphans(ctx)
	if err != nil {
		logger.Warnf("ReconcileOrphanScans: failed to clean orphan rows: %v", err)
		return
	}
	if res.Interrupted > 0 {
		logger.Infof("ReconcileOrphanScans: demoted %d orphan scan row(s) to 'interrupted' for auto-resume (residue of a previous hard restart)", res.Interrupted)
	}
	if res.Cancelled > 0 {
		logger.Infof("ReconcileOrphanScans: marked %d orphan scan row(s) cancelled (no resumable progress)", res.Cancelled)
	}
}

// findActiveScanLocked returns the activeScans entry matching scanID, trying
// the in-memory UUID (the map key) first, then falling back to matching
// against ScanDBID. The HTTP handlers route by DB integer id (the only
// stable identifier the frontend has from /api/scans); internal callers
// like CancelAllScans iterate activeScans and pass the UUID directly. Both
// shapes are valid scanID inputs and we want both to find the same entry.
//
// Caller must hold s.mu. activeScans is small (one entry per concurrent
// scan), so the linear DB-id fallback is cheaper than maintaining a
// secondary index.
func (s *ScannerService) findActiveScanLocked(scanID string) (*ScanProgress, bool) {
	if scan, ok := s.activeScans[scanID]; ok {
		return scan, true
	}
	dbID, err := strconv.ParseInt(scanID, 10, 64)
	if err != nil {
		return nil, false
	}
	for _, p := range s.activeScans {
		if p.ScanDBID == dbID {
			return p, true
		}
	}
	return nil, false
}

// CancelScan cancels a scan. It signals the in-memory ctx if the scan is
// actually running in this process AND persists "cancelled" to the DB row,
// so the page reflects the new state on reload. Both halves run regardless
// of which is reachable: a live in-process scan + a stale "running" row left
// by a previous hard restart both deserve to be cleaned up by Cancel. Returns
// an error only when neither half found anything (no such scan id anywhere).
func (s *ScannerService) CancelScan(scanID string) error {
	// 1. Signal the in-memory scan to stop, if it is running in this process.
	//    Use findActiveScanLocked so both UUID and DB-id forms of scanID find
	//    the same entry (the HTTP handler passes the DB id, internal callers
	//    pass the UUID — both reach this code path).
	s.mu.Lock()
	scan, exists := s.findActiveScanLocked(scanID)
	if exists && scan.cancel != nil {
		scan.cancel()
	}
	s.mu.Unlock()

	// 2. Persist the cancellation to the DB. Before this, the in-memory
	//    ctx.cancel was the only signal of cancellation, and the scan loop's
	//    exit path did not write a "cancelled" status, so /scans would still
	//    show the row as "running" on reload (and the Dashboard's "Scan
	//    cancelled" toast was misleading). Doing the UPDATE here also cleans
	//    up stale rows from a previous hard restart where there is nothing
	//    to signal in memory.
	// Resolve the DB id: prefer parsing scanID directly (HTTP path), else
	// fall back to the in-memory entry's ScanDBID (callers that passed the
	// UUID still want the DB row updated).
	dbID, parseErr := strconv.ParseInt(scanID, 10, 64)
	if parseErr != nil && exists && scan.ScanDBID > 0 {
		dbID = scan.ScanDBID
		parseErr = nil
	}
	updated := false
	if parseErr == nil && s.scanRepo() != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ok, err := s.scanRepo().MarkCancelled(ctx, dbID, "cancelled by user")
		if err != nil {
			logger.Warnf("CancelScan: failed to persist cancellation for scan %s: %v", scanID, err)
		}
		updated = ok
	}

	if !exists && !updated {
		return fmt.Errorf("scan not found")
	}
	return nil
}

// PauseScan pauses an ongoing scan
func (s *ScannerService) PauseScan(scanID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	scan, exists := s.findActiveScanLocked(scanID)
	if !exists {
		return fmt.Errorf("scan not found: %s", scanID)
	}

	if scan.isPaused {
		return nil // Already paused
	}

	if scan.Status != "scanning" {
		return fmt.Errorf("scan is not in scanning state: %s", scan.Status)
	}

	// Allocate a fresh resume channel for this pause. ResumeScan closes it to
	// broadcast the wake-up; a previously-closed channel must not be reused (a
	// closed channel would let handleScanPause fall straight through without
	// pausing). A fresh channel per pause also lets multiple waiters (the future
	// scan worker pool) all wake from one close.
	scan.resumeChan = make(chan struct{})
	scan.isPaused = true
	scan.Status = "paused"
	return nil
}

// ResumeScan resumes a paused scan
func (s *ScannerService) ResumeScan(scanID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	scan, exists := s.findActiveScanLocked(scanID)
	if !exists {
		return fmt.Errorf("scan not found: %s", scanID)
	}

	if !scan.isPaused {
		return nil // Not paused
	}

	// Clear paused state and broadcast resume by closing the channel. Closing
	// (vs a non-blocking send) can't be "missed" if the scan goroutine has not
	// yet reached handleScanPause — the old send-with-default could silently drop
	// the signal and hang the scan paused — and it wakes every waiter at once.
	// Setting isPaused here also makes a concurrent ResumeScan a no-op, so the
	// channel is closed exactly once. Status is reset so a not-yet-blocked
	// goroutine (which will skip handleScanPause) still reflects "scanning".
	scan.isPaused = false
	scan.Status = ScanStatusScanning
	close(scan.resumeChan)

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
			ID:            p.ID,
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
// Returns auto_remediate, dry_run, the matched scan path's ID, and any error.
//
// The returned pathID rides on the CorruptionDetected event so the remediator
// can re-read the path's CURRENT consent at action time: without it, webhook
// corruptions carried path_id=0 and the recovery/retry paths could invent
// auto-remediate consent the operator never gave.
//
// Matching uses pathutil.IsWithinRoot, the shared separator-agnostic boundary
// semantics, so a trailing slash in the stored local_path or a Windows-native
// deployment does not silently drop the configured dry_run/auto_remediate.
func (s *ScannerService) getScanPathConfig(filePath string) (autoRemediate bool, dryRun bool, pathID int64, err error) {
	// Ensure cache is fresh
	if err := s.refreshScanPathCache(); err != nil {
		return false, false, 0, err
	}

	s.scanPathCacheMu.RLock()
	defer s.scanPathCacheMu.RUnlock()

	var bestMatchLen int
	found := false

	for _, cfg := range s.scanPathCache {
		if !pathutil.IsWithinRoot(cfg.LocalPath, filePath) {
			continue
		}
		if l := pathutil.MatchedRootLen(cfg.LocalPath); l > bestMatchLen {
			bestMatchLen = l
			autoRemediate = cfg.AutoRemediate
			dryRun = cfg.DryRun
			pathID = cfg.ID
			found = true
		}
	}

	if !found {
		return false, false, 0, fmt.Errorf("no matching scan path found")
	}
	return autoRemediate, dryRun, pathID, nil
}
