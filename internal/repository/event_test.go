package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/mescon/Healarr/internal/domain"
)

const eventSchema = `
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
`

func newEventTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(eventSchema); err != nil {
		t.Fatalf("create events schema: %v", err)
	}
	return db
}

func TestEventRepository_Append(t *testing.T) {
	db := newEventTestDB(t)
	repo := NewEventRepository(db)

	id, err := repo.Append(domain.Event{
		AggregateType: "corruption",
		AggregateID:   "c1",
		EventType:     domain.CorruptionDetected,
		EventData:     map[string]interface{}{"file_path": "/a.mkv", "path_id": float64(3)},
		EventVersion:  1,
		CreatedAt:     time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Append: %v", err)
	}
	if id == 0 {
		t.Error("Append returned id=0")
	}

	var aggID, eventType, dataJSON string
	if err := db.QueryRow(`SELECT aggregate_id, event_type, event_data FROM events WHERE id = ?`, id).
		Scan(&aggID, &eventType, &dataJSON); err != nil {
		t.Fatalf("query: %v", err)
	}
	if aggID != "c1" || eventType != string(domain.CorruptionDetected) {
		t.Errorf("persisted row: agg=%q type=%q", aggID, eventType)
	}
	if dataJSON != `{"file_path":"/a.mkv","path_id":3}` {
		t.Errorf("event_data = %q", dataJSON)
	}
}

func TestEventRepository_ListUnprocessed_endOfStreamOnly(t *testing.T) {
	db := newEventTestDB(t)
	repo := NewEventRepository(db)
	ctx := context.Background()

	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	appendEv := func(aggID string, et domain.EventType, at time.Time) {
		if _, err := db.Exec(
			`INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data, created_at) VALUES ('corruption', ?, ?, '{}', ?)`,
			aggID, et, at.UTC().Format("2006-01-02 15:04:05.000"),
		); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	// c1: detected then resolved → NOT unprocessed (has a later event).
	appendEv("c1", domain.CorruptionDetected, base)
	appendEv("c1", domain.VerificationSuccess, base.Add(time.Minute))
	// c2: detected only → unprocessed.
	appendEv("c2", domain.CorruptionDetected, base.Add(time.Hour))

	events, skipped, err := repo.ListUnprocessed(ctx, domain.CorruptionDetected)
	if err != nil {
		t.Fatalf("ListUnprocessed: %v", err)
	}
	if skipped != 0 {
		t.Errorf("skipped = %d, want 0", skipped)
	}
	if len(events) != 1 || events[0].AggregateID != "c2" {
		t.Fatalf("got %+v, want only c2", events)
	}
	// Verify the round-tripped event data unmarshaled.
	if events[0].EventType != domain.CorruptionDetected {
		t.Errorf("event type = %q, want CorruptionDetected", events[0].EventType)
	}
}

func TestEventRepository_ListUnprocessed_skipsCorruptData(t *testing.T) {
	db := newEventTestDB(t)
	repo := NewEventRepository(db)

	// One valid, one with non-JSON event_data → the corrupt row is skipped,
	// not fatal (replay must survive a bad persisted row).
	if _, err := db.Exec(
		`INSERT INTO events (aggregate_type, aggregate_id, event_type, event_data) VALUES
		 ('corruption', 'good', ?, '{"file_path":"/a"}'),
		 ('corruption', 'bad',  ?, 'not-json')`,
		domain.CorruptionDetected, domain.CorruptionDetected,
	); err != nil {
		t.Fatalf("seed: %v", err)
	}

	events, skipped, err := repo.ListUnprocessed(context.Background(), domain.CorruptionDetected)
	if err != nil {
		t.Fatalf("ListUnprocessed should not error on corrupt row: %v", err)
	}
	if skipped != 1 {
		t.Errorf("skipped = %d, want 1", skipped)
	}
	if len(events) != 1 || events[0].AggregateID != "good" {
		t.Errorf("got %+v, want only the good row", events)
	}
}
