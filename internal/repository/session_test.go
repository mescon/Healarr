package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

const sessionSchema = `
CREATE TABLE sessions (
	token         TEXT      PRIMARY KEY,
	created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	expires_at    TIMESTAMP NOT NULL,
	last_used_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
	user_agent    TEXT,
	ip_address    TEXT
);
`

func newSessionTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open in-memory sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(sessionSchema); err != nil {
		t.Fatalf("create sessions schema: %v", err)
	}
	return db
}

func TestSessionRepository_Create_persistsRow(t *testing.T) {
	t.Parallel()
	repo := NewSessionRepository(newSessionTestDB(t))

	session, err := repo.Create(context.Background(), "tok-abc", time.Hour, "ua/1.0", "192.0.2.1")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if session.Token != "tok-abc" {
		t.Errorf("Token = %q, want %q", session.Token, "tok-abc")
	}
	if !session.ExpiresAt.After(time.Now().Add(30 * time.Minute)) {
		t.Errorf("ExpiresAt = %v, expected ~1h in future", session.ExpiresAt)
	}
	if session.UserAgent != "ua/1.0" || session.IPAddress != "192.0.2.1" {
		t.Errorf("metadata not stored: ua=%q ip=%q", session.UserAgent, session.IPAddress)
	}
}

func TestSessionRepository_Validate_active(t *testing.T) {
	t.Parallel()
	repo := NewSessionRepository(newSessionTestDB(t))
	if _, err := repo.Create(context.Background(), "tok", time.Hour, "", ""); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if err := repo.Validate(context.Background(), "tok"); err != nil {
		t.Errorf("Validate active token returned %v, want nil", err)
	}
}

func TestSessionRepository_Validate_notFound(t *testing.T) {
	t.Parallel()
	repo := NewSessionRepository(newSessionTestDB(t))

	err := repo.Validate(context.Background(), "no-such-token")
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("Validate unknown token = %v, want ErrNotFound", err)
	}
}

func TestSessionRepository_Validate_expired(t *testing.T) {
	t.Parallel()
	db := newSessionTestDB(t)
	// Insert an already-expired row directly so we don't depend on time travel.
	past := time.Now().UTC().Add(-time.Hour)
	if _, err := db.Exec(
		`INSERT INTO sessions (token, created_at, expires_at, last_used_at, user_agent, ip_address)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"old-tok", past, past, past, "", "",
	); err != nil {
		t.Fatalf("seed expired session: %v", err)
	}

	err := NewSessionRepository(db).Validate(context.Background(), "old-tok")
	if !errors.Is(err, ErrSessionExpired) {
		t.Errorf("Validate expired token = %v, want ErrSessionExpired", err)
	}
}

func TestSessionRepository_BumpLastUsed_updatesRow(t *testing.T) {
	t.Parallel()
	db := newSessionTestDB(t)
	repo := NewSessionRepository(db)

	originalLastUsed := time.Now().UTC().Add(-time.Hour)
	if _, err := db.Exec(
		`INSERT INTO sessions (token, created_at, expires_at, last_used_at, user_agent, ip_address)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		"tok", originalLastUsed, time.Now().UTC().Add(time.Hour), originalLastUsed, "", "",
	); err != nil {
		t.Fatalf("seed session: %v", err)
	}

	if err := repo.BumpLastUsed(context.Background(), "tok"); err != nil {
		t.Fatalf("BumpLastUsed: %v", err)
	}

	var bumpedRaw string
	if err := db.QueryRow(`SELECT last_used_at FROM sessions WHERE token = ?`, "tok").Scan(&bumpedRaw); err != nil {
		t.Fatalf("query last_used_at: %v", err)
	}
	bumped, err := time.Parse(time.RFC3339Nano, bumpedRaw)
	if err != nil {
		// SQLite TIMESTAMP storage varies — fall back to the common forms.
		bumped, err = time.Parse("2006-01-02 15:04:05.999999999-07:00", bumpedRaw)
		if err != nil {
			t.Fatalf("parse last_used_at %q: %v", bumpedRaw, err)
		}
	}
	if !bumped.After(originalLastUsed) {
		t.Errorf("last_used_at = %v not after original %v", bumped, originalLastUsed)
	}
}

func TestSessionRepository_BumpLastUsed_missingTokenIsNotError(t *testing.T) {
	t.Parallel()
	repo := NewSessionRepository(newSessionTestDB(t))
	// Bumping a token that doesn't exist is fine — last_used_at is purely
	// diagnostic, so a no-op update isn't an error condition.
	if err := repo.BumpLastUsed(context.Background(), "nope"); err != nil {
		t.Errorf("BumpLastUsed on missing token: %v", err)
	}
}

func TestSessionRepository_Delete_removesRow(t *testing.T) {
	t.Parallel()
	db := newSessionTestDB(t)
	repo := NewSessionRepository(db)

	if _, err := repo.Create(context.Background(), "tok", time.Hour, "", ""); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := repo.Delete(context.Background(), "tok"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions WHERE token = ?`, "tok").Scan(&count); err != nil {
		t.Fatalf("count: %v", err)
	}
	if count != 0 {
		t.Errorf("count = %d, want 0", count)
	}
}

func TestSessionRepository_SweepExpired(t *testing.T) {
	t.Parallel()
	db := newSessionTestDB(t)
	repo := NewSessionRepository(db)

	now := time.Now().UTC()
	rows := []struct {
		token   string
		expires time.Time
	}{
		{"keep-1", now.Add(time.Hour)},
		{"keep-2", now.Add(2 * time.Hour)},
		{"drop-1", now.Add(-time.Minute)},
		{"drop-2", now.Add(-time.Hour)},
	}
	for _, row := range rows {
		if _, err := db.Exec(
			`INSERT INTO sessions (token, created_at, expires_at, last_used_at, user_agent, ip_address)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			row.token, now, row.expires, now, "", "",
		); err != nil {
			t.Fatalf("seed %s: %v", row.token, err)
		}
	}

	n, err := repo.SweepExpired(context.Background())
	if err != nil {
		t.Fatalf("SweepExpired: %v", err)
	}
	if n != 2 {
		t.Errorf("SweepExpired returned %d, want 2", n)
	}

	var remaining int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&remaining); err != nil {
		t.Fatalf("count: %v", err)
	}
	if remaining != 2 {
		t.Errorf("rows remaining = %d, want 2", remaining)
	}
}
