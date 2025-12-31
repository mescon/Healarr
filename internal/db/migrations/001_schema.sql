-- ============================================================================
-- Healarr Database Schema v1.0
-- Consolidated schema for initial release
-- ============================================================================

-- ============================================================================
-- CONFIGURATION TABLES
-- ============================================================================

-- *arr instance configuration (Sonarr, Radarr, Whisparr)
CREATE TABLE arr_instances (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    type TEXT NOT NULL CHECK(type IN ('sonarr', 'radarr', 'whisparr-v2', 'whisparr-v3')),
    url TEXT NOT NULL,
    api_key TEXT NOT NULL,
    enabled BOOLEAN DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Scan path configuration
CREATE TABLE scan_paths (
    id INTEGER PRIMARY KEY,
    local_path TEXT NOT NULL UNIQUE,
    arr_path TEXT NOT NULL,
    arr_instance_id INTEGER REFERENCES arr_instances(id),
    enabled BOOLEAN DEFAULT 1,
    health_check_mode TEXT DEFAULT 'thorough' CHECK(health_check_mode IN ('quick', 'thorough')),
    auto_remediate BOOLEAN DEFAULT 0,
    dry_run BOOLEAN DEFAULT 0,
    detection_method TEXT NOT NULL DEFAULT 'ffprobe',
    detection_args TEXT,
    detection_mode TEXT NOT NULL DEFAULT 'quick',
    max_retries INTEGER DEFAULT 3,
    verification_timeout_hours INTEGER DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- System settings (password hash, API keys, etc.)
CREATE TABLE settings (
    key TEXT PRIMARY KEY,
    value TEXT NOT NULL,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Scheduled scans
CREATE TABLE scan_schedules (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scan_path_id INTEGER NOT NULL,
    cron_expression TEXT NOT NULL,
    enabled BOOLEAN DEFAULT 1,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY(scan_path_id) REFERENCES scan_paths(id) ON DELETE CASCADE
);

-- Notification providers
CREATE TABLE notifications (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL,
    provider_type TEXT NOT NULL,
    config TEXT NOT NULL,
    events TEXT NOT NULL,
    enabled BOOLEAN DEFAULT 1,
    throttle_seconds INTEGER DEFAULT 5,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Notification history
CREATE TABLE notification_log (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    notification_id INTEGER NOT NULL,
    event_type TEXT NOT NULL,
    message TEXT NOT NULL,
    status TEXT NOT NULL,
    error TEXT,
    sent_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (notification_id) REFERENCES notifications(id) ON DELETE CASCADE
);

CREATE INDEX idx_notification_log_notification_id ON notification_log(notification_id);
CREATE INDEX idx_notification_log_sent_at ON notification_log(sent_at);

-- ============================================================================
-- EVENT STORE (Append-Only, Immutable)
-- ============================================================================

CREATE TABLE events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    aggregate_type TEXT NOT NULL,
    aggregate_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    event_data JSON NOT NULL,
    event_version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    user_id TEXT
);

CREATE INDEX idx_aggregate ON events(aggregate_type, aggregate_id);
CREATE INDEX idx_event_type ON events(event_type);
CREATE INDEX idx_created_at ON events(created_at);
CREATE INDEX idx_events_aggregate_event ON events(aggregate_id, event_type);

-- ============================================================================
-- SCAN TRACKING
-- ============================================================================

CREATE TABLE scans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL,
    path_id INTEGER REFERENCES scan_paths(id),
    status TEXT NOT NULL,
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

CREATE INDEX idx_scans_status ON scans(status);
CREATE INDEX idx_scans_path_id_status ON scans(path_id, status);

CREATE TABLE scan_files (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    scan_id INTEGER NOT NULL,
    file_path TEXT NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('healthy', 'corrupt', 'error', 'inaccessible', 'skipped')),
    corruption_type TEXT,
    error_details TEXT,
    file_size INTEGER,
    scanned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (scan_id) REFERENCES scans(id) ON DELETE CASCADE
);

CREATE INDEX idx_scan_files_scan_id ON scan_files(scan_id);
CREATE INDEX idx_scan_files_status ON scan_files(status);
CREATE INDEX idx_scan_files_scan_status ON scan_files(scan_id, status);

-- ============================================================================
-- PENDING RESCANS (Infrastructure Error Recovery)
-- ============================================================================

CREATE TABLE pending_rescans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    file_path TEXT NOT NULL UNIQUE,
    path_id INTEGER,
    error_type TEXT NOT NULL,
    error_message TEXT,
    retry_count INTEGER DEFAULT 0,
    max_retries INTEGER DEFAULT 5,
    first_seen_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    last_attempt_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    next_retry_at TIMESTAMP,
    status TEXT DEFAULT 'pending' CHECK (status IN ('pending', 'resolved', 'abandoned')),
    resolved_at TIMESTAMP,
    resolution TEXT
);

CREATE INDEX idx_pending_rescans_status_retry ON pending_rescans(status, next_retry_at);
CREATE INDEX idx_pending_rescans_path_id ON pending_rescans(path_id);
CREATE INDEX idx_pending_rescans_file_path ON pending_rescans(file_path);

-- ============================================================================
-- READ MODEL VIEWS
-- ============================================================================

-- Corruption status derived from events
CREATE VIEW corruption_status AS
SELECT
    aggregate_id as corruption_id,
    (SELECT event_type FROM events e2
     WHERE e2.aggregate_id = e.aggregate_id
     ORDER BY id DESC LIMIT 1) as current_state,
    (SELECT COUNT(*) FROM events e3
     WHERE e3.aggregate_id = e.aggregate_id
     AND e3.event_type LIKE '%Failed') as retry_count,
    (SELECT json_extract(event_data, '$.file_path') FROM events e4
     WHERE e4.aggregate_id = e.aggregate_id
     AND e4.event_type = 'CorruptionDetected'
     LIMIT 1) as file_path,
    (SELECT json_extract(event_data, '$.path_id') FROM events e7
     WHERE e7.aggregate_id = e.aggregate_id
     AND e7.event_type = 'CorruptionDetected'
     LIMIT 1) as path_id,
    (SELECT json_extract(event_data, '$.error') FROM events e5
     WHERE e5.aggregate_id = e.aggregate_id
     ORDER BY id DESC LIMIT 1) as last_error,
    (SELECT json_extract(event_data, '$.corruption_type') FROM events e6
     WHERE e6.aggregate_id = e.aggregate_id
     AND e6.event_type = 'CorruptionDetected'
     LIMIT 1) as corruption_type,
    MIN(created_at) as detected_at,
    MAX(created_at) as last_updated_at
FROM events e
WHERE aggregate_type = 'corruption'
GROUP BY aggregate_id;

-- Dashboard statistics
CREATE VIEW dashboard_stats AS
SELECT
    COUNT(DISTINCT CASE
        WHEN current_state != 'VerificationSuccess'
        AND current_state != 'MaxRetriesReached'
        AND current_state != 'CorruptionIgnored'
        THEN corruption_id END) as active_corruptions,
    COUNT(DISTINCT CASE
        WHEN current_state = 'VerificationSuccess'
        THEN corruption_id END) as resolved_corruptions,
    COUNT(DISTINCT CASE
        WHEN current_state = 'MaxRetriesReached'
        THEN corruption_id END) as orphaned_corruptions,
    COUNT(DISTINCT CASE
        WHEN (current_state LIKE '%Started'
        OR current_state LIKE '%Queued'
        OR current_state LIKE '%Progress'
        OR current_state = 'SearchCompleted'
        OR current_state = 'DeletionCompleted'
        OR current_state = 'FileDetected')
        AND current_state != 'CorruptionIgnored'
        THEN corruption_id END) as in_progress
FROM corruption_status
WHERE current_state != 'CorruptionIgnored';
