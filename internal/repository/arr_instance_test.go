package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	_ "modernc.org/sqlite"
)

const arrInstanceSchema = `
CREATE TABLE arr_instances (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	name           TEXT NOT NULL,
	type           TEXT NOT NULL,
	url            TEXT NOT NULL,
	api_key        TEXT NOT NULL,
	enabled        INTEGER DEFAULT 1,
	webhook_secret TEXT,
	created_at     TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);
CREATE TABLE scan_paths (
	id              INTEGER PRIMARY KEY AUTOINCREMENT,
	local_path      TEXT NOT NULL,
	arr_path        TEXT NOT NULL,
	arr_instance_id INTEGER
);
`

func newArrInstanceTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(arrInstanceSchema); err != nil {
		t.Fatalf("create arr_instances schema: %v", err)
	}
	return db
}

func TestArrInstanceRepository_Count_empty(t *testing.T) {
	t.Parallel()
	repo := NewArrInstanceRepository(newArrInstanceTestDB(t))
	n, err := repo.Count(context.Background())
	if err != nil {
		t.Fatalf("Count: %v", err)
	}
	if n != 0 {
		t.Errorf("Count = %d, want 0", n)
	}
}

func TestArrInstanceRepository_Create_andGetByID(t *testing.T) {
	t.Parallel()
	repo := NewArrInstanceRepository(newArrInstanceTestDB(t))

	id, err := repo.Create(context.Background(), CreateArrInstanceParams{
		Name:                   "Sonarr",
		Type:                   "sonarr",
		URL:                    "http://localhost:8989",
		EncryptedAPIKey:        "enc-key",
		Enabled:                true,
		EncryptedWebhookSecret: "enc-secret",
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == 0 {
		t.Error("Create returned id=0")
	}

	got, err := repo.GetByID(context.Background(), id)
	if err != nil {
		t.Fatalf("GetByID: %v", err)
	}
	if got.Name != "Sonarr" || got.URL != "http://localhost:8989" {
		t.Errorf("unexpected row: %+v", got)
	}
	if !got.EncryptedWebhookSecret.Valid || got.EncryptedWebhookSecret.String != "enc-secret" {
		t.Errorf("webhook_secret not preserved: %+v", got.EncryptedWebhookSecret)
	}
}

func TestArrInstanceRepository_Create_nullWebhookSecret(t *testing.T) {
	t.Parallel()
	// Empty EncryptedWebhookSecret should land as SQL NULL, matching the
	// legacy instance shape that pre-dates per-instance webhook secrets.
	repo := NewArrInstanceRepository(newArrInstanceTestDB(t))

	id, err := repo.Create(context.Background(), CreateArrInstanceParams{
		Name:            "Radarr",
		Type:            "radarr",
		URL:             "http://localhost:7878",
		EncryptedAPIKey: "k",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	got, _ := repo.GetByID(context.Background(), id)
	if got.EncryptedWebhookSecret.Valid {
		t.Errorf("expected webhook_secret NULL, got %q", got.EncryptedWebhookSecret.String)
	}
}

func TestArrInstanceRepository_GetByID_notFound(t *testing.T) {
	t.Parallel()
	repo := NewArrInstanceRepository(newArrInstanceTestDB(t))
	_, err := repo.GetByID(context.Background(), 999)
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("GetByID = %v, want ErrNotFound", err)
	}
}

func TestArrInstanceRepository_FindIDByURL(t *testing.T) {
	t.Parallel()
	repo := NewArrInstanceRepository(newArrInstanceTestDB(t))
	id, _ := repo.Create(context.Background(), CreateArrInstanceParams{
		Name: "S", Type: "sonarr", URL: "http://a", EncryptedAPIKey: "k", Enabled: true,
	})

	got, err := repo.FindIDByURL(context.Background(), "http://a")
	if err != nil || got != id {
		t.Errorf("FindIDByURL = (%d, %v), want (%d, nil)", got, err, id)
	}

	_, err = repo.FindIDByURL(context.Background(), "http://nowhere")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("FindIDByURL missing = %v, want ErrNotFound", err)
	}
}

func TestArrInstanceRepository_ListAll_andListEnabled(t *testing.T) {
	t.Parallel()
	repo := NewArrInstanceRepository(newArrInstanceTestDB(t))
	ctx := context.Background()
	mustCreate := func(p CreateArrInstanceParams) int64 {
		id, err := repo.Create(ctx, p)
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		return id
	}
	mustCreate(CreateArrInstanceParams{Name: "a", Type: "sonarr", URL: "http://a", EncryptedAPIKey: "k", Enabled: true})
	mustCreate(CreateArrInstanceParams{Name: "b", Type: "radarr", URL: "http://b", EncryptedAPIKey: "k", Enabled: false})
	mustCreate(CreateArrInstanceParams{Name: "c", Type: "sonarr", URL: "http://c", EncryptedAPIKey: "k", Enabled: true})

	all, err := repo.ListAll(ctx)
	if err != nil || len(all) != 3 {
		t.Fatalf("ListAll = (%d, %v), want (3, nil)", len(all), err)
	}

	enabled, err := repo.ListEnabled(ctx)
	if err != nil || len(enabled) != 2 {
		t.Fatalf("ListEnabled = (%d, %v), want (2, nil)", len(enabled), err)
	}
	for _, inst := range enabled {
		if !inst.Enabled {
			t.Errorf("ListEnabled returned disabled row: %+v", inst)
		}
	}
}

func TestArrInstanceRepository_ListEnabledWithScanPaths(t *testing.T) {
	t.Parallel()
	db := newArrInstanceTestDB(t)
	repo := NewArrInstanceRepository(db)
	ctx := context.Background()

	enabledID, err := repo.Create(ctx, CreateArrInstanceParams{
		Name: "sonarr", Type: "sonarr", URL: "http://s", EncryptedAPIKey: "enc-k", Enabled: true,
	})
	if err != nil {
		t.Fatalf("Create enabled: %v", err)
	}
	disabledID, err := repo.Create(ctx, CreateArrInstanceParams{
		Name: "radarr", Type: "radarr", URL: "http://r", EncryptedAPIKey: "enc-k", Enabled: false,
	})
	if err != nil {
		t.Fatalf("Create disabled: %v", err)
	}

	// Enabled instance with two scan paths → two joined rows.
	mustExecArr(t, db, `INSERT INTO scan_paths (local_path, arr_path, arr_instance_id) VALUES ('/m', '/data/movies', ?)`, enabledID)
	mustExecArr(t, db, `INSERT INTO scan_paths (local_path, arr_path, arr_instance_id) VALUES ('/t', '/data/tv', ?)`, enabledID)
	// Disabled instance's scan path → excluded (WHERE i.enabled = 1).
	mustExecArr(t, db, `INSERT INTO scan_paths (local_path, arr_path, arr_instance_id) VALUES ('/x', '/data/x', ?)`, disabledID)
	// Orphan scan path (no instance) → excluded by the INNER JOIN.
	mustExecArr(t, db, `INSERT INTO scan_paths (local_path, arr_path, arr_instance_id) VALUES ('/o', '/data/o', 999)`)

	rows, err := repo.ListEnabledWithScanPaths(ctx)
	if err != nil {
		t.Fatalf("ListEnabledWithScanPaths: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2 (both paths of the enabled instance)", len(rows))
	}

	arrPaths := map[string]bool{}
	for _, r := range rows {
		if r.ID != enabledID {
			t.Errorf("row references instance %d, want enabled %d", r.ID, enabledID)
		}
		if r.EncryptedAPIKey != "enc-k" {
			t.Errorf("EncryptedAPIKey = %q, want enc-k", r.EncryptedAPIKey)
		}
		arrPaths[r.ArrPath] = true
	}
	if !arrPaths["/data/movies"] || !arrPaths["/data/tv"] {
		t.Errorf("arr_paths = %v, want both /data/movies and /data/tv", arrPaths)
	}
}

func mustExecArr(t *testing.T, db *sql.DB, query string, args ...interface{}) {
	t.Helper()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func TestArrInstanceRepository_CountByType_andPrefix(t *testing.T) {
	t.Parallel()
	repo := NewArrInstanceRepository(newArrInstanceTestDB(t))
	ctx := context.Background()
	for _, p := range []CreateArrInstanceParams{
		{Name: "s1", Type: "sonarr", URL: "http://s1", EncryptedAPIKey: "k", Enabled: true},
		{Name: "s2", Type: "sonarr", URL: "http://s2", EncryptedAPIKey: "k", Enabled: true},
		{Name: "w1", Type: "whisparr-v2", URL: "http://w1", EncryptedAPIKey: "k", Enabled: true},
		{Name: "w2", Type: "whisparr-v3", URL: "http://w2", EncryptedAPIKey: "k", Enabled: true},
		{Name: "r1", Type: "radarr", URL: "http://r1", EncryptedAPIKey: "k", Enabled: true},
	} {
		if _, err := repo.Create(ctx, p); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	if n, _ := repo.CountByType(ctx, "sonarr"); n != 2 {
		t.Errorf("CountByType sonarr = %d, want 2", n)
	}
	if n, _ := repo.CountByTypePrefix(ctx, "whisparr"); n != 2 {
		t.Errorf("CountByTypePrefix whisparr = %d, want 2", n)
	}
}

func TestArrInstanceRepository_Update(t *testing.T) {
	t.Parallel()
	repo := NewArrInstanceRepository(newArrInstanceTestDB(t))
	ctx := context.Background()
	id, _ := repo.Create(ctx, CreateArrInstanceParams{
		Name: "old", Type: "sonarr", URL: "http://old", EncryptedAPIKey: "k", Enabled: true,
		EncryptedWebhookSecret: "secret-stays",
	})

	if err := repo.Update(ctx, id, UpdateArrInstanceParams{
		Name: "new", Type: "radarr", URL: "http://new", EncryptedAPIKey: "k2", Enabled: false,
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, _ := repo.GetByID(ctx, id)
	if got.Name != "new" || got.URL != "http://new" || got.Enabled {
		t.Errorf("Update did not apply: %+v", got)
	}
	// Webhook secret should be unchanged — UpdateArrInstanceParams intentionally
	// excludes it, so a routine instance edit can't accidentally clear it.
	if got.EncryptedWebhookSecret.String != "secret-stays" {
		t.Errorf("Update clobbered webhook_secret: %q", got.EncryptedWebhookSecret.String)
	}
}

func TestArrInstanceRepository_UpdateWebhookSecret(t *testing.T) {
	t.Parallel()
	repo := NewArrInstanceRepository(newArrInstanceTestDB(t))
	ctx := context.Background()
	id, _ := repo.Create(ctx, CreateArrInstanceParams{
		Name: "x", Type: "sonarr", URL: "http://x", EncryptedAPIKey: "k", Enabled: true,
	})

	if err := repo.UpdateWebhookSecret(ctx, id, "fresh-secret"); err != nil {
		t.Fatalf("UpdateWebhookSecret: %v", err)
	}
	got, _ := repo.GetByID(ctx, id)
	if !got.EncryptedWebhookSecret.Valid || got.EncryptedWebhookSecret.String != "fresh-secret" {
		t.Errorf("UpdateWebhookSecret did not apply: %+v", got.EncryptedWebhookSecret)
	}

	if err := repo.UpdateWebhookSecret(ctx, 9999, "secret"); !errors.Is(err, ErrNotFound) {
		t.Errorf("UpdateWebhookSecret missing id = %v, want ErrNotFound", err)
	}
}

func TestArrInstanceRepository_Delete(t *testing.T) {
	t.Parallel()
	repo := NewArrInstanceRepository(newArrInstanceTestDB(t))
	ctx := context.Background()
	id, _ := repo.Create(ctx, CreateArrInstanceParams{
		Name: "x", Type: "sonarr", URL: "http://x", EncryptedAPIKey: "k", Enabled: true,
	})

	if err := repo.Delete(ctx, id); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := repo.GetByID(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("After Delete: GetByID = %v, want ErrNotFound", err)
	}

	// Deleting an absent id is not an error (idempotent).
	if err := repo.Delete(ctx, 9999); err != nil {
		t.Errorf("Delete missing id = %v, want nil", err)
	}
}
