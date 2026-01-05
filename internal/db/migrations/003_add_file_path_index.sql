-- ============================================================================
-- Migration 003: Add index for json_extract file_path lookups
-- ============================================================================
--
-- This improves performance for the common pattern of looking up corruptions
-- by file_path, which currently requires a full table scan with json_extract.

-- Add a generated column for file_path extraction (SQLite 3.31+)
-- This allows indexing on the extracted value
ALTER TABLE events ADD COLUMN file_path_extracted TEXT
    GENERATED ALWAYS AS (json_extract(event_data, '$.file_path')) STORED;

-- Index the extracted file path for fast lookups
CREATE INDEX IF NOT EXISTS idx_events_file_path ON events(file_path_extracted)
    WHERE file_path_extracted IS NOT NULL;

-- Compound index for common query patterns (event_type + file_path)
CREATE INDEX IF NOT EXISTS idx_events_type_file_path ON events(event_type, file_path_extracted)
    WHERE file_path_extracted IS NOT NULL;
