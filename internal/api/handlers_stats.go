package api

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/logger"
)

func (s *RESTServer) getDashboardStats(c *gin.Context) {
	var stats struct {
		TotalCorruptions              int      `json:"total_corruptions"`
		PendingCorruptions            int      `json:"pending_corruptions"` // Just CorruptionDetected state
		ResolvedCorruptions           int      `json:"resolved_corruptions"`
		OrphanedCorruptions           int      `json:"orphaned_corruptions"`
		IgnoredCorruptions            int      `json:"ignored_corruptions"`
		InProgressCorruptions         int      `json:"in_progress_corruptions"`
		FailedCorruptions             int      `json:"failed_corruptions"`              // *Failed states (not MaxRetriesReached)
		ManualInterventionCorruptions int      `json:"manual_intervention_corruptions"` // ImportBlocked or ManuallyRemoved
		SuccessfulRemediations        int      `json:"successful_remediations"`
		ActiveScans                   int      `json:"active_scans"`
		TotalScans                    int      `json:"total_scans"`
		FilesScannedToday             int      `json:"files_scanned_today"`
		FilesScannedWeek              int      `json:"files_scanned_week"`
		CorruptionsToday              int      `json:"corruptions_today"`
		SuccessRate                   int      `json:"success_rate"`
		Warnings                      []string `json:"warnings,omitempty"` // Query failures (partial results returned)
	}

	var warnings []string

	// Query 1: All corruption stats in a single query (was 5 separate queries)
	var resolved, orphaned, inProgress, manualIntervention, pending, failed, ignored int
	if err := s.db.QueryRow(`
		SELECT
			COUNT(DISTINCT CASE WHEN current_state = 'VerificationSuccess' THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state = 'MaxRetriesReached' THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state IN ('SearchStarted', 'SearchQueued', 'RemediationQueued',
				'DownloadStarted', 'DownloadProgress', 'SearchCompleted', 'DeletionCompleted', 'FileDetected')
				THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state IN ('ImportBlocked', 'ManuallyRemoved') THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state = 'CorruptionDetected' THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state LIKE '%Failed' AND current_state != 'MaxRetriesReached' THEN corruption_id END),
			COUNT(DISTINCT CASE WHEN current_state = 'CorruptionIgnored' THEN corruption_id END)
		FROM corruption_status
	`).Scan(&resolved, &orphaned, &inProgress, &manualIntervention, &pending, &failed, &ignored); err != nil {
		warnings = append(warnings, "failed to query corruption stats")
		logger.Debugf("Failed to query corruption stats: %v", err)
	}

	stats.ResolvedCorruptions = resolved
	stats.OrphanedCorruptions = orphaned
	stats.InProgressCorruptions = inProgress
	stats.ManualInterventionCorruptions = manualIntervention
	stats.PendingCorruptions = pending
	stats.FailedCorruptions = failed
	stats.IgnoredCorruptions = ignored
	stats.SuccessfulRemediations = resolved
	// Total excludes ignored - they're not part of active remediation
	stats.TotalCorruptions = pending + resolved + orphaned + manualIntervention + inProgress + failed

	// Query 2: All scan stats in a single query (was 4 separate queries)
	if err := s.db.QueryRow(`
		SELECT
			COUNT(CASE WHEN status = 'running' THEN 1 END),
			COUNT(*),
			COALESCE(SUM(CASE WHEN substr(started_at, 1, 10) = date('now') THEN files_scanned END), 0),
			COALESCE(SUM(CASE WHEN substr(started_at, 1, 10) >= date('now', '-7 days') THEN files_scanned END), 0)
		FROM scans
	`).Scan(&stats.ActiveScans, &stats.TotalScans, &stats.FilesScannedToday, &stats.FilesScannedWeek); err != nil {
		warnings = append(warnings, "failed to query scan stats")
		logger.Debugf("Failed to query scan stats: %v", err)
	}

	// Query 3: Corruptions detected today (needs events table)
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
		warnings = append(warnings, "failed to query corruptions today")
		logger.Debugf("Failed to query corruptions today: %v", err)
	}

	// Calculate success rate
	totalAttempts := resolved + orphaned
	if totalAttempts > 0 {
		stats.SuccessRate = (resolved * 100) / totalAttempts
	} else if inProgress > 0 {
		stats.SuccessRate = 0
	} else {
		stats.SuccessRate = 100
	}

	stats.Warnings = warnings
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
		respondDatabaseError(c, err)
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
		respondDatabaseError(c, err)
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
