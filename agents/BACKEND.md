# Healarr Backend

## Overview

The backend is written in **Go 1.25+** using the **Gin v1.11.0** web framework. It follows a clean architecture with clear separation between API, services, integrations, and data access.

## Directory Structure

```
internal/
├── api/
│   ├── rest.go              # Server setup, routes, auth middleware (~400 lines)
│   ├── websocket.go         # WebSocket hub for real-time updates
│   ├── rate_limit.go        # Rate limiters for login/setup/webhook
│   ├── handlers_health.go   # Health check, system info endpoints
│   ├── handlers_auth.go     # Authentication, API key, password management
│   ├── handlers_config.go   # Settings, restart, export/import, backup
│   ├── handlers_arr.go      # *arr instance CRUD, connection testing
│   ├── handlers_paths.go    # Scan path CRUD, directory browser, detection preview
│   ├── handlers_scans.go    # Scan triggering, status, pause/resume/cancel
│   ├── handlers_corruptions.go  # Corruption listing, history, retry/ignore/delete
│   ├── handlers_stats.go    # Dashboard stats and history
│   ├── handlers_schedules.go    # Schedule CRUD
│   ├── handlers_notifications.go # Notification CRUD and testing
│   ├── handlers_webhook.go  # Incoming webhooks from *arr
│   └── handlers_logs.go     # Log viewing and download
├── auth/
│   └── auth.go          # Password hashing (bcrypt) and verification
├── config/
│   └── config.go        # Environment variable loading
├── crypto/
│   └── crypto.go        # Encryption for API keys at rest
├── db/
│   ├── repository.go    # Database operations
│   └── migrations/
│       ├── 001_schema.sql           # Base schema
│       ├── 002_resumable_scans.sql  # Pause/resume support
│       ├── 003_accessibility_errors.sql
│       ├── 004_pending_rescans.sql
│       └── 005_per_path_dry_run.sql # Per-path dry-run mode
├── domain/
│   └── events.go        # Event type definitions (22 types)
├── eventbus/
│   └── eventbus.go      # Pub/sub with persistence
├── integration/
│   ├── arr_client.go    # Sonarr/Radarr/Whisparr API client (rate-limited)
│   ├── health_checker.go # ffprobe corruption detection
│   ├── interfaces.go    # Integration interfaces with error types
│   └── path_mapper.go   # Path translation
├── logger/
│   └── logger.go        # Structured logging with file rotation
├── notifier/
│   └── notifier.go      # Webhook notifications (Discord, Slack, custom)
└── services/
    ├── scanner.go       # File scanning with pause/resume/cancel
    ├── remediator.go    # Remediation orchestration
    ├── verifier.go      # Queue-based verification
    ├── monitor.go       # Lifecycle tracking
    └── scheduler.go     # Cron scheduling
```

## Entry Point

`cmd/server/main.go`:

```go
func main() {
    // 1. Parse command-line flags (override environment variables)
    flagPort := flag.String("port", "", "HTTP server port")
    flagRetentionDays := flag.Int("retention-days", -1, "Days to keep old data")
    // ... more flags
    flag.Parse()

    // 2. Load config from environment variables
    cfg := config.Load()

    // 3. Apply command-line flag overrides
    config.ApplyFlags(flagOverrides)

    // 4. Initialize logger
    logger.Initialize(cfg.LogLevel)

    // 5. Initialize database with migrations
    repo, _ := db.NewRepository(cfg.DatabasePath)
    // Database automatically configured with:
    // - WAL mode for concurrency
    // - Auto-vacuum (incremental)
    // - Integrity check on startup

    // 6. Start scheduled backup (every 6 hours)
    go func() { /* backup goroutine */ }()

    // 7. Start scheduled maintenance (daily at 3 AM)
    go func() {
        // Prunes old data, vacuums, analyzes
        repo.RunMaintenance(cfg.RetentionDays)
    }()

    // 8. Initialize EventBus
    eb := eventbus.NewEventBus(repo.DB)

    // 9. Initialize integrations
    pathMapper := integration.NewPathMapper(repo.DB)
    healthChecker := integration.NewHealthChecker()
    arrClient := integration.NewArrClient(repo.DB) // Rate-limited

    // 10. Initialize services
    scannerService := services.NewScannerService(...)
    remediatorService := services.NewRemediatorService(...)
    verifierService := services.NewVerifierService(...)
    monitorService := services.NewMonitorService(...)
    schedulerService := services.NewSchedulerService(...)
    notifier := notifier.NewNotifier(...)

    // 11. Start services (subscribe to events)
    remediatorService.Start()
    verifierService.Start()
    monitorService.Start()
    schedulerService.Start()
    notifier.Start()

    // 12. Start API server
    apiServer := api.NewRESTServer(...)
    apiServer.Start(":3090")
}
```

## API Layer (`internal/api/`)

The API layer is organized into domain-specific handler files for maintainability:

### Handler File Organization

| File | Purpose | ~Lines |
|------|---------|--------|
| `rest.go` | Server setup, route registration, auth middleware | 400 |
| `handlers_health.go` | Health check, system info | 136 |
| `handlers_auth.go` | Authentication, API key, password | 225 |
| `handlers_config.go` | Settings, restart, export/import, backup | 326 |
| `handlers_arr.go` | *arr instance CRUD, connection testing | 168 |
| `handlers_paths.go` | Scan path CRUD, directory browser | 343 |
| `handlers_scans.go` | Scan triggering, status, pause/resume | 417 |
| `handlers_corruptions.go` | Corruption listing, history, actions | 340 |
| `handlers_stats.go` | Dashboard stats and history | 172 |
| `handlers_schedules.go` | Schedule CRUD | 115 |
| `handlers_notifications.go` | Notification CRUD and testing | 176 |
| `handlers_webhook.go` | Incoming webhooks from *arr | 128 |
| `handlers_logs.go` | Log viewing and download | 132 |

### REST Endpoints (55+ routes)

**Public Endpoints:**
| Method | Path | Handler File | Purpose |
|--------|------|--------------|---------|
| `GET` | `/api/health` | handlers_health.go | Health check (no auth) |
| `POST` | `/api/auth/setup` | handlers_auth.go | Initial password setup |
| `POST` | `/api/auth/login` | handlers_auth.go | User login |
| `GET` | `/api/auth/status` | handlers_auth.go | Check auth status |
| `POST` | `/api/webhook/:instance_id` | handlers_webhook.go | Incoming webhooks from *arr |

**Protected Endpoints (require `X-API-Key`):**

| Category | Method | Path | Handler File |
|----------|--------|------|--------------|
| **Auth** | `GET` | `/auth/key` | handlers_auth.go |
| | `POST` | `/auth/regenerate` | handlers_auth.go |
| | `POST` | `/auth/password` | handlers_auth.go |
| **Config** | `PUT` | `/config/settings` | handlers_config.go |
| | `POST` | `/config/restart` | handlers_config.go |
| | `GET` | `/config/export` | handlers_config.go |
| | `POST` | `/config/import` | handlers_config.go |
| | `GET` | `/config/backup` | handlers_config.go |
| **Instances** | `GET` | `/config/arr` | handlers_arr.go |
| | `POST` | `/config/arr` | handlers_arr.go |
| | `POST` | `/config/arr/test` | handlers_arr.go |
| | `PUT` | `/config/arr/:id` | handlers_arr.go |
| | `DELETE` | `/config/arr/:id` | handlers_arr.go |
| **Paths** | `GET` | `/config/paths` | handlers_paths.go |
| | `POST` | `/config/paths` | handlers_paths.go |
| | `PUT` | `/config/paths/:id` | handlers_paths.go |
| | `DELETE` | `/config/paths/:id` | handlers_paths.go |
| | `GET` | `/config/browse` | handlers_paths.go |
| | `GET` | `/config/detection-preview` | handlers_paths.go |
| **Schedules** | `GET` | `/config/schedules` | handlers_schedules.go |
| | `POST` | `/config/schedules` | handlers_schedules.go |
| | `PUT` | `/config/schedules/:id` | handlers_schedules.go |
| | `DELETE` | `/config/schedules/:id` | handlers_schedules.go |
| **Notifications** | `GET` | `/config/notifications` | handlers_notifications.go |
| | `POST` | `/config/notifications` | handlers_notifications.go |
| | `PUT` | `/config/notifications/:id` | handlers_notifications.go |
| | `DELETE` | `/config/notifications/:id` | handlers_notifications.go |
| | `POST` | `/config/notifications/test` | handlers_notifications.go |
| **Stats** | `GET` | `/stats/dashboard` | handlers_stats.go |
| | `GET` | `/stats/history` | handlers_stats.go |
| | `GET` | `/stats/types` | handlers_stats.go |
| **Corruptions** | `GET` | `/corruptions` | handlers_corruptions.go |
| | `GET` | `/corruptions/:id/history` | handlers_corruptions.go |
| | `POST` | `/corruptions/retry` | handlers_corruptions.go |
| | `POST` | `/corruptions/ignore` | handlers_corruptions.go |
| | `POST` | `/corruptions/delete` | handlers_corruptions.go |
| **Scans** | `GET` | `/scans` | handlers_scans.go |
| | `GET` | `/scans/active` | handlers_scans.go |
| | `POST` | `/scans` | handlers_scans.go |
| | `POST` | `/scans/all` | handlers_scans.go |
| | `POST` | `/scans/pause-all` | handlers_scans.go |
| | `POST` | `/scans/resume-all` | handlers_scans.go |
| | `POST` | `/scans/cancel-all` | handlers_scans.go |
| | `GET` | `/scans/:id` | handlers_scans.go |
| | `GET` | `/scans/:id/files` | handlers_scans.go |
| | `DELETE` | `/scans/:id` | handlers_scans.go |
| | `POST` | `/scans/:id/pause` | handlers_scans.go |
| | `POST` | `/scans/:id/resume` | handlers_scans.go |
| | `POST` | `/scans/:id/rescan` | handlers_scans.go |
| **Logs** | `GET` | `/logs/recent` | handlers_logs.go |
| | `GET` | `/logs/download` | handlers_logs.go |
| **WebSocket** | `GET` | `/ws` | rest.go (inline) |

### Route Registration Note

**IMPORTANT**: When adding routes with path parameters, specific literal routes MUST come BEFORE parameterized routes:

```go
// ✅ CORRECT ORDER
protected.POST("/scans/all", s.triggerScanAll)      // Literal first
protected.POST("/scans/pause-all", s.pauseAllScans) // Literal first
protected.POST("/scans/:scan_id/pause", s.pauseScan) // Parameter after

// ❌ WRONG ORDER (pause-all would match as :scan_id)
protected.POST("/scans/:scan_id/pause", s.pauseScan)
protected.POST("/scans/pause-all", s.pauseAllScans)  // Never reached!
```

### Adding New Endpoints

1. Identify the appropriate handler file based on domain (e.g., `handlers_scans.go` for scan-related endpoints)
2. Add the handler method as a receiver on `*RESTServer`
3. Register the route in `rest.go` in the `setupRoutes()` function
4. Follow the route ordering rules above for parameterized routes

### Authentication

- Password-based with bcrypt hashing
- API key stored in `X-API-Key` header
- Also supports `Authorization: Bearer <token>` and `?apikey=` query param
- Rate limiting on login (5/min), setup (3/hour), webhooks (100/min)

### WebSocket

```go
// websocket.go
type Hub struct {
    clients    map[*Client]bool
    broadcast  chan []byte
    register   chan *Client
    unregister chan *Client
}
```

Broadcasts all events to connected clients for real-time UI updates.

## Services Layer (`internal/services/`)

### ScannerService

```go
type ScannerService struct {
    db          *sql.DB
    eventBus    *eventbus.EventBus
    detector    integration.HealthChecker
    pathMapper  integration.PathMapper
    activeScans map[string]*ScanProgress
    mu          sync.Mutex
}

// Key methods
func (s *ScannerService) ScanPath(pathID int) error
func (s *ScannerService) CancelScan(scanID string) error
func (s *ScannerService) PauseScan(scanID string) error
func (s *ScannerService) ResumeScan(scanID string) error
func (s *ScannerService) ScanAllPaths() (started, skipped int, err error)
func (s *ScannerService) PauseAllScans() int
func (s *ScannerService) ResumeAllScans() int
func (s *ScannerService) CancelAllScans() int
```

Features:
- Per-path `dry_run` mode
- Accessibility error detection (skips transient issues)
- Pause/resume support
- Bulk operations (scan all, pause all, etc.)

### VerifierService

```go
type VerifierService struct {
    eventBus      *eventbus.EventBus
    arrClient     integration.ArrClient
    pathMapper    integration.PathMapper
    healthChecker integration.HealthChecker
}

// Primary verification method - uses *arr queue/history
func (v *VerifierService) pollWithQueueMonitoring(ctx context.Context, ...) error {
    // 1. Check *arr queue for active download
    // 2. Check *arr history for import events
    // 3. Check *arr API for current file path
    // 4. Fallback to filesystem polling
}
```

## Integration Layer (`internal/integration/`)

### Interfaces (`interfaces.go`)

```go
// ArrClient - *arr API operations
type ArrClient interface {
    FindMediaByPath(path string) (int64, error)
    DeleteFile(mediaID int64, path string) (map[string]interface{}, error)
    GetFilePath(mediaID int64, metadata map[string]interface{}, referencePath string) (string, error)
    TriggerSearch(mediaID int64, path string, episodeIDs []int64) error
    GetAllInstances() ([]*ArrInstanceInfo, error)
    GetInstanceByID(id int64) (*ArrInstanceInfo, error)
    GetQueueForPath(arrPath string) ([]QueueItemInfo, error)
    FindQueueItemsByMediaIDForPath(arrPath string, mediaID int64) ([]QueueItemInfo, error)
    GetDownloadStatusForPath(arrPath string, downloadID string) (status string, progress float64, errMsg string, err error)
    GetRecentHistoryForMediaByPath(arrPath string, mediaID int64, limit int) ([]HistoryItemInfo, error)
    RemoveFromQueueByPath(arrPath string, queueID int64, removeFromClient, blocklist bool) error
    RefreshMonitoredDownloadsByPath(arrPath string) error
}

// HealthChecker - file validation
type HealthChecker interface {
    Check(path string, mode string) (bool, *HealthCheckError)
    CheckWithConfig(path string, config DetectionConfig) (bool, *HealthCheckError)
}

// PathMapper - path translation
type PathMapper interface {
    ToArrPath(localPath string) (string, error)
    ToLocalPath(arrPath string) (string, error)
    Reload() error
}
```

### Error Types

```go
// Corruption types (trigger remediation)
ErrorTypeZeroByte      = "ZeroByte"
ErrorTypeCorruptHeader = "CorruptHeader"
ErrorTypeCorruptStream = "CorruptStream"
ErrorTypeInvalidFormat = "InvalidFormat"

// Accessibility types (skip, don't remediate)
ErrorTypeAccessDenied  = "AccessDenied"
ErrorTypePathNotFound  = "PathNotFound"
ErrorTypeMountLost     = "MountLost"
ErrorTypeIOError       = "IOError"
ErrorTypeTimeout       = "Timeout"
```

### ArrClient with Rate Limiting

```go
// arr_client.go
type ArrClient struct {
    db      *sql.DB
    limiter *rate.Limiter  // 5 req/s, burst 10
}

func (c *ArrClient) rateLimitedRequest(method, url string, body interface{}) (*http.Response, error) {
    c.limiter.Wait(context.Background())  // Block until allowed
    // ... make request
}
```

### Instance Types

- `sonarr` - Sonarr v3 (`/api/v3/episodefile`, episode-based)
- `radarr` - Radarr v3 (`/api/v3/moviefile`, movie-based)
- `whisparr-v2` - Uses Sonarr-like API
- `whisparr-v3` - Uses Radarr-like API

## EventBus (`internal/eventbus/`)

```go
type EventBus struct {
    db          *sql.DB
    subscribers map[domain.EventType][]func(domain.Event)
    mu          sync.RWMutex
    wsHub       *api.Hub
}

func (eb *EventBus) Publish(event domain.Event) {
    // 1. Persist to database
    eb.db.Exec(`INSERT INTO events ...`, event)
    
    // 2. Deliver to subscribers (async)
    for _, handler := range eb.subscribers[event.EventType] {
        go handler(event)
    }
    
    // 3. Broadcast to WebSocket clients
    if eb.wsHub != nil {
        eb.wsHub.Broadcast(event)
    }
}
```

## Logger (`internal/logger/`)

```go
// Structured logging with rotation
logger.Debugf("Processing file: %s", path)
logger.Infof("Scan completed: %d files", count)
logger.Errorf("Failed to connect: %v", err)

// Log file: logs/healarr.log (with rotation)
// No console output goes to stdout (redirected to /dev/null)
```

## Building

```bash
# Development
go run ./cmd/server

# Production binary
go build -o healarr ./cmd/server

# Using management script
./healarr.sh rebuild    # Build and restart
./healarr.sh start      # Start server
./healarr.sh stop       # Stop server
./healarr.sh status     # Check status

# Docker
docker build -t healarr .
```

## Testing

```bash
# Run all tests
go test ./...

# With coverage
go test -cover ./...

# Specific package
go test ./internal/services/...
```

## Common Patterns

### Error Handling

```go
// Return errors up the stack
func (s *Service) DoSomething() error {
    result, err := s.dependency.Action()
    if err != nil {
        logger.Errorf("Action failed: %v", err)
        return fmt.Errorf("action failed: %w", err)
    }
    return nil
}

// Emit failure events for async operations
func (r *RemediatorService) handleDeletion(event domain.Event) {
    err := r.arrClient.DeleteFile(...)
    if err != nil {
        r.eventBus.Publish(domain.Event{
            EventType: domain.DeletionFailed,
            EventData: map[string]interface{}{"error": err.Error()},
        })
        return
    }
    r.eventBus.Publish(domain.Event{EventType: domain.DeletionCompleted})
}
```

### Dry-Run Mode

```go
func (r *RemediatorService) handleCorruptionDetected(event domain.Event) {
    // Check if path has dry_run enabled
    if pathConfig.DryRun {
        logger.Infof("Dry-run mode: would remediate %s", filePath)
        return  // Don't actually remediate
    }
    // Proceed with remediation...
}
```
