# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.1.21] - 2026-01-08

### Security
- **SSRF Protection**: Added URL validation for *arr instance endpoints
  - Restricts URL schemes to `http` and `https` only
  - Blocks dangerous protocols (`file://`, `gopher://`, `ldap://`, `javascript:`, `data:`)
  - Validates host presence in URLs
- **Path Traversal Protection**: Enhanced directory browser security
  - Sanitizes paths using `filepath.Clean`
  - Blocks `..` sequences and null bytes
  - Validates paths remain within allowed directories
- **Backup Cleanup Hardening**: Added path validation in database backup cleanup
  - Uses `filepath.Base` to ensure safe filenames
  - Defense-in-depth against directory traversal
- **Open Redirect Prevention**: Added base path validation in frontend
  - Blocks protocol-relative URLs (`//evil.com`)
  - Blocks absolute URLs with protocols
  - Blocks control characters in paths

### Added
- **Security Test Suites**: Comprehensive tests for all security protections
  - SSRF scheme blocking tests (7 schemes)
  - Path traversal pattern tests (5 patterns)
  - Integration tests for security endpoints

## [1.1.20] - 2026-01-06

### Added
- **First-time Setup Wizard**: Complete onboarding experience for new users
  - Multi-step wizard with animated transitions (Welcome → Security → *arr Instance → Scan Path → Complete)
  - Three setup modes: Fresh Start, Import Configuration, or Restore Database Backup
  - Connection testing for *arr instances with real-time feedback
  - Auto-detection of root folders from connected *arr instances
  - Skip option for power users who prefer manual configuration
- **Onboarding API endpoints**: Backend support for first-time setup wizard
  - `GET /api/setup/status` - Returns setup state (needs_setup, has_password, has_instances, etc.)
  - `POST /api/setup/dismiss` - Allows power users to skip the onboarding wizard
  - `POST /api/setup/import` - Import JSON config during first-time setup (public endpoint)
  - `POST /api/setup/restore` - Restore database backup during first-time setup (public endpoint)
- **Database restore functionality**: New `POST /api/config/restore` endpoint for authenticated users
  - Validates uploaded SQLite database before restore
  - Creates pre-restore backup automatically
  - Stages restore as `.pending` file (restart applies changes)
  - Requires `X-Confirm-Restore: true` header for safety
- **Root folder fetching**: New `GET /api/config/arr/:id/rootfolders` endpoint
  - Fetches configured root folders (library paths) from *arr instances
  - Returns path, free space, and total space for each root folder
  - Enables scan path configuration using paths already configured in Sonarr/Radarr

### Fixed
- **Null pointer in config import**: Added nil check for pathMapper before calling Reload()

## [1.1.19] - 2026-01-06

### Fixed
- **Migration 004 NULL safety**: Added COALESCE wrapper for `current_state` column population
  - Migration could fail with "NOT NULL constraint failed" on databases with corrupt or inconsistent event data
  - Now gracefully handles missing events by setting `current_state` to 'Unknown'
  - Only affected users upgrading from pre-1.1.18 with database corruption

## [1.1.18] - 2026-01-06

### Added
- **Custom binary path configuration**: Use custom or newer versions of detection tools
  - Environment variables: `HEALARR_FFPROBE_PATH`, `HEALARR_FFMPEG_PATH`, `HEALARR_MEDIAINFO_PATH`, `HEALARR_HANDBRAKE_PATH`
  - Tools directory: Place binaries in `$HEALARR_DATA_DIR/tools/` (default: `/config/tools/`) - auto-added to PATH
  - Supports both absolute paths and PATH-based lookup
  - Useful for users needing newer codec support or specific tool versions
- **Periodic WAL checkpoint**: Background goroutine checkpoints every 5 minutes to prevent unbounded WAL file growth
- **Graceful shutdown with checkpoint**: Final WAL checkpoint on shutdown ensures all data is synced to main database file

### Changed
- **Alpine Linux upgrade**: Updated from Alpine 3.20 to Alpine 3.23
  - ffmpeg: 6.1.1 → **8.0.1**
  - HandBrake: 1.6.1 → **1.10.2**
  - MediaInfo: 24.04 → **25.09**
- **Notifier shutdown**: `Stop()` now waits for background goroutine to exit, preventing test race conditions
- **Database reliability overhaul**: Major improvements to prevent database corruption
  - Backup mechanism: Replaced unsafe file copy with SQLite `VACUUM INTO` for atomic, consistent backups
  - Pre-backup integrity check: Refuses to backup if source database fails integrity check
  - Post-backup verification: Opens backup and runs integrity check before accepting it
  - Synchronous mode: Changed from `PRAGMA synchronous=NORMAL` to `FULL` for crash safety
  - Connection pool: Reduced from 10→4 connections to minimize WAL contention
- **Corruption status performance**: Added `corruption_summary` materialized table with trigger-based maintenance
  - Replaces slow VIEW with 8 correlated subqueries (O(n*m) → O(1) lookups)
  - Automatically maintained via SQLite trigger on event inserts
  - Backwards-compatible: existing `corruption_status` VIEW now wraps the table

### Fixed
- **Test deadlock in notifier**: Fixed race condition where `CreateConfig()` triggered a reload that raced with test cleanup
  - Added `sync.WaitGroup` to ensure clean shutdown sequence

## [1.1.17] - 2026-01-06

### Security
- **SQL injection prevention**: Added strict whitelist validation for `sort_by` parameter in all paginated API endpoints
  - Handlers now specify explicit `AllowedSortBy` maps to prevent arbitrary SQL in ORDER BY clauses
  - Affects: `/api/scans`, `/api/corruptions`, `/api/logs`

### Fixed
- **Database row iteration errors**: Added `rows.Err()` checks after all `for rows.Next()` loops
  - Previously, errors occurring during iteration could be silently lost
  - Now properly propagates errors in 15+ locations across handlers and services
- **Scan progress off-by-one**: Progress counter now increments AFTER file is processed, not before
  - Previously showed "100/100" while still processing the last file
  - Extracted `markFileProcessed()` helper for accurate progress tracking
- **Retry event validation**: Remediator now validates `file_path` before processing retry events
  - Empty or missing file paths now log a warning and emit `SearchFailed` event
- **Monitor error handling**: Distinguished between "corruption not found" vs database errors
  - `sql.ErrNoRows` now logs a warning instead of an error
  - Actual database errors are properly logged with full details
- **Critical database pragma failures**: SQLite pragma failures now properly propagate errors
  - Critical pragmas (WAL mode, foreign keys, busy timeout) must succeed or startup fails
  - Optional pragmas (synchronous, cache, temp_store) log warnings but continue

### Improved
- **Error logging**: Added debug logging for JSON unmarshal errors during event data enrichment
- **Silent skip logging**: Added warning logs when row scans fail in batch operations
- **Type assertion logging**: Unexpected types in episode ID parsing now logged at debug level
- **URL normalization**: Extracted `normalizeAPIURL()` helper function in notifier package
- **Error messages**: Signal provider error now shows generic port format instead of hardcoded 8080

## [1.1.16] - 2026-01-06

### Added
- **Real-time scan progress on ScanDetails page**: View live progress when watching a running scan
  - Progress bar showing percentage complete with file count
  - "Currently Scanning" indicator with live file path
  - Files table auto-refreshes every 3 seconds during scan
  - WebSocket-powered updates for instant feedback
- **Scan progress in status badge**: Running scans now show `(X/Y)` file count in the status badge

### Fixed
- **WebSocket event handling**: Fixed critical bug where real-time events weren't being processed
  - Backend sent `{"type": "event", "data": {event_type: "ScanProgress", ...}}`
  - Frontend expected `{"type": "ScanProgress", ...}` - events were silently dropped
  - Added transformation layer in WebSocketProvider to normalize message format
  - Dashboard "Active Scans" table now updates in real-time as expected
- **WebSocket event subscription**: Backend now broadcasts all corruption lifecycle events
  - Previously only 9 event types were broadcast; now includes all 30 relevant events
  - Added missing `FileDetected`, `NotificationSent`, `NotificationFailed` subscriptions
  - Fixes: file detection, verification, search completion, notifications not updating UI
- **Frontend query invalidation**: Fixed incorrect event names and added missing events
  - Removed non-existent `RemediationStarted`/`RemediationCompleted` event checks
  - Removed dead `HealthCheckPassed` code (event type doesn't exist in backend)
  - Added all corruption lifecycle events including `FileDetected` and notification events
  - Corruption list now auto-refreshes when any remediation state changes
- **Scan progress emission frequency**: Progress now emitted after every file (was every 10 files)
  - UI updates in real-time instead of 10-50 second intervals
  - Database saves remain every 10 files to minimize I/O overhead
- **Missing scan_db_id in WebSocket events**: Progress events now include database ID for navigation

### Changed
- Event type names now use PascalCase consistently (`ScanStarted`, `CorruptionDetected`)
- WebSocket events include `_raw` field for debugging original event data

## [1.1.15] - 2026-01-05

### Added
- **Tool detection at startup**: Healarr now checks for required detection tools on startup
  - Detects ffprobe, ffmpeg (required), mediainfo, and HandBrakeCLI (optional)
  - Shows version and path for available tools
  - Red warning banner displayed when required tools are missing
  - Tool status shown in System Information section
- **About section on Help page**: Version info, system information, and tool status now available on Help page
  - Collapsed accordion at bottom of Help page
  - Shows same information as Config → About section
  - Shared `AboutSection` component for consistent UI
- **Notification event labels and tooltips**: Events now display friendly names with hover descriptions
  - "ScanStarted" → "Scan Started" with tooltip "When a scan begins on a configured media path"
  - All 19 event types have human-readable labels and contextual descriptions
  - Backend returns `EventInfo` objects with `name`, `label`, and `description` fields
- **Verbose update instructions**: "How to Update" section now shows detailed, commented commands
  - Docker: Step-by-step with directory navigation, pull, restart, and log verification
  - Linux: Includes distro-specific ffprobe installation (Debian/Ubuntu, Fedora/RHEL, Arch)
  - Linux: Now shows both curl and wget options for downloading
  - macOS: Apple Silicon download example with Homebrew ffprobe installation
  - Windows: Includes ffprobe installation instructions

### Fixed
- **Matrix logo visibility**: Matrix notification provider icon now visible on light mode
  - Uses CSS `invert` filter on light backgrounds to flip white logo to black
  - Automatically reverts on dark mode

### Changed
- README now acknowledges [dashboard-icons](https://github.com/homarr-labs/dashboard-icons) for service icons
- AboutSection extracted to shared component (`components/AboutSection.tsx`)
- `/api/system-info` now returns `tools` object with detection tool availability

## [1.1.14] - 2026-01-05

### Fixed
- **Health monitor instance checks**: Now uses proper `/api/v3/system/status` endpoint
  - Previously incorrectly used path-based queue lookup which failed with "no instance found for path: /sonarr"
  - Added `CheckInstanceHealth(instanceID)` method to ArrClient interface for proper health verification
- **Notification provider icons**: Now display proper SVG icons instead of emojis
  - Custom dropdown component (`ProviderSelect`) with full icon support
  - Provider icons shown in notification list cards and settings header
  - 18 provider icons included: Discord, Slack, Telegram, Pushover, Gotify, ntfy, and more
- **Config export/import**: Now properly handles schedules and notifications
  - Import function maps schedules by `local_path` to correct `scan_path_id`
  - Notifications import properly creates configurations with all fields
- **Status badge text wrapping**: Badges like "No Replacement Found" no longer break onto two lines
  - Added `whitespace-nowrap` CSS class to all status badge spans
- **Icon static file serving**: Provider icons now load correctly in browser
  - Added `/icons` route to serve embedded icon assets (was falling back to SPA routing)

### Added
- **StuckRemediation state**: New UI state for items stuck in remediation for 24+ hours
  - Orange color scheme to indicate attention needed
  - Description: "Item stuck for 24+ hours - check *arr queue or retry"
- **Provider icon component**: Reusable `ProviderIcon` component for consistent icon rendering
  - SVG icons for 18 notification providers
  - Emoji fallback for providers without icons (Email, Join, Custom)

### Changed
- Health monitor now calls `CheckInstanceHealth()` instead of queue-based checks
- Provider selection dropdown uses custom component instead of native `<select>`
- Cleaned up linting issues in test files (nil checks, `strings.ReplaceAll`)

## [1.1.13] - 2026-01-05

### Fixed
- **Database connection starvation under load**: Background services could block API responses
  - Changed SQLite connection pool from `MaxOpenConns(1)` to `MaxOpenConns(10)` for WAL mode concurrency
  - SQLite WAL mode safely supports multiple concurrent readers with one writer
- **Context deadlines in background services**: All database queries now have proper timeouts
  - Added 10-30 second context timeouts to scanner, verifier, notifier, and recovery services
  - Prevents individual slow queries from blocking the entire application
  - Uses exponential backoff retry for SQLITE_BUSY errors
- **Removed dead code**: Cleaned up unused `filepath.Clean()` call in health checker

### Added
- **Database performance index**: New migration adds optimized index for file_path lookups
  - Expression-based index on `json_extract(event_data, '$.file_path')` for efficient lookups
  - Compound index on `(event_type, file_path)` for common query patterns
  - Significantly improves corruption lookup performance on large databases

## [1.1.12] - 2026-01-04

### Fixed
- **API endpoints hanging**: Multiple API endpoints could hang indefinitely if database was locked by background services
  - Added 5-second timeout context to all database operations in health and corruptions handlers
  - `/api/health` now guaranteed to return within timeout for Docker healthchecks
  - `/api/corruptions` and related endpoints now return gracefully under database contention
- **Test infrastructure**: Fixed test database connection starvation causing test timeouts
  - Enabled WAL mode for concurrent reads in tests
  - Increased max connections to prevent EventBus/handler contention

## [1.1.11] - 2026-01-04

### Added
- **Enriched Media Information**: Corruptions list now shows friendly media titles
  - Movie/TV show titles instead of raw file paths (e.g., "Colony S01E08" instead of "/tv/Colony/S01E08.mkv")
  - *arr instance icons (Sonarr, Radarr, Whisparr) for quick identification
  - File size display in human-readable format
  - Download progress indicator for items currently downloading
- **Enhanced Remediation Journey**: Rich download and quality information
  - Full file path displayed in header with copy-to-clipboard button
  - SearchCompleted events show download client icon, protocol (Usenet/Torrent), and indexer
  - DownloadProgress events display visual progress bar with size and ETA
  - VerificationSuccess shows quality badge (4K/1080p/720p), release group, and duration metrics
- **Quality Tier Badges**: Color-coded quality indicators
  - UHD/4K (purple), 1080p (blue), 720p (green), SD (gray)
  - Release group tags for easy identification
- **Duration Metrics**: Track how long remediations take
  - Download duration (time from search to import)
  - Total duration (time from detection to resolution)

### Changed
- API now returns enriched corruption data from all event types (CorruptionDetected, SearchCompleted, DownloadProgress, VerificationSuccess)
- Download client icons added: SABnzbd, NZBget, qBittorrent, Deluge, Transmission, ruTorrent, Flood, aria2, Download Station
- Protocol icons added for Usenet and Torrent downloads

### Fixed
- Docker image version now shows proper semver (e.g., "v1.1.10-5-g1a2b3c4") instead of branch name when built from main

## [1.1.10] - 2026-01-04

### Added
- **Startup Recovery Service**: Automatically recovers stale in-progress items on startup
  - Reconciles Healarr state with actual *arr queue/history
  - Marks items as resolved if arr reports file exists and is healthy
  - Marks items as "No Replacement Found" if vanished from arr without import
- **Periodic Arr State Sync**: HealthMonitorService syncs with arr state every 30 minutes
  - Catches missed webhooks and state drift
  - Automatically resolves items that completed while Healarr wasn't watching
- **SearchExhausted Event Type**: New non-terminal state for "No Replacement Found"
  - Distinct from MaxRetriesReached (which is for verification failures)
  - Allows unlimited manual retries via the Retry button
  - Notifications supported for this event type
- **Configurable Stale Threshold**: `HEALARR_STALE_THRESHOLD` / `--stale-threshold`
  - Auto-fixes items Healarr lost track of (after restarts, missed webhooks, etc.)
  - Items inactive longer than this get checked against *arr to see what really happened
  - Default: 24h - increase for slow download clients (e.g., `48h` for long seeding)

### Changed
- HealthMonitorService now properly started (was defined but never wired up)

### Removed
- Wiki and Discussions links from About page (not yet available)

## [1.1.6] - 2026-01-02

### Fixed
- **Scheduler Startup Hang** (#8): Fixed potential hang during scheduler initialization
  - Added context with timeout (10s) to all scheduler database queries
  - Added orphaned schedule cleanup at startup (removes schedules for deleted scan paths)
  - Added pre-validation of cron expressions before attempting to register jobs
  - Added detailed debug logging throughout scheduler initialization
  - Improved error handling with wrapped errors for better diagnostics

### Changed
- Scheduler interface now includes `CleanupOrphanedSchedules()` method
- Database queries in scheduler now use `QueryContext` for cancellation support

## [1.1.5] - 2026-01-02

### Fixed
- **Connection Lost on Root Deployments**: Fixed API calls going to `https://api/...` instead of relative URLs
  - When deployed at root (no subpath), `getApiBasePath()` returned `/` which created `//api/health` URLs
  - Browser interpreted `//api/health` as protocol-relative URL, treating "api" as hostname
  - Fix: Return empty string for root deployments to prevent double-slash URLs

## [1.1.4] - 2026-01-02

### Fixed
- **Reverse Proxy Login Redirect** (#6): Fixed login redirect ignoring `HEALARR_BASE_PATH`
  - When accessing via reverse proxy at `/healarr/`, redirected to `/login` instead of `/healarr/login`
  - Server now injects base path into HTML as `window.__HEALARR_BASE_PATH__`
  - Frontend reads injected value before falling back to URL detection

## [1.1.3] - 2026-01-02

### Fixed
- **Docker Permissions** (#5): Fixed PUID/PGID environment variables being ignored on Unraid
  - Added docker-entrypoint.sh to handle runtime UID/GID modification
  - Uses su-exec to drop privileges after setup
- **Add Server Button** (#1): Fixed silent validation failure when adding *arr servers
  - Added toast notifications for validation errors and success/failure states
  - Added required field indicators with HTML5 validation
- **ImportBlocked Event Spam**: Fixed 289 duplicate events per blocked import
  - Added state deduplication - only emits on actual state change
- **NotificationSent Aggregate ID**: Fixed using file path instead of corruption UUID

### Added
- **Manual Intervention Alert**: Prominent banner on Corruptions page when items need attention
- **ManualInterventionRequired**: New notification event type for blocked imports

## [1.1.2] - 2026-01-01

### Fixed
- **BASE_PATH Asset Loading**: Fixed static assets not loading when using `HEALARR_BASE_PATH`

### Changed
- Improved reverse proxy documentation in Help page

## [1.1.1] - 2026-01-01

### Changed
- **Test Coverage**: Improved test coverage across all packages to 80%+
- Internal code organization and cleanup

## [1.1.0] - 2025-12-31

### Added
- **Circuit Breaker Pattern**: Added resilience pattern for external service calls
  - Protects against cascading failures when *arr instances are unavailable
  - Configurable failure threshold, reset timeout, and success threshold
  - Automatic state transitions: Closed → Open → HalfOpen → Closed
- **Clock Abstraction**: New internal clock package for testable time operations
  - `Clock` interface with `Now()` and `AfterFunc()` methods
  - Enables deterministic testing of time-dependent code
- **Comprehensive Test Coverage**: Added 17 new test files with 9,664 lines of test code
  - `internal/api/handlers_*_test.go` - Full API handler coverage
  - `internal/services/*_test.go` - Service layer tests
  - `internal/integration/errno_*_test.go` - Platform-specific error handling tests
  - `internal/clock/clock_test.go` - Clock abstraction tests
  - `internal/logger/logger_test.go` - Logger package tests
  - `internal/metrics/metrics_test.go` - Metrics service tests
- **Pagination Support**: Standardized pagination across all list endpoints
  - Consistent `page`, `pageSize`, `total`, `totalPages` response format
  - Default page size of 50 with configurable limits

### Changed
- **Go Version**: Updated minimum Go version from 1.24 to 1.25
- **Error Classification**: Platform-specific syscall error handling
  - Unix: ESTALE, ETIMEDOUT, ENODEV, ENXIO, EIO, EHOSTDOWN, etc.
  - Windows: ERROR_BAD_NETPATH, ERROR_SEM_TIMEOUT, ERROR_DEV_NOT_EXIST, etc.
- **Code Quality**: Improved error handling throughout the codebase
  - All deferred Close() calls now properly check for errors
  - Consistent error logging patterns across all packages

### Fixed
- **gofmt Compliance**: Fixed formatting issues in 3 files
- **Test Reliability**: Fixed flaky tests with proper synchronization

### Security
- No security changes in this release

## [1.0.3] - 2025-12-02

### Added
- **Manual Intervention Detection**: New states to track when *arr requires user action
  - `ImportBlocked` event when Sonarr/Radarr blocks import (e.g., quality cutoff, existing file issues)
  - `ManuallyRemoved` event when user removes item from *arr queue without importing
- **Dashboard Manual Action Card**: New stat card showing corruptions requiring manual intervention
  - Orange "Manual Action" card with HandMetal icon in Corruption Status breakdown
  - Clickable to filter corruptions list to manual intervention items
- **Clickable Active Scans**: Dashboard active scan table rows now navigate to scan details page
  - Click any running scan row to view `/scans/{id}` details
  - Cancel button properly isolated with stopPropagation
- **Clickable Scan Details Stats**: Stat cards on scan details page now filter the file list
  - Click "Files Scanned", "Healthy Files", or "Corruptions Found" to filter
  - Active filter highlighted with ring indicator
- **Scan Duration Display**: New duration/elapsed stat card on scan details page
  - Shows elapsed time for running scans
  - Shows total duration for completed scans
  - Human-readable format (e.g., "2h 15m", "45m 30s")
- **Notification Support**: New "Manual Intervention Required" event group for notifications
  - Subscribe to ImportBlocked and ManuallyRemoved events
  - Rich notification messages with context about the issue

### Changed
- Dashboard stats API now returns `manual_intervention_corruptions` count
- Corruptions filter now supports `status=manual_intervention` parameter
- Scanner service exposes `scan_db_id` in progress updates for navigation

## [1.0.2] - 2025-12-01

### Fixed
- Fixed overly aggressive path validation rejecting curly braces `{}` in file paths
  - Radarr/Sonarr naming conventions like `{imdb-tt0848228}` are now properly supported
  - Path validation now only rejects truly dangerous characters (null bytes, newlines)
  - Shell metacharacters are safe with `exec.Command` (no shell interpretation)

## [1.0.1] - 2025-12-01

### Security
- Fixed G204 command injection vulnerability in health_checker.go
  - Replaced shell execution with direct exec.Command calls
  - Sanitized file paths to prevent injection attacks

### Fixed
- Fixed ~90 instances of unhandled errors (G104) throughout the codebase
- Improved error logging consistency (Errorf for failures, Debugf for non-critical)

### Changed
- Refactored `buildShoutrrrURL` function (complexity reduced from 84 to 2)
  - Extracted URL building logic into Strategy pattern (url_builders.go)
  - Each notification provider now has a dedicated URLBuilder implementation
- Refactored `handleCorruptionDetected` function (complexity reduced from 24 to <10)
  - Extracted corruption type handlers into separate functions

### Added
- Comprehensive test suite for ScannerService (coverage: 19.8% → 50.1%)
- Performance benchmarks for critical code paths
  - Scanner operations: IsMediaFile, IsHiddenOrTempFile, scan lifecycle
  - URL builder benchmarks for all notification providers
- New test files: url_builders_test.go, health_checker_test.go, arr_client_test.go

## [1.0.0] - 2025-11-28

### Added
- Initial public release
- Multi-method corruption detection (ffprobe, MediaInfo, HandBrake)
- Support for Sonarr, Radarr, Whisparr v2, and Whisparr v3
- Automatic remediation with file deletion and *arr search triggers
- Queue-based verification using *arr APIs
- Real-time WebSocket updates for scan progress
- Dashboard with statistics, charts, and corruption breakdown
- Per-path configuration (auto-remediate, dry-run, max retries)
- Detection modes: Quick (header check) and Thorough (full decode)
- Scheduled scans with cron expressions
- Webhook integration for instant scanning on *arr imports
- Notifications via Discord, Slack, Telegram, Pushover, Gotify, ntfy, Email
- Config import/export and database backup
- Dark/light theme support
- Password-protected UI with API key authentication
- Docker support with multi-arch images (amd64, arm64)

### Security
- All UI pages require authentication
- API endpoints protected with token-based auth
- Passwords hashed with bcrypt
- API keys encrypted in database
