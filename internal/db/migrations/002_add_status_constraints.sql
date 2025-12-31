-- ============================================================================
-- Healarr Database Migration 002
-- Add CHECK constraints for scan status and notification provider type
-- ============================================================================

-- SQLite doesn't support adding CHECK constraints to existing tables directly.
-- We need to recreate the table. Using a transaction to ensure atomicity.

-- 1. Create new scans table with CHECK constraint
CREATE TABLE scans_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL,
    path_id INTEGER REFERENCES scan_paths(id),
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'paused', 'completed', 'cancelled', 'error', 'interrupted', 'aborted')),
    files_scanned INTEGER DEFAULT 0,
    corruptions_found INTEGER DEFAULT 0,
    total_files INTEGER DEFAULT 0,
    current_file_index INTEGER DEFAULT 0,
    file_list TEXT,
    detection_config TEXT,
    auto_remediate INTEGER DEFAULT 0,
    dry_run BOOLEAN DEFAULT 0,
    error_message TEXT,
    started_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    completed_at TIMESTAMP
);

-- 2. Copy data from old table
INSERT INTO scans_new SELECT * FROM scans;

-- 3. Drop old table
DROP TABLE scans;

-- 4. Rename new table
ALTER TABLE scans_new RENAME TO scans;

-- 5. Recreate indexes for scans
CREATE INDEX idx_scans_status ON scans(status);
CREATE INDEX idx_scans_path_id_status ON scans(path_id, status);

-- 6. Add CHECK constraint for notification provider types
CREATE TABLE notifications_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    provider_type TEXT NOT NULL CHECK (provider_type IN ('discord', 'telegram', 'slack', 'pushover', 'email', 'gotify', 'ntfy', 'custom')),
    config TEXT NOT NULL,
    events TEXT NOT NULL,
    enabled BOOLEAN DEFAULT 1,
    throttle_seconds INTEGER DEFAULT 5,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

INSERT INTO notifications_new SELECT * FROM notifications;
DROP TABLE notifications;
ALTER TABLE notifications_new RENAME TO notifications;

-- 7. Add NOT NULL constraint to events.aggregate_type
-- (Already has NOT NULL, but ensuring consistency with CHECK)
