-- 008_scan_path_overrides.sql
--
-- Per-scan-path overrides for the scan tunables that used to be global-only.
--
-- The thorough health-check timing knobs (thorough_duration_seconds,
-- thorough_timeout_seconds) and the ffmpeg hwaccel selector are currently
-- stored as global rows in the settings table under keys
-- "scan.thorough_duration_seconds", "scan.thorough_timeout_seconds" and
-- "scan.hwaccel" (see internal/repository/settings_keys.go). That is the
-- right default for most homelabs, but breaks down on mixed setups:
--   - a 4K library on a CUDA box wants -hwaccel cuda;
--   - the same Healarr instance also scanning a remote SMB share wants
--     hwaccel=off (no GPU there) plus a longer thorough duration to ride
--     out network latency.
--
-- Rather than forcing the operator to pick one global value, this migration
-- adds three nullable override columns on scan_paths. NULL means "inherit
-- the global setting" - that's the existing behavior, so every pre-
-- migration row keeps working without any backfill. A non-NULL value wins
-- over the global for that specific path. The hwaccel CHECK mirrors the
-- enum exposed by the tunables catalog (see internal/repository/tunables.go)
-- so the DB rejects junk before it reaches ffmpeg.
--
-- Same migration also drops scan_paths.health_check_mode, which has been
-- dead since the codebase moved to detection_mode. The column was created
-- in 001_schema.sql with a CHECK on ('quick','thorough') but no Go code
-- ever reads or writes it; detection_mode (added to 001 in the same
-- consolidation) is what the scanner actually consults. Carrying both
-- around invites future confusion ("which one wins?"), so we remove it.
--
-- DROP COLUMN is available natively from SQLite 3.35+. Healarr runs on
-- modernc.org/sqlite (see internal/db/repository.go), currently v1.50.x,
-- which embeds SQLite well past 3.35 - no table-rebuild dance needed.
-- The column has no index, trigger, view or FK referencing it, so the
-- drop is a clean single statement.
--
-- Heads-up for the caller of this skill: internal/testutil/testdb.go still
-- declares health_check_mode in its hand-written test schema. That file
-- needs the column removed in the same change so tests that spin up a DB
-- via testutil match the migrated schema. No production repo code reads
-- the column, so nothing else needs to move.

ALTER TABLE scan_paths ADD COLUMN thorough_duration_seconds INTEGER;
ALTER TABLE scan_paths ADD COLUMN thorough_timeout_seconds INTEGER;
ALTER TABLE scan_paths ADD COLUMN hwaccel TEXT
    CHECK (hwaccel IS NULL OR hwaccel IN
        ('auto', 'off', 'cuda', 'vaapi', 'qsv', 'videotoolbox', 'vdpau', 'drm'));

ALTER TABLE scan_paths DROP COLUMN health_check_mode;
