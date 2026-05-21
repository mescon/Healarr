package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/logger"
)

// defaultSessionTTL is how long a newly issued session token remains valid.
// Long enough for normal browser use without daily re-logins; short enough
// that a leaked token expires automatically if logout was missed.
const defaultSessionTTL = 24 * time.Hour

// errSessionExpired indicates a session row exists but is past its expires_at.
var errSessionExpired = errors.New("session expired")

// createSession generates a fresh session token, persists it with an expiry,
// and returns the token + expiry time for the caller to send to the client.
//
// The token uses the same 32-byte cryptographic primitive as the master API
// key but is stored in a separate table so it can be expired/revoked
// independently of the integration key used by Sonarr/Radarr webhooks.
func (s *RESTServer) createSession(ctx context.Context, userAgent, ipAddress string) (string, time.Time, error) {
	token, err := auth.GenerateAPIKey()
	if err != nil {
		return "", time.Time{}, fmt.Errorf("generate session token: %w", err)
	}

	now := time.Now().UTC()
	expiresAt := now.Add(defaultSessionTTL)

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO sessions (token, created_at, expires_at, last_used_at, user_agent, ip_address)
		VALUES (?, ?, ?, ?, ?, ?)
	`, token, now, expiresAt, now, userAgent, ipAddress)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("persist session: %w", err)
	}

	return token, expiresAt, nil
}

// validateSession looks up the token in the sessions table. If found and
// unexpired, it bumps last_used_at and returns nil. If the token isn't a
// known session, returns sql.ErrNoRows so the caller can distinguish
// "this is not a session token, try the master key path" from "this is
// an expired/known-bad session token."
func (s *RESTServer) validateSession(ctx context.Context, token string) error {
	var expiresAt time.Time
	err := s.db.QueryRowContext(ctx,
		`SELECT expires_at FROM sessions WHERE token = ?`, token).Scan(&expiresAt)
	if err != nil {
		// sql.ErrNoRows or any other DB error — propagate so the caller
		// can decide whether to try the master-key path.
		return err
	}

	if time.Now().UTC().After(expiresAt) {
		return errSessionExpired
	}

	// Bump last_used_at on a successful validation. We don't fail the
	// request if this update errors — the session is valid, the bump is
	// purely diagnostic ("when did this session last act?").
	if _, updateErr := s.db.ExecContext(ctx,
		`UPDATE sessions SET last_used_at = ? WHERE token = ?`,
		time.Now().UTC(), token); updateErr != nil {
		logger.Debugf("session last_used_at update failed for token: %v", updateErr)
	}

	return nil
}

// deleteSession removes a session row, used by logout.
func (s *RESTServer) deleteSession(ctx context.Context, token string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token = ?`, token)
	return err
}

// handleLogout invalidates the session token from the request. Returns 200
// in both the "session deleted" and "no such session" cases — logout should
// be idempotent from the client's perspective; whether or not the token
// was a known session is not useful information for the caller.
func (s *RESTServer) handleLogout(c *gin.Context) {
	token := s.extractAPIToken(c)
	if token == "" {
		c.JSON(http.StatusOK, gin.H{"message": "Already logged out"})
		return
	}

	if err := s.deleteSession(c.Request.Context(), token); err != nil {
		// DB failure during logout is logged but doesn't change the
		// response — the token might still be valid if we say so.
		logger.Errorf("Logout: failed to delete session: %v", err)
	}
	c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
}

// expiredSessionsSweep deletes session rows whose expires_at is in the past.
// Called periodically from the existing maintenance goroutine; bounded by
// the number of expired sessions, so a single DELETE is fine without LIMIT.
//
//nolint:unused // exposed for the maintenance scheduler to wire in
func (s *RESTServer) expiredSessionsSweep(ctx context.Context) (int64, error) {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ?`, time.Now().UTC())
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return n, nil
}

// (no errInvalidSession alias yet — callers in this package use sql.ErrNoRows
// directly via errors.Is.)
