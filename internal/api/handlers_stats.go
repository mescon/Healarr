package api

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/logger"
)

func (s *RESTServer) getDashboardStats(c *gin.Context) {
	var stats struct {
		TotalCorruptions              int `json:"total_corruptions"`
		ActiveCorruptions             int `json:"active_corruptions"`  // Deprecated: use pending_corruptions instead
		PendingCorruptions            int `json:"pending_corruptions"` // Just CorruptionDetected state
		ResolvedCorruptions           int `json:"resolved_corruptions"`
		OrphanedCorruptions           int `json:"orphaned_corruptions"`
		IgnoredCorruptions            int `json:"ignored_corruptions"`
		InProgressCorruptions         int `json:"in_progress_corruptions"`
		FailedCorruptions             int `json:"failed_corruptions"`              // *Failed states (not MaxRetriesReached)
		ManualInterventionCorruptions int `json:"manual_intervention_corruptions"` // ImportBlocked or ManuallyRemoved
		SuccessfulRemediations        int `json:"successful_remediations"`
		ActiveScans                   int `json:"active_scans"`
		TotalScans                    int `json:"total_scans"`
		FilesScannedToday             int `json:"files_scanned_today"`
		FilesScannedWeek              int `json:"files_scanned_week"`
		CorruptionsToday              int `json:"corruptions_today"`
		SuccessRate                   int `json:"success_rate"`
	}

	// Get corruption stats from view
	var active, resolved, orphaned, inProgress, manualIntervention int
	// Query dashboard stats; on error, values default to zero
	_ = s.db.QueryRow("SELECT active_corruptions, resolved_corruptions, orphaned_corruptions, in_progress, COALESCE(manual_intervention_required, 0) FROM dashboard_stats").Scan(
		&active, &resolved, &orphaned, &inProgress, &manualIntervention,
	)

	stats.ActiveCorruptions = active
	stats.ResolvedCorruptions = resolved
	stats.OrphanedCorruptions = orphaned
	stats.InProgressCorruptions = inProgress
	stats.ManualInterventionCorruptions = manualIntervention
	// Total corruptions excludes ignored - they're not part of active remediation
	stats.TotalCorruptions = active + resolved + orphaned + manualIntervention
	stats.SuccessfulRemediations = resolved

	// Get pending count (just CorruptionDetected state - waiting to be processed)
	// Excluded from CorruptionIgnored check as it's a separate state
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM corruption_status
		WHERE current_state = 'CorruptionDetected'
	`).Scan(&stats.PendingCorruptions); err != nil {
		logger.Debugf("Failed to query pending corruptions: %v", err)
	}

	// Get failed count (*Failed states, not MaxRetriesReached, not ignored)
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM corruption_status
		WHERE current_state LIKE '%Failed'
		AND current_state != 'MaxRetriesReached'
		AND current_state != 'CorruptionIgnored'
	`).Scan(&stats.FailedCorruptions); err != nil {
		logger.Debugf("Failed to query failed corruptions: %v", err)
	}

	// Get ignored count (separate stat for reference)
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM corruption_status
		WHERE current_state = 'CorruptionIgnored'
	`).Scan(&stats.IgnoredCorruptions); err != nil {
		logger.Debugf("Failed to query ignored corruptions: %v", err)
	}

	// Get active scans from scans table
	if err := s.db.QueryRow("SELECT COUNT(*) FROM scans WHERE status = 'running'").Scan(&stats.ActiveScans); err != nil {
		logger.Debugf("Failed to query active scans: %v", err)
	}

	// Get total scans
	if err := s.db.QueryRow("SELECT COUNT(*) FROM scans").Scan(&stats.TotalScans); err != nil {
		logger.Debugf("Failed to query total scans: %v", err)
	}

	// Get files scanned today
	// Use substr to extract YYYY-MM-DD from timestamp (works with Go's time format)
	if err := s.db.QueryRow(`
		SELECT COALESCE(SUM(files_scanned), 0) FROM scans
		WHERE substr(started_at, 1, 10) = date('now')
	`).Scan(&stats.FilesScannedToday); err != nil {
		logger.Debugf("Failed to query files scanned today: %v", err)
	}

	// Get files scanned this week
	if err := s.db.QueryRow(`
		SELECT COALESCE(SUM(files_scanned), 0) FROM scans
		WHERE substr(started_at, 1, 10) >= date('now', '-7 days')
	`).Scan(&stats.FilesScannedWeek); err != nil {
		logger.Debugf("Failed to query files scanned this week: %v", err)
	}

	// Get corruptions detected today (excluding ones that are now ignored)
	// Use substr to extract YYYY-MM-DD from Go's time.Time format
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM events e
		WHERE e.event_type = 'CorruptionDetected'
		AND substr(e.created_at, 1, 10) = date('now')
		AND NOT EXISTS (
			SELECT 1 FROM corruption_status cs
			WHERE cs.corruption_id = e.aggregate_id
			AND cs.current_state = 'CorruptionIgnored'
		)
	`).Scan(&stats.CorruptionsToday); err != nil {
		logger.Debugf("Failed to query corruptions today: %v", err)
	}

	// Calculate success rate (excluding ignored from totals)
	// Success rate = resolved / (resolved + orphaned) for completed remediations
	// If nothing has completed yet (all still in progress), show N/A as 0
	totalAttempts := resolved + orphaned
	if totalAttempts > 0 {
		stats.SuccessRate = (resolved * 100) / totalAttempts
	} else if inProgress > 0 {
		// Active remediations but none completed yet - can't calculate rate
		stats.SuccessRate = 0
	} else {
		// No remediations at all - show 100% (no failures)
		stats.SuccessRate = 100
	}

	c.JSON(http.StatusOK, stats)
}

func (s *RESTServer) getStatsHistory(c *gin.Context) {
	// Group by date for the last 30 days
	// Use substr to extract YYYY-MM-DD from Go's time.Time format
	rows, err := s.db.Query(`
		SELECT substr(created_at, 1, 10) as date, COUNT(*) as count
		FROM events
		WHERE event_type = 'CorruptionDetected'
		AND substr(created_at, 1, 10) >= date('now', '-30 days')
		GROUP BY substr(created_at, 1, 10)
		ORDER BY date ASC
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	stats := make([]map[string]interface{}, 0)
	for rows.Next() {
		var date string
		var count int
		if err := rows.Scan(&date, &count); err != nil {
			continue
		}
		stats = append(stats, map[string]interface{}{
			"date":  date,
			"count": count,
		})
	}
	c.JSON(http.StatusOK, stats)
}

func (s *RESTServer) getStatsTypes(c *gin.Context) {
	// Group by corruption type
	rows, err := s.db.Query(`
		SELECT json_extract(event_data, '$.corruption_type') as type, COUNT(*) as count
		FROM events
		WHERE event_type = 'CorruptionDetected'
		GROUP BY type
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	stats := make([]map[string]interface{}, 0)
	for rows.Next() {
		var corruptionType sql.NullString
		var count int
		if err := rows.Scan(&corruptionType, &count); err != nil {
			continue
		}

		typeName := "Unknown"
		if corruptionType.Valid {
			typeName = corruptionType.String
		}

		stats = append(stats, map[string]interface{}{
			"type":  typeName,
			"count": count,
		})
	}
	c.JSON(http.StatusOK, stats)
}
