# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.3.1] - 2026-05-27

### Added
- **Parallel scanning**: file detection now runs across a worker pool, so large libraries scan substantially faster. The default worker count is tuned to the memory available to the container so a constrained host won't be pushed into an out-of-memory kill; override with `HEALARR_SCANNER_WORKERS` (1 to 32).
- Configurable shutdown grace period for in-flight scans via `HEALARR_SCANNER_SHUTDOWN_TIMEOUT` (default 30s).
- Defense-in-depth HTTP security headers on all responses (`X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`).

### Changed
- Faster database writes during scans: SQLite now uses `synchronous=NORMAL` under WAL (still corruption-safe), and per-file scan-progress updates no longer hit the durable event store.

### Fixed
- **Files on a temporarily-unavailable mount or network share are no longer mistaken for corruption**, preventing deletion of healthy files when a mount drops mid-scan. Mount/network errors are now classified by OS error code, so this also holds on non-English systems.
- **Deletion now refuses to act on an ambiguous match.** If the corrupt file can't be identified unambiguously in the *arr media (for example two files share a name), or the owning *arr instance is misconfigured, the remediation fails and is retried instead of risking deletion of a healthy file. An exact path match is preferred, with a unique-basename match as the only fallback.
- **Per-path `auto_remediate` and dry-run settings are now honored on retries and on startup recovery.** Previously a corruption re-triggered after a restart could delete a file on a path configured as manual or dry-run; the remediator now re-reads the path's current settings before acting.
- Live log and scan-progress updates no longer drop messages under load (WebSocket delivery rework).
- A paused scan could fail to resume if the resume signal raced with the scan loop; resume is now reliable.

### Security
- The authentication token is no longer written to the browser console on WebSocket connect.

## [1.3.0] - 2026-02-19

### Added
- **Content Analysis Detection**: Detect corruptions that pass basic integrity checks
  - Black video detection: finds files where video is entirely black frames
  - Frozen video detection: finds files where video is a single still frame
  - Silent audio detection: finds files with no audible audio track
  - Configurable per scan path alongside existing detection modes

### Security
- **API Hardening**: Strengthened endpoint protection across the board
  - Rate limiting now applied to all protected API routes
  - Metrics endpoint (`/metrics`) now requires authentication
  - API error responses no longer expose internal Go type information (29 handlers fixed)
  - CORS wildcard mode now sets `Vary: Origin` to prevent cache poisoning
  - Setup dismiss endpoint now rate-limited like other setup endpoints

### Improved
- **Accessibility**: Active scans table rows now fully keyboard-navigable
- **Code Quality**: Drove SonarCloud to zero issues across all categories
  - 0 bugs, 0 vulnerabilities, 0 security hotspots, 0 code smells
  - Reduced cognitive complexity in several critical functions
  - 84.7% test coverage maintained throughout

### Dependencies
- Bumped modernc.org/sqlite, quic-go, prometheus, golang.org/x/net, protobuf
- Bumped lucide-react, framer-motion, tailwindcss, recharts, axios, msw, typescript-eslint

## [1.2.1] - 2026-02-12

### Improved
- **Code Quality Enforcement**: Lint and type checking are now required to pass in CI
  - Previously these checks ran but failures were silently ignored
  - Pull requests with lint or type errors will now be blocked until fixed
- **Cleaner React Patterns**: Replaced several state-syncing effects with derived state
  - "How to Update" section now expands automatically when an update is available, without extra re-renders
  - Configuration warning banner loads faster by reading session state upfront
  - Base path and About section settings initialize without flicker

### Dependencies
- Bumped axios, typescript-eslint, eslint-plugin-react-refresh, react-router-dom

## [1.2.0] - 2026-02-07

### Added
- **Form Validation**: Configuration forms now check your input before saving
  - Clear error messages shown next to each field
  - Validates *arr instances, scan paths, and notification settings
- **Crash Recovery**: If something goes wrong in the UI, you get a friendly error screen with a retry button instead of a blank page
- **Monitoring Metrics**: Prometheus-compatible metrics endpoint for Grafana or similar monitoring stacks
  - HTTP request duration and count
  - Database query performance
  - WebSocket connection tracking
- **Keyboard Navigation**: Notification provider dropdown and dashboard cards fully support keyboard-only navigation
- **Screen Reader Support**: Improved accessibility throughout the UI
  - Skip navigation link for keyboard users
  - Dashboard cards use proper button semantics
  - Dropdown menus use ARIA listbox pattern

### Fixed
- **Gotify/ntfy Notifications Broken**: Fixed "Please fill in all required fields" error when adding Gotify or ntfy notification providers, even though all fields were filled correctly (#105)
- **Container Restart Hangs**: Healarr could hang when restarting the Docker container if a verification was in progress
  - Verifier service now included in graceful shutdown sequence
- **Background Tasks Leaking**: Database maintenance tasks now stop cleanly on shutdown instead of continuing to run in the background
- **Error Messages Exposing Internals**: API error responses no longer show raw Go type information
  - All handler error messages sanitized

### Improved
- **Docker Security**: Container now runs with `no-new-privileges` and drops all Linux capabilities by default
- **Server Timeouts**: HTTP server now enforces read (15s), write (30s), and idle (120s) timeouts to prevent connections from hanging indefinitely
- **Setup Wizard**: Split into smaller, faster-loading step components for a smoother first-run experience
- **Rate Limiting**: Config import and database restore endpoints during setup are now rate-limited to prevent abuse
- **System Info**: The system info endpoint now requires authentication

### Dependencies
- Upgraded Zod from v3 to v4 for faster form validation
- Upgraded Vitest from v3 to v4
- Bumped framer-motion, lucide-react, recharts, jsdom, @types/node
- Bumped Go dependencies (SQLite driver, crypto, validator)

## [1.1.33] - 2026-01-13

### Added
- **New Notification Events**: Three additional events for better monitoring
  - "Stuck Remediation" - When a remediation hasn't progressed for too long
  - "Arr Instance Healthy" - When a previously unreachable *arr instance recovers
  - "Corruption Ignored" - When you manually dismiss a detected corruption
- **Docstring Coverage Enforcement**: CI now validates code documentation
  - Ensures all exported functions and types are documented
  - Currently at 100% coverage
- **Stuck Remediation Recovery**: Automatic retry when remediation is stuck
  - HealthMonitorService detects stuck items and triggers immediate retry
  - Prevents items from sitting idle indefinitely

### Fixed
- **Stability Improvements**: Fixed several race conditions that could cause hangs
  - Scan progress updates no longer conflict with shutdown operations
  - Health check timeouts handled more gracefully
  - File verification counters now thread-safe
  - Verification goroutines now properly cancelled on retry (prevents duplicates)
- **Memory Leak**: Fixed gradual memory growth from retry timers
  - Retry timers now properly cleaned up after firing
  - Long-running instances stay lean
- **Duplicate Scanning Prevention**: Files scanned via webhook no longer re-scanned during bulk scans
  - Prevents wasted processing when webhook and scheduled scan overlap
- **Near-Complete Download Detection**: Improved handling of downloads at 99%+ progress
  - Verifier retries history API multiple times before marking as ManuallyRemoved
  - Handles timing delays where import appears in history after queue clears

### Improved
- **Graceful Shutdown**: New scans blocked during shutdown to prevent hangs
  - In-progress scans complete cleanly before exit
  - No more stuck shutdown states
- **Test Coverage**: Comprehensive tests for all concurrent code paths
  - 85%+ coverage on services package
  - All race conditions have corresponding test cases

## [1.1.32] - 2026-01-11

### Added
- **Retry All Button**: Manual intervention banner now has a "Retry All" button
  - Previously mentioned clicking "Retry here" but no button existed
  - Bulk retry functionality for all items needing manual intervention
- **Episode Titles**: TV shows now display episode titles in the format "Series S01E08 - Episode Title"
  - Richer context for identifying specific episodes
- **Event Replay Service**: Unprocessed events are now replayed on startup
  - Fixes race condition where events published just before restart weren't processed
  - Ensures CorruptionDetected events are delivered to remediator after restart

### Fixed
- **False ManuallyRemoved State**: Items no longer incorrectly marked as removed when import succeeds
  - Checks for import events in history before marking as ManuallyRemoved
  - Handles NFS sync delays and path mapping timing issues
- **Recovery Service State Coverage**: All intermediate states now recovered on startup
  - Previously missed `RemediationQueued`, `DeletionStarted`, `DeletionCompleted` states
  - Items stuck in early remediation states are now properly reprocessed
- **Lost Retry Timers**: Retry schedules are now preserved across restarts
  - Failed states (`SearchFailed`, `RemediationFailed`, etc.) trigger retry on startup
  - Items at max retries correctly transition to `MaxRetriesReached`

### Improved
- **EventBus Buffer Monitoring**: Warning logged when subscriber buffer is full
  - Events are still persisted to DB (not lost), warning aids debugging
- **Semaphore Timeout**: Remediator semaphore now has 2-minute timeout
  - Prevents indefinite hangs if HTTP calls get stuck
  - Emits failure event on timeout for proper retry flow
- **Verifier Concurrency Limit**: Maximum 50 concurrent verification goroutines
  - Prevents resource exhaustion during bulk scans
  - 5-minute timeout with appropriate failure events

## [1.1.31] - 2026-01-10

### Fixed
- **Mobile Table View**: Fixed tables showing mobile card view instead of desktop table
  - Tailwind JIT now correctly compiles responsive breakpoint classes
  - Scans and Scan Details pages display proper tables on desktop
- **Filter Dropdown**: Fixed filter disappearing when no results match
  - Filter controls now stay visible regardless of filtered result count
  - Users can always change filters even with zero matching items
- **Database Query Stability**: Fixed flaky "context canceled" errors during queries
  - QueryWithRetry no longer cancels context prematurely
  - Prevents intermittent failures when iterating database results

## [1.1.30] - 2026-01-10

### Improved
- **Code Organization**: Refactored Config page into modular section components
  - ArrServersSection, ScanPathsSection, SchedulesSection, NotificationsSection
  - Each section is self-contained with its own React Query hooks
  - Easier to maintain and extend individual configuration areas
- **Confirmation Dialogs**: Replaced browser alerts with animated modal dialogs
  - Supports danger, warning, and info variants
  - Focus management and keyboard navigation (Escape to close)
  - Loading state support during async operations
- **Skeleton Loaders**: Added loading placeholders for better perceived performance
  - DataGrid shows skeleton rows while loading
  - Smoother transitions when data is fetching
- **Accessibility**: Improved keyboard navigation and screen reader support
  - Dialogs trap focus and support Escape key
  - Better ARIA labels throughout the UI

## [1.1.29] - 2026-01-10

### Fixed
- **WebSocket Stability**: Fixed disconnect/reconnect on every menu navigation
  - Connection now stays stable during route changes
  - Only reconnects when actually disconnected or token changes

### Improved
- **Tux Icon**: Converted to inline SVG matching Docker, Apple, and Windows icons
  - Uses `fill="currentColor"` for proper CSS color inheritance
  - Smaller bundle size (no external file needed)
  - Consistent styling across all platform icons

## [1.1.28] - 2026-01-10

### Fixed
- **Config Import in Wizard**: Fixed redirect to /login when importing JSON config
  - Uses authenticated endpoint when user has a token
  - Public endpoint still works during initial setup
- **Duplicate Prevention**: Importing config no longer creates duplicate entries
  - Skips arr instances with matching URL
  - Skips scan paths with matching local_path
  - Skips schedules with matching path + cron expression
  - Skips notifications with matching name
- **Logo Display**: Removed green gradient background from logo containers
  - SVG logo now displays without decorative background
  - Cleaner appearance in sidebar, login, and wizard
- **README.md Logo**: Increased logo size to 96px with vertically centered text
- **Setup Wizard Reset**: Fixed wizard not appearing after using "Reset Setup Wizard"
  - Wizard now correctly shows after reset, skipping password step if already set
  - Allows users to reconfigure arr instances, paths, and notifications

## [1.1.27] - 2026-01-10

### Added
- **Full Notification Support in Setup Wizard**: All 21 notification providers now available
  - Same feature parity as the Config page
  - Provider selection with icons and categories
  - Event selection for which notifications to receive
  - Test notification button with result feedback
- **Restore Pre-population**: Wizard fields now pre-fill after config/database restore
  - Shows "Restored: X instances, Y paths, Z notifications" banner
  - Values can be reviewed and modified before saving
- **Shared Notification Components**: Reusable components for Config and Wizard
  - ProviderSelect, ProviderFields, EventSelector, ProviderIcon
  - Consistent UI across both pages

### Improved
- **Updated Logo**: New healarr.svg logo in sidebar, wizard, and login pages
- **README.md**: Logo now displayed beside "Healarr" title with matching height

### Fixed
- **Setup Wizard Navigation**: Going back after setting password no longer causes error
  - Previously showed "Setup already completed" when clicking Continue
  - Now correctly skips to next step if password already set
- **Subdirectory Deployment**: Fixed all absolute asset paths for reverse proxy support
  - All icons now use `import.meta.env.BASE_URL` prefix
  - Works correctly with HEALARR_BASE_PATH (e.g., /healarr/)
  - Fixes broken notification icons when deployed behind a reverse proxy subdirectory

## [1.1.26] - 2026-01-09

### Added
- **Setup Wizard Reset**: Re-run the setup wizard anytime from the Config page
  - Useful if you skipped steps during initial setup
  - Access via Config → General Settings → Reset Setup Wizard
- **Smart Instance Naming**: Arr instances now get friendly auto-generated names
  - First Sonarr instance named "Sonarr", second becomes "Sonarr 2", etc.
  - Works for Sonarr, Radarr, and Whisparr

### Improved
- **URL Validation**: Clearer error messages when adding arr instances
  - Explicitly tells you if URL is missing http:// or https://
  - Better feedback for malformed URLs
- **Test Coverage**: Comprehensive testing across all packages
  - Added tests for pagination, path validation, and instance naming
  - Database package now at 80%+ coverage
  - Overall test coverage improved to ~84%

### Fixed
- **Setup Wizard File Upload**: Fixed file selection not working in restore section
  - Clicking "Click to select .db file" or ".json file" now properly opens file picker
  - Added hover effects for better visual feedback

## [1.1.25] - 2026-01-09

### Added
- **Mobile-Friendly Tables**: All data tables now adapt to mobile screens
  - Tables collapse into expandable cards on phones and tablets
  - Tap to expand and see all details
  - Works on Dashboard, Corruptions, Scan Details, and Configuration pages
- **Skipped & Inaccessible Files**: Scan details now show why files weren't checked
  - New stat cards for Skipped and Inaccessible file counts
  - Filter by status to see exactly which files had issues
  - Helps identify permission problems or unsupported file types

### Improved
- **Setup Experience**: Better feedback during configuration
  - Progress indicators when testing connections
  - Clearer error messages when things go wrong
  - Path validation shows file counts before saving
- **Error Handling**: More helpful messages throughout the app
  - Network errors show retry options
  - Server errors explain what went wrong
  - Validation errors highlight exactly what to fix
- **Performance**: Faster loading on large libraries
  - Scan details load with fewer database queries
  - Path validation limits file scanning to prevent timeouts

### Fixed
- **Logs Page Scroll**: Fixed auto-scroll jumping to bottom when viewing older logs
  - Auto-scroll now pauses when you scroll up
  - Resumes automatically when you scroll back to the bottom

## [1.1.24] - 2026-01-09

### Improved
- **CI/CD Quality**: Fixed code quality pipeline configuration
  - SonarCloud now correctly excludes test utilities from coverage calculations
  - Quality gate checks pass consistently on all pull requests
- **Test Coverage**: Additional edge case testing
  - Media details lookup now thoroughly tested (movie and series)
  - File accessibility checks have improved coverage

## [1.1.23] - 2026-01-09

### Improved
- **Reliability**: Major internal code refactoring for improved stability
  - Simplified complex code paths in authentication, notifications, database maintenance, and file scanning
  - Easier to debug and fix issues faster in future releases
- **Test Coverage**: Expanded automated testing to 85% coverage
  - More bugs caught before they reach users
  - Better confidence in updates and new features

## [1.1.22] - 2026-01-08

### Improved
- **Reliability**: Internal code improvements for better stability
  - Cleaner error handling throughout the application
  - Better error logging helps diagnose issues faster
- **Test Coverage**: Added tests for the recovery system
  - Stale item detection now thoroughly tested
  - Recovery workflows validated automatically

## [1.1.21] - 2026-01-08

### Security
- **Server Protection**: Strengthened security against common web attacks
  - *arr instance URLs now validated to prevent malicious redirects
  - Directory browser locked down to prevent unauthorized file access
  - Database backup cleanup hardened against path manipulation
  - Frontend protected against open redirect attacks

## [1.1.20] - 2026-01-06

### Added
- **First-time Setup Wizard**: Guided onboarding for new users
  - Step-by-step wizard walks you through initial configuration
  - Choose between fresh start, import existing config, or restore backup
  - Test *arr connections with real-time feedback
  - Auto-detect library paths from your *arr instances
  - Skip option available for power users
- **Database Restore**: Restore from backup directly in the UI
  - Upload a previous backup to restore your configuration
  - Automatic safety backup created before restore
  - Validates backup integrity before applying

### Fixed
- Fixed crash when importing configuration without path mappings

## [1.1.19] - 2026-01-06

### Fixed
- **Upgrade Fix**: Fixed upgrade failure for some users coming from older versions
  - Database migration no longer fails on corrupted or incomplete data
  - Gracefully handles edge cases from previous versions

## [1.1.18] - 2026-01-06

### Added
- **Custom Tool Paths**: Use your own versions of detection tools
  - Set custom paths via environment variables for ffprobe, ffmpeg, mediainfo, HandBrake
  - Or simply place binaries in `/config/tools/` folder
  - Useful for users needing newer codec support

### Changed
- **Updated Tools**: Alpine Linux 3.23 with newer media tools
  - ffmpeg 8.0.1 (was 6.1.1)
  - HandBrake 1.10.2 (was 1.6.1)
  - MediaInfo 25.09 (was 24.04)
- **Database Reliability**: Major improvements to prevent data loss
  - Safer backup mechanism with integrity verification
  - Better crash protection for unexpected shutdowns
  - Improved performance for corruption status lookups

## [1.1.17] - 2026-01-06

### Security
- **Input Validation**: Protected sorting options against manipulation
  - Only allowed sort fields accepted by the API

### Fixed
- **Scan Progress**: Progress now shows correctly (was off by one file)
  - "100/100" now means actually finished, not still processing
- **Database Reliability**: Fixed several edge cases where errors could be silently lost
- **Retry Handling**: Better validation before retrying failed items
- **Error Messages**: Clearer error messages throughout the application

## [1.1.16] - 2026-01-06

### Added
- **Live Scan Progress**: Watch scans happen in real-time
  - Progress bar with file count on scan details page
  - See which file is currently being scanned
  - Files table updates automatically during scan
- **Running Scan Count**: Active scans now show progress in status badge

### Fixed
- **Real-time Updates**: Fixed WebSocket events not updating the UI
  - Dashboard now updates instantly when scans progress
  - Corruption list refreshes automatically on state changes
  - All notification events now properly reflected in UI

## [1.1.15] - 2026-01-05

### Added
- **Tool Detection**: Healarr now checks for required tools on startup
  - Shows which tools are installed and their versions
  - Warning banner when required tools are missing
  - Tool status visible in System Information
- **About Section**: Version and system info now on Help page too
- **Friendly Event Names**: Notification events now show readable names
  - "ScanStarted" displays as "Scan Started" with helpful description
- **Better Update Instructions**: Detailed, step-by-step update commands
  - Docker, Linux, macOS, and Windows instructions
  - Includes tool installation for each platform

### Fixed
- **Matrix Icon**: Matrix notification icon now visible in light mode

## [1.1.14] - 2026-01-05

### Fixed
- **Health Monitor**: Instance health checks now work correctly
  - Previously failed with confusing "no instance found" error
- **Provider Icons**: Notification providers now show proper icons
  - 18 provider icons: Discord, Slack, Telegram, Pushover, and more
- **Config Import/Export**: Schedules and notifications now transfer correctly
- **Status Badges**: Long status text no longer wraps awkwardly

### Added
- **Stuck Remediation State**: New orange status for items stuck over 24 hours
  - Helps identify items that need manual attention

## [1.1.13] - 2026-01-05

### Fixed
- **Performance**: Fixed slowdowns when background services were busy
  - API now responds quickly even during heavy scanning
  - Database queries have proper timeouts to prevent hangs
- **Database Speed**: Added optimizations for faster file lookups
  - Significant improvement on large libraries

## [1.1.12] - 2026-01-04

### Fixed
- **API Responsiveness**: Fixed endpoints hanging when database was busy
  - Health check now always responds (important for Docker)
  - Corruptions page loads reliably under heavy load

## [1.1.11] - 2026-01-04

### Added
- **Rich Media Information**: Corruptions now show friendly titles
  - See "Colony S01E08" instead of raw file paths
  - *arr instance icons for quick identification
  - File size and download progress displayed
- **Enhanced Remediation Details**: See the full download journey
  - Download client, protocol (Usenet/Torrent), and indexer shown
  - Visual progress bar during downloads
  - Quality badges (4K, 1080p, 720p) on completion
  - Release group tags for easy identification
- **Duration Tracking**: See how long remediations take
  - Download time and total resolution time displayed

### Fixed
- **Version Display**: Docker builds now show proper version numbers

## [1.1.10] - 2026-01-04

### Added
- **Auto-Recovery on Startup**: Healarr now recovers gracefully after restarts
  - Checks *arr for current status of in-progress items
  - Automatically resolves items that completed while Healarr was down
  - Marks abandoned items appropriately
- **Periodic Sync**: Checks *arr status every 30 minutes
  - Catches missed webhooks and state drift
  - Self-heals when things get out of sync
- **"No Replacement Found" Status**: New distinct state when search exhausted
  - Clearly different from verification failures
  - Allows unlimited manual retries
- **Configurable Stale Threshold**: Control when items are considered stuck
  - Default 24 hours, adjustable for slow download clients

## [1.1.6] - 2026-01-02

### Fixed
- **Startup Reliability**: Fixed potential hang during scheduler startup (#8)
  - Added timeouts to prevent indefinite waiting
  - Cleans up orphaned schedules from deleted scan paths
  - Better error messages for troubleshooting

## [1.1.5] - 2026-01-02

### Fixed
- **Connection Lost**: Fixed "Connection Lost" error on root deployments
  - API calls no longer break when not using a subpath

## [1.1.4] - 2026-01-02

### Fixed
- **Reverse Proxy Login**: Fixed login redirect ignoring base path (#6)
  - Now correctly redirects to `/healarr/login` when using subpath

## [1.1.3] - 2026-01-02

### Fixed
- **Docker Permissions**: Fixed PUID/PGID being ignored on Unraid (#5)
  - Container now properly runs as specified user
- **Add Server Button**: Fixed silent failure when adding *arr servers (#1)
  - Now shows clear error messages and success confirmations
- **Duplicate Events**: Fixed excessive event spam for blocked imports
- **Notification IDs**: Fixed incorrect IDs in notification events

### Added
- **Manual Intervention Alert**: Banner when items need your attention

## [1.1.2] - 2026-01-01

### Fixed
- **Base Path Assets**: Fixed static files not loading with custom base path

## [1.1.1] - 2026-01-01

### Improved
- Internal code organization and test coverage improvements

## [1.1.0] - 2025-12-31

### Added
- **Resilient Connections**: Automatic recovery when *arr instances go offline
  - No more cascading failures from temporary outages
  - Automatic reconnection when services come back
- **Comprehensive Testing**: Major test coverage improvements
  - All API handlers thoroughly tested
  - Service layer fully covered
  - Platform-specific error handling validated
- **Better Pagination**: Consistent paging across all list views
  - Page size configurable, default 50 items

### Changed
- Minimum Go version updated to 1.25
- Improved error handling throughout the application

## [1.0.3] - 2025-12-02

### Added
- **Manual Intervention Detection**: Know when *arr needs your help
  - Detects blocked imports (quality cutoff, existing files)
  - Detects when you manually remove items from queue
- **Dashboard Improvements**:
  - New "Manual Action" card for items needing attention
  - Click active scans to view details
  - Click stat cards to filter the file list
  - Scan duration/elapsed time display
- **Notification Support**: Get notified when manual action is needed

## [1.0.2] - 2025-12-01

### Fixed
- **Path Support**: Fixed rejection of valid Radarr/Sonarr naming patterns
  - Curly braces `{imdb-tt0848228}` now work correctly

## [1.0.1] - 2025-12-01

### Security
- Fixed potential command injection vulnerability in health checker

### Changed
- Simplified notification system internals
- Improved corruption detection handler

### Added
- Expanded test coverage for scanner operations
- Performance benchmarks for critical operations

## [1.0.0] - 2025-11-28

### Added
- **Initial Release**
- Detect corrupted media files using ffprobe, MediaInfo, or HandBrake
- Works with Sonarr, Radarr, and Whisparr
- Automatic healing: delete corrupt file and trigger re-download
- Real-time progress updates via WebSocket
- Dashboard with statistics and charts
- Per-path settings: auto-remediate, dry-run mode, retry limits
- Quick and thorough detection modes
- Scheduled scans with cron expressions
- Instant scanning via *arr webhooks
- Notifications: Discord, Slack, Telegram, Pushover, Gotify, ntfy, Email
- Import/export configuration and database backups
- Dark and light themes
- Password-protected with API key authentication
- Docker images for amd64 and arm64

