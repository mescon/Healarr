# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
