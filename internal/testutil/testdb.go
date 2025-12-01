package testutil

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mescon/Healarr/internal/domain"
	_ "modernc.org/sqlite"
)

// NewTestDB creates an in-memory SQLite database with the Healarr schema.
// Returns a database handle that should be closed by the caller.
func NewTestDB() (*sql.DB, error) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		return nil, fmt.Errorf("failed to open in-memory database: %w", err)
	}

	if err := initializeSchema(db); err != nil {
		_ = db.Close() // Ignore close error since we're already returning an error
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	return db, nil
}

// initializeSchema creates all required tables for testing.
func initializeSchema(db *sql.DB) error {
	// Configure SQLite for testing
	pragmas := []string{
		"PRAGMA foreign_keys=ON",
		"PRAGMA busy_timeout=5000",
	}
	for _, pragma := range pragmas {
		if _, err := db.Exec(pragma); err != nil {
			return fmt.Errorf("failed to set pragma: %w", err)
		}
	}

	// Create events table (core of the event-sourcing system)
	_, err := db.Exec(`
		CREATE TABLE events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			aggregate_type TEXT NOT NULL,
			aggregate_id TEXT NOT NULL,
			event_type TEXT NOT NULL,
			event_data JSON NOT NULL,
			event_version INTEGER NOT NULL DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			user_id TEXT
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create events table: %w", err)
	}

	// Create indexes for events
	_, err = db.Exec(`CREATE INDEX idx_aggregate ON events(aggregate_type, aggregate_id)`)
	if err != nil {
		return fmt.Errorf("failed to create aggregate index: %w", err)
	}
	_, err = db.Exec(`CREATE INDEX idx_event_type ON events(event_type)`)
	if err != nil {
		return fmt.Errorf("failed to create event_type index: %w", err)
	}

	// Create scan_paths table
	_, err = db.Exec(`
		CREATE TABLE scan_paths (
			id INTEGER PRIMARY KEY,
			local_path TEXT NOT NULL UNIQUE,
			arr_path TEXT NOT NULL,
			arr_instance_id INTEGER,
			enabled BOOLEAN DEFAULT 1,
			health_check_mode TEXT DEFAULT 'thorough',
			auto_remediate BOOLEAN DEFAULT 0,
			dry_run BOOLEAN DEFAULT 0,
			detection_method TEXT NOT NULL DEFAULT 'ffprobe',
			detection_args TEXT,
			detection_mode TEXT NOT NULL DEFAULT 'quick',
			max_retries INTEGER DEFAULT 3,
			verification_timeout_hours INTEGER DEFAULT NULL,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create scan_paths table: %w", err)
	}

	// Create scans table
	_, err = db.Exec(`
		CREATE TABLE scans (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			path TEXT NOT NULL,
			path_id INTEGER,
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
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create scans table: %w", err)
	}

	// Create scan_files table
	_, err = db.Exec(`
		CREATE TABLE scan_files (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			scan_id INTEGER NOT NULL,
			file_path TEXT NOT NULL,
			status TEXT NOT NULL,
			corruption_type TEXT,
			error_details TEXT,
			file_size INTEGER,
			scanned_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create scan_files table: %w", err)
	}

	// Create pending_rescans table
	_, err = db.Exec(`
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
			status TEXT DEFAULT 'pending',
			resolved_at TIMESTAMP,
			resolution TEXT
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create pending_rescans table: %w", err)
	}

	// Create settings table
	_, err = db.Exec(`
		CREATE TABLE settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create settings table: %w", err)
	}

	// Create arr_instances table
	_, err = db.Exec(`
		CREATE TABLE arr_instances (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			url TEXT NOT NULL,
			api_key TEXT NOT NULL,
			enabled BOOLEAN DEFAULT 1,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
			updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create arr_instances table: %w", err)
	}

	return nil
}

// SeedEvent inserts a single event into the test database.
func SeedEvent(db *sql.DB, event domain.Event) (int64, error) {
	eventDataJSON, err := json.Marshal(event.EventData)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal event data: %w", err)
	}

	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now()
	}
	if event.EventVersion == 0 {
		event.EventVersion = 1
	}

	result, err := db.Exec(`
		INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, event_version, created_at, user_id)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`, event.AggregateType, event.AggregateID, event.EventType, eventDataJSON, event.EventVersion, event.CreatedAt, event.UserID)
	if err != nil {
		return 0, fmt.Errorf("failed to insert event: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get inserted ID: %w", err)
	}
	return id, nil
}

// SeedEvents inserts multiple events into the test database.
func SeedEvents(db *sql.DB, events []domain.Event) error {
	for _, event := range events {
		if _, err := SeedEvent(db, event); err != nil {
			return err
		}
	}
	return nil
}

// SeedScanPath inserts a scan path configuration into the test database.
func SeedScanPath(db *sql.DB, id int64, localPath, arrPath string, autoRemediate, dryRun bool) error {
	_, err := db.Exec(`
		INSERT INTO scan_paths (id, local_path, arr_path, auto_remediate, dry_run, enabled)
		VALUES (?, ?, ?, ?, ?, 1)
	`, id, localPath, arrPath, autoRemediate, dryRun)
	return err
}

// SeedArrInstance inserts an arr instance configuration into the test database.
func SeedArrInstance(db *sql.DB, id int64, name, instanceType, url, apiKey string) error {
	_, err := db.Exec(`
		INSERT INTO arr_instances (id, name, type, url, api_key, enabled)
		VALUES (?, ?, ?, ?, ?, 1)
	`, id, name, instanceType, url, apiKey)
	return err
}

// GetEventsByAggregate retrieves all events for a given aggregate ID.
func GetEventsByAggregate(db *sql.DB, aggregateID string) ([]domain.Event, error) {
	rows, err := db.Query(`
		SELECT id, aggregate_type, aggregate_id, event_type, event_data, event_version, created_at, user_id
		FROM events WHERE aggregate_id = ? ORDER BY id ASC
	`, aggregateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var events []domain.Event
	for rows.Next() {
		var e domain.Event
		var eventDataJSON string
		var userID sql.NullString
		if err := rows.Scan(&e.ID, &e.AggregateType, &e.AggregateID, &e.EventType, &eventDataJSON, &e.EventVersion, &e.CreatedAt, &userID); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(eventDataJSON), &e.EventData); err != nil {
			return nil, err
		}
		if userID.Valid {
			e.UserID = userID.String
		}
		events = append(events, e)
	}
	return events, rows.Err()
}

// CountEventsByType counts events of a given type.
func CountEventsByType(db *sql.DB, eventType domain.EventType) (int, error) {
	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM events WHERE event_type = ?", eventType).Scan(&count)
	return count, err
}

// ClearEvents removes all events from the database.
func ClearEvents(db *sql.DB) error {
	_, err := db.Exec("DELETE FROM events")
	return err
}

// ClearAllTables removes all data from all tables.
func ClearAllTables(db *sql.DB) error {
	tables := []string{"events", "scans", "scan_files", "scan_paths", "pending_rescans", "settings", "arr_instances"}
	for _, table := range tables {
		if _, err := db.Exec("DELETE FROM " + table); err != nil {
			return fmt.Errorf("failed to clear %s: %w", table, err)
		}
	}
	return nil
}
