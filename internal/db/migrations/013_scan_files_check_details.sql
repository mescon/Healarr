-- 013_scan_files_check_details.sql
--
-- Adds a per-file check-details column to scan_files.
--
-- scan_files rows record each scanned file's outcome (healthy / corrupt /
-- skipped / inaccessible) but not WHAT was checked. check_details holds a
-- small JSON document written by the scanner: detection method, mode,
-- hardware acceleration, structural-check duration, and the
-- content-analysis outcome (passed / skipped with reason / flagged).
--
-- It backs the scan-details UI where clicking a file shows its check
-- journey - including for healthy files, which have no corruption
-- aggregate and therefore no event journey to fall back on.
--
-- Existing rows keep NULL (no backfill is possible - the information was
-- never recorded). NULL means "scanned before this feature existed" and
-- the UI says so.

ALTER TABLE scan_files ADD COLUMN check_details TEXT;
