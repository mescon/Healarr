package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const notificationSchema = `
CREATE TABLE notifications (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	name             TEXT NOT NULL,
	provider_type    TEXT NOT NULL,
	config           TEXT NOT NULL,
	events           TEXT NOT NULL,
	enabled          BOOLEAN DEFAULT 1,
	throttle_seconds INTEGER DEFAULT 5,
	created_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
	updated_at       TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE notification_log (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	notification_id INTEGER NOT NULL,
	event_type      TEXT NOT NULL,
	message         TEXT NOT NULL,
	status          TEXT NOT NULL,
	error           TEXT,
	sent_at         TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`

func newNotificationTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(notificationSchema); err != nil {
		t.Fatalf("create notification schema: %v", err)
	}
	return db
}

func defaultNotifFields(name string) NotificationFields {
	return NotificationFields{
		Name:            name,
		ProviderType:    "discord",
		EncryptedConfig: "encrypted-blob",
		EventsJSON:      `["CorruptionDetected"]`,
		Enabled:         true,
		ThrottleSeconds: 5,
	}
}

func TestNotificationRepository_Create_andGetByID(t *testing.T) {
	t.Parallel()
	repo := NewNotificationRepository(newNotificationTestDB(t))
	id, err := repo.Create(context.Background(), defaultNotifFields("test"))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	got, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "test" || got.ProviderType != "discord" {
		t.Errorf("unexpected row: %+v", got)
	}
	if got.EncryptedConfig != "encrypted-blob" {
		t.Errorf("EncryptedConfig not preserved: %q", got.EncryptedConfig)
	}
}

func TestNotificationRepository_GetByID_notFound(t *testing.T) {
	t.Parallel()
	repo := NewNotificationRepository(newNotificationTestDB(t))
	if _, err := repo.GetByID(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID = %v, want ErrNotFound", err)
	}
}

func TestNotificationRepository_ListEnabled_andListAll(t *testing.T) {
	t.Parallel()
	repo := NewNotificationRepository(newNotificationTestDB(t))
	ctx := context.Background()

	f1 := defaultNotifFields("a")
	f2 := defaultNotifFields("b")
	f2.Enabled = false
	f3 := defaultNotifFields("c")
	for _, f := range []NotificationFields{f1, f2, f3} {
		if _, err := repo.Create(ctx, f); err != nil {
			t.Fatalf("Create: %v", err)
		}
	}

	all, err := repo.ListAll(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListAll = (%d, %v), want (3, nil)", len(all), err)
	}
	// ListAll is ordered by name; check the order.
	if all[0].Name != "a" || all[1].Name != "b" || all[2].Name != "c" {
		t.Errorf("ListAll order: %v %v %v, want a/b/c", all[0].Name, all[1].Name, all[2].Name)
	}

	enabled, err := repo.ListEnabled(ctx)
	if err != nil || len(enabled) != 2 {
		t.Fatalf("ListEnabled = (%d, %v), want (2, nil)", len(enabled), err)
	}
}

func TestNotificationRepository_Update(t *testing.T) {
	t.Parallel()
	repo := NewNotificationRepository(newNotificationTestDB(t))
	ctx := context.Background()
	id, _ := repo.Create(ctx, defaultNotifFields("old"))

	updated := defaultNotifFields("new")
	updated.ProviderType = "pushover"
	updated.Enabled = false
	if err := repo.Update(ctx, id, updated); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := repo.GetByID(ctx, id)
	if got.Name != "new" || got.ProviderType != "pushover" || got.Enabled {
		t.Errorf("Update did not apply: %+v", got)
	}
}

func TestNotificationRepository_Delete_cascadesLogs(t *testing.T) {
	t.Parallel()
	db := newNotificationTestDB(t)
	repo := NewNotificationRepository(db)
	ctx := context.Background()

	id, _ := repo.Create(ctx, defaultNotifFields("x"))
	_ = repo.AppendLog(ctx, id, "evt", "msg", "sent", "")
	_ = repo.AppendLog(ctx, id, "evt", "msg2", "sent", "")

	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("After Delete: GetByID = %v, want ErrNotFound", err)
	}
	var logCount int
	_ = db.QueryRow(`SELECT COUNT(*) FROM notification_log WHERE notification_id = ?`, id).Scan(&logCount)
	if logCount != 0 {
		t.Errorf("logs not cascaded: %d remaining", logCount)
	}
}

func TestNotificationRepository_AppendLog_andListLog(t *testing.T) {
	t.Parallel()
	repo := NewNotificationRepository(newNotificationTestDB(t))
	ctx := context.Background()
	id, _ := repo.Create(ctx, defaultNotifFields("x"))

	if err := repo.AppendLog(ctx, id, "CorruptionDetected", "msg1", "sent", ""); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}
	if err := repo.AppendLog(ctx, id, "ScanFailed", "msg2", "failed", "boom"); err != nil {
		t.Fatalf("AppendLog: %v", err)
	}

	entries, err := repo.ListLog(ctx, id, 10)
	if err != nil || len(entries) != 2 {
		t.Fatalf("ListLog = (%d, %v), want (2, nil)", len(entries), err)
	}
	// SQLite's CURRENT_TIMESTAMP has 1-second granularity, so two log rows
	// inserted in the same test can tie on sent_at. Just verify both rows
	// came back rather than asserting an order.
	statuses := map[string]bool{entries[0].Status: true, entries[1].Status: true}
	if !statuses["sent"] || !statuses["failed"] {
		t.Errorf("missing status: got %+v", statuses)
	}
}

func TestNotificationRepository_ListLog_unfilteredReturnsAll(t *testing.T) {
	t.Parallel()
	repo := NewNotificationRepository(newNotificationTestDB(t))
	ctx := context.Background()
	a, _ := repo.Create(ctx, defaultNotifFields("a"))
	b, _ := repo.Create(ctx, defaultNotifFields("b"))
	_ = repo.AppendLog(ctx, a, "e", "m", "sent", "")
	_ = repo.AppendLog(ctx, b, "e", "m", "sent", "")

	entries, err := repo.ListLog(ctx, 0, 10)
	if err != nil || len(entries) != 2 {
		t.Fatalf("ListLog(0, ...) = (%d, %v), want (2, nil)", len(entries), err)
	}
}

func TestNotificationRepository_SweepLogsOlderThan(t *testing.T) {
	t.Parallel()
	db := newNotificationTestDB(t)
	repo := NewNotificationRepository(db)
	ctx := context.Background()
	id, _ := repo.Create(ctx, defaultNotifFields("x"))

	// Recent log entry (defaults to now via DEFAULT CURRENT_TIMESTAMP)
	_ = repo.AppendLog(ctx, id, "e", "fresh", "sent", "")
	// Backdated log entry — must seed directly because AppendLog doesn't take a timestamp.
	if _, err := db.Exec(
		`INSERT INTO notification_log (notification_id, event_type, message, status, error, sent_at)
		 VALUES (?, ?, ?, ?, ?, datetime('now', '-30 days'))`,
		id, "e", "old", "sent", "",
	); err != nil {
		t.Fatalf("seed old: %v", err)
	}

	n, err := repo.SweepLogsOlderThan(ctx, 7)
	if err != nil {
		t.Fatalf("SweepLogsOlderThan: %v", err)
	}
	if n != 1 {
		t.Errorf("SweepLogsOlderThan removed %d, want 1", n)
	}

	entries, _ := repo.ListLog(ctx, id, 10)
	if len(entries) != 1 {
		t.Errorf("expected 1 entry remaining, got %d", len(entries))
	}
}

func TestNotificationRepository_LimitLogTotal(t *testing.T) {
	t.Parallel()
	repo := NewNotificationRepository(newNotificationTestDB(t))
	ctx := context.Background()
	id, _ := repo.Create(ctx, defaultNotifFields("x"))

	for i := 0; i < 5; i++ {
		_ = repo.AppendLog(ctx, id, "e", "m", "sent", "")
	}

	if err := repo.LimitLogTotal(ctx, 2); err != nil {
		t.Fatalf("LimitLogTotal: %v", err)
	}

	entries, _ := repo.ListLog(ctx, id, 10)
	if len(entries) != 2 {
		t.Errorf("expected 2 entries after LimitLogTotal, got %d", len(entries))
	}
}
