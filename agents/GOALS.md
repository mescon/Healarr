# Healarr Goals & Design Philosophy

## Executive Summary

Healarr (**H**ealth **E**valuation **A**nd **L**ibrary **A**uto-**R**ecovery for ***aRR**) solves the silent data corruption problem in self-hosted media libraries by providing automated detection, remediation, and verification integrated with the *arr ecosystem.

**Current Version**: v1.0.3
**Status**: Production-ready, feature-complete for v1.x scope

---

## The Problem Space

### Core Problem
Media files stored on home servers and NAS devices suffer from silent corruption due to:

| Cause | Impact |
|-------|--------|
| **Bitrot** | Gradual data decay on spinning disks and flash storage |
| **Network transfer errors** | Incomplete or corrupted downloads marked as complete |
| **Storage system issues** | NFS/SMB/MergerFS protocol errors, mount failures |
| **Disk failures** | Sector errors, controller malfunctions |
| **Power events** | Incomplete writes during outages |

### Why This Matters
- Users often discover corruption only when attempting playback
- Manual detection requires scanning every file with ffprobe/MediaInfo
- Manual remediation requires:
  1. Identifying the media in Sonarr/Radarr
  2. Deleting the corrupt file
  3. Triggering a re-search
  4. Monitoring for successful replacement
  5. Verifying the new file is healthy
- This process is tedious, error-prone, and doesn't scale

### Target Users
Self-hosted media enthusiasts running:
- Sonarr (TV shows)
- Radarr (Movies)
- Whisparr v2/v3 (Adult content)
- Large media libraries (10,000+ files)
- Network-attached storage (NAS, SAN, cloud mounts)

---

## Solution Philosophy

### 1. Automation with Control

**Principle**: Automate the tedious, but give users control over when and how automation happens.

| Feature | Control Mechanism |
|---------|-------------------|
| Detection | Scheduled scans (cron), on-demand, webhook-triggered |
| Remediation | Per-path `auto_remediate` toggle |
| Testing | Per-path `dry_run` mode (detect without action) |
| Retry limits | Per-path `max_retries` configuration |
| Timeouts | Per-path `verification_timeout` |

**Why**: Users need to trust the system before enabling full automation. Dry-run mode and per-path controls let them validate behavior before committing.

### 2. Event-Driven Architecture

**Principle**: Every state change is an event, creating a complete audit trail.

```
User action → Event published → Services react → New events → Database persisted
```

**Benefits**:
- **Auditability**: Complete history of every corruption from detection to resolution
- **Debuggability**: Trace any issue through the event log
- **Loose coupling**: Services don't know about each other, only events
- **Extensibility**: Add features by subscribing to existing events
- **Resilience**: Failed operations retry via events, not in-band

### 3. Distinguish Corruption from Accessibility

**Principle**: Don't delete files just because the storage is temporarily unavailable.

| Error Type | Action | Examples |
|------------|--------|----------|
| **Corruption** | Remediate | Zero-byte, corrupt header, invalid stream |
| **Accessibility** | Skip, log warning | Mount lost, permission denied, timeout |

**Why**: NFS mounts drop, Docker volumes unmount during restarts, permissions change. These are infrastructure issues, not file corruption. Aggressive remediation would cause data loss.

### 4. Respect the *arr Ecosystem

**Principle**: Play nice with Sonarr/Radarr - don't monopolize their APIs.

- **Rate limiting**: 5 requests/second with burst of 10
- **Queue-based verification**: Use *arr's queue/history APIs instead of filesystem polling
- **Path mapping**: Explicit mappings handle Docker volume differences

**Why**: Users are actively using *arr UIs. Hammering the API degrades their experience and could cause *arr to throttle or fail.

### 5. Zero Dependencies, Simple Deployment

**Principle**: One binary, one database file, no external services.

| Component | Choice | Rationale |
|-----------|--------|-----------|
| Database | SQLite (embedded) | No PostgreSQL/MySQL to manage |
| Backend | Go single binary | No runtime dependencies |
| Frontend | Embedded in binary | No separate web server |
| Detection | ffprobe/MediaInfo (optional) | User provides, or use Docker image |

**Why**: Self-hosted users want minimal operational burden. Every external dependency is a potential failure point.

---

## Design Decisions

### Architecture Choices

| Decision | Choice | Alternatives Considered |
|----------|--------|------------------------|
| **Event sourcing** | Append-only events table | Direct state mutation |
| **Service decomposition** | Scanner, Remediator, Verifier, Monitor, Scheduler | Monolithic processor |
| **API framework** | Gin (Go) | Echo, Fiber, net/http |
| **Frontend framework** | React 19 + TanStack Query | Vue, Svelte, HTMX |
| **State management** | Server state via TanStack Query | Redux, Zustand |
| **Styling** | Tailwind CSS v4 | CSS modules, Styled Components |
| **Real-time updates** | WebSocket | Server-Sent Events, polling |

### Verification Strategy

Multi-stage verification ensures accurate detection of successful remediation:

```
1. Check *arr queue → Is download in progress?
   └── Yes → Monitor download progress events
   └── No → Step 2

2. Check *arr history → Was file imported recently?
   └── Yes → Health check the imported file
   └── No → Step 3

3. Query *arr API → What's the current file path?
   └── Different from corrupt file → Health check new file
   └── Same → Fallback

4. Filesystem polling (fallback) → Does file exist?
   └── Yes → Health check
   └── No → Wait (up to verification_timeout)
```

**Why multiple stages**: *arr's import process is async. The file might be downloading, importing, or already imported. Each stage catches a different state.

---

## Current Capabilities (v1.0.x)

### Core Features
- Multi-method detection (ffprobe, MediaInfo, HandBrake)
- Automatic remediation via *arr APIs
- Queue-based verification with progress tracking
- Per-path configuration (auto-remediate, dry-run, max-retries)
- Scheduled and on-demand scanning
- Webhook integration for immediate post-download scanning

### User Interface
- Real-time dashboard with stats and charts
- Corruption lifecycle visualization ("Remediation Journey")
- Scan management (pause, resume, cancel - individual and bulk)
- Configuration export/import for backup/migration
- Built-in help and troubleshooting documentation

### Notifications
- Discord, Slack, custom webhooks
- Telegram, Pushover, Gotify, ntfy, Email (SMTP)
- Per-notification event filtering

### Operations
- Automatic database maintenance (daily at 3 AM)
- Database backup (on startup, every 6 hours)
- Log rotation (100MB max, 7 days retention)
- Configurable data retention (default: 90 days)

---

## Future Considerations

### Short-term Enhancements (v1.x)
| Feature | Value | Complexity |
|---------|-------|------------|
| **Sample-based scanning** | Faster scans for huge libraries | Medium |
| **Parallel file checking** | Faster scans with multiple ffprobe workers | Low |
| **Detection config presets** | Quick vs thorough scan templates | Low |
| **Corruption trend analytics** | Identify problematic storage/paths | Medium |

### Medium-term Features (v2.x)
| Feature | Value | Complexity |
|---------|-------|------------|
| **Prometheus metrics export** | Integration with existing monitoring | Medium |
| **Multi-user support** | Role-based access for families/friends | High |
| **Bazarr integration** | Scan/remediate subtitles | Medium |
| **Custom ffprobe arguments** | Advanced detection tuning | Low |

### Long-term Vision
| Feature | Value | Complexity |
|---------|-------|------------|
| **Cluster mode** | Distributed scanning across multiple hosts | Very High |
| **Plugin system** | User-defined detection/remediation methods | High |
| **Machine learning detection** | Detect corruption without full decode | Very High |
| **Plex/Jellyfin direct integration** | Skip *arr for non-managed libraries | Medium |

---

## Non-Goals

Healarr explicitly does **not** aim to:

| Anti-goal | Reason |
|-----------|--------|
| **Replace Sonarr/Radarr** | Complement, don't compete |
| **Transcode media** | Tdarr, Handbrake exist for this |
| **Organize/rename files** | *arr apps handle this |
| **Download management** | SABnzbd, qBittorrent, Deluge exist |
| **Streaming** | Plex, Jellyfin, Emby exist |
| **Backup** | Duplicati, Restic, Borg exist |

---

## Quality Standards

### Code Quality
- Type safety: Go's static typing, TypeScript for frontend
- Testing: Unit tests for services, integration tests for API
- Error handling: Explicit error propagation, structured logging
- Concurrency: Proper mutex usage, no data races

### Operational Quality
- Graceful shutdown: Complete in-progress operations
- Recovery: Resume scans after restart
- Observability: Structured JSON logs, event audit trail
- Security: Bcrypt passwords, rate limiting, encrypted backups

### User Experience
- Responsive design: Mobile-friendly dashboard
- Accessibility: Semantic HTML, ARIA labels
- Progressive disclosure: Advanced options behind accordions
- Real-time feedback: WebSocket updates for all operations

---

## Success Metrics

Healarr succeeds when users can:

1. **Set and forget**: Configure paths, enable auto-remediate, trust it works
2. **Sleep soundly**: Know their library is being monitored 24/7
3. **Investigate easily**: Trace any corruption from detection to resolution
4. **Stay informed**: Receive notifications about important events
5. **Recover quickly**: Handle mount failures without data loss

---

## Development Principles

### For Contributors

1. **Prefer editing over creating**: Modify existing files before adding new ones
2. **Keep handlers thin**: Business logic lives in services, not API handlers
3. **Emit events for state changes**: Don't mutate state silently
4. **Test the happy path and the error path**: Both matter equally
5. **Document decisions**: Explain *why*, not just *what*

### For AI Agents

1. **Read ARCHITECTURE.md first**: Understand the event flow before making changes
2. **Check handler organization**: API handlers are split by domain (handlers_*.go)
3. **Route ordering matters**: Literal routes before parameterized routes in Gin
4. **Per-path settings**: Many features are configured per-path, not globally
5. **Accessibility vs corruption**: Understand the distinction before modifying detection

---

## Version History Context

| Version | Milestone |
|---------|-----------|
| v0.1 | Initial prototype with ffprobe detection |
| v0.5 | Event-driven architecture, Sonarr/Radarr integration |
| v0.8 | React frontend, WebSocket real-time updates |
| v0.9 | Per-path configuration, dry-run mode |
| v1.0 | Queue-based verification, Whisparr v2/v3 support |
| v1.0.3 | **Current** - Stability improvements, accessibility error separation |

---

## Summary

Healarr transforms media library health management from a manual, reactive process into an automated, proactive system. By respecting user control, providing complete observability, and integrating seamlessly with the *arr ecosystem, it gives self-hosters confidence that their libraries are protected against silent corruption.

The event-driven architecture ensures every action is auditable, the per-path configuration enables gradual trust-building, and the accessibility error distinction prevents false positives from causing data loss. This is a tool built for the paranoid data hoarder who wants automation without sacrificing control.
