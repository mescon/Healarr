# Healarr Architecture

## System Overview

Healarr follows an **event-driven architecture** where state changes are captured as events and processed asynchronously. This enables loose coupling, auditability, and resilience.

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                              HEALARR SYSTEM                                  │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                         REST API (Gin)                                │   │
│  │   /api/stats  /api/corruptions  /api/scans  /api/config  /api/logs   │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                    │                                         │
│                                    ▼                                         │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                         WebSocket Hub                                 │   │
│  │              Real-time updates to connected clients                   │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                    │                                         │
│       ┌────────────────────────────┼────────────────────────────┐           │
│       ▼                            ▼                            ▼           │
│  ┌──────────┐              ┌──────────────┐              ┌──────────┐       │
│  │ Scanner  │              │   EventBus   │              │ Notifier │       │
│  │ Service  │─────────────▶│  (Pub/Sub)   │◀─────────────│ Service  │       │
│  └──────────┘              └──────────────┘              └──────────┘       │
│       │                           │                            │            │
│       │         ┌─────────────────┼─────────────────┐          │            │
│       │         ▼                 ▼                 ▼          │            │
│       │    ┌──────────┐    ┌──────────┐    ┌──────────┐        │            │
│       │    │Remediator│    │ Monitor  │    │ Verifier │        │            │
│       │    │ Service  │    │ Service  │    │ Service  │        │            │
│       │    └──────────┘    └──────────┘    └──────────┘        │            │
│       │         │                                │              │            │
│       │         └────────────────────────────────┘              │            │
│       │                          │                              │            │
│       ▼                          ▼                              ▼            │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                       Integration Layer                               │   │
│  │  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  │   │
│  │  │HealthChecker│  │  ArrClient  │  │ PathMapper  │  │  Scheduler  │  │   │
│  │  │  (ffprobe)  │  │(rate-limited│  │(path xlate) │  │   (cron)    │  │   │
│  │  │             │  │ 5req/s)     │  │             │  │             │  │   │
│  │  └─────────────┘  └─────────────┘  └─────────────┘  └─────────────┘  │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                    │                                         │
│                                    ▼                                         │
│  ┌──────────────────────────────────────────────────────────────────────┐   │
│  │                        SQLite Database                                │   │
│  │    events │ corruptions │ scans │ scan_paths │ instances │ settings  │   │
│  └──────────────────────────────────────────────────────────────────────┘   │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## Event Flow

### 1. Corruption Detection Flow

```
User clicks "Scan" → API → ScannerService.ScanPath()
                              │
                              ▼
                    Enumerate media files
                              │
                              ▼
                    For each file: HealthChecker.Check()
                              │
                              ├── Healthy → Log & continue
                              │
                              ├── Accessibility Error → Log warning, skip (don't remediate)
                              │   (mount lost, permission denied, timeout)
                              │
                              └── Corruption → Publish CorruptionDetected
                                              │
                                              ▼
                                   MonitorService.handleCorruptionDetected()
                                   (creates DB record, tracks lifecycle)
                                              │
                                              ▼
                                   RemediatorService.handleCorruptionDetected()
                                   (if auto_remediate enabled AND NOT dry_run)
```

### 2. Remediation Flow

```
CorruptionDetected event
        │
        ▼
RemediatorService.handleCorruptionDetected()
        │
        ├── Check dry_run mode → If true, skip remediation
        │
        ├── Map local path → *arr path (PathMapper)
        │
        ├── Publish RemediationQueued
        │
        └── If auto_remediate:
                │
                ▼
        ArrClient.DeleteFile(mediaID) [rate-limited]
                │
                ├── Success → Publish DeletionCompleted
                │               │
                │               ▼
                │     ArrClient.TriggerSearch(mediaID)
                │               │
                │               ├── Success → Publish SearchCompleted
                │               │               │
                │               │               ▼
                │               │     VerifierService takes over
                │               │
                │               └── Failure → Publish SearchFailed
                │
                └── Failure → Publish DeletionFailed
```

### 3. Verification Flow (Queue-Based)

```
SearchCompleted event
        │
        ▼
VerifierService.handleSearchCompleted()
        │
        ▼
pollWithQueueMonitoring() - PRIMARY METHOD
        │
        ├── Step 1: GetQueueForPath() - Check *arr download queue
        │            │
        │            ├── Found in queue → monitorDownloadProgress()
        │            │                    │
        │            │                    └── Wait for download, emit DownloadProgress events
        │            │
        │            └── Not in queue → Step 2
        │
        ├── Step 2: GetRecentHistoryForMediaByPath() - Check history for import
        │            │
        │            ├── Found import event → Extract imported path
        │            │                        │
        │            │                        └── HealthChecker.Check()
        │            │                            │
        │            │                            ├── Healthy → Publish VerificationSuccess
        │            │                            │
        │            │                            └── Corrupt → Retry or fail
        │            │
        │            └── No import found → Step 3
        │
        ├── Step 3: ArrClient.GetFilePath() - Ask *arr for current file path
        │            │
        │            └── If different from corrupt file → Check new file health
        │
        └── Fallback: pollFileExistence() - Direct file system check
                     │
                     └── Timeout → Publish DownloadTimeout
```

## Event Types (22 Total)

| Event | Publisher | Subscribers |
|-------|-----------|-------------|
| `CorruptionDetected` | Scanner | Monitor, Remediator, Notifier |
| `RemediationQueued` | Remediator | Monitor |
| `DeletionStarted` | Remediator | Monitor |
| `DeletionCompleted` | Remediator | Monitor, Verifier |
| `DeletionFailed` | Remediator | Monitor, Notifier |
| `SearchStarted` | Remediator | Monitor |
| `SearchCompleted` | Remediator | Monitor, Verifier |
| `SearchFailed` | Remediator | Monitor, Notifier |
| `FileDetected` | Verifier | Monitor |
| `VerificationStarted` | Verifier | Monitor |
| `VerificationSuccess` | Verifier | Monitor, Notifier |
| `VerificationFailed` | Verifier | Monitor, Notifier |
| `DownloadTimeout` | Verifier | Monitor |
| `DownloadProgress` | Verifier | Monitor, WebSocket |
| `DownloadFailed` | Verifier | Monitor, Notifier |
| `RetryScheduled` | Monitor | Remediator |
| `MaxRetriesReached` | Monitor | Notifier |
| `ScanStarted` | Scanner | WebSocket |
| `ScanCompleted` | Scanner | WebSocket |
| `ScanFailed` | Scanner | WebSocket |
| `ScanProgress` | Scanner | WebSocket |
| `ScanPaused` | Scanner | WebSocket |

## Service Responsibilities

### ScannerService

- **Purpose**: Scan directories/files for corruption
- **Triggers**: Manual API call, scheduled cron job, bulk "Scan All"
- **Publishes**: `ScanStarted`, `ScanProgress`, `ScanCompleted`, `ScanPaused`, `CorruptionDetected`
- **Key Methods**:
  - `ScanPath(pathID)` - Scan all files in a configured path
  - `ScanFile(filePath)` - Scan a single file
  - `CancelScan(scanID)` - Cancel an in-progress scan
  - `PauseScan(scanID)` / `ResumeScan(scanID)` - Pause/resume support
- **Features**:
  - Per-path dry-run mode (detect but don't remediate)
  - Accessibility error detection (skip transient issues)
  - Resumable scans (tracks last processed file)

### RemediatorService

- **Purpose**: Orchestrate file deletion and re-download via *arr
- **Subscribes To**: `CorruptionDetected`, `RetryScheduled`
- **Publishes**: `RemediationQueued`, `DeletionStarted`, `DeletionCompleted`, `DeletionFailed`, `SearchStarted`, `SearchCompleted`, `SearchFailed`
- **Key Behavior**: 
  - Only acts if `auto_remediate` is enabled for the path
  - Respects `dry_run` mode (skips actual remediation)
  - Rate-limited API calls to *arr (5 req/s, burst 10)

### VerifierService

- **Purpose**: Confirm replacement file is healthy
- **Subscribes To**: `SearchCompleted`, `DeletionCompleted`
- **Publishes**: `VerificationStarted`, `VerificationSuccess`, `VerificationFailed`, `DownloadTimeout`, `DownloadProgress`, `DownloadFailed`
- **Verification Strategy** (in priority order):
  1. **Queue monitoring**: Check *arr download queue for active download
  2. **History check**: Look for import events in *arr history
  3. **API check**: Ask *arr for current file path
  4. **Filesystem fallback**: Direct file existence check

### MonitorService

- **Purpose**: Track corruption lifecycle, manage retries
- **Subscribes To**: All events
- **Publishes**: `RetryScheduled`, `MaxRetriesReached`
- **Key Behavior**: Updates DB records, enforces retry limits (per-path configurable)

### SchedulerService

- **Purpose**: Run scans on cron schedules
- **Configuration**: Per-path cron expressions stored in DB
- **Key Methods**:
  - `Start()` - Load schedules from DB, start cron runner
  - `UpdateSchedule(pathID, cronExpr)` - Update a path's schedule

### Notifier

- **Purpose**: Send webhooks for important events
- **Subscribes To**: `CorruptionDetected`, `VerificationSuccess`, `VerificationFailed`, `MaxRetriesReached`, `DownloadFailed`
- **Configuration**:
  - Global notification configs in DB
  - Per-instance webhook URLs (takes precedence)
- **Supported Types**: Discord, Slack, custom HTTP webhook

### RecoveryService

- **Purpose**: Recover items stuck in intermediate states on startup
- **Runs**: Once during application startup (after all other services subscribe to events)
- **Publishes**: `RetryScheduled`, `SearchStarted`, `SearchCompleted`, `SearchFailed`, `MaxRetriesReached`, `VerificationSuccess`, `SearchExhausted`
- **Key Behavior**:
  - Finds items stuck in non-terminal states older than `staleThreshold` (default: 24h)
  - Routes items to appropriate recovery handler based on state category:
    1. **Early remediation states** (`RemediationQueued`, `DeletionStarted`, `DeletionCompleted`): Re-trigger remediation flow
    2. **Post-search states** (`SearchStarted`, `SearchCompleted`, `DownloadProgress`, etc.): Verify if file exists and is healthy
    3. **Failed states** (`DeletionFailed`, `SearchFailed`, `VerificationFailed`, etc.): Schedule retry if under max retries, else mark exhausted
- **Critical for Autonomous Operation**:
  - Handles restart scenarios where in-memory retry timers (`time.AfterFunc`) were lost
  - Prevents items from being permanently stuck due to missed events
  - Completes remediation cycles that were interrupted mid-flight

```
Startup Sequence:
  1. All services start and subscribe to events
  2. RecoveryService.Run() executes
  3. ScannerService.ResumeInterruptedScans()
```

## Integration Layer

### HealthChecker

Uses **ffprobe** to validate media files:

```go
type HealthChecker interface {
    Check(path string, mode string) (bool, *HealthCheckError)
    CheckWithConfig(path string, config DetectionConfig) (bool, *HealthCheckError)
}
```

**Error Types** (in `interfaces.go`):
- **Corruption types** (trigger remediation):
  - `ZeroByte` - File is 0 bytes
  - `CorruptHeader` - Container/header corruption
  - `CorruptStream` - Stream-level corruption
  - `InvalidFormat` - Not a valid media file
  
- **Accessibility types** (skip, don't remediate):
  - `AccessDenied` - Permission error
  - `PathNotFound` - File or parent directory missing
  - `MountLost` - Mount point appears unmounted
  - `IOError` - Generic I/O error
  - `Timeout` - Operation timed out

### ArrClient

Communicates with Sonarr/Radarr/Whisparr APIs with **rate limiting**:

```go
type ArrClient interface {
    // Media operations
    FindMediaByPath(path string) (int64, error)
    DeleteFile(mediaID int64, path string) (map[string]interface{}, error)
    GetFilePath(mediaID int64, metadata map[string]interface{}, referencePath string) (string, error)
    TriggerSearch(mediaID int64, path string, episodeIDs []int64) error

    // Instance management
    GetAllInstances() ([]*ArrInstanceInfo, error)
    GetInstanceByID(id int64) (*ArrInstanceInfo, error)

    // Queue monitoring
    GetQueueForPath(arrPath string) ([]QueueItemInfo, error)
    FindQueueItemsByMediaIDForPath(arrPath string, mediaID int64) ([]QueueItemInfo, error)
    GetDownloadStatusForPath(arrPath string, downloadID string) (status string, progress float64, errMsg string, err error)

    // History
    GetRecentHistoryForMediaByPath(arrPath string, mediaID int64, limit int) ([]HistoryItemInfo, error)

    // Queue management
    RemoveFromQueueByPath(arrPath string, queueID int64, removeFromClient, blocklist bool) error
    RefreshMonitoredDownloadsByPath(arrPath string) error
}
```

**Rate Limiting**: Token bucket algorithm - 5 requests/second, burst of 10.

**Supported Instance Types**:
- `sonarr` - Sonarr v3 (episode-based)
- `radarr` - Radarr v3 (movie-based)
- `whisparr-v2` - Whisparr v2 (Sonarr-like API)
- `whisparr-v3` - Whisparr v3 (Radarr-like API)

### PathMapper

Translates between local and *arr paths:

```go
type PathMapper interface {
    ToArrPath(localPath string) (string, error)
    ToLocalPath(arrPath string) (string, error)
    Reload() error
}
```

## Concurrency Model

- **Scanner**: Runs in dedicated goroutine per scan, supports pause/resume via channels
- **Services**: Subscribe to events, handle in goroutines
- **WebSocket**: Hub pattern with client registry
- **Database**: SQLite with WAL mode for concurrent reads
- **Rate Limiter**: Thread-safe token bucket shared across all *arr API calls

## Error Handling

- **Transient failures**: Retry with backoff (per-path max_retries)
- **API errors**: Log and emit failure event
- **Accessibility errors**: Log warning, skip file, don't trigger remediation
- **Unrecoverable**: Emit `MaxRetriesReached`, require manual intervention

## Configuration Sources

Priority (highest to lowest):
1. Environment variables (`HEALARR_*`)
2. Database settings table
3. Compiled defaults

## Per-Path Configuration

Each scan path can have independent settings:
- `auto_remediate` - Whether to automatically fix detected corruptions
- `dry_run` - Detect only, don't trigger any remediation actions
- `max_retries` - Number of retry attempts before giving up
- `verification_timeout` - How long to wait for re-download
- `cron_schedule` - When to run scheduled scans
