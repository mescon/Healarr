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
		LastScanTime                  *string  `json:"last_scan_time,omitempty"` // Timestamp of most recent completed scan
		LastScanPath                  *string  `json:"last_scan_path,omitempty"` // Path that was scanned
		LastScanID                    *int     `json:"last_scan_id,omitempty"`   // ID for linking to scan details
		Warnings                      []string `json:"warnings,omitempty"`       // Query failures (partial results returned)
	}

	var warnings []string

	// All corruption stats in a single query
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

	// All scan stats in a single query
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

	// Query 4: Last completed scan info
	var lastScanID sql.NullInt64
	var lastScanTime, lastScanPath sql.NullString
	if err := s.db.QueryRow(`
		SELECT id, completed_at, path
		FROM scans
		WHERE status = 'completed' AND completed_at IS NOT NULL
		ORDER BY completed_at DESC
		LIMIT 1
	`).Scan(&lastScanID, &lastScanTime, &lastScanPath); err != nil && err != sql.ErrNoRows {
		warnings = append(warnings, "failed to query last scan")
		logger.Debugf("Failed to query last scan: %v", err)
	}
	if lastScanID.Valid {
		id := int(lastScanID.Int64)
		stats.LastScanID = &id
	}
	if lastScanTime.Valid {
		stats.LastScanTime = &lastScanTime.String
	}
	if lastScanPath.Valid {
		stats.LastScanPath = &lastScanPath.String
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
		if rows.Scan(&date, &count) != nil {
			continue
		}
		stats = append(stats, map[string]interface{}{
			"date":  date,
			"count": count,
		})
	}
	if err := rows.Err(); err != nil {
		respondDatabaseError(c, err)
		return
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
		if rows.Scan(&corruptionType, &count) != nil {
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
	if err := rows.Err(); err != nil {
		respondDatabaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, stats)
}

// PathHealth represents the health status of a configured scan path.
type PathHealth struct {
	PathID            int     `json:"path_id"`
	LocalPath         string  `json:"local_path"`
	Enabled           bool    `json:"enabled"`
	LastScanTime      *string `json:"last_scan_time,omitempty"`
	LastScanID        *int    `json:"last_scan_id,omitempty"`
	ActiveCorruptions int     `json:"active_corruptions"` // Pending + in-progress + failed + manual
	TotalCorruptions  int     `json:"total_corruptions"`  // All-time for this path
	ResolvedCount     int     `json:"resolved_count"`
	Status            string  `json:"status"` // "healthy", "warning", "critical", "unknown"
}

// getPathHealth returns health status for each configured scan path.
// GET /api/stats/path-health
func (s *RESTServer) getPathHealth(c *gin.Context) {
	// Get all configured scan paths
	scanPaths, err := s.scanPaths.ListOrderedByLocalPath(c.Request.Context())
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	paths := make([]PathHealth, 0, len(scanPaths))
	for _, sp := range scanPaths {
		paths = append(paths, PathHealth{
			PathID:    int(sp.ID),
			LocalPath: sp.LocalPath,
			Enabled:   sp.Enabled,
		})
	}

	if len(paths) == 0 {
		c.JSON(http.StatusOK, []PathHealth{})
		return
	}

	// For each path, get last scan and corruption stats
	for i := range paths {
		paths[i].LastScanID, paths[i].LastScanTime = s.loadPathLastScan(paths[i].PathID)
		paths[i].ActiveCorruptions, paths[i].TotalCorruptions, paths[i].ResolvedCount = s.loadPathCorruptionStats(paths[i].PathID)
		paths[i].Status = determinePathHealthStatus(paths[i])
	}

	c.JSON(http.StatusOK, paths)
}

// loadPathLastScan queries the last completed scan for a given path, returning nullable ID and time.
func (s *RESTServer) loadPathLastScan(pathID int) (*int, *string) {
	var lastScanID sql.NullInt64
	var lastScanTime sql.NullString
	err := s.db.QueryRow(`
		SELECT id, completed_at
		FROM scans
		WHERE path_id = ? AND status = 'completed' AND completed_at IS NOT NULL
		ORDER BY completed_at DESC
		LIMIT 1
	`, pathID).Scan(&lastScanID, &lastScanTime)
	if err != nil {
		return nil, nil
	}

	var idPtr *int
	if lastScanID.Valid {
		id := int(lastScanID.Int64)
		idPtr = &id
	}
	var timePtr *string
	if lastScanTime.Valid {
		timePtr = &lastScanTime.String
	}
	return idPtr, timePtr
}

// loadPathCorruptionStats queries active, total, and resolved corruption counts for a given path.
func (s *RESTServer) loadPathCorruptionStats(pathID int) (active, total, resolved int) {
	err := s.db.QueryRow(`
		SELECT
			COUNT(DISTINCT CASE WHEN current_state NOT IN ('VerificationSuccess', 'MaxRetriesReached', 'CorruptionIgnored') THEN corruption_id END),
			COUNT(DISTINCT corruption_id),
			COUNT(DISTINCT CASE WHEN current_state = 'VerificationSuccess' THEN corruption_id END)
		FROM corruption_status
		WHERE path_id = ?
	`, pathID).Scan(&active, &total, &resolved)
	if err != nil {
		return 0, 0, 0
	}
	return active, total, resolved
}

// determinePathHealthStatus calculates the health status based on corruption counts and scan recency.
func determinePathHealthStatus(p PathHealth) string {
	if !p.Enabled {
		return "disabled"
	}
	if p.LastScanTime == nil {
		return "unknown" // Never scanned
	}
	if p.ActiveCorruptions > 5 {
		return "critical"
	}
	if p.ActiveCorruptions > 0 {
		return "warning"
	}
	return "healthy"
}
