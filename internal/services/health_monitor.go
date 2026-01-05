package services

import (
	"context"
	"database/sql"
	"sync"
	"time"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
)

// queryTimeout is the maximum time allowed for background service database queries.
// This prevents any single query from blocking the database connection indefinitely.
const queryTimeout = 10 * time.Second

// HealthMonitorService monitors system health and detects stuck remediations
type HealthMonitorService struct {
	db         *sql.DB
	eventBus   *eventbus.EventBus
	arrClient  integration.ArrClient
	shutdownCh chan struct{}
	wg         sync.WaitGroup

	// Configuration
	checkInterval          time.Duration
	stuckThreshold         time.Duration
	repeatedFailureCount   int
	instanceHealthInterval time.Duration
	arrSyncInterval        time.Duration
}

// NewHealthMonitorService creates a new health monitoring service
func NewHealthMonitorService(db *sql.DB, eb *eventbus.EventBus, arrClient integration.ArrClient, staleThreshold time.Duration) *HealthMonitorService {
	if staleThreshold <= 0 {
		staleThreshold = 24 * time.Hour
	}
	return &HealthMonitorService{
		db:                     db,
		eventBus:               eb,
		arrClient:              arrClient,
		shutdownCh:             make(chan struct{}),
		checkInterval:          15 * time.Minute,
		stuckThreshold:         staleThreshold,
		repeatedFailureCount:   2,
		instanceHealthInterval: 5 * time.Minute,
		arrSyncInterval:        30 * time.Minute,
	}
}

// Start begins health monitoring
func (h *HealthMonitorService) Start() {
	h.wg.Add(1)
	go h.runHealthChecks()

	h.wg.Add(1)
	go h.runInstanceHealthChecks()

	h.wg.Add(1)
	go h.runArrStateSync()

	logger.Infof("Health monitor started (check interval: %s, stuck threshold: %s, arr sync: %s)", h.checkInterval, h.stuckThreshold, h.arrSyncInterval)
}

// Shutdown gracefully stops the health monitor
func (h *HealthMonitorService) Shutdown() {
	logger.Infof("Health monitor: initiating shutdown...")
	close(h.shutdownCh)
	h.wg.Wait()
	logger.Infof("Health monitor: shutdown complete")
}

// runHealthChecks periodically checks for stuck remediations and repeated failures
func (h *HealthMonitorService) runHealthChecks() {
	defer h.wg.Done()

	ticker := time.NewTicker(h.checkInterval)
	defer ticker.Stop()

	// Run initial check after short delay - interruptible for graceful shutdown
	select {
	case <-h.shutdownCh:
		return
	case <-time.After(30 * time.Second):
	}
	h.performHealthChecks()

	for {
		select {
		case <-h.shutdownCh:
			return
		case <-ticker.C:
			h.performHealthChecks()
		}
	}
}

// performHealthChecks runs all health checks
func (h *HealthMonitorService) performHealthChecks() {
	h.checkStuckRemediations()
	h.checkRepeatedFailures()
	h.checkDatabaseHealth()
}

// checkStuckRemediations finds remediations that have been in progress for too long
func (h *HealthMonitorService) checkStuckRemediations() {
	if h.db == nil {
		return
	}

	// Find corruptions where:
	// 1. CorruptionDetected exists
	// 2. No VerificationSuccess or MaxRetriesReached
	// 3. Last event was more than stuckThreshold ago
	query := `
		SELECT
			e1.aggregate_id,
			json_extract(e1.event_data, '$.file_path') as file_path,
			MAX(e2.created_at) as last_event_time
		FROM events e1
		JOIN events e2 ON e1.aggregate_id = e2.aggregate_id
		WHERE e1.event_type = 'CorruptionDetected'
		AND e1.created_at > datetime('now', '-7 days')
		AND NOT EXISTS (
			SELECT 1 FROM events e3
			WHERE e3.aggregate_id = e1.aggregate_id
			AND e3.event_type IN ('VerificationSuccess', 'MaxRetriesReached')
		)
		GROUP BY e1.aggregate_id
		HAVING MAX(e2.created_at) < datetime('now', '-' || ? || ' hours')
	`

	thresholdHours := int(h.stuckThreshold.Hours())
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	rows, err := h.db.QueryContext(ctx, query, thresholdHours)
	if err != nil {
		logger.Debugf("Health monitor: failed to check stuck remediations: %v", err)
		return
	}
	defer rows.Close()

	var stuckCount int
	for rows.Next() {
		var corruptionID, filePath sql.NullString
		var lastEventTime sql.NullString

		if err := rows.Scan(&corruptionID, &filePath, &lastEventTime); err != nil {
			continue
		}

		stuckCount++
		logger.Warnf("STUCK REMEDIATION: %s (file: %s, last event: %s)",
			corruptionID.String, filePath.String, lastEventTime.String)

		// Emit event for UI display and notification system
		if err := h.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID.String,
			AggregateType: "corruption",
			EventType:     domain.StuckRemediation,
			EventData: map[string]interface{}{
				"file_path":       filePath.String,
				"last_event_time": lastEventTime.String,
				"threshold_hours": thresholdHours,
			},
		}); err != nil {
			logger.Errorf("Failed to publish StuckRemediation event: %v", err)
		}
	}

	if stuckCount > 0 {
		logger.Warnf("Health monitor: found %d stuck remediations", stuckCount)
	}
}

// checkRepeatedFailures finds files that have failed verification multiple times
func (h *HealthMonitorService) checkRepeatedFailures() {
	if h.db == nil {
		return
	}

	// Find files where VerificationFailed has occurred multiple times
	// with different download attempts (different SearchCompleted events)
	query := `
		SELECT
			json_extract(e1.event_data, '$.file_path') as file_path,
			COUNT(DISTINCT e1.aggregate_id) as failure_count
		FROM events e1
		WHERE e1.event_type = 'VerificationFailed'
		AND e1.created_at > datetime('now', '-7 days')
		AND json_extract(e1.event_data, '$.file_path') IS NOT NULL
		GROUP BY json_extract(e1.event_data, '$.file_path')
		HAVING COUNT(DISTINCT e1.aggregate_id) >= ?
	`

	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	rows, err := h.db.QueryContext(ctx, query, h.repeatedFailureCount)
	if err != nil {
		logger.Debugf("Health monitor: failed to check repeated failures: %v", err)
		return
	}
	defer rows.Close()

	for rows.Next() {
		var filePath sql.NullString
		var failureCount int

		if err := rows.Scan(&filePath, &failureCount); err != nil {
			continue
		}

		logger.Warnf("REPEATED FAILURE: %s has failed verification %d times with different replacements",
			filePath.String, failureCount)

		// Emit event for notification system
		if err := h.eventBus.Publish(domain.Event{
			AggregateType: "health",
			AggregateID:   "repeated_failure_" + filePath.String,
			EventType:     domain.SystemHealthDegraded,
			EventData: map[string]interface{}{
				"type":          "repeated_failure",
				"file_path":     filePath.String,
				"failure_count": failureCount,
				"message":       "File has failed verification multiple times - manual intervention may be required",
			},
		}); err != nil {
			logger.Errorf("Failed to publish SystemHealthDegraded event for repeated failure: %v", err)
		}
	}
}

// checkDatabaseHealth checks database connection pool health
func (h *HealthMonitorService) checkDatabaseHealth() {
	if h.db == nil {
		return
	}

	stats := h.db.Stats()

	// Log database stats periodically
	logger.Debugf("Database health: open=%d, in_use=%d, idle=%d, wait_count=%d, wait_duration=%s",
		stats.OpenConnections, stats.InUse, stats.Idle, stats.WaitCount, stats.WaitDuration)

	// Warn if connections are exhausted
	if stats.OpenConnections > 0 && stats.InUse == stats.OpenConnections {
		logger.Warnf("Database connection pool exhausted: all %d connections in use", stats.OpenConnections)

		if err := h.eventBus.Publish(domain.Event{
			AggregateType: "health",
			AggregateID:   "database_pool",
			EventType:     domain.SystemHealthDegraded,
			EventData: map[string]interface{}{
				"type":             "database_pool_exhausted",
				"open_connections": stats.OpenConnections,
				"in_use":           stats.InUse,
			},
		}); err != nil {
			logger.Errorf("Failed to publish SystemHealthDegraded event for database pool: %v", err)
		}
	}

	// Warn if wait duration is high (indicates connection contention)
	if stats.WaitDuration > 5*time.Second {
		logger.Warnf("Database connection wait time high: %s (waited %d times)", stats.WaitDuration, stats.WaitCount)
	}
}

// runInstanceHealthChecks periodically verifies *arr instances are reachable
func (h *HealthMonitorService) runInstanceHealthChecks() {
	defer h.wg.Done()

	ticker := time.NewTicker(h.instanceHealthInterval)
	defer ticker.Stop()

	// Run initial check after short delay - interruptible for graceful shutdown
	select {
	case <-h.shutdownCh:
		return
	case <-time.After(60 * time.Second):
	}
	h.checkInstanceHealth()

	for {
		select {
		case <-h.shutdownCh:
			return
		case <-ticker.C:
			h.checkInstanceHealth()
		}
	}
}

// checkInstanceHealth verifies all *arr instances are reachable
func (h *HealthMonitorService) checkInstanceHealth() {
	if h.arrClient == nil {
		return
	}

	instances, err := h.arrClient.GetAllInstances()
	if err != nil {
		logger.Warnf("Health monitor: failed to get *arr instances: %v", err)
		return
	}

	for _, instance := range instances {
		// Check instance health using the system status endpoint
		err := h.arrClient.CheckInstanceHealth(instance.ID)
		if err != nil {
			logger.Warnf("*arr instance unreachable: %s (%s) - %v", instance.Name, instance.URL, err)

			// Emit event for monitoring
			if pubErr := h.eventBus.Publish(domain.Event{
				AggregateType: "health",
				AggregateID:   "instance_" + instance.Name,
				EventType:     domain.InstanceUnhealthy,
				EventData: map[string]interface{}{
					"instance_name": instance.Name,
					"instance_type": instance.Type,
					"instance_url":  instance.URL,
					"error":         err.Error(),
				},
			}); pubErr != nil {
				logger.Errorf("Failed to publish InstanceUnhealthy event for %s: %v", instance.Name, pubErr)
			}
		} else {
			logger.Debugf("*arr instance healthy: %s (%s)", instance.Name, instance.URL)
		}
	}
}

// GetHealthStatus returns current health status for API/UI
func (h *HealthMonitorService) GetHealthStatus() map[string]interface{} {
	status := make(map[string]interface{})

	// Database health
	if h.db != nil {
		stats := h.db.Stats()
		status["database"] = map[string]interface{}{
			"open_connections": stats.OpenConnections,
			"in_use":           stats.InUse,
			"idle":             stats.Idle,
			"wait_count":       stats.WaitCount,
			"wait_duration_ms": stats.WaitDuration.Milliseconds(),
		}
	}

	// Stuck remediations count
	if h.db != nil {
		var stuckCount int
		thresholdHours := int(h.stuckThreshold.Hours())
		ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
		defer cancel()
		if err := h.db.QueryRowContext(ctx, `
			SELECT COUNT(DISTINCT e1.aggregate_id)
			FROM events e1
			JOIN events e2 ON e1.aggregate_id = e2.aggregate_id
			WHERE e1.event_type = 'CorruptionDetected'
			AND e1.created_at > datetime('now', '-7 days')
			AND NOT EXISTS (
				SELECT 1 FROM events e3
				WHERE e3.aggregate_id = e1.aggregate_id
				AND e3.event_type IN ('VerificationSuccess', 'MaxRetriesReached')
			)
			GROUP BY e1.aggregate_id
			HAVING MAX(e2.created_at) < datetime('now', '-' || ? || ' hours')
		`, thresholdHours).Scan(&stuckCount); err != nil {
			logger.Debugf("Failed to query stuck remediations count: %v", err)
		}
		status["stuck_remediations"] = stuckCount
	}

	return status
}

// runArrStateSync periodically syncs in-progress items with arr state
func (h *HealthMonitorService) runArrStateSync() {
	defer h.wg.Done()

	ticker := time.NewTicker(h.arrSyncInterval)
	defer ticker.Stop()

	// Run initial sync after 5 minutes (give recovery service time to run first)
	select {
	case <-h.shutdownCh:
		return
	case <-time.After(5 * time.Minute):
	}
	h.syncWithArrState()

	for {
		select {
		case <-h.shutdownCh:
			return
		case <-ticker.C:
			h.syncWithArrState()
		}
	}
}

// syncWithArrState checks in-progress items against arr state and resolves discrepancies.
// This catches cases where:
// - Download completed but Healarr's monitoring goroutine died (e.g., after restart)
// - User manually fixed the issue in arr UI
// - arr imported the file but Healarr wasn't notified
func (h *HealthMonitorService) syncWithArrState() {
	if h.db == nil || h.arrClient == nil {
		return
	}

	logger.Debugf("Health monitor: syncing in-progress items with arr state")

	// Find items in active states that haven't been updated in the last hour
	// (to avoid interfering with active monitoring)
	query := `
		SELECT
			cs.corruption_id,
			cs.file_path,
			COALESCE(cs.path_id, 0) as path_id,
			(
				SELECT json_extract(e.event_data, '$.media_id')
				FROM events e
				WHERE e.aggregate_id = cs.corruption_id
				AND e.event_type IN ('SearchCompleted', 'SearchStarted')
				ORDER BY e.id DESC
				LIMIT 1
			) as media_id
		FROM corruption_status cs
		WHERE cs.current_state IN ('DownloadProgress', 'SearchCompleted', 'DownloadStarted')
		AND cs.last_updated_at < datetime('now', '-1 hours')
		AND cs.last_updated_at > datetime('now', '-7 days')
	`

	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()

	rows, err := h.db.QueryContext(ctx, query)
	if err != nil {
		logger.Debugf("Health monitor: failed to query in-progress items: %v", err)
		return
	}
	defer rows.Close()

	var synced, exhausted int
	for rows.Next() {
		var corruptionID, filePath string
		var pathID int64
		var mediaIDRaw sql.NullFloat64

		if err := rows.Scan(&corruptionID, &filePath, &pathID, &mediaIDRaw); err != nil {
			continue
		}

		mediaID := int64(0)
		if mediaIDRaw.Valid {
			mediaID = int64(mediaIDRaw.Float64)
		}

		if mediaID == 0 {
			continue // Can't check arr without media ID
		}

		// Check if arr has the file
		hasFile, err := h.checkArrHasFile(filePath, mediaID)
		if err != nil {
			logger.Debugf("Health monitor: failed to check arr for %s: %v", filePath, err)
			continue
		}

		if hasFile {
			// File exists in arr - emit VerificationSuccess
			// Note: We can't do full health verification here (no healthChecker),
			// but if arr says hasFile=true, the import was successful
			logger.Infof("Health monitor: arr reports file exists for %s, marking as resolved", filePath)
			if err := h.eventBus.Publish(domain.Event{
				AggregateID:   corruptionID,
				AggregateType: "corruption",
				EventType:     domain.VerificationSuccess,
				EventData: map[string]interface{}{
					"file_path":       filePath,
					"path_id":         pathID,
					"recovery_action": "arr_sync",
					"note":            "Resolved via periodic arr state sync - arr reports hasFile=true",
				},
			}); err != nil {
				logger.Errorf("Health monitor: failed to publish VerificationSuccess for %s: %v", corruptionID, err)
			} else {
				synced++
			}
		} else {
			// Check if item is in arr queue
			inQueue, _ := h.isInArrQueue(filePath)
			if !inQueue {
				// Not in queue and no file - item is gone
				logger.Infof("Health monitor: item %s not in arr queue and no file, marking as exhausted", filePath)
				if err := h.eventBus.Publish(domain.Event{
					AggregateID:   corruptionID,
					AggregateType: "corruption",
					EventType:     domain.SearchExhausted,
					EventData: map[string]interface{}{
						"file_path":       filePath,
						"path_id":         pathID,
						"reason":          "item_vanished",
						"recovery_action": "arr_sync",
					},
				}); err != nil {
					logger.Errorf("Health monitor: failed to publish SearchExhausted for %s: %v", corruptionID, err)
				} else {
					exhausted++
				}
			}
		}
	}

	if synced > 0 || exhausted > 0 {
		logger.Infof("Health monitor: arr sync complete - resolved=%d, exhausted=%d", synced, exhausted)
	}
}

// checkArrHasFile checks if arr reports the media as having a file
func (h *HealthMonitorService) checkArrHasFile(filePath string, mediaID int64) (bool, error) {
	// Use GetAllFilePaths to check if arr has file(s) for this media
	// Pass nil metadata since we're just checking existence
	allPaths, err := h.arrClient.GetAllFilePaths(mediaID, nil, filePath)
	if err != nil {
		return false, err
	}
	return len(allPaths) > 0, nil
}

// isInArrQueue checks if there's an active download for this file path
func (h *HealthMonitorService) isInArrQueue(filePath string) (bool, error) {
	queueItems, err := h.arrClient.GetQueueForPath(filePath)
	if err != nil {
		return false, err
	}
	return len(queueItems) > 0, nil
}
