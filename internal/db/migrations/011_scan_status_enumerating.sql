-- 011_scan_status_enumerating.sql
--
-- Promotes two scanner status values that were, until now, in-memory only.
-- internal/services/scanner.go already defines ScanStatusEnumerating
-- ("enumerating") and ScanStatusScanning ("scanning"), and the reconcile
-- query in internal/repository/scan.go selects WHERE status IN
-- ('running', 'enumerating', 'scanning'). But the scans.status CHECK
-- constraint set in 002_add_status_constraints.sql only allowed
-- ('pending', 'running', 'paused', 'completed', 'cancelled', 'error',
-- 'interrupted', 'aborted'), so any attempt to persist 'enumerating' or
-- 'scanning' would fail the constraint -- those states could never reach
-- the database and existed solely in the running scanner's memory.
--
-- We are about to start persisting status='enumerating' on a scan row that
-- is created BEFORE file enumeration finishes, so the scan is visible in
-- /scans during a slow directory walk, then transitioning the row to
-- 'scanning' once the walk completes and per-file work begins. This
-- migration makes both values legal in the CHECK constraint.
--
-- SQLite cannot ALTER a CHECK constraint in place, so we follow the exact
-- table-recreate pattern from 002: build scans_new with the full current
-- column set plus the expanded CHECK, copy the data, drop the old table,
-- rename, and recreate the scans indexes. Forward-only; no DOWN section.

-- 1. Create new scans table with the expanded status CHECK constraint
CREATE TABLE scans_new (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    path TEXT NOT NULL,
    path_id INTEGER REFERENCES scan_paths(id),
    status TEXT NOT NULL CHECK (status IN ('pending', 'running', 'enumerating', 'scanning', 'paused', 'completed', 'cancelled', 'error', 'interrupted', 'aborted')),
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

-- 5. Recreate indexes for scans (dropped with the old table)
CREATE INDEX idx_scans_status ON scans(status);
CREATE INDEX idx_scans_path_id_status ON scans(path_id, status);
