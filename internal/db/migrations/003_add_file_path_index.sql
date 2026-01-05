-- ============================================================================
-- Migration 003: Add expression index for json_extract file_path lookups
-- ============================================================================
--
-- This improves performance for the common pattern of looking up corruptions
-- by file_path, which currently requires a full table scan with json_extract.
--
-- Note: SQLite doesn't allow adding STORED generated columns via ALTER TABLE,
-- so we use expression-based indexes directly on the json_extract result.

-- Expression index for file_path lookups
-- SQLite will use this index when queries use the exact same expression
CREATE INDEX IF NOT EXISTS idx_events_file_path
    ON events(json_extract(event_data, '$.file_path'))
    WHERE json_extract(event_data, '$.file_path') IS NOT NULL;

-- Compound expression index for common query patterns (event_type + file_path)
CREATE INDEX IF NOT EXISTS idx_events_type_file_path
    ON events(event_type, json_extract(event_data, '$.file_path'))
    WHERE json_extract(event_data, '$.file_path') IS NOT NULL;
