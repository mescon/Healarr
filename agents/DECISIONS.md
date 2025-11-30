# Healarr Design Decisions

## Overview

This document explains the key architectural and design decisions made during Healarr's development, including the rationale and trade-offs considered.

---

## Architecture Decisions

### 1. Event-Driven Architecture

**Decision**: Use an event-driven architecture with an EventBus for state changes.

**Rationale**:
- **Loose coupling**: Services don't need direct references to each other
- **Auditability**: All actions are logged as events, creating a natural audit trail
- **Debuggability**: Easy to reconstruct what happened by viewing event history
- **Extensibility**: New features can subscribe to existing events
- **Async processing**: Long-running operations don't block the API

**Trade-offs**:
- More complex than direct method calls
- Eventual consistency (state may lag behind events briefly)
- Debugging distributed flows requires tracing events

---

### 2. SQLite Database

**Decision**: Use embedded SQLite instead of PostgreSQL/MySQL.

**Rationale**:
- **Zero dependencies**: No database server to install/manage
- **Simple deployment**: Single binary + single file
- **Sufficient performance**: Read-heavy workload with moderate writes
- **Easy backup**: Just copy the file (or use API endpoint)
- **Portable**: Works on any system

**Trade-offs**:
- No concurrent write scaling (single writer)
- Not suitable for clustered deployments

---

### 3. Go Backend with Gin

**Decision**: Use Go 1.25+ with Gin v1.11.0 framework.

**Rationale**:
- **Single binary**: Easy deployment, no runtime dependencies
- **Good concurrency**: Goroutines perfect for async event processing
- **Performance**: Fast compilation, low memory usage
- **Type safety**: Compile-time error catching

---

### 4. React Frontend with TanStack Query

**Decision**: Use React 19 + TypeScript + Vite + TanStack Query.

**Rationale**:
- **Component model**: Clean separation of UI concerns
- **TanStack Query**: Automatic caching, background refresh, cache invalidation
- **TypeScript**: Type safety reduces bugs
- **Vite**: Fast development experience

---

### 5. Rate Limiting on *arr API

**Decision**: Implement token bucket rate limiting (5 req/s, burst 10) on all *arr API calls.

**Rationale**:
- **Protect *arr instances**: Prevent overwhelming Sonarr/Radarr during large scans
- **Play nice**: Don't monopolize API when user is actively using *arr UI
- **Predictable**: Consistent request rate regardless of scan parallelism

**Implementation**:
```go
type ArrClient struct {
    limiter *rate.Limiter  // 5 req/s, burst 10
}
```

---

### 6. Queue-Based Verification

**Decision**: Use *arr queue and history APIs for verification instead of simple file polling.

**Rationale**:
- **Accurate**: Know exactly when download completes and imports
- **Progress tracking**: Can show download progress to user
- **Better error detection**: Catch failed downloads immediately
- **Reduced filesystem polling**: Less load on storage

**Verification Strategy** (priority order):
1. Check *arr queue for active download
2. Check *arr history for import events
3. Query *arr API for current file path
4. Fallback to filesystem polling

---

### 7. Per-Path Configuration

**Decision**: Allow different settings per scan path.

**Rationale**:
- **Flexibility**: Different paths may need different policies
- **Dry-run mode**: Test detection without remediation
- **Granular control**: Auto-remediate some paths but not others

**Per-path settings**:
- `auto_remediate` - Whether to automatically fix
- `dry_run` - Detect only, no remediation actions
- `max_retries` - Retry limit before giving up
- `verification_timeout` - How long to wait for re-download

---

### 8. Accessibility Error Separation

**Decision**: Distinguish between corruption errors and accessibility errors.

**Rationale**:
- **Avoid false positives**: Mount failures, permission issues are transient
- **No unnecessary remediation**: Don't delete files just because mount is down
- **Better logging**: Clear visibility into infrastructure issues

**Error Categories**:
- **Corruption** (trigger remediation): ZeroByte, CorruptHeader, CorruptStream, InvalidFormat
- **Accessibility** (skip, don't remediate): AccessDenied, PathNotFound, MountLost, IOError, Timeout

---

### 9. Pause/Resume Scans

**Decision**: Support pausing and resuming scans.

**Rationale**:
- **User control**: Pause during high-load periods
- **Resumability**: Don't lose progress on server restart
- **Bulk controls**: Pause/resume all scans at once

**Implementation**: Tracks `last_file_processed` in database for resume.

---

### 10. Consolidated Logging

**Decision**: All logs go to `logs/healarr.log`, console output to /dev/null.

**Rationale**:
- **Single source**: Easy to find all logs
- **Structured**: JSON format for parsing
- **Rotation**: Built-in log rotation
- **UI accessible**: Logs viewable and downloadable from web UI

---

## Feature Decisions

### 11. Path Mapping

**Decision**: Require explicit path mappings instead of auto-detection.

**Rationale**:
- **Reliability**: Explicit mappings never guess wrong
- **Docker support**: Complex volume mappings are common
- **Predictability**: User always knows what will happen

---

### 12. WebSocket for Real-time Updates

**Decision**: Use WebSocket for live UI updates.

**Rationale**:
- **Immediate feedback**: See scan progress instantly
- **Efficient**: No repeated HTTP requests
- **Better UX**: Feels more responsive

---

### 13. HTTP Clipboard Fallback

**Decision**: Provide fallback clipboard copy for non-HTTPS contexts.

**Rationale**:
- **Local development**: HTTP localhost is common
- **Home users**: Not everyone has HTTPS internally
- **Works everywhere**: Modern API with legacy fallback

```typescript
if (navigator.clipboard && window.isSecureContext) {
    await navigator.clipboard.writeText(text);
} else {
    // Legacy textarea + execCommand fallback
}
```

---

### 14. Config Export/Import

**Decision**: Allow exporting and importing configuration as JSON.

**Rationale**:
- **Backup**: Easy configuration backup
- **Migration**: Move settings between instances
- **Disaster recovery**: Quickly restore after problems

---

### 15. Database Backup Download

**Decision**: Provide encrypted database backup download via API.

**Rationale**:
- **Complete backup**: Includes all data, not just config
- **User-friendly**: No SSH required
- **Encryption**: Sensitive data protected in transit

---

### 16. Logs Download as .txt

**Decision**: Rename .log files to .txt when downloading.

**Rationale**:
- **Windows compatibility**: .txt opens in Notepad by default
- **User-friendly**: No file association issues
- **ZIP format**: All logs bundled together

---

## UI/UX Decisions

### 17. Quick Actions Section

**Decision**: Add dedicated Quick Actions section in Config page.

**Rationale**:
- **Easy access**: Common operations front and center
- **Bulk controls**: Scan all, pause all, resume all, cancel all
- **Discoverability**: Users find features faster

---

### 18. Separated Advanced Settings

**Decision**: Move data management to Advanced accordion.

**Rationale**:
- **Clean UI**: Don't overwhelm with options
- **Progressive disclosure**: Advanced users can find it
- **Safety**: Dangerous operations (backup, import) are less prominent

**Quick Actions**: Scan All, Pause All, Resume All, Cancel All
**Advanced**: Export Config, Import Config, Download Backup

---

### 19. Click-to-Select API Key

**Decision**: Clicking API key input selects all text.

**Rationale**:
- **Easy copy**: One click to select, then copy
- **User expectation**: Common pattern for secrets
- **Visual feedback**: Cursor indicates clickable

---

## Security Decisions

### 20. Simple Password Authentication

**Decision**: Simple password authentication, not OAuth/OIDC.

**Rationale**:
- **Simplicity**: No external auth provider needed
- **Self-hosted**: Works offline
- **Sufficient**: For single-user home use

---

### 21. Rate Limiting on Auth Endpoints

**Decision**: Apply rate limits to login and setup endpoints.

**Rationale**:
- **Brute-force protection**: Limit password guessing
- **Simple implementation**: Token bucket per IP

**Limits**:
- Login: 5 per minute
- Setup: 3 per hour
- Webhooks: 100 per minute

---

## Deployment Decisions

### 22. Management Script

**Decision**: Provide `healarr.sh` for server management.

**Commands**:
- `./healarr.sh start` - Start server
- `./healarr.sh stop` - Stop server
- `./healarr.sh rebuild` - Build and restart
- `./healarr.sh status` - Check if running

**Rationale**:
- **Consistent operations**: Standard way to manage
- **Build integration**: Handles both Go and frontend builds
- **Logging**: Proper output handling

---

### 23. Single Binary Named `healarr`

**Decision**: Build output is `healarr`, not `server`.

**Rationale**:
- **Consistency**: Binary name matches project
- **Process identification**: Easy to find in ps/top
- **No confusion**: Only one binary to track

---

## Future Considerations

### Not Yet Implemented

1. **Clustering**: Single-instance only
2. **Fine-grained permissions**: Single admin user
3. **Metrics export**: No Prometheus integration
4. **Plugin system**: Hardcoded integrations

### Potential Enhancements

1. **More notification types**: Telegram, email
2. **Health check alternatives**: MediaInfo, HandBrake
3. **Scheduled reports**: Email summaries
4. **Multiple user accounts**: With role-based access
