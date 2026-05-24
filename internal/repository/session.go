package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// ErrSessionExpired is returned when a session row exists but its
// expires_at is in the past. Distinguished from ErrNotFound so callers
// can decide whether to surface "your session expired, log in again"
// versus "this token isn't ours, try a different auth path."
var ErrSessionExpired = errors.New("repository: session expired")

// Session is a value snapshot of a row in the sessions table.
type Session struct {
	Token      string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastUsedAt time.Time
	UserAgent  string
	IPAddress  string
}

// SessionRepository persists per-login browser session tokens.
//
// Sessions are kept separate from the master API key (used by Sonarr /
// Radarr integrations) so a leaked browser token can be revoked without
// rotating the integration key. See migration 005_sessions.sql.
type SessionRepository struct {
	db *sql.DB
}

// NewSessionRepository returns a repository backed by the given DB handle.
func NewSessionRepository(db *sql.DB) *SessionRepository {
	return &SessionRepository{db: db}
}

// Create inserts a new session row valid for ttl from now.
func (r *SessionRepository) Create(ctx context.Context, token string, ttl time.Duration, userAgent, ipAddress string) (Session, error) {
	now := time.Now().UTC()
	expiresAt := now.Add(ttl)

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO sessions (token, created_at, expires_at, last_used_at, user_agent, ip_address)
		VALUES (?, ?, ?, ?, ?, ?)
	`, token, now, expiresAt, now, userAgent, ipAddress)
	if err != nil {
		return Session{}, fmt.Errorf("persist session: %w", err)
	}

	return Session{
		Token:      token,
		CreatedAt:  now,
		ExpiresAt:  expiresAt,
		LastUsedAt: now,
		UserAgent:  userAgent,
		IPAddress:  ipAddress,
	}, nil
}

// Validate returns nil if the token corresponds to a known, unexpired
// session row. Returns ErrNotFound if no row matches, ErrSessionExpired
// if the row exists but is past expires_at, or a wrapped DB error
// otherwise.
//
// Validate does NOT update last_used_at; callers that want to record
// activity should follow with BumpLastUsed (whose error is purely
// diagnostic and safe to log-and-ignore).
func (r *SessionRepository) Validate(ctx context.Context, token string) error {
	var expiresAt time.Time
	err := r.db.QueryRowContext(ctx,
		`SELECT expires_at FROM sessions WHERE token = ?`, token).Scan(&expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("lookup session: %w", err)
	}
	if time.Now().UTC().After(expiresAt) {
		return ErrSessionExpired
	}
	return nil
}

// BumpLastUsed updates last_used_at to now for the given token. Returns
// nil if no row matched — this update is diagnostic ("when did this
// session last act?"), so a missing row is not an error.
func (r *SessionRepository) BumpLastUsed(ctx context.Context, token string) error {
	_, err := r.db.ExecContext(ctx,
		`UPDATE sessions SET last_used_at = ? WHERE token = ?`,
		time.Now().UTC(), token)
	return err
}

// Delete removes the session row for token. Returns nil if no row
// matched — logout is idempotent from the client's perspective.
func (r *SessionRepository) Delete(ctx context.Context, token string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// SweepExpired deletes session rows whose expires_at is in the past and
// returns the number of rows removed. Bounded by the number of expired
// sessions, so a single DELETE without LIMIT is fine.
func (r *SessionRepository) SweepExpired(ctx context.Context) (int64, error) {
	res, err := r.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}
