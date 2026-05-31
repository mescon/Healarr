package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// newTestSettingsRepo opens an in-memory SQLite, creates the settings
// table, and returns a populated repository.
func newTestSettingsRepo(t *testing.T) *SettingsRepository {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	return NewSettingsRepository(db)
}

func TestTunables_Precedence_EnvBeatsDBBeatsDefault(t *testing.T) {
	ctx := context.Background()
	repo := newTestSettingsRepo(t)
	tn := NewTunables(repo)

	// Default: env unset, DB empty -> 0 (the historical default for thorough_duration)
	t.Setenv("HEALARR_HEALTH_CHECK_THOROUGH_DURATION", "")
	got := tn.ThoroughDuration(ctx)
	if got.Value != 0 || got.Source != SourceDefault {
		t.Errorf("default: got %v / %s, want 0 / default", got.Value, got.Source)
	}

	// DB only -> DB wins, source=db, parsed as integer seconds
	if err := repo.Set(ctx, SettingKeyThoroughDuration, "60"); err != nil {
		t.Fatalf("seed db: %v", err)
	}
	got = tn.ThoroughDuration(ctx)
	if got.Value != 60*time.Second || got.Source != SourceDB {
		t.Errorf("db-only: got %v / %s, want 60s / db", got.Value, got.Source)
	}

	// Env + DB -> env wins; "10m" form is accepted for env
	t.Setenv("HEALARR_HEALTH_CHECK_THOROUGH_DURATION", "10m")
	got = tn.ThoroughDuration(ctx)
	if got.Value != 10*time.Minute || got.Source != SourceEnv {
		t.Errorf("env+db: got %v / %s, want 10m / env", got.Value, got.Source)
	}
}

func TestTunables_HwAccel_DefaultLowercase(t *testing.T) {
	ctx := context.Background()
	repo := newTestSettingsRepo(t)
	tn := NewTunables(repo)

	t.Setenv("HEALARR_HEALTH_CHECK_HWACCEL", "")
	got := tn.HwAccel(ctx)
	if got.Value != "auto" || got.Source != SourceDefault {
		t.Errorf("default: got %q / %s, want auto / default", got.Value, got.Source)
	}

	// DB value is uppercased; getter must lowercase so callers can compare against constants.
	if err := repo.Set(ctx, SettingKeyHwAccel, "CUDA"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	got = tn.HwAccel(ctx)
	if got.Value != "cuda" {
		t.Errorf("db lowercase: got %q, want cuda", got.Value)
	}
}

func TestTunables_CorruptDBValueFallsThroughToDefault(t *testing.T) {
	ctx := context.Background()
	repo := newTestSettingsRepo(t)
	tn := NewTunables(repo)

	// Corrupt int in DB for thorough_duration. ResolveAll should not crash;
	// the catalog-driven resolver falls back to default.
	if err := repo.Set(ctx, SettingKeyThoroughDuration, "not-a-number"); err != nil {
		t.Fatalf("seed: %v", err)
	}
	all, err := tn.ResolveAll(ctx)
	if err != nil {
		t.Fatalf("resolve all: %v", err)
	}

	found := false
	for _, v := range all {
		if v.Key != SettingKeyThoroughDuration {
			continue
		}
		found = true
		if v.Source != SourceDefault {
			t.Errorf("corrupt-db: source = %s, want default", v.Source)
		}
		if v.Value != int64(0) {
			t.Errorf("corrupt-db: value = %v, want 0", v.Value)
		}
	}
	if !found {
		t.Fatal("thorough_duration not in catalog response")
	}
}

func TestTunables_ResolveAll_ContainsEveryCatalogEntry(t *testing.T) {
	ctx := context.Background()
	repo := newTestSettingsRepo(t)
	tn := NewTunables(repo)

	// Clear any env vars that would otherwise affect the test.
	for _, m := range Catalog {
		t.Setenv(m.EnvVar, "")
	}

	all, err := tn.ResolveAll(ctx)
	if err != nil {
		t.Fatalf("resolve all: %v", err)
	}
	if len(all) != len(Catalog) {
		t.Fatalf("got %d entries, want %d", len(all), len(Catalog))
	}
	for i, m := range Catalog {
		if all[i].Key != m.Key {
			t.Errorf("entry %d: key = %q, want %q", i, all[i].Key, m.Key)
		}
		if all[i].Source != SourceDefault {
			t.Errorf("entry %d (%s): source = %s, want default", i, m.Key, all[i].Source)
		}
	}
}

func TestTunables_ValidateAndStore_RejectsEnvLocked(t *testing.T) {
	ctx := context.Background()
	repo := newTestSettingsRepo(t)
	tn := NewTunables(repo)

	// Lock the field via env
	t.Setenv("HEALARR_HEALTH_CHECK_HWACCEL", "cuda")

	err := tn.ValidateAndStore(ctx, map[string]any{
		SettingKeyHwAccel: "off",
	})
	if err == nil {
		t.Fatal("expected env-lock error, got nil")
	}
}

func TestTunables_ValidateAndStore_RejectsUnknownKey(t *testing.T) {
	ctx := context.Background()
	repo := newTestSettingsRepo(t)
	tn := NewTunables(repo)

	err := tn.ValidateAndStore(ctx, map[string]any{
		"not.a.real.key": 42,
	})
	if err == nil {
		t.Fatal("expected unknown-key error, got nil")
	}
}

func TestTunables_ValidateAndStore_EnforcesBounds(t *testing.T) {
	ctx := context.Background()
	repo := newTestSettingsRepo(t)
	tn := NewTunables(repo)

	for _, m := range Catalog {
		t.Setenv(m.EnvVar, "")
	}

	tests := []struct {
		name    string
		updates map[string]any
		wantErr bool
	}{
		{
			name:    "max_retries 0 is allowed (min)",
			updates: map[string]any{SettingKeyDefaultMaxRetries: float64(0)},
			wantErr: false,
		},
		{
			name:    "max_retries 100 is allowed (max)",
			updates: map[string]any{SettingKeyDefaultMaxRetries: float64(100)},
			wantErr: false,
		},
		{
			name:    "max_retries 101 rejected (over max)",
			updates: map[string]any{SettingKeyDefaultMaxRetries: float64(101)},
			wantErr: true,
		},
		{
			name:    "max_retries -1 rejected (under min)",
			updates: map[string]any{SettingKeyDefaultMaxRetries: float64(-1)},
			wantErr: true,
		},
		{
			name:    "hwaccel valid enum value",
			updates: map[string]any{SettingKeyHwAccel: "vaapi"},
			wantErr: false,
		},
		{
			name:    "hwaccel garbage rejected",
			updates: map[string]any{SettingKeyHwAccel: "not-a-real-accel"},
			wantErr: true,
		},
		{
			name:    "duration accepts string form",
			updates: map[string]any{SettingKeyThoroughDuration: "60s"},
			wantErr: false,
		},
		{
			name:    "duration accepts int seconds",
			updates: map[string]any{SettingKeyThoroughDuration: float64(120)},
			wantErr: false,
		},
		{
			name:    "rate limit float in range",
			updates: map[string]any{SettingKeyArrRateLimitRPS: float64(7.5)},
			wantErr: false,
		},
		{
			name:    "rate limit too high",
			updates: map[string]any{SettingKeyArrRateLimitRPS: float64(999)},
			wantErr: true,
		},
		{
			name:    "bool round-trips",
			updates: map[string]any{SettingKeyDryRunMode: true},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tn.ValidateAndStore(ctx, tt.updates)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateAndStore err = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestTunables_ValidateAndStore_RoundTrip(t *testing.T) {
	ctx := context.Background()
	repo := newTestSettingsRepo(t)
	tn := NewTunables(repo)
	for _, m := range Catalog {
		t.Setenv(m.EnvVar, "")
	}

	if err := tn.ValidateAndStore(ctx, map[string]any{
		SettingKeyThoroughDuration: "90s",
		SettingKeyHwAccel:          "off",
	}); err != nil {
		t.Fatalf("store: %v", err)
	}

	td := tn.ThoroughDuration(ctx)
	if td.Value != 90*time.Second || td.Source != SourceDB {
		t.Errorf("thorough_duration: got %v / %s, want 90s / db", td.Value, td.Source)
	}
	hw := tn.HwAccel(ctx)
	if hw.Value != "off" || hw.Source != SourceDB {
		t.Errorf("hwaccel: got %q / %s, want off / db", hw.Value, hw.Source)
	}
}
