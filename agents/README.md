# Healarr Agent Documentation

**Health Evaluation And Library Auto-Recovery for *aRR**

This documentation is designed for AI agents (LLMs) to understand the Healarr codebase, architecture, and design decisions. Read this index first, then dive into specific files based on your task.

## Quick Reference

| Aspect | Details |
|--------|---------|
| **Name** | Healarr (Health Evaluation And Library Auto-Recovery for *aRR) |
| **Version** | v1.0.1 |
| **License** | GPLv3 |
| **Repository** | `github.com/mescon/Healarr` |
| **Backend** | Go 1.25+ with Gin v1.11.0 framework |
| **Frontend** | React 19 + TypeScript + Vite 7 + Tailwind CSS v4 |
| **Database** | SQLite (embedded, WAL mode) |
| **Port** | 3090 (default) |

## Documentation Files

| File | Purpose |
|------|---------|
| [INDEX.md](INDEX.md) | This file - start here |
| [ARCHITECTURE.md](ARCHITECTURE.md) | System architecture, event flow, service interactions |
| [BACKEND.md](BACKEND.md) | Go backend structure, services, integrations |
| [FRONTEND.md](FRONTEND.md) | React frontend structure, components, state management |
| [DATABASE.md](DATABASE.md) | Schema, migrations, data flow |
| [API.md](API.md) | REST endpoints, WebSocket events, authentication |
| [DECISIONS.md](DECISIONS.md) | Design decisions and rationale |

## What is Healarr?

Healarr is a **self-hosted media library health monitoring and automatic recovery tool** for Sonarr, Radarr, and Whisparr (v2 and v3). It:

1. **Scans** media files for corruption using ffprobe
2. **Detects** problems like truncated files, invalid headers, corrupt streams
3. **Remediates** by triggering Sonarr/Radarr/Whisparr to delete and re-download
4. **Verifies** the replacement file is healthy using *arr queue/history APIs
5. **Notifies** via webhooks (Discord, Slack, custom) when issues are found/resolved

## Core Problem Solved

Media files on storage can become corrupted due to:
- Disk failures or bitrot
- Network transfer errors
- Incomplete downloads that *arr marked as complete
- Storage system issues (NFS, SMB, mergerfs)

Healarr automates the detection and recovery process that would otherwise require manual intervention.

## Key Features (v1.0.0)

- **Per-path configuration**: Different settings per scan path (auto-remediate, dry-run, max retries)
- **Detection modes**: Quick (header check) or Thorough (full decode) scanning options
- **Dry-run mode**: Test scans without triggering remediation
- **Rate limiting**: Token bucket rate limiting (5 req/s, burst 10) on *arr API calls
- **Queue monitoring**: Uses *arr queue/history APIs for accurate verification
- **Bulk scan controls**: Pause/resume/cancel all active scans
- **Config import/export**: Backup and restore configuration as JSON
- **Database backup**: Download encrypted SQLite backup
- **Real-time updates**: WebSocket for live scan progress and events
- **Accessibility error handling**: Distinguishes transient errors (mount lost, permission denied) from true corruption
- **Protected UI**: All pages require authentication; login page for password setup

## Key Concepts

### Corruption Lifecycle

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│  detected   │────▶│   queued    │────▶│ remediating │────▶│  verifying  │
└─────────────┘     └─────────────┘     └─────────────┘     └─────────────┘
                                                                   │
                          ┌────────────────────────────────────────┤
                          ▼                                        ▼
                    ┌─────────────┐                         ┌─────────────┐
                    │   failed    │                         │  resolved   │
                    └─────────────┘                         └─────────────┘
```

### Event-Driven Architecture

All state changes flow through an **EventBus** with SQLite persistence:

```
Scanner ──▶ CorruptionDetected ──▶ Remediator ──▶ DeletionCompleted ──▶ Verifier
                                                                           │
                                                                           ▼
                                                    (polls *arr queue/history)
                                                                           │
                                                                           ▼
                                                                  VerificationSuccess
```

### Path Mapping

Healarr runs on a different host than Sonarr/Radarr, so paths must be mapped:

```
Healarr sees:  /mnt/media/Movies/Film.mkv
Sonarr sees:   /media/Movies/Film.mkv

Path mapping: /mnt/media → /media
```

## Project Structure

```
Healarr/
├── cmd/server/main.go           # Entry point
├── internal/
│   ├── api/                     # REST API + WebSocket
│   │   ├── rest.go              # Server setup, routes, auth middleware (~400 lines)
│   │   ├── websocket.go         # Real-time event broadcasting
│   │   ├── rate_limit.go        # Rate limiters for login/setup/webhook
│   │   ├── handlers_health.go   # Health check, system info
│   │   ├── handlers_auth.go     # Authentication, API key, password management
│   │   ├── handlers_config.go   # Settings, restart, export/import, backup
│   │   ├── handlers_arr.go      # *arr instance CRUD, connection testing
│   │   ├── handlers_paths.go    # Scan path CRUD, directory browser
│   │   ├── handlers_scans.go    # Scan triggering, status, pause/resume/cancel
│   │   ├── handlers_corruptions.go  # Corruption listing, history, retry/ignore
│   │   ├── handlers_stats.go    # Dashboard stats and history
│   │   ├── handlers_schedules.go    # Schedule CRUD
│   │   ├── handlers_notifications.go # Notification CRUD and testing
│   │   ├── handlers_webhook.go  # Incoming webhooks from *arr
│   │   └── handlers_logs.go     # Log viewing and download
│   ├── auth/                    # Password authentication (bcrypt)
│   ├── config/                  # Environment config
│   ├── crypto/                  # Encryption for API keys
│   ├── db/                      # SQLite repository + migrations
│   │   └── migrations/
│   │       └── 001_schema.sql   # Consolidated schema (all tables + indexes)
│   ├── domain/                  # Event types (22 event types)
│   ├── eventbus/                # Pub/sub + persistence
│   ├── integration/             # *arr client, ffprobe, path mapper
│   │   ├── arr_client.go        # Sonarr/Radarr/Whisparr API client with rate limiting
│   │   ├── health_checker.go    # ffprobe-based corruption detection
│   │   ├── interfaces.go        # ArrClient, HealthChecker, PathMapper interfaces
│   │   └── path_mapper.go       # Path translation
│   ├── logger/                  # Structured logging with rotation
│   ├── notifier/                # Webhook notifications (Discord, Slack, custom)
│   └── services/                # Core business logic
│       ├── scanner.go           # File scanning with pause/resume/cancel
│       ├── remediator.go        # Delete + search orchestration
│       ├── verifier.go          # Queue-based verification
│       ├── monitor.go           # Lifecycle tracking + retries
│       └── scheduler.go         # Cron-based scheduled scans
├── frontend/
│   ├── src/
│   │   ├── pages/               # Route components (Dashboard, Config, Corruptions, etc.)
│   │   ├── components/          # Reusable UI components
│   │   ├── lib/                 # API client, utilities
│   │   └── types/               # TypeScript types
│   └── public/                  # Static assets
├── web/                         # Built frontend - generated by `npm run build` (not in git)
├── logs/                        # Application logs (healarr.log)
├── healarr.sh                   # Server management script
├── Dockerfile                   # Multi-stage container build
└── docker-compose.yml           # Docker deployment
```

## Quick Commands

```bash
# Development
cd frontend && npm run dev      # Frontend hot reload (port 5173, proxies to 3090)
go run ./cmd/server             # Run backend

# Production build
cd frontend && npm run build    # Build frontend to ../web/
go build -o healarr ./cmd/server

# Using management script
./healarr.sh start              # Start server
./healarr.sh stop               # Stop server
./healarr.sh rebuild            # Build and restart
./healarr.sh status             # Check if running

# Docker
docker compose up -d
```

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `HEALARR_PORT` | `3090` | HTTP server port |
| `HEALARR_BASE_PATH` | `/` | Reverse proxy base path |
| `HEALARR_LOG_LEVEL` | `info` | `debug`, `info`, `error` |
| `HEALARR_DATA_DIR` | `/config` (Docker) or `./data` | Base directory for all persistent data |
| `HEALARR_DATABASE_PATH` | `{DATA_DIR}/healarr.db` | SQLite database location (overrides DATA_DIR) |
| `HEALARR_VERIFICATION_TIMEOUT` | `72h` | Max wait for re-download |
| `HEALARR_VERIFICATION_INTERVAL` | `30s` | Poll interval during verification |

## For Agents: Common Tasks

### Adding a New Feature
1. If it needs new data: Add migration in `internal/db/migrations/` (use next number)
2. If it's business logic: Add/modify service in `internal/services/`
3. If it needs API: Add handler in appropriate `internal/api/handlers_*.go` file, register route in `rest.go`
4. If it needs UI: Add component in `frontend/src/`
5. Run `./healarr.sh rebuild` after changes

### Debugging Issues
1. Check `logs/healarr.log` (structured JSON logs)
2. Check browser console for frontend errors
3. Use `HEALARR_LOG_LEVEL=debug` for verbose logging
4. Check `/api/health` for system status

### Understanding Data Flow
1. Start with the event in `internal/domain/events.go`
2. Find who publishes it (usually a service)
3. Find who subscribes (check `Start()` methods)
4. Trace through the handler functions

### Route Registration Note
When adding new routes with path parameters (e.g., `/scans/:id`), ensure specific literal routes (e.g., `/scans/pause-all`) are registered **BEFORE** parameterized routes to avoid Gin's router matching the literal as a parameter value.

## Next Steps

- [ARCHITECTURE.md](ARCHITECTURE.md) - Deep dive into system design
- [BACKEND.md](BACKEND.md) - Go code organization
- [FRONTEND.md](FRONTEND.md) - React/TypeScript details
- [API.md](API.md) - Complete endpoint reference
