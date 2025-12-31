package integration

// ArrInstance represents a configured *arr instance
type ArrInstanceInfo struct {
	ID     int64
	Name   string
	Type   string // sonarr, radarr, whisparr
	URL    string
	APIKey string
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

	// Queue monitoring - track active downloads
	GetQueueForPath(arrPath string) ([]QueueItemInfo, error)
	FindQueueItemsByMediaIDForPath(arrPath string, mediaID int64) ([]QueueItemInfo, error)
	GetDownloadStatusForPath(arrPath string, downloadID string) (status string, progress float64, errMsg string, err error)

	// History - detect completed imports
	GetRecentHistoryForMediaByPath(arrPath string, mediaID int64, limit int) ([]HistoryItemInfo, error)

	// Queue management
	RemoveFromQueueByPath(arrPath string, queueID int64, removeFromClient, blocklist bool) error
	RefreshMonitoredDownloadsByPath(arrPath string) error
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
	Size                  int64
	SizeLeft              int64
	Progress              float64 // calculated: (size - sizeleft) / size * 100
	TimeLeft              string
	EstimatedCompletion   string
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
}

// HealthChecker defines the interface for checking file health
type HealthChecker interface {
	Check(path string, mode string) (bool, *HealthCheckError)
	CheckWithConfig(path string, config DetectionConfig) (bool, *HealthCheckError)
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

// IsRecoverable returns true if this error type represents a potentially
// transient condition that should NOT trigger file remediation.
// Examples: NAS offline, mount lost, permission issues, network glitches.
func (e *HealthCheckError) IsRecoverable() bool {
	switch e.Type {
	case ErrorTypeAccessDenied, ErrorTypePathNotFound, ErrorTypeMountLost,
		ErrorTypeIOError, ErrorTypeTimeout, ErrorTypeInvalidConfig:
		return true
	default:
		return false
	}
}

// IsTrueCorruption returns true if this error represents actual file corruption
// that warrants remediation (re-download).
func (e *HealthCheckError) IsTrueCorruption() bool {
	switch e.Type {
	case ErrorTypeZeroByte, ErrorTypeCorruptHeader, ErrorTypeCorruptStream, ErrorTypeInvalidFormat:
		return true
	default:
		return false
	}
}
