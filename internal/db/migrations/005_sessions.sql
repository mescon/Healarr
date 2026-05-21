-- 005_sessions.sql
--
-- Per-login session tokens. The previous behavior was that POST /api/auth/login
-- returned the master API key (used by Sonarr/Radarr webhook integrations) as
-- the "session token" — meaning every browser session shared the same
-- non-revocable, non-expiring secret with the long-lived integration key.
-- A leaked browser token couldn't be revoked without rotating the integration
-- key and reconfiguring every *arr instance.
--
-- This migration adds a sessions table; the auth middleware accepts either
-- the master API key (for integrations) or a valid, unexpired session token
-- (for browsers).

CREATE TABLE sessions (
    token         TEXT      PRIMARY KEY,
    created_at    TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    expires_at    TIMESTAMP NOT NULL,
    last_used_at  TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    user_agent    TEXT,
    ip_address    TEXT
);

CREATE INDEX idx_sessions_expires_at ON sessions(expires_at);
