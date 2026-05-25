package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Well-known setting keys. Defining them as constants here gives a
// single source of truth — handlers reference SettingKeyPasswordHash
// rather than the string literal, so a rename is one edit.
const (
	SettingKeyPasswordHash        = "password_hash"
	SettingKeyAPIKey              = "api_key"
	SettingKeyBasePath            = "base_path"
	SettingKeyOnboardingDismissed = "onboarding_dismissed"
)

// SettingsRepository wraps the settings KV table. Schema is
// (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TIMESTAMP).
//
// Values are opaque strings; the repo doesn't know or care whether
// they're encrypted, JSON, bools, etc. That's caller policy.
type SettingsRepository struct {
	db *sql.DB
}

// NewSettingsRepository returns a repository backed by db.
func NewSettingsRepository(db *sql.DB) *SettingsRepository {
	return &SettingsRepository{db: db}
}

// Get returns the value for key, or ErrNotFound if no row matches.
func (r *SettingsRepository) Get(ctx context.Context, key string) (string, error) {
	var v string
	err := r.db.QueryRowContext(ctx,
		`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("get setting %q: %w", key, err)
	}
	return v, nil
}

// GetOr returns the value for key, or fallback if the key is absent.
// DB errors (other than not-found) are returned to the caller.
func (r *SettingsRepository) GetOr(ctx context.Context, key, fallback string) (string, error) {
	v, err := r.Get(ctx, key)
	if errors.Is(err, ErrNotFound) {
		return fallback, nil
	}
	return v, err
}

// Exists returns true if the row exists.
func (r *SettingsRepository) Exists(ctx context.Context, key string) (bool, error) {
	var n int
	err := r.db.QueryRowContext(ctx,
		`SELECT 1 FROM settings WHERE key = ? LIMIT 1`, key).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("check setting exists %q: %w", key, err)
	}
	return true, nil
}

// Set upserts the (key, value) pair, bumping updated_at to now.
// SQLite's INSERT OR REPLACE wins here: callers don't have to know
// whether the row already exists, and we don't risk the
// SELECT-then-INSERT-or-UPDATE race.
func (r *SettingsRepository) Set(ctx context.Context, key, value string) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`, key, value)
	if err != nil {
		return fmt.Errorf("set setting %q: %w", key, err)
	}
	return nil
}

// SetMany upserts multiple settings in a single transaction — all rows
// commit together, or none does. Used at first-run setup where the
// password and API key must both land or neither should.
func (r *SettingsRepository) SetMany(ctx context.Context, pairs map[string]string) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback() }() // safe to call after Commit

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO settings (key, value, updated_at) VALUES (?, ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = excluded.value, updated_at = excluded.updated_at
	`)
	if err != nil {
		return fmt.Errorf("prepare upsert: %w", err)
	}
	defer stmt.Close()

	for key, value := range pairs {
		if _, err := stmt.ExecContext(ctx, key, value); err != nil {
			return fmt.Errorf("set setting %q: %w", key, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// Delete removes the row for key. Returns nil if no row matched.
func (r *SettingsRepository) Delete(ctx context.Context, key string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	if err != nil {
		return fmt.Errorf("delete setting %q: %w", key, err)
	}
	return nil
}
