package services

import (
	"database/sql"
	"sync"
	"time"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
)

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
}

// NewHealthMonitorService creates a new health monitoring service
func NewHealthMonitorService(db *sql.DB, eb *eventbus.EventBus, arrClient integration.ArrClient) *HealthMonitorService {
	return &HealthMonitorService{
		db:                     db,
		eventBus:               eb,
		arrClient:              arrClient,
		shutdownCh:             make(chan struct{}),
		checkInterval:          15 * time.Minute,
		stuckThreshold:         24 * time.Hour,
		repeatedFailureCount:   2,
		instanceHealthInterval: 5 * time.Minute,
	}
}

// Start begins health monitoring
func (h *HealthMonitorService) Start() {
	h.wg.Add(1)
	go h.runHealthChecks()

	h.wg.Add(1)
	go h.runInstanceHealthChecks()

	logger.Infof("Health monitor started (check interval: %s, stuck threshold: %s)", h.checkInterval, h.stuckThreshold)
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

	// Run initial check after short delay
	time.Sleep(30 * time.Second)
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
	rows, err := h.db.Query(query, thresholdHours)
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

	rows, err := h.db.Query(query, h.repeatedFailureCount)
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

	// Run initial check after short delay
	time.Sleep(60 * time.Second)
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
		// Try to make a simple API call to check health
		// We'll use the queue endpoint as a simple health check
		_, err := h.arrClient.GetQueueForPath("/" + instance.Type) // Dummy path to trigger instance selection
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
		if err := h.db.QueryRow(`
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
