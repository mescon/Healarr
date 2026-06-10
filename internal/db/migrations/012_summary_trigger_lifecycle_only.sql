-- 012_summary_trigger_lifecycle_only.sql
--
-- Stops notification bookkeeping from corrupting the corruption state
-- machine, and unifies the retry-count definition (audit finding 13).
--
-- The migration-004 trigger updated corruption_summary for EVERY event on a
-- corruption aggregate: current_state = NEW.event_type and retry_count =
-- COUNT(event_type LIKE '%Failed'). The notifier publishes
-- NotificationSent / NotificationFailed under the SOURCE event's aggregate
-- (so the corruption timeline shows them), which meant:
--   a) a "remediation complete" notification set current_state =
--      'NotificationSent', knocking the item out of the resolved filter and
--      counting it active forever;
--   b) a broken notification provider appended NotificationFailed per
--      corruption event, each matching LIKE '%Failed' - three of those
--      exhausted the retry budget so the first real failure jumped straight
--      to MaxRetriesReached with zero actual remediation attempts.
--
-- The trigger now ignores notification bookkeeping entirely, and
-- retry_count counts the explicit retry-relevant set - including
-- DownloadTimeout, which the monitor's cap check already counted while the
-- summary's counter did not (the two cap checks disagreed in both
-- directions). monitor.getRetryCount is aligned to the same list in Go.
--
-- Step 2 repairs rows already clobbered by notification events.

-- 1. Replace the trigger
DROP TRIGGER IF EXISTS trg_update_corruption_summary;

CREATE TRIGGER trg_update_corruption_summary
AFTER INSERT ON events
WHEN NEW.aggregate_type = 'corruption'
  AND NEW.event_type NOT IN ('NotificationSent', 'NotificationFailed')
BEGIN
    INSERT OR REPLACE INTO corruption_summary (
        corruption_id,
        current_state,
        retry_count,
        file_path,
        path_id,
        last_error,
        corruption_type,
        detected_at,
        last_updated_at
    )
    SELECT
        NEW.aggregate_id,
        NEW.event_type,
        (SELECT COUNT(*) FROM events WHERE aggregate_id = NEW.aggregate_id
           AND event_type IN ('DeletionFailed', 'SearchFailed', 'VerificationFailed', 'DownloadFailed', 'DownloadTimeout')),
        COALESCE(
            CASE WHEN NEW.event_type = 'CorruptionDetected' THEN json_extract(NEW.event_data, '$.file_path') ELSE NULL END,
            (SELECT file_path FROM corruption_summary WHERE corruption_id = NEW.aggregate_id),
            (SELECT json_extract(event_data, '$.file_path') FROM events
             WHERE aggregate_id = NEW.aggregate_id AND event_type = 'CorruptionDetected' LIMIT 1)
        ),
        COALESCE(
            CASE WHEN NEW.event_type = 'CorruptionDetected' THEN json_extract(NEW.event_data, '$.path_id') ELSE NULL END,
            (SELECT path_id FROM corruption_summary WHERE corruption_id = NEW.aggregate_id),
            (SELECT json_extract(event_data, '$.path_id') FROM events
             WHERE aggregate_id = NEW.aggregate_id AND event_type = 'CorruptionDetected' LIMIT 1)
        ),
        json_extract(NEW.event_data, '$.error'),
        COALESCE(
            CASE WHEN NEW.event_type = 'CorruptionDetected' THEN json_extract(NEW.event_data, '$.corruption_type') ELSE NULL END,
            (SELECT corruption_type FROM corruption_summary WHERE corruption_id = NEW.aggregate_id),
            (SELECT json_extract(event_data, '$.corruption_type') FROM events
             WHERE aggregate_id = NEW.aggregate_id AND event_type = 'CorruptionDetected' LIMIT 1)
        ),
        COALESCE(
            (SELECT detected_at FROM corruption_summary WHERE corruption_id = NEW.aggregate_id),
            NEW.created_at
        ),
        NEW.created_at;
END;

-- 2. Repair rows already clobbered: recompute current_state from the latest
--    LIFECYCLE event and retry_count from the explicit failure set. Rows
--    whose aggregates only ever had notification events keep their state
--    (the subquery guard below skips them).
UPDATE corruption_summary
SET current_state = (
        SELECT e.event_type FROM events e
        WHERE e.aggregate_id = corruption_summary.corruption_id
          AND e.aggregate_type = 'corruption'
          AND e.event_type NOT IN ('NotificationSent', 'NotificationFailed')
        ORDER BY e.created_at DESC, e.id DESC
        LIMIT 1
    ),
    retry_count = (
        SELECT COUNT(*) FROM events e
        WHERE e.aggregate_id = corruption_summary.corruption_id
          AND e.event_type IN ('DeletionFailed', 'SearchFailed', 'VerificationFailed', 'DownloadFailed', 'DownloadTimeout')
    ),
    last_updated_at = (
        SELECT MAX(e.created_at) FROM events e
        WHERE e.aggregate_id = corruption_summary.corruption_id
          AND e.aggregate_type = 'corruption'
          AND e.event_type NOT IN ('NotificationSent', 'NotificationFailed')
    )
WHERE EXISTS (
    SELECT 1 FROM events e
    WHERE e.aggregate_id = corruption_summary.corruption_id
      AND e.aggregate_type = 'corruption'
      AND e.event_type NOT IN ('NotificationSent', 'NotificationFailed')
);
