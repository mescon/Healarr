-- 006_webhook_secret.sql
--
-- Per-instance webhook secret. Previously the /api/webhook/:instance_id
-- endpoint accepted the master API key (the same secret used by the admin
-- UI) — anyone who could read a Sonarr/Radarr webhook config in a
-- compromised *arr instance got full Healarr admin access. This migration
-- adds a webhook_secret column so each *arr instance can have its own
-- webhook credential, distinct from the admin key.
--
-- Existing rows get NULL; the webhook handler falls back to the master key
-- for backward compatibility until the user generates a per-instance secret
-- via the UI. New instances created after this migration get a secret
-- generated at INSERT time.

ALTER TABLE arr_instances ADD COLUMN webhook_secret TEXT;
