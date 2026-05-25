package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const settingsSchema = `
CREATE TABLE settings (
	key        TEXT PRIMARY KEY,
	value      TEXT NOT NULL,
	updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
`

func newSettingsTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(settingsSchema); err != nil {
		t.Fatalf("create settings schema: %v", err)
	}
	return db
}

func TestSettingsRepository_Set_andGet(t *testing.T) {
	repo := NewSettingsRepository(newSettingsTestDB(t))
	ctx := context.Background()

	if err := repo.Set(ctx, "k1", "v1"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	got, err := repo.Get(ctx, "k1")
	if err != nil || got != "v1" {
		t.Errorf("Get k1 = (%q, %v), want (v1, nil)", got, err)
	}
}

func TestSettingsRepository_Set_overwrites(t *testing.T) {
	// SQLite ON CONFLICT(key) DO UPDATE — Set is upsert, not strict insert.
	repo := NewSettingsRepository(newSettingsTestDB(t))
	ctx := context.Background()

	_ = repo.Set(ctx, "k", "v1")
	_ = repo.Set(ctx, "k", "v2")
	got, _ := repo.Get(ctx, "k")
	if got != "v2" {
		t.Errorf("after second Set, Get = %q, want v2", got)
	}
}

func TestSettingsRepository_Get_notFound(t *testing.T) {
	repo := NewSettingsRepository(newSettingsTestDB(t))
	_, err := repo.Get(context.Background(), "nope")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Get missing = %v, want ErrNotFound", err)
	}
}

func TestSettingsRepository_GetOr_fallback(t *testing.T) {
	repo := NewSettingsRepository(newSettingsTestDB(t))
	got, err := repo.GetOr(context.Background(), "nope", "default-value")
	if err != nil || got != "default-value" {
		t.Errorf("GetOr missing = (%q, %v), want (default-value, nil)", got, err)
	}
}

func TestSettingsRepository_GetOr_existing(t *testing.T) {
	repo := NewSettingsRepository(newSettingsTestDB(t))
	_ = repo.Set(context.Background(), "k", "actual")
	got, _ := repo.GetOr(context.Background(), "k", "default-value")
	if got != "actual" {
		t.Errorf("GetOr existing = %q, want actual (fallback should NOT apply)", got)
	}
}

func TestSettingsRepository_Exists(t *testing.T) {
	repo := NewSettingsRepository(newSettingsTestDB(t))
	ctx := context.Background()

	if ok, _ := repo.Exists(ctx, "nope"); ok {
		t.Error("Exists for missing key returned true")
	}
	_ = repo.Set(ctx, "k", "v")
	if ok, _ := repo.Exists(ctx, "k"); !ok {
		t.Error("Exists for present key returned false")
	}
}

func TestSettingsRepository_Delete(t *testing.T) {
	repo := NewSettingsRepository(newSettingsTestDB(t))
	ctx := context.Background()
	_ = repo.Set(ctx, "k", "v")

	if err := repo.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.Get(ctx, "k"); !errors.Is(err, ErrNotFound) {
		t.Errorf("after Delete, Get = %v, want ErrNotFound", err)
	}
	// Deleting an absent key is a no-op.
	if err := repo.Delete(ctx, "nope"); err != nil {
		t.Errorf("Delete missing key = %v, want nil (idempotent)", err)
	}
}

func TestSettingsRepository_SetMany_atomic(t *testing.T) {
	// SetMany commits all keys or none. Verify the happy path first.
	repo := NewSettingsRepository(newSettingsTestDB(t))
	ctx := context.Background()

	if err := repo.SetMany(ctx, map[string]string{"a": "1", "b": "2"}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if got, _ := repo.Get(ctx, "a"); got != "1" {
		t.Errorf("a = %q, want 1", got)
	}
	if got, _ := repo.Get(ctx, "b"); got != "2" {
		t.Errorf("b = %q, want 2", got)
	}
}

func TestSettingsRepository_SetMany_overwrites(t *testing.T) {
	repo := NewSettingsRepository(newSettingsTestDB(t))
	ctx := context.Background()
	_ = repo.Set(ctx, "a", "old")

	if err := repo.SetMany(ctx, map[string]string{"a": "new", "b": "fresh"}); err != nil {
		t.Fatalf("SetMany: %v", err)
	}
	if got, _ := repo.Get(ctx, "a"); got != "new" {
		t.Errorf("a = %q, want new", got)
	}
}
