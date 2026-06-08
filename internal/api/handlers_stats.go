package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/repository"
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
	counts, err := s.corruptions.StateCounts(c.Request.Context())
	if err != nil {
		warnings = append(warnings, "failed to query corruption stats")
		logger.Debugf("Failed to query corruption stats: %v", err)
	}
	resolved, inProgress := counts.Resolved, counts.InProgress
	orphaned := counts.Orphaned

	stats.ResolvedCorruptions = counts.Resolved
	stats.OrphanedCorruptions = counts.Orphaned
	stats.InProgressCorruptions = counts.InProgress
	stats.ManualInterventionCorruptions = counts.ManualIntervention
	stats.PendingCorruptions = counts.Pending
	stats.FailedCorruptions = counts.Failed
	stats.IgnoredCorruptions = counts.Ignored
	stats.SuccessfulRemediations = counts.Resolved
	// Total excludes ignored - they're not part of active remediation
	stats.TotalCorruptions = counts.Pending + counts.Resolved + counts.Orphaned +
		counts.ManualIntervention + counts.InProgress + counts.Failed

	// All scan stats in a single query
	if scanStats, err := s.scans.GetScanStats(c.Request.Context()); err != nil {
		warnings = append(warnings, "failed to query scan stats")
		logger.Debugf("Failed to query scan stats: %v", err)
	} else {
		stats.ActiveScans = scanStats.ActiveScans
		stats.TotalScans = scanStats.TotalScans
		stats.FilesScannedToday = scanStats.FilesScannedToday
		stats.FilesScannedWeek = scanStats.FilesScannedWeek
	}

	// Query 3: Corruptions detected today (needs events table)
	if today, err := s.corruptions.CountDetectedToday(c.Request.Context()); err != nil {
		warnings = append(warnings, "failed to query corruptions today")
		logger.Debugf("Failed to query corruptions today: %v", err)
	} else {
		stats.CorruptionsToday = today
	}

	// Query 4: Last completed scan info
	last, err := s.scans.GetLastScan(c.Request.Context())
	if err != nil && !errors.Is(err, repository.ErrNotFound) {
		warnings = append(warnings, "failed to query last scan")
		logger.Debugf("Failed to query last scan: %v", err)
	}
	if last.ID.Valid {
		id := int(last.ID.Int64)
		stats.LastScanID = &id
	}
	if last.CompletedAt.Valid {
		stats.LastScanTime = &last.CompletedAt.String
	}
	if last.Path.Valid {
		stats.LastScanPath = &last.Path.String
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
	days, err := s.corruptions.CountDetectedByDay(c.Request.Context(), 30)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	stats := make([]map[string]interface{}, 0, len(days))
	for _, d := range days {
		stats = append(stats, map[string]interface{}{
			"date":  d.Date,
			"count": d.Count,
		})
	}
	c.JSON(http.StatusOK, stats)
}

func (s *RESTServer) getStatsTypes(c *gin.Context) {
	// Group by corruption type
	types, err := s.corruptions.CountByCorruptionType(c.Request.Context())
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	stats := make([]map[string]interface{}, 0, len(types))
	for _, t := range types {
		typeName := t.Type
		if typeName == "" {
			typeName = "Unknown"
		}
		stats = append(stats, map[string]interface{}{
			"type":  typeName,
			"count": t.Count,
		})
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
		paths[i].LastScanID, paths[i].LastScanTime = s.loadPathLastScan(c.Request.Context(), int64(paths[i].PathID))
		paths[i].ActiveCorruptions, paths[i].TotalCorruptions, paths[i].ResolvedCount = s.loadPathCorruptionStats(c.Request.Context(), int64(paths[i].PathID))
		paths[i].Status = determinePathHealthStatus(paths[i])
	}

	c.JSON(http.StatusOK, paths)
}

// loadPathLastScan queries the last completed scan for a given path, returning nullable ID and time.
func (s *RESTServer) loadPathLastScan(ctx context.Context, pathID int64) (*int, *string) {
	last, err := s.scans.GetLastScanByPathID(ctx, pathID)
	if err != nil {
		return nil, nil
	}

	var idPtr *int
	if last.ID.Valid {
		id := int(last.ID.Int64)
		idPtr = &id
	}
	var timePtr *string
	if last.CompletedAt.Valid {
		timePtr = &last.CompletedAt.String
	}
	return idPtr, timePtr
}

// loadPathCorruptionStats queries active, total, and resolved corruption counts for a given path.
func (s *RESTServer) loadPathCorruptionStats(ctx context.Context, pathID int64) (active, total, resolved int) {
	active, total, resolved, err := s.corruptions.PathCorruptionStats(ctx, pathID)
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
