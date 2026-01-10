# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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

