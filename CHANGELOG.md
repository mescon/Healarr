# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
