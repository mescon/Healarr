package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// corruptionSchema mirrors the production events table + corruption_status
// view (001_schema.sql) so the repo's view queries exercise real SQL.
const corruptionSchema = `
CREATE TABLE events (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	aggregate_type TEXT NOT NULL,
	aggregate_id   TEXT NOT NULL,
	event_type     TEXT NOT NULL,
	event_data     JSON NOT NULL,
	event_version  INTEGER NOT NULL DEFAULT 1,
	created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	user_id        TEXT
);
CREATE VIEW corruption_status AS
SELECT
	aggregate_id as corruption_id,
	(SELECT event_type FROM events e2 WHERE e2.aggregate_id = e.aggregate_id ORDER BY id DESC LIMIT 1) as current_state,
	(SELECT COUNT(*) FROM events e3 WHERE e3.aggregate_id = e.aggregate_id AND e3.event_type LIKE '%Failed') as retry_count,
	(SELECT json_extract(event_data, '$.file_path') FROM events e4 WHERE e4.aggregate_id = e.aggregate_id AND e4.event_type = 'CorruptionDetected' LIMIT 1) as file_path,
	(SELECT json_extract(event_data, '$.path_id') FROM events e7 WHERE e7.aggregate_id = e.aggregate_id AND e7.event_type = 'CorruptionDetected' LIMIT 1) as path_id,
	(SELECT json_extract(event_data, '$.error') FROM events e5 WHERE e5.aggregate_id = e.aggregate_id ORDER BY id DESC LIMIT 1) as last_error,
	(SELECT json_extract(event_data, '$.corruption_type') FROM events e6 WHERE e6.aggregate_id = e.aggregate_id AND e6.event_type = 'CorruptionDetected' LIMIT 1) as corruption_type,
	MIN(created_at) as detected_at,
	MAX(created_at) as last_updated_at
FROM events e
WHERE aggregate_type = 'corruption'
GROUP BY aggregate_id;
`

func newCorruptionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(corruptionSchema); err != nil {
		t.Fatalf("create corruption schema: %v", err)
	}
	return db
}

// appendEvent inserts an event row with an explicit created_at so test
// ordering is deterministic (SQLite CURRENT_TIMESTAMP has 1s granularity).
func appendEvent(t *testing.T, db *sql.DB, aggregateID, eventType, eventData string, createdAt time.Time) {
	t.Helper()
	_, err := db.Exec(
		`INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, created_at) VALUES ('corruption', ?, ?, ?, ?)`,
		aggregateID, eventType, eventData, createdAt.UTC().Format("2006-01-02 15:04:05"),
	)
	if err != nil {
		t.Fatalf("append event: %v", err)
	}
}

// seedCorruption writes a detected→resolved (or custom final) corruption.
func seedCorruption(t *testing.T, db *sql.DB, id, filePath, finalState string, base time.Time) {
	t.Helper()
	appendEvent(t, db, id, "CorruptionDetected",
		`{"file_path":"`+filePath+`","path_id":1,"corruption_type":"CorruptHeader"}`, base)
	if finalState != "CorruptionDetected" {
		appendEvent(t, db, id, finalState, `{}`, base.Add(time.Minute))
	}
}

func TestCorruptionRepository_CountFiltered_andListFiltered(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	seedCorruption(t, db, "c1", "/a.mkv", "VerificationSuccess", base)
	seedCorruption(t, db, "c2", "/b.mkv", "CorruptionDetected", base.Add(time.Hour))

	// No filter — both aggregates.
	total, err := repo.CountFiltered(context.Background(), "")
	if err != nil || total != 2 {
		t.Fatalf("CountFiltered no filter = (%d, %v), want (2, nil)", total, err)
	}

	// Filter to pending only.
	where := " WHERE current_state = ?"
	total, err = repo.CountFiltered(context.Background(), where, "CorruptionDetected")
	if err != nil || total != 1 {
		t.Fatalf("CountFiltered pending = (%d, %v), want (1, nil)", total, err)
	}

	rows, err := repo.ListFiltered(context.Background(), where, "ORDER BY last_updated_at DESC", 10, 0, "CorruptionDetected")
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListFiltered = (%d, %v), want (1, nil)", len(rows), err)
	}
	if rows[0].CorruptionID != "c2" || rows[0].FilePath != "/b.mkv" {
		t.Errorf("unexpected row: %+v", rows[0])
	}
}

func TestCorruptionRepository_CountByState_andListByState(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	seedCorruption(t, db, "c1", "/a.mkv", "VerificationSuccess", base)
	seedCorruption(t, db, "c2", "/b.mkv", "VerificationSuccess", base.Add(time.Hour))
	seedCorruption(t, db, "c3", "/c.mkv", "CorruptionDetected", base.Add(2*time.Hour))

	n, err := repo.CountByState(context.Background(), "VerificationSuccess")
	if err != nil || n != 2 {
		t.Fatalf("CountByState = (%d, %v), want (2, nil)", n, err)
	}

	rows, err := repo.ListByState(context.Background(), "VerificationSuccess", 10, 0)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ListByState = (%d, %v), want (2, nil)", len(rows), err)
	}
	// Ordered by last_updated_at DESC → c2 first.
	if rows[0].CorruptionID != "c2" {
		t.Errorf("ListByState order: got %s first, want c2", rows[0].CorruptionID)
	}
}

func TestCorruptionRepository_LatestEventData(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	appendEvent(t, db, "c1", "DownloadProgress", `{"percent":10}`, base)
	appendEvent(t, db, "c1", "DownloadProgress", `{"percent":90}`, base.Add(time.Minute))

	// DESC → newest (90).
	raw, err := repo.LatestEventData(context.Background(), "c1", "DownloadProgress", "DESC")
	if err != nil || raw != `{"percent":90}` {
		t.Errorf("LatestEventData DESC = (%q, %v), want newest", raw, err)
	}

	// ASC → oldest (10).
	raw, err = repo.LatestEventData(context.Background(), "c1", "DownloadProgress", "ASC")
	if err != nil || raw != `{"percent":10}` {
		t.Errorf("LatestEventData ASC = (%q, %v), want oldest", raw, err)
	}

	// Missing event type.
	if _, err := repo.LatestEventData(context.Background(), "c1", "Nonexistent", "DESC"); !errors.Is(err, ErrNotFound) {
		t.Errorf("LatestEventData missing = %v, want ErrNotFound", err)
	}
}

func TestCorruptionRepository_ListEvents(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	appendEvent(t, db, "c1", "CorruptionDetected", `{"file_path":"/a.mkv"}`, base)
	appendEvent(t, db, "c1", "DeletionCompleted", `{}`, base.Add(time.Minute))
	appendEvent(t, db, "c1", "VerificationSuccess", `{}`, base.Add(2*time.Minute))

	events, err := repo.ListEvents(context.Background(), "c1")
	if err != nil || len(events) != 3 {
		t.Fatalf("ListEvents = (%d, %v), want (3, nil)", len(events), err)
	}
	// Oldest first.
	if events[0].EventType != "CorruptionDetected" || events[2].EventType != "VerificationSuccess" {
		t.Errorf("unexpected event order: %s ... %s", events[0].EventType, events[2].EventType)
	}
}

func TestCorruptionRepository_CorruptionDetectedFileInfo(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	appendEvent(t, db, "c1", "CorruptionDetected", `{"file_path":"/movies/x.mkv","path_id":42}`, base)

	fp, pathID, err := repo.CorruptionDetectedFileInfo(context.Background(), "c1")
	if err != nil {
		t.Fatalf("CorruptionDetectedFileInfo: %v", err)
	}
	if fp != "/movies/x.mkv" {
		t.Errorf("file_path = %q, want /movies/x.mkv", fp)
	}
	if !pathID.Valid || pathID.Int64 != 42 {
		t.Errorf("path_id = %+v, want 42", pathID)
	}

	// Aggregate with no CorruptionDetected event.
	appendEvent(t, db, "c2", "DownloadProgress", `{"percent":1}`, base)
	if _, _, err := repo.CorruptionDetectedFileInfo(context.Background(), "c2"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing detected event = %v, want ErrNotFound", err)
	}
}

func TestCorruptionRepository_DeleteEvents(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	appendEvent(t, db, "c1", "CorruptionDetected", `{"file_path":"/a"}`, base)
	appendEvent(t, db, "c1", "VerificationSuccess", `{}`, base.Add(time.Minute))

	n, err := repo.DeleteEvents(context.Background(), "c1")
	if err != nil {
		t.Fatalf("DeleteEvents: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteEvents removed %d, want 2", n)
	}

	events, _ := repo.ListEvents(context.Background(), "c1")
	if len(events) != 0 {
		t.Errorf("after delete, ListEvents returned %d rows", len(events))
	}
}
