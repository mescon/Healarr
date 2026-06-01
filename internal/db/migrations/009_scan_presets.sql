-- 009_scan_presets.sql
--
-- Adds a "scan presets" table: named bundles of scan-related field values
-- that the operator can apply to a scan path (or to an ad-hoc scan) with
-- one click in the UI.
--
-- Background. Migration 008 made the previously-global scan tunables
-- (thorough_duration_seconds, thorough_timeout_seconds, hwaccel)
-- overridable per scan_path. That removed the "one global setting"
-- bottleneck but pushed a fresh problem onto the operator: every new
-- path now has six or seven knobs to tune (detection_method,
-- detection_mode, detection_args, the three 008 overrides, plus
-- max_retries). Most operators only ever want a small set of sensible
-- combinations - "just check headers", "decode the first minute on the
-- GPU", "full thorough sweep", and so on. Asking them to remember and
-- re-enter those combinations on every path is friction we can remove.
--
-- A scan preset bundles those field values under a human-readable name
-- and description. The UI exposes them as a dropdown ("Apply preset...")
-- on the scan path edit form and on the "Scan now" dialog; selecting one
-- fills in the underlying scan_paths columns / scan request fields. The
-- preset itself is NOT referenced by foreign key from scan_paths - we
-- copy the values at apply time so that editing a preset later does not
-- silently rewrite the behavior of every path that ever used it. This
-- matches how Sonarr/Radarr treat quality profiles vs. applied items
-- and avoids surprise behavior changes on the next scheduled scan.
--
-- Built-in vs. custom. is_builtin separates the five presets seeded by
-- this migration (factory defaults that ship with Healarr) from
-- operator-defined ones. The /api/scan-presets handlers gate DELETE and
-- the destructive fields of UPDATE on is_builtin=0 so the operator
-- cannot accidentally lobotomize the dropdown by deleting "Quick" or
-- rewriting "Deep scan" to mean something else. Custom presets
-- (is_builtin=0) are fully mutable. The UI is expected to render the
-- built-ins above a divider and disable the trash icon on them.
--
-- Column shape. The columns mirror the per-path scan configuration so
-- that "apply preset to path" is a column-for-column copy with no
-- translation layer. detection_args is JSON-encoded []string to match
-- the existing scan_paths.detection_args column (see
-- internal/repository/scan_paths.go). The three 008 override columns
-- (thorough_duration_seconds, thorough_timeout_seconds, hwaccel) are
-- nullable with the exact same semantics as on scan_paths: NULL means
-- "inherit the global setting at scan time". Note the distinction in
-- the seed data between thorough_duration_seconds=NULL ("inherit") and
-- thorough_duration_seconds=0 ("explicit full file"); the scanner
-- already distinguishes these two cases.
--
-- The CHECK constraints duplicate the enums used elsewhere
-- (detection_method, detection_mode, hwaccel) rather than relying on
-- application-level validation, so a buggy handler or a hand-edited row
-- cannot smuggle a junk value into the table and break the scanner
-- later.
--
-- Forward-only, no down. The seed uses INSERT ... ON CONFLICT(name) DO
-- NOTHING so that if this migration ever has to be re-applied against a
-- partially-seeded DB (e.g. a previous attempt failed mid-file and the
-- version row was not recorded) it does not blow up on the UNIQUE name
-- constraint. Re-running a successfully-applied migration is not
-- something the runner does, but cheap idempotency here costs nothing.
--
-- Heads-up for the caller of this skill:
--   - New file expected: internal/repository/scan_presets.go (CRUD plus
--     an ApplyToScanPath helper that copies the columns onto a
--     scan_paths row).
--   - New handlers expected: internal/api/handlers_scan_presets.go
--     wired into the router; DELETE and the value-bearing fields of
--     PUT must reject is_builtin=1 rows with 403.
--   - Frontend: add the dropdown to the scan path form and the
--     "Scan now" dialog; render built-ins above a divider.

CREATE TABLE IF NOT EXISTS scan_presets (
    id INTEGER PRIMARY KEY,
    name TEXT NOT NULL UNIQUE,
    description TEXT NOT NULL DEFAULT '',
    detection_method TEXT NOT NULL DEFAULT 'ffprobe'
        CHECK (detection_method IN ('zero_byte', 'ffprobe', 'mediainfo', 'handbrake')),
    detection_mode TEXT NOT NULL DEFAULT 'quick'
        CHECK (detection_mode IN ('quick', 'thorough')),
    detection_args TEXT,
    thorough_duration_seconds INTEGER,
    thorough_timeout_seconds INTEGER,
    hwaccel TEXT
        CHECK (hwaccel IS NULL OR hwaccel IN
            ('auto', 'off', 'cuda', 'vaapi', 'qsv', 'videotoolbox', 'vdpau', 'drm')),
    is_builtin INTEGER NOT NULL DEFAULT 0
        CHECK (is_builtin IN (0, 1)),
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Seed the five built-in presets. Order of insertion sets the implicit
-- id ordering (1..5), which the UI uses to render the dropdown in a
-- stable, predictable order. ON CONFLICT DO NOTHING keeps the seed safe
-- against retries; it does NOT update existing rows, so if a future
-- migration needs to change a built-in's description it must do so
-- explicitly with an UPDATE statement in that migration.

INSERT INTO scan_presets (
    name, description, detection_method, detection_mode,
    detection_args, thorough_duration_seconds, thorough_timeout_seconds,
    hwaccel, is_builtin
) VALUES (
    'Zero-byte only',
    'Only flags completely empty files. Instant per file - useful as a first-pass sweep on cold/archived storage.',
    'zero_byte', 'quick',
    NULL, NULL, NULL,
    NULL, 1
) ON CONFLICT(name) DO NOTHING;

INSERT INTO scan_presets (
    name, description, detection_method, detection_mode,
    detection_args, thorough_duration_seconds, thorough_timeout_seconds,
    hwaccel, is_builtin
) VALUES (
    'Quick',
    'Checks container headers and stream info. Fast and reliable for obvious corruption (the default).',
    'ffprobe', 'quick',
    NULL, NULL, NULL,
    NULL, 1
) ON CONFLICT(name) DO NOTHING;

INSERT INTO scan_presets (
    name, description, detection_method, detection_mode,
    detection_args, thorough_duration_seconds, thorough_timeout_seconds,
    hwaccel, is_builtin
) VALUES (
    'Fast triage',
    'Decodes only the first 60 seconds with hardware acceleration. Catches header / decode-init / early-stream errors in seconds, ideal for triaging large AV1 / 4K libraries.',
    'ffprobe', 'thorough',
    NULL, 60, NULL,
    'auto', 1
) ON CONFLICT(name) DO NOTHING;

INSERT INTO scan_presets (
    name, description, detection_method, detection_mode,
    detection_args, thorough_duration_seconds, thorough_timeout_seconds,
    hwaccel, is_builtin
) VALUES (
    'Deep scan',
    'Full-file ffmpeg decode with a generous 30-minute timeout. Catches mid-file decode errors and bad frames. Slow but thorough.',
    'ffprobe', 'thorough',
    NULL, 0, 1800,
    'auto', 1
) ON CONFLICT(name) DO NOTHING;

INSERT INTO scan_presets (
    name, description, detection_method, detection_mode,
    detection_args, thorough_duration_seconds, thorough_timeout_seconds,
    hwaccel, is_builtin
) VALUES (
    'Paranoid',
    'HandBrake''s stricter decoder, CPU-only. Catches issues ffmpeg''s lenient parsers can miss, at the cost of speed.',
    'handbrake', 'thorough',
    NULL, NULL, 1800,
    'off', 1
) ON CONFLICT(name) DO NOTHING;
