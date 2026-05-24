package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const scheduleSchema = `
CREATE TABLE scan_paths (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	local_path TEXT NOT NULL UNIQUE,
	arr_path TEXT NOT NULL DEFAULT ''
);
CREATE TABLE scan_schedules (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	scan_path_id    INTEGER NOT NULL,
	cron_expression TEXT NOT NULL,
	enabled         BOOLEAN DEFAULT 1,
	created_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

func newScheduleTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(scheduleSchema); err != nil {
		t.Fatalf("create schedule schema: %v", err)
	}
	return db
}

func seedScanPath(t *testing.T, db *sql.DB, localPath string) int64 {
	t.Helper()
	res, err := db.Exec(`INSERT INTO scan_paths (local_path, arr_path) VALUES (?, ?)`, localPath, "/arr"+localPath)
	if err != nil {
		t.Fatalf("seed scan_path: %v", err)
	}
	id, _ := res.LastInsertId()
	return id
}

func TestScheduleRepository_Create_andListEnabled(t *testing.T) {
	db := newScheduleTestDB(t)
	repo := NewScheduleRepository(db)
	pathID := seedScanPath(t, db, "/a")

	id, err := repo.Create(context.Background(), pathID, "0 * * * *", true)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == 0 {
		t.Error("Create returned id=0")
	}

	enabled, err := repo.ListEnabled(context.Background())
	if err != nil || len(enabled) != 1 {
		t.Fatalf("ListEnabled = (%d, %v), want (1, nil)", len(enabled), err)
	}
	if enabled[0].CronExpression != "0 * * * *" || !enabled[0].Enabled {
		t.Errorf("unexpected row: %+v", enabled[0])
	}
}

func TestScheduleRepository_ListEnabled_skipsDisabled(t *testing.T) {
	db := newScheduleTestDB(t)
	repo := NewScheduleRepository(db)
	pathID := seedScanPath(t, db, "/a")
	_, _ = repo.Create(context.Background(), pathID, "0 * * * *", true)
	_, _ = repo.Create(context.Background(), pathID, "0 0 * * *", false)

	enabled, err := repo.ListEnabled(context.Background())
	if err != nil || len(enabled) != 1 {
		t.Fatalf("ListEnabled = (%d, %v), want (1, nil)", len(enabled), err)
	}
}

func TestScheduleRepository_ListWithPaths(t *testing.T) {
	db := newScheduleTestDB(t)
	repo := NewScheduleRepository(db)
	pathID := seedScanPath(t, db, "/movies")
	_, _ = repo.Create(context.Background(), pathID, "0 * * * *", true)

	rows, err := repo.ListWithPaths(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListWithPaths = (%d, %v), want (1, nil)", len(rows), err)
	}
	if rows[0].LocalPath != "/movies" {
		t.Errorf("LocalPath = %q, want /movies", rows[0].LocalPath)
	}
}

func TestScheduleRepository_ListWithPaths_skipsOrphans(t *testing.T) {
	// A schedule referencing a deleted scan_path should be excluded by the
	// INNER JOIN (the export endpoint doesn't show stale rows).
	db := newScheduleTestDB(t)
	repo := NewScheduleRepository(db)
	pathID := seedScanPath(t, db, "/a")
	_, _ = repo.Create(context.Background(), pathID, "0 * * * *", true)
	// Insert a schedule referencing a non-existent scan_path.
	if _, err := db.Exec(
		`INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled) VALUES (?, ?, ?)`,
		999, "0 0 * * *", true,
	); err != nil {
		t.Fatalf("seed orphan: %v", err)
	}

	rows, err := repo.ListWithPaths(context.Background())
	if err != nil || len(rows) != 1 {
		t.Fatalf("ListWithPaths = (%d, %v), want (1, nil) - orphan should be filtered", len(rows), err)
	}
}

func TestScheduleRepository_GetByID_notFound(t *testing.T) {
	repo := NewScheduleRepository(newScheduleTestDB(t))
	if _, err := repo.GetByID(context.Background(), 999); !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID = %v, want ErrNotFound", err)
	}
}

func TestScheduleRepository_FindIDByPathAndCron(t *testing.T) {
	db := newScheduleTestDB(t)
	repo := NewScheduleRepository(db)
	pathID := seedScanPath(t, db, "/a")
	id, _ := repo.Create(context.Background(), pathID, "0 * * * *", true)

	got, err := repo.FindIDByPathAndCron(context.Background(), pathID, "0 * * * *")
	if err != nil || got != id {
		t.Errorf("FindIDByPathAndCron = (%d, %v), want (%d, nil)", got, err, id)
	}

	if _, err := repo.FindIDByPathAndCron(context.Background(), pathID, "5 * * * *"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing = %v, want ErrNotFound", err)
	}
}

func TestScheduleRepository_Update_cronAndEnabled(t *testing.T) {
	db := newScheduleTestDB(t)
	repo := NewScheduleRepository(db)
	pathID := seedScanPath(t, db, "/a")
	id, _ := repo.Create(context.Background(), pathID, "0 * * * *", true)

	if err := repo.Update(context.Background(), id, "5 * * * *", false); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), id)
	if got.CronExpression != "5 * * * *" || got.Enabled {
		t.Errorf("Update did not apply: %+v", got)
	}
}

func TestScheduleRepository_Update_emptyCronOnlyChangesEnabled(t *testing.T) {
	db := newScheduleTestDB(t)
	repo := NewScheduleRepository(db)
	pathID := seedScanPath(t, db, "/a")
	id, _ := repo.Create(context.Background(), pathID, "0 * * * *", true)

	if err := repo.Update(context.Background(), id, "", false); err != nil {
		t.Fatalf("Update: %v", err)
	}
	got, _ := repo.GetByID(context.Background(), id)
	if got.CronExpression != "0 * * * *" {
		t.Errorf("Update with empty cron clobbered cron_expression: %q", got.CronExpression)
	}
	if got.Enabled {
		t.Errorf("Update did not disable: %+v", got)
	}
}

func TestScheduleRepository_Delete(t *testing.T) {
	db := newScheduleTestDB(t)
	repo := NewScheduleRepository(db)
	pathID := seedScanPath(t, db, "/a")
	id, _ := repo.Create(context.Background(), pathID, "0 * * * *", true)

	if err := repo.Delete(context.Background(), id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(context.Background(), id); !errors.Is(err, ErrNotFound) {
		t.Errorf("After Delete: GetByID = %v, want ErrNotFound", err)
	}
}

func TestScheduleRepository_DeleteOrphaned(t *testing.T) {
	db := newScheduleTestDB(t)
	repo := NewScheduleRepository(db)
	pathID := seedScanPath(t, db, "/a")
	_, _ = repo.Create(context.Background(), pathID, "0 * * * *", true)
	// Insert orphans directly (their scan_path_id doesn't exist).
	if _, err := db.Exec(
		`INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled) VALUES (?, ?, ?), (?, ?, ?)`,
		888, "0 0 * * *", true, 999, "0 12 * * *", true,
	); err != nil {
		t.Fatalf("seed orphans: %v", err)
	}

	n, err := repo.DeleteOrphaned(context.Background())
	if err != nil {
		t.Fatalf("DeleteOrphaned: %v", err)
	}
	if n != 2 {
		t.Errorf("DeleteOrphaned removed %d, want 2", n)
	}

	// The valid schedule should still be there.
	enabled, _ := repo.ListEnabled(context.Background())
	if len(enabled) != 1 {
		t.Errorf("after DeleteOrphaned, ListEnabled = %d, want 1", len(enabled))
	}
}
