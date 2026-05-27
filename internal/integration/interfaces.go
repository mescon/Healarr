package integration

import (
	"fmt"
	"testing"

	"github.com/mescon/Healarr/internal/logger"
)

// ArrInstanceInfo represents a configured *arr instance.
//
// Type is the typed ArrType enum (defined in arr_client.go) rather than a
// bare string — the compiler now rejects misspelled or unknown values at
// every comparison and DB scan site.
type ArrInstanceInfo struct {
	ID     int64
	Name   string
	Type   ArrType // sonarr, radarr, whisparr-v2, whisparr-v3
	URL    string
	APIKey string
}

// RootFolder represents a configured root folder in a *arr instance.
// Root folders are the base library paths where media is stored.
type RootFolder struct {
	ID         int64  `json:"id"`
	Path       string `json:"path"`
	FreeSpace  int64  `json:"freeSpace"`
	TotalSpace int64  `json:"totalSpace"`
}

// ArrClient defines the interface for interacting with Sonarr/Radarr
type ArrClient interface {
	// Media operations
	FindMediaByPath(path string) (int64, error)
	DeleteFile(mediaID int64, path string) (map[string]interface{}, error)
	GetFilePath(mediaID int64, metadata map[string]interface{}, referencePath string) (string, error)
	// GetAllFilePaths returns all unique file paths for the tracked episodes/movie.
	// For multi-episode files replaced with individual files, this returns multiple paths.
	GetAllFilePaths(mediaID int64, metadata map[string]interface{}, referencePath string) ([]string, error)
	TriggerSearch(mediaID int64, path string, episodeIDs []int64) error

	// Instance management
	GetAllInstances() ([]*ArrInstanceInfo, error)
	GetInstanceByID(id int64) (*ArrInstanceInfo, error)
	CheckInstanceHealth(instanceID int64) error

	// Root folders - library paths configured in *arr instances
	GetRootFolders(instanceID int64) ([]RootFolder, error)

	// Queue monitoring - track active downloads
	GetQueueForPath(arrPath string) ([]QueueItemInfo, error)
	FindQueueItemsByMediaIDForPath(arrPath string, mediaID int64) ([]QueueItemInfo, error)
	GetDownloadStatusForPath(arrPath, downloadID string) (status string, progress float64, errMsg string, err error)

	// History - detect completed imports
	GetRecentHistoryForMediaByPath(arrPath string, mediaID int64, limit int) ([]HistoryItemInfo, error)

	// Queue management
	RemoveFromQueueByPath(arrPath string, queueID int64, removeFromClient, blocklist bool) error
	RefreshMonitoredDownloadsByPath(arrPath string) error

	// Blocklist - mark a specific grabbed release as failed so the *arr won't
	// re-grab it. historyID is the *arr "grabbed" history record's ID.
	MarkReleaseAsFailed(arrPath string, historyID int64) error

	// Media details - fetch friendly titles for display
	// Returns nil (not error) if media not found, to allow graceful degradation
	GetMediaDetails(mediaID int64, arrPath string) (*MediaDetails, error)
}

// QueueItemInfo represents a download queue item (simplified for interface)
type QueueItemInfo struct {
	ID                    int64
	DownloadID            string
	Title                 string
	Status                string   // downloading, completed, delay, etc.
	TrackedDownloadState  string   // downloading, importPending, imported, failedPending, failed
	TrackedDownloadStatus string   // ok, warning, error
	ErrorMessage          string   // primary error message
	StatusMessages        []string // detailed status/warning messages from *arr
	Protocol              string   // usenet, torrent
	DownloadClient        string
	Indexer               string // Source indexer (NZBgeek, 1337x, etc.)
	Size                  int64
	SizeLeft              int64
	Progress              float64 // calculated: (size - sizeleft) / size * 100
	TimeLeft              string
	EstimatedCompletion   string
	AddedAt               string // When added to queue (ISO timestamp)
	MovieID               int64
	SeriesID              int64
	EpisodeID             int64
}

// HistoryItemInfo represents a history event (simplified for interface)
type HistoryItemInfo struct {
	ID           int64
	EventType    string // grabbed, downloadFolderImported, episodeFileDeleted, movieFileDeleted, etc.
	Date         string
	DownloadID   string
	SourceTitle  string
	MovieID      int64
	SeriesID     int64
	EpisodeID    int64
	ImportedPath string // from data.importedPath for import events
	// Quality and release info (from data field)
	Quality        string // e.g., "Bluray-1080p"
	ReleaseGroup   string // e.g., "DEMAND", "SPARKS"
	Indexer        string // e.g., "NZBgeek", "1337x"
	DownloadClient string // e.g., "SABnzbd", "qBittorrent"
}

// MediaDetails contains friendly display information about a movie or TV episode.
// Used to show "Colony S01E08" instead of raw file paths.
type MediaDetails struct {
	Title         string // Movie title or Series name
	Year          int    // Release year
	MediaType     string // "movie" or "series"
	SeasonNumber  int    // For TV only (0 for movies)
	EpisodeNumber int    // For TV only (0 for movies)
	EpisodeTitle  string // For TV only (empty for movies)
	ArrType       string // "sonarr", "radarr", "whisparr"
	InstanceName  string // e.g., "Radarr", "Radarr4K"
}

// FormatDisplayTitle returns a user-friendly title like "Colony S01E08" or "The Matrix (1999)"
func (m *MediaDetails) FormatDisplayTitle() string {
	if m == nil {
		return ""
	}
	if m.MediaType == "series" && m.SeasonNumber > 0 && m.EpisodeNumber > 0 {
		// Format: "Colony S01E08"
		return m.Title + " S" + padZero(m.SeasonNumber) + "E" + padZero(m.EpisodeNumber)
	}
	if m.Year > 0 {
		// Format: "The Matrix (1999)"
		return m.Title + " (" + itoa(m.Year) + ")"
	}
	return m.Title
}

// padZero pads a number with leading zero if < 10
func padZero(n int) string {
	if n < 10 {
		return "0" + itoa(n)
	}
	return itoa(n)
}

// itoa converts int to string without importing strconv
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		return "-" + itoa(-n)
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// HealthChecker defines the interface for checking file health
type HealthChecker interface {
	Check(path, mode string) (bool, *HealthCheckError)
	CheckWithConfig(path string, config DetectionConfig) (bool, *HealthCheckError)
	AnalyzeContent(path string) (bool, *HealthCheckError)
}

// PathMapper defines the interface for translating paths
type PathMapper interface {
	ToArrPath(localPath string) (string, error)
	ToLocalPath(arrPath string) (string, error)
	Reload() error
}

// Error types for health check failures
const (
	// Corruption types - file exists but is damaged
	ErrorTypeZeroByte      = "ZeroByte"      // File is 0 bytes
	ErrorTypeCorruptHeader = "CorruptHeader" // Container/header corruption
	ErrorTypeCorruptStream = "CorruptStream" // Stream-level corruption
	ErrorTypeInvalidFormat = "InvalidFormat" // Not a valid media file

	// Content analysis types - structurally valid but content is corrupt
	ErrorTypeBlackVideo  = "BlackVideo"  // Video is entirely/mostly black
	ErrorTypeFrozenVideo = "FrozenVideo" // Video is frozen on a single frame
	ErrorTypeSilentAudio = "SilentAudio" // Audio is completely silent

	// Accessibility types - transient/infrastructure issues (should NOT trigger remediation)
	ErrorTypeAccessDenied  = "AccessDenied"  // Permission error
	ErrorTypePathNotFound  = "PathNotFound"  // File or parent directory missing
	ErrorTypeMountLost     = "MountLost"     // Mount point appears unmounted
	ErrorTypeIOError       = "IOError"       // Generic I/O error (network, disk)
	ErrorTypeTimeout       = "Timeout"       // Operation timed out
	ErrorTypeInvalidConfig = "InvalidConfig" // Bad detection configuration
)

// HealthCheckError contains details about why a file is unhealthy
type HealthCheckError struct {
	Type    string
	Message string
}

// ErrorCategory classifies a HealthCheckError for remediation routing.
// Every ErrorType constant defined above must be registered in
// errorCategories below — the package init verifies this. A missing
// registration is treated as a test-time panic; in production it falls
// back to CategoryRecoverable so unknown errors retry rather than delete
// the user's files.
type ErrorCategory int

const (
	// CategoryUnknown means the error type isn't registered. Only returned
	// when an unregistered type is looked up at runtime; ErrorCategory
	// itself should never be persisted with this value.
	CategoryUnknown ErrorCategory = iota
	// CategoryRecoverable: transient infrastructure issue; the file remains
	// untouched and the scan retries later (NAS offline, mount lost,
	// permission glitch, network timeout, config error).
	CategoryRecoverable
	// CategoryTrueCorruption: actual file damage; the remediation pipeline
	// deletes and re-downloads.
	CategoryTrueCorruption
)

// errorCategories is the authoritative map from ErrorType string → category.
//
// Adding a new ErrorType constant MUST add a corresponding entry here, or
// the package init test will panic at CI time. This is the property that
// closes T1 from the audit: previously a new error type silently fell
// through both IsRecoverable and IsTrueCorruption (returning false from
// each), making it invisible to remediation routing.
var errorCategories = map[string]ErrorCategory{
	// Corruption types — file exists but is damaged.
	ErrorTypeZeroByte:      CategoryTrueCorruption,
	ErrorTypeCorruptHeader: CategoryTrueCorruption,
	ErrorTypeCorruptStream: CategoryTrueCorruption,
	ErrorTypeInvalidFormat: CategoryTrueCorruption,

	// Content analysis types — structurally valid but content is corrupt.
	ErrorTypeBlackVideo:  CategoryTrueCorruption,
	ErrorTypeFrozenVideo: CategoryTrueCorruption,
	ErrorTypeSilentAudio: CategoryTrueCorruption,

	// Accessibility types — transient / infrastructure issues.
	ErrorTypeAccessDenied:  CategoryRecoverable,
	ErrorTypePathNotFound:  CategoryRecoverable,
	ErrorTypeMountLost:     CategoryRecoverable,
	ErrorTypeIOError:       CategoryRecoverable,
	ErrorTypeTimeout:       CategoryRecoverable,
	ErrorTypeInvalidConfig: CategoryRecoverable,
}

// category returns the registered category for this error type. Missing
// entries panic in test binaries (so new ErrorType constants force a
// matching registration) and fall back to CategoryRecoverable in production
// (so unknown errors retry rather than triggering destructive remediation).
func (e *HealthCheckError) category() ErrorCategory {
	if cat, ok := errorCategories[e.Type]; ok {
		return cat
	}
	if testing.Testing() {
		panic(fmt.Sprintf("HealthCheckError: unregistered error type %q — add it to errorCategories in internal/integration/interfaces.go", e.Type))
	}
	logger.Errorf("HealthCheckError: unregistered error type %q; treating as Recoverable (conservative fallback to avoid destructive remediation). Add to errorCategories in internal/integration/interfaces.go.", e.Type)
	return CategoryRecoverable
}

// IsRecoverable returns true if this error type represents a potentially
// transient condition that should NOT trigger file remediation.
// Examples: NAS offline, mount lost, permission issues, network glitches.
func (e *HealthCheckError) IsRecoverable() bool {
	return e.category() == CategoryRecoverable
}

// IsTrueCorruption returns true if this error represents actual file corruption
// that warrants remediation (re-download).
func (e *HealthCheckError) IsTrueCorruption() bool {
	return e.category() == CategoryTrueCorruption
}
