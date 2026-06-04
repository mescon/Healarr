-- 010_scan_files_unique_index.sql
--
-- Makes scan_files inserts idempotent on (scan_id, file_path) so the
-- refactored scanner from #290 can safely replay work on resume.
--
-- Background: the scanner is moving from a batched fan-out/fan-in pipeline
-- to a semaphore-bounded worker pool that persists progress via a
-- completion bitmap plus a contiguous-completion watermark. When a scan
-- is interrupted (process crash, host reboot, deliberate cancel) the
-- watermark lags behind the actual highest-completed index because
-- workers complete out of order; on the next startup the runner replays
-- the window between the persisted watermark and the highest-completed
-- index to guarantee no file is missed. Files in that replay window will
-- already have a row in scan_files from their first processing pass, so
-- a naive INSERT would create duplicate (scan_id, file_path) rows --
-- inflating progress counts, polluting the corruption view, and breaking
-- per-file lookups.
--
-- After this migration the repository switches its INSERT to
-- ON CONFLICT(scan_id, file_path) DO NOTHING, making replay safe and
-- letting the scanner treat "did we already record this?" as a database
-- invariant rather than an application-level check.
--
-- Step 1 is defensive: any historical duplicates (none expected, but the
-- absence of the constraint until now means we cannot prove it) must be
-- collapsed first, otherwise the UNIQUE INDEX creation in step 2 will
-- fail and roll the migration back. We keep the lowest id per group so
-- foreign-key-ish references by id (if any external tooling held them)
-- stay stable on the oldest row.

DELETE FROM scan_files
WHERE id NOT IN (
    SELECT MIN(id) FROM scan_files GROUP BY scan_id, file_path
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_scan_files_scan_id_file_path_unique
    ON scan_files(scan_id, file_path);
