// Package schemas exposes canonical CREATE TABLE SQL for use in tests
// that need an in-memory SQLite seeded with just the schema (no
// migration runner). Production code does NOT use this package - the
// real schema comes from internal/db/migrations.
//
// The constants here are the *result* of applying every migration in
// internal/db/migrations to a fresh DB. When a migration changes a
// table's columns, update the matching constant here so every test
// that uses the shared schema picks up the change in one edit instead
// of needing fixes across a dozen hand-rolled CREATE TABLE blocks.
//
// Stub schemas (where a test only needs an `id` and `local_path` to
// satisfy a foreign-key reference and never queries the full row)
// should NOT be replaced with these constants - they're intentionally
// minimal. Use these constants only where the test actually exercises
// the repo layer or anything that does `SELECT col1, col2, ... FROM
// scan_paths`.
//
// The package has no imports from elsewhere in the project so that any
// test, in any package, can import it without circular-import concerns.
// In particular, internal/repository test files (which can't import
// internal/testutil because testutil transitively depends on
// repository) can import this sub-package directly.
package schemas

// ArrInstances is the CREATE TABLE SQL for the arr_instances table. Many
// tests that use ScanPaths also need this because scan_paths declares a
// FK reference to arr_instances(id); without the referenced table, an
// INSERT into scan_paths fails the FK check under PRAGMA foreign_keys=ON.
// The shape mirrors the production migration 001 column set the
// repository.ArrInstance code touches.
const ArrInstances = `
CREATE TABLE IF NOT EXISTS arr_instances (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL,
	type TEXT NOT NULL,
	url TEXT NOT NULL,
	api_key TEXT NOT NULL,
	enabled BOOLEAN DEFAULT 1,
	webhook_secret TEXT,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);`

// ScanPaths is the CREATE TABLE SQL for the scan_paths table after all
// migrations in internal/db/migrations have been applied. Matches the
// shape of the production schema after migration 008 (per-path
// overrides + dropped health_check_mode column) with ONE intentional
// difference: arr_instance_id is declared without the REFERENCES clause.
//
// The production schema does declare `REFERENCES arr_instances(id)`,
// which means tests under PRAGMA foreign_keys=ON cannot insert a scan
// path with arr_instance_id=N unless an arr_instances row with id=N
// already exists. Most tests in this repo don't exercise *arr APIs
// and don't want the bookkeeping. Tests that *do* want the FK
// enforced can seed ArrInstances first and add the FK manually.
//
// IF NOT EXISTS makes the statement idempotent so callers that build
// up the schema in stages (or that already have an ALTER-based stub
// in place) don't have to special-case re-creation.
const ScanPaths = `
CREATE TABLE IF NOT EXISTS scan_paths (
	id INTEGER PRIMARY KEY,
	local_path TEXT NOT NULL UNIQUE,
	arr_path TEXT NOT NULL DEFAULT '',
	arr_instance_id INTEGER,
	enabled BOOLEAN DEFAULT 1,
	auto_remediate BOOLEAN DEFAULT 0,
	dry_run BOOLEAN DEFAULT 0,
	detection_method TEXT NOT NULL DEFAULT 'ffprobe',
	detection_args TEXT,
	detection_mode TEXT NOT NULL DEFAULT 'quick',
	max_retries INTEGER DEFAULT 3,
	verification_timeout_hours INTEGER,
	thorough_duration_seconds INTEGER,
	thorough_timeout_seconds INTEGER,
	hwaccel TEXT,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);`

// ScanPresets is the CREATE TABLE SQL for the scan_presets table after
// migration 009. The 5 built-in rows (Zero-byte only, Quick, Fast
// triage, Deep scan, Paranoid) are NOT seeded by this constant - tests
// that need them should INSERT explicitly so the test's intent is
// visible at the call site. See ScanPresetsBuiltinSeed for a
// drop-in helper that mirrors what migration 009 inserts.
const ScanPresets = `
CREATE TABLE IF NOT EXISTS scan_presets (
	id INTEGER PRIMARY KEY,
	name TEXT NOT NULL UNIQUE,
	description TEXT NOT NULL DEFAULT '',
	detection_method TEXT NOT NULL DEFAULT 'ffprobe',
	detection_mode TEXT NOT NULL DEFAULT 'quick',
	detection_args TEXT,
	thorough_duration_seconds INTEGER,
	thorough_timeout_seconds INTEGER,
	hwaccel TEXT,
	is_builtin INTEGER NOT NULL DEFAULT 0,
	created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);`

// ScanPresetsBuiltinSeed inserts the same five built-in rows that
// migration 009 seeds in production. Tests that exercise the
// is_builtin gate need these in place; tests that only verify CRUD on
// custom presets do not.
const ScanPresetsBuiltinSeed = `
INSERT INTO scan_presets (name, detection_method, detection_mode, is_builtin) VALUES
	('Zero-byte only', 'zero_byte', 'quick', 1),
	('Quick',          'ffprobe',   'quick', 1),
	('Fast triage',    'ffprobe',   'thorough', 1),
	('Deep scan',      'ffprobe',   'thorough', 1),
	('Paranoid',       'handbrake', 'thorough', 1);`
