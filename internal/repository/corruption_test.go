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

func TestCorruptionRepository_StateCounts(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	seedCorruption(t, db, "resolved1", "/a", "VerificationSuccess", base)
	seedCorruption(t, db, "orphan1", "/b", "MaxRetriesReached", base)
	seedCorruption(t, db, "pending1", "/c", "CorruptionDetected", base)
	seedCorruption(t, db, "pending2", "/d", "CorruptionDetected", base)
	seedCorruption(t, db, "ignored1", "/e", "CorruptionIgnored", base)
	seedCorruption(t, db, "failed1", "/f", "DeletionFailed", base)

	c, err := repo.StateCounts(context.Background())
	if err != nil {
		t.Fatalf("StateCounts: %v", err)
	}
	if c.Resolved != 1 || c.Orphaned != 1 || c.Pending != 2 || c.Ignored != 1 || c.Failed != 1 {
		t.Errorf("unexpected counts: %+v", c)
	}
}

func TestCorruptionRepository_CountDetectedToday(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	now := time.Now().UTC()

	// Today, not ignored → counts.
	seedCorruption(t, db, "today1", "/a", "CorruptionDetected", now)
	// Today but ignored → excluded.
	seedCorruption(t, db, "today2", "/b", "CorruptionIgnored", now)
	// Old → excluded.
	seedCorruption(t, db, "old1", "/c", "CorruptionDetected", now.Add(-72*time.Hour))

	n, err := repo.CountDetectedToday(context.Background())
	if err != nil {
		t.Fatalf("CountDetectedToday: %v", err)
	}
	if n != 1 {
		t.Errorf("CountDetectedToday = %d, want 1", n)
	}
}

func TestCorruptionRepository_CountByCorruptionType(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	appendEvent(t, db, "c1", "CorruptionDetected", `{"file_path":"/a","corruption_type":"CorruptHeader"}`, base)
	appendEvent(t, db, "c2", "CorruptionDetected", `{"file_path":"/b","corruption_type":"CorruptHeader"}`, base)
	appendEvent(t, db, "c3", "CorruptionDetected", `{"file_path":"/c","corruption_type":"TruncatedFile"}`, base)

	types, err := repo.CountByCorruptionType(context.Background())
	if err != nil {
		t.Fatalf("CountByCorruptionType: %v", err)
	}
	got := map[string]int{}
	for _, tc := range types {
		got[tc.Type] = tc.Count
	}
	if got["CorruptHeader"] != 2 || got["TruncatedFile"] != 1 {
		t.Errorf("type counts = %v, want CorruptHeader=2 TruncatedFile=1", got)
	}
}

func TestCorruptionRepository_PathCorruptionStats(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	// path_id 1: one active (detected), one resolved.
	appendEvent(t, db, "p1a", "CorruptionDetected", `{"file_path":"/m/a","path_id":1}`, base)
	appendEvent(t, db, "p1b", "CorruptionDetected", `{"file_path":"/m/b","path_id":1}`, base)
	appendEvent(t, db, "p1b", "VerificationSuccess", `{}`, base.Add(time.Minute))
	// path_id 2: one active — must not bleed into path 1's stats.
	appendEvent(t, db, "p2a", "CorruptionDetected", `{"file_path":"/t/a","path_id":2}`, base)

	active, total, resolved, err := repo.PathCorruptionStats(context.Background(), 1)
	if err != nil {
		t.Fatalf("PathCorruptionStats: %v", err)
	}
	if active != 1 || total != 2 || resolved != 1 {
		t.Errorf("path 1 stats = active=%d total=%d resolved=%d, want 1/2/1", active, total, resolved)
	}
}

func TestCorruptionRepository_HasActive(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	now := time.Now().UTC()

	// Active corruption (detected, unresolved, recent).
	appendEvent(t, db, "act", "CorruptionDetected", `{"file_path":"/active.mkv"}`, now)
	// Resolved → not active.
	appendEvent(t, db, "res", "CorruptionDetected", `{"file_path":"/resolved.mkv"}`, now)
	appendEvent(t, db, "res", "VerificationSuccess", `{}`, now.Add(time.Minute))

	if ok, err := repo.HasActive(context.Background(), "/active.mkv"); err != nil || !ok {
		t.Errorf("HasActive(/active.mkv) = (%v, %v), want (true, nil)", ok, err)
	}
	if ok, err := repo.HasActive(context.Background(), "/resolved.mkv"); err != nil || ok {
		t.Errorf("HasActive(/resolved.mkv) = (%v, %v), want (false, nil)", ok, err)
	}
	if ok, err := repo.HasActive(context.Background(), "/unknown.mkv"); err != nil || ok {
		t.Errorf("HasActive(/unknown.mkv) = (%v, %v), want (false, nil)", ok, err)
	}
}

func TestCorruptionRepository_ListActiveFilePathsUnderRoot(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	now := time.Now().UTC()

	appendEvent(t, db, "a", "CorruptionDetected", `{"file_path":"/movies/a.mkv"}`, now)
	appendEvent(t, db, "b", "CorruptionDetected", `{"file_path":"/movies/sub/b.mkv"}`, now)
	// Under a different root → excluded.
	appendEvent(t, db, "c", "CorruptionDetected", `{"file_path":"/tv/c.mkv"}`, now)
	// Resolved → excluded.
	appendEvent(t, db, "d", "CorruptionDetected", `{"file_path":"/movies/d.mkv"}`, now)
	appendEvent(t, db, "d", "VerificationSuccess", `{}`, now.Add(time.Minute))

	set, err := repo.ListActiveFilePathsUnderRoot(context.Background(), "/movies")
	if err != nil {
		t.Fatalf("ListActiveFilePathsUnderRoot: %v", err)
	}
	if len(set) != 2 || !set["/movies/a.mkv"] || !set["/movies/sub/b.mkv"] {
		t.Errorf("active set = %v, want /movies/a.mkv and /movies/sub/b.mkv", set)
	}
}

func TestCorruptionRepository_CountDetectedByDay(t *testing.T) {
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	now := time.Now().UTC()

	// Two on the same recent day, one older-but-within-window.
	appendEvent(t, db, "d1", "CorruptionDetected", `{"file_path":"/a"}`, now)
	appendEvent(t, db, "d2", "CorruptionDetected", `{"file_path":"/b"}`, now)
	appendEvent(t, db, "d3", "CorruptionDetected", `{"file_path":"/c"}`, now.Add(-48*time.Hour))
	// Outside the 30-day window → excluded.
	appendEvent(t, db, "d4", "CorruptionDetected", `{"file_path":"/d"}`, now.Add(-60*24*time.Hour))

	days, err := repo.CountDetectedByDay(context.Background(), 30)
	if err != nil {
		t.Fatalf("CountDetectedByDay: %v", err)
	}
	total := 0
	for _, d := range days {
		total += d.Count
	}
	if total != 3 {
		t.Errorf("total within 30d = %d, want 3", total)
	}
	// Days are ordered ascending; the most recent day has 2.
	if len(days) == 0 || days[len(days)-1].Count != 2 {
		t.Errorf("most recent day count = %+v, want 2", days)
	}
}
