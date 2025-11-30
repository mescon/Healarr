# Healarr Database

## Overview

Healarr uses **SQLite** with WAL (Write-Ahead Logging) mode for:
- Simple deployment (single file)
- Good read performance
- Adequate write performance for this use case
- No external dependencies

Database file: `healarr.db` (location: `{DATA_DIR}/healarr.db`, configurable via `HEALARR_DATA_DIR` or `HEALARR_DATABASE_PATH`)

**Data Directory Structure:**
```
/config (Docker) or ./data (local)
├── healarr.db      # SQLite database
├── healarr.pid     # Process ID file (daemon mode)
├── backups/        # Automatic database backups
└── logs/
    └── healarr.log # Application logs (auto-rotated)
```

## Migrations

Migrations are stored in `internal/db/migrations/` and applied in order:

| File | Purpose |
|------|---------|
| `001_schema.sql` | Consolidated schema - all tables, indexes, and features |

**Note:** The schema was consolidated into a single migration file for cleaner deployments. All features (resumable scans, accessibility errors, pending rescans, per-path dry-run) are included in the base schema.

## Schema

### Core Tables

#### `events` - Event Store

Append-only log of all system events for audit trail and debugging.

```sql
CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    aggregate_type TEXT NOT NULL,      -- 'corruption', 'scan', 'system'
    aggregate_id TEXT NOT NULL,        -- UUID of related entity
    event_type TEXT NOT NULL,          -- 'CorruptionDetected', etc.
    event_data TEXT,                   -- JSON payload
    event_version INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    user_id TEXT
);

CREATE INDEX idx_events_aggregate ON events(aggregate_type, aggregate_id);
CREATE INDEX idx_events_type ON events(event_type);
CREATE INDEX idx_events_created ON events(created_at);
```

#### `corruptions` - Corruption State

Current state of each detected corruption.

```sql
CREATE TABLE corruptions (
    id TEXT PRIMARY KEY,               -- UUID
    file_path TEXT NOT NULL,
    arr_path TEXT,
    instance_id INTEGER,
    media_id INTEGER,
    status TEXT DEFAULT 'detected',    -- detected, queued, remediating, verifying, resolved, failed, ignored
    corruption_type TEXT,              -- ZeroByte, CorruptHeader, CorruptStream, InvalidFormat
    detected_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    resolved_at DATETIME,
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 3,
    last_error TEXT,
    path_id INTEGER,
    FOREIGN KEY (instance_id) REFERENCES instances(id),
    FOREIGN KEY (path_id) REFERENCES scan_paths(id)
);

CREATE INDEX idx_corruptions_status ON corruptions(status);
CREATE INDEX idx_corruptions_path ON corruptions(file_path);
CREATE INDEX idx_corruptions_detected ON corruptions(detected_at);
```

#### `scans` - Scan History

```sql
CREATE TABLE scans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL,
    path_id INTEGER,
    status TEXT DEFAULT 'running',     -- running, paused, completed, failed, cancelled
    files_scanned INTEGER DEFAULT 0,
    corruptions_found INTEGER DEFAULT 0,
    started_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    completed_at DATETIME,
    error TEXT,
    dry_run BOOLEAN DEFAULT 0,         -- Added in migration 005
    last_file_processed TEXT,          -- For resume support (migration 002)
    FOREIGN KEY (path_id) REFERENCES scan_paths(id)
);
```

#### `scan_files` - Per-File Results

```sql
CREATE TABLE scan_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scan_id INTEGER NOT NULL,
    file_path TEXT NOT NULL,
    status TEXT NOT NULL,              -- healthy, corrupt, error, accessibility_error
    corruption_type TEXT,
    error_details TEXT,
    file_size INTEGER,
    scanned_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (scan_id) REFERENCES scans(id)
);
```

### Configuration Tables

#### `scan_paths` - Monitored Directories

```sql
CREATE TABLE scan_paths (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    local_path TEXT NOT NULL,          -- Path as seen by Healarr
    arr_path TEXT,                     -- Path as seen by *arr
    instance_id INTEGER,
    enabled INTEGER DEFAULT 1,
    auto_remediate INTEGER DEFAULT 0,
    dry_run BOOLEAN DEFAULT 0,         -- Added in migration 005
    max_retries INTEGER DEFAULT 3,
    verification_timeout TEXT DEFAULT '72h',
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (instance_id) REFERENCES instances(id)
);
```

#### `instances` - *arr Instances

```sql
CREATE TABLE instances (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL,                -- sonarr, radarr, whisparr-v2, whisparr-v3
    url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    enabled INTEGER DEFAULT 1,
    webhook_url TEXT,                  -- Per-instance webhook URL
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

**Instance Types:**
- `sonarr` - Sonarr v3 (episode-based, TV shows)
- `radarr` - Radarr v3 (movie-based)
- `whisparr-v2` - Whisparr v2 (uses Sonarr-like API)
- `whisparr-v3` - Whisparr v3 (uses Radarr-like API)

#### `path_mappings` - Path Translation

```sql
CREATE TABLE path_mappings (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    local_path TEXT NOT NULL,
    arr_path TEXT NOT NULL,
    enabled INTEGER DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

#### `settings` - Key-Value Store

```sql
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Common settings:
-- 'password_hash' - bcrypt hash of admin password
-- 'api_key' - API authentication key
-- 'base_path' - URL base path for reverse proxy
-- 'log_level' - debug/info/error
```

#### `scan_schedules` - Cron Schedules

```sql
CREATE TABLE scan_schedules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scan_path_id INTEGER NOT NULL,
    cron_expression TEXT NOT NULL,
    enabled INTEGER DEFAULT 1,
    last_run DATETIME,
    next_run DATETIME,
    FOREIGN KEY (scan_path_id) REFERENCES scan_paths(id)
);
```

#### `notification_configs` - Webhook Setup

```sql
CREATE TABLE notification_configs (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    type TEXT NOT NULL,                -- webhook, discord, slack
    url TEXT NOT NULL,
    enabled INTEGER DEFAULT 1,
    events TEXT,                       -- JSON array of event types
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);
```

## Writing New Migrations

Create a new file with the next number:

```sql
-- internal/db/migrations/006_new_feature.sql

-- Add new column (SQLite-safe)
ALTER TABLE some_table ADD COLUMN new_column TEXT DEFAULT '';

-- Add new table
CREATE TABLE IF NOT EXISTS new_table (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP
);

-- Add new index
CREATE INDEX IF NOT EXISTS idx_new_index ON some_table(some_column);
```

**Important**: SQLite doesn't support all ALTER TABLE operations. For complex changes, you may need to:
1. Create new table
2. Copy data
3. Drop old table
4. Rename new table

## Common Queries

### Dashboard Statistics

```sql
-- Active corruptions by status
SELECT status, COUNT(*) as count
FROM corruptions
WHERE status NOT IN ('resolved', 'ignored')
GROUP BY status;

-- Resolution rate
SELECT 
    COUNT(CASE WHEN status = 'resolved' THEN 1 END) as resolved,
    COUNT(CASE WHEN status = 'failed' THEN 1 END) as failed,
    COUNT(*) as total
FROM corruptions
WHERE status IN ('resolved', 'failed');
```

### Corruption History

```sql
-- All events for a corruption
SELECT event_type, event_data, created_at
FROM events
WHERE aggregate_type = 'corruption' AND aggregate_id = ?
ORDER BY created_at ASC;
```

### Per-Path Settings

```sql
-- Get path with dry_run setting
SELECT id, local_path, arr_path, auto_remediate, dry_run, max_retries
FROM scan_paths
WHERE id = ?;
```

## Backup & Recovery

### Backup

```bash
# Using the API (encrypted download)
curl -H "X-API-Key: $KEY" http://localhost:3090/api/config/backup -o backup.db

# Direct file copy (stop app first)
cp healarr.db healarr.db.backup

# SQLite backup command
sqlite3 healarr.db ".backup healarr.db.backup"
```

### Recovery

```bash
# Replace database
mv healarr.db.backup healarr.db

# Restore from SQL dump
sqlite3 healarr.db < backup.sql
```

### Password Reset

```bash
sqlite3 healarr.db "DELETE FROM settings WHERE key = 'password_hash';"
# Restart server, then set new password via /api/auth/setup
```

## Performance

### Indexes

All frequently queried columns have indexes:
- `corruptions(status)` - Filtering by status
- `corruptions(detected_at)` - Sorting by date
- `events(aggregate_id)` - Event history lookup
- `scans(started_at)` - Recent scans

### WAL Mode

Enabled for better concurrent read/write:
```go
db.Exec("PRAGMA journal_mode=WAL")
```

### Automatic Maintenance

Healarr performs automatic database maintenance:

**On Startup (`NewRepository`):**
```go
// Configure SQLite for optimal performance
pragmas := []string{
    "PRAGMA journal_mode=WAL",           // Better concurrency
    "PRAGMA synchronous=NORMAL",         // Safe with WAL, faster
    "PRAGMA auto_vacuum=INCREMENTAL",    // Auto reclaim space
    "PRAGMA temp_store=MEMORY",          // Faster temp tables
    "PRAGMA foreign_keys=ON",            // Enforce FK constraints
    "PRAGMA cache_size=-8000",           // 8MB cache
    "PRAGMA busy_timeout=5000",          // 5s wait on locks
}

// Run integrity check
db.QueryRow("PRAGMA quick_check")
```

**Daily Maintenance (`RunMaintenance`):**
Runs automatically at 3 AM local time:
```go
func (r *Repository) RunMaintenance(retentionDays int) error {
    // 1. Prune old events (keep last N days)
    r.DB.Exec("DELETE FROM events WHERE created_at < ?", cutoff)

    // 2. Prune old completed scans
    r.DB.Exec(`DELETE FROM scans WHERE status IN ('completed', 'cancelled', 'error') AND ended_at < ?`, cutoff)

    // 3. Delete orphaned corruption records
    r.DB.Exec(`DELETE FROM corruptions WHERE scan_id NOT IN (SELECT id FROM scans) AND status IN ('resolved', 'failed')`)

    // 4. Incremental vacuum
    r.DB.Exec("PRAGMA incremental_vacuum")

    // 5. Update query planner statistics
    r.DB.Exec("ANALYZE")

    // 6. WAL checkpoint
    r.DB.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
}
```

**Configuration:**
- `HEALARR_RETENTION_DAYS` / `--retention-days`: Days to keep old data (default: 90)
- Set to `0` to disable automatic pruning

**Automatic Backups:**
- Created on startup and every 6 hours
- Stored in `{DATA_DIR}/backups/`
- Last 5 backups retained (older ones auto-deleted)

### Manual Maintenance

```sql
-- Reclaim space from deleted records
VACUUM;

-- Update query planner statistics
ANALYZE;

-- Check database integrity
PRAGMA integrity_check;
```

## Troubleshooting

### Database Locked

- Only one Healarr instance should access the database
- Check for orphaned processes: `lsof healarr.db`
- Avoid NFS/SMB mounted storage for the database

### Slow Queries

- Ensure indexes exist
- Run `ANALYZE` to update statistics
- Consider archiving old events

### Corruption Recovery

```bash
# Check integrity
sqlite3 healarr.db "PRAGMA integrity_check;"

# If corrupt, try recovery
sqlite3 healarr.db ".recover" | sqlite3 healarr-recovered.db
```
