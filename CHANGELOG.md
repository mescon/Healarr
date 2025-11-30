# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

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
