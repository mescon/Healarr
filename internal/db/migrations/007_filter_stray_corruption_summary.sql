-- 007_filter_stray_corruption_summary.sql
--
-- Cleans up non-corruption rows that leaked into the corruption_summary
-- table via a notifier bug (notifier.publishNotificationEvent hardcoded
-- AggregateType: "corruption" on every NotificationSent/NotificationFailed
-- event, even when the original event being notified about was on a
-- different aggregate, e.g. a SystemHealthDegraded with aggregate_id
-- "database_pool"). The trigger that maintains corruption_summary correctly
-- gates on aggregate_type='corruption', so it dutifully inserted those
-- mis-tagged notification events; with no underlying CorruptionDetected for
-- the aggregate the row's file_path stayed NULL. Loading /corruptions with
-- the "All" filter then tripped a scan error converting NULL to string.
--
-- The publisher is fixed in the same change. This migration removes any
-- already-leaked rows and tightens the corruption_status view with
-- WHERE file_path IS NOT NULL so a future mis-tag (in this codebase or
-- another) cannot crash the API.

DELETE FROM corruption_summary WHERE file_path IS NULL;

DROP VIEW IF EXISTS corruption_status;

CREATE VIEW corruption_status AS
		SELECT
			corruption_id,
			current_state,
			retry_count,
			file_path,
			path_id,
			last_error,
			corruption_type,
			detected_at,
			last_updated_at
		FROM corruption_summary
		WHERE file_path IS NOT NULL;
