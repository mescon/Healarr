package services

import (
	"database/sql"
	"errors"
	"math"
	"sync"
	"time"

	"github.com/mescon/Healarr/internal/clock"
	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/logger"
)

// MonitorService handles failure events and schedules retries with exponential backoff.
type MonitorService struct {
	eventBus      *eventbus.EventBus
	db            *sql.DB
	clk           clock.Clock
	wg            sync.WaitGroup         // Tracks in-flight timer callbacks
	pendingTimers map[string]clock.Timer // key: corruptionID - cleaned up when timers fire
	timerMu       sync.Mutex             // Protects pendingTimers map
	stopChan      chan struct{}          // Signals shutdown
	stopped       bool                   // Prevents scheduling after Stop()
}

// NewMonitorService creates a new MonitorService.
// An optional Clock can be provided for testing; if none is provided, RealClock is used.
func NewMonitorService(eb *eventbus.EventBus, db *sql.DB, clocks ...clock.Clock) *MonitorService {
	var c clock.Clock = clock.NewRealClock()
	if len(clocks) > 0 && clocks[0] != nil {
		c = clocks[0]
	}
	return &MonitorService{
		eventBus:      eb,
		db:            db,
		clk:           c,
		pendingTimers: make(map[string]clock.Timer),
		stopChan:      make(chan struct{}),
	}
}

// Start subscribes to failure events and begins monitoring for retries.
func (m *MonitorService) Start() {
	// Failure events that trigger retry with exponential backoff
	m.eventBus.Subscribe(domain.DeletionFailed, m.handleFailure)
	m.eventBus.Subscribe(domain.SearchFailed, m.handleFailure)
	m.eventBus.Subscribe(domain.VerificationFailed, m.handleFailure)
	m.eventBus.Subscribe(domain.DownloadTimeout, m.handleFailure)
	m.eventBus.Subscribe(domain.DownloadFailed, m.handleFailure) // BUG FIX: was orphaned event

	// Events requiring manual intervention - emit NeedsAttention for UI visibility
	m.eventBus.Subscribe(domain.ImportBlocked, m.handleNeedsAttention)
	m.eventBus.Subscribe(domain.SearchExhausted, m.handleNeedsAttention)
	// Terminal states from VerifierService - user-initiated actions that ended the flow
	m.eventBus.Subscribe(domain.DownloadIgnored, m.handleNeedsAttention)
	m.eventBus.Subscribe(domain.ManuallyRemoved, m.handleNeedsAttention)
}

// Stop gracefully shuts down the MonitorService.
// It cancels pending retry timers and waits for in-flight callbacks to complete.
func (m *MonitorService) Stop() {
	m.timerMu.Lock()
	if m.stopped {
		m.timerMu.Unlock()
		return
	}
	m.stopped = true

	// Cancel all pending timers and clear the map
	canceled := 0
	for id, timer := range m.pendingTimers {
		if timer.Stop() {
			canceled++
			// Timer was stopped before firing, decrement WaitGroup
			m.wg.Done()
		}
		delete(m.pendingTimers, id)
	}
	m.timerMu.Unlock()

	// Signal any in-flight callbacks to abort
	close(m.stopChan)

	// Wait for any callbacks that already started
	m.wg.Wait()

	if canceled > 0 {
		logger.Infof("MonitorService stopped: canceled %d pending retry timers", canceled)
	}
}

func (m *MonitorService) handleFailure(event domain.Event) {
	corruptionID := event.AggregateID

	// Get retry count and max limit
	retryCount, maxRetries, err := m.getRetryCount(corruptionID)
	if err != nil {
		logger.Errorf("Failed to get retry count for %s: %v", corruptionID, err)
		return
	}

	if retryCount >= maxRetries {
		if err := m.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.MaxRetriesReached,
		}); err != nil {
			logger.Errorf("Failed to publish MaxRetriesReached event for %s: %v", corruptionID, err)
		}
		return
	}

	// Fetch file_path and path_id from the original CorruptionDetected event
	// so the Remediator has the context it needs.
	// Use retry logic for transient database errors (e.g., database temporarily unavailable).
	filePath, pathID, err := m.getCorruptionContextWithRetry(corruptionID, 3)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			logger.Warnf("Corruption %s not found in database, skipping retry", corruptionID)
		} else {
			// Still failed after retries - publish system health event for visibility
			logger.Errorf("Database error getting context for %s after retries: %v", corruptionID, err)
			if pubErr := m.eventBus.Publish(domain.Event{
				AggregateID:   corruptionID,
				AggregateType: "corruption",
				EventType:     domain.SystemHealthDegraded,
				EventData: map[string]interface{}{
					"reason": "database_error_during_retry_scheduling",
					"error":  err.Error(),
				},
			}); pubErr != nil {
				logger.Errorf("Failed to publish SystemHealthDegraded event: %v", pubErr)
			}
		}
		return
	}

	// Exponential backoff: 15m, 30m, 60m
	delay := time.Duration(math.Pow(2, float64(retryCount))) * 15 * time.Minute

	// Check if we're shutting down before scheduling
	m.timerMu.Lock()
	if m.stopped {
		m.timerMu.Unlock()
		logger.Debugf("MonitorService stopped, not scheduling retry for %s", corruptionID)
		return
	}

	// Cancel any existing timer for this corruption (prevents duplicate retries)
	if existingTimer, exists := m.pendingTimers[corruptionID]; exists {
		if existingTimer.Stop() {
			m.wg.Done() // Timer was stopped before firing
		}
		delete(m.pendingTimers, corruptionID)
	}

	// Track the WaitGroup before scheduling to ensure we wait for callbacks
	m.wg.Add(1)
	timer := m.clk.AfterFunc(delay, func() {
		defer m.wg.Done()

		// Remove timer from map after firing (fixes memory leak)
		m.timerMu.Lock()
		delete(m.pendingTimers, corruptionID)
		m.timerMu.Unlock()

		// Check if we should proceed (not shut down)
		select {
		case <-m.stopChan:
			logger.Debugf("MonitorService stopped, skipping retry publish for %s", corruptionID)
			return
		default:
		}

		if err := m.eventBus.Publish(domain.Event{
			AggregateID:   corruptionID,
			AggregateType: "corruption",
			EventType:     domain.RetryScheduled,
			EventData: map[string]interface{}{
				"file_path":      filePath,
				"path_id":        pathID,
				"auto_remediate": true, // Retries should always auto-remediate
			},
		}); err != nil {
			logger.Errorf("Failed to publish RetryScheduled event for %s: %v", corruptionID, err)
		}
	})
	m.pendingTimers[corruptionID] = timer
	m.timerMu.Unlock()
}

// getCorruptionContext retrieves the file_path and path_id from the original CorruptionDetected event
func (m *MonitorService) getCorruptionContext(corruptionID string) (string, int64, error) {
	var filePath sql.NullString
	var pathID sql.NullInt64

	err := m.db.QueryRow(`
		SELECT
			json_extract(event_data, '$.file_path'),
			json_extract(event_data, '$.path_id')
		FROM events
		WHERE aggregate_id = ? AND event_type = 'CorruptionDetected'
		LIMIT 1
	`, corruptionID).Scan(&filePath, &pathID)

	if err != nil {
		return "", 0, err
	}

	if !filePath.Valid || filePath.String == "" {
		return "", 0, sql.ErrNoRows
	}

	return filePath.String, pathID.Int64, nil
}

// getCorruptionContextWithRetry attempts to get corruption context with retries for transient DB errors.
// Returns early on sql.ErrNoRows (not retryable). Uses exponential backoff: 100ms, 200ms, 400ms.
func (m *MonitorService) getCorruptionContextWithRetry(corruptionID string, maxRetries int) (string, int64, error) {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		filePath, pathID, err := m.getCorruptionContext(corruptionID)
		if err == nil {
			return filePath, pathID, nil
		}
		if errors.Is(err, sql.ErrNoRows) {
			// Not found is not retryable
			return "", 0, err
		}
		lastErr = err
		if attempt < maxRetries-1 {
			// Exponential backoff: 100ms, 200ms, 400ms
			time.Sleep(time.Duration(1<<uint(attempt)) * 100 * time.Millisecond)
		}
	}
	return "", 0, lastErr
}

func (m *MonitorService) getRetryCount(corruptionID string) (int, int, error) {
	var count int
	var maxRetries sql.NullInt64
	defaultMaxRetries := config.Get().DefaultMaxRetries

	// Get retry count and max_retries from view and scan_paths
	// We use a LEFT JOIN to handle cases where path_id is missing or scan path is deleted
	query := `
		SELECT 
			cs.retry_count,
			sp.max_retries
		FROM corruption_status cs
		LEFT JOIN scan_paths sp ON sp.id = cs.path_id
		WHERE cs.corruption_id = ?
	`

	err := m.db.QueryRow(query, corruptionID).Scan(&count, &maxRetries)
	if err != nil {
		if err == sql.ErrNoRows {
			return 0, defaultMaxRetries, nil // Use configured default if not found
		}
		return 0, defaultMaxRetries, err
	}

	limit := defaultMaxRetries
	if maxRetries.Valid {
		limit = int(maxRetries.Int64)
	}
	// Ensure at least 1 retry if configured to 0 (unless 0 means no retries? Let's assume 0 is valid "no retry")
	// But usually 0 means "disable", but here it's max_retries.
	// If user sets 0, they want 0 retries.

	return count, limit, nil
}

// handleNeedsAttention handles events that require manual intervention.
// These are terminal states where automatic retry is not appropriate.
// The notifier handles user notifications; this handler logs for observability.
func (m *MonitorService) handleNeedsAttention(event domain.Event) {
	corruptionID := event.AggregateID

	// Extract relevant info for logging
	filePath, _ := event.GetString("file_path")
	if filePath == "" {
		// Try to get from corruption context
		filePath, _, _ = m.getCorruptionContext(corruptionID)
	}

	switch event.EventType {
	case domain.ImportBlocked:
		errorMsg, _ := event.GetString("error_message")
		logger.Warnf("Manual intervention required for %s: import blocked - %s (file: %s)",
			corruptionID, errorMsg, filePath)

	case domain.SearchExhausted:
		reason, _ := event.GetString("reason")
		logger.Warnf("Manual intervention required for %s: search exhausted - %s (file: %s)",
			corruptionID, reason, filePath)

	case domain.DownloadIgnored:
		reason, _ := event.GetString("reason")
		logger.Infof("Item closed by user for %s: download ignored - %s (file: %s)",
			corruptionID, reason, filePath)

	case domain.ManuallyRemoved:
		reason, _ := event.GetString("reason")
		logger.Infof("Item closed by user for %s: manually removed - %s (file: %s)",
			corruptionID, reason, filePath)

	default:
		logger.Warnf("Manual intervention required for %s: %s (file: %s)",
			corruptionID, event.EventType, filePath)
	}

	// Note: No automatic retry is scheduled for these events.
	// User must manually retry via the UI or API after resolving the issue.
}
