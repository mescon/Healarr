package api

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/repository"
	"github.com/mescon/Healarr/internal/safego"
)

func (s *RESTServer) triggerScan(c *gin.Context) {
	var req struct {
		PathID int64 `json:"path_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}

	// Look up path
	path, err := s.scanPaths.GetByID(c.Request.Context(), req.PathID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Path not found"})
		return
	}
	localPath := path.LocalPath

	// Check if scan is already in progress
	if s.scanner.IsPathBeingScanned(localPath) {
		c.JSON(http.StatusConflict, gin.H{"error": "Scan already in progress for this path"})
		return
	}

	// Trigger scan in background
	safego.Run("trigger-scan", func() {
		if err := s.scanner.ScanPath(req.PathID, localPath); err != nil {
			logger.Errorf("Scan failed for path %d (%s): %v", req.PathID, localPath, err)
		}
	})

	c.JSON(http.StatusAccepted, gin.H{"message": "Scan started"})
}

func (s *RESTServer) getScans(c *gin.Context) {
	// Parse pagination with config
	cfg := PaginationConfig{
		DefaultLimit:     50,
		MaxLimit:         500,
		DefaultSortBy:    "started_at",
		DefaultSortOrder: "desc",
		AllowedSortBy: map[string]bool{
			"started_at":        true,
			"path":              true,
			"status":            true,
			"files_scanned":     true,
			"corruptions_found": true,
		},
	}
	p := ParsePagination(c, cfg)

	// Get total count
	total, err := s.scans.Count(c.Request.Context())
	if err != nil {
		logger.Errorf("Failed to query scans count: %v", err)
		respondDatabaseError(c, err)
		return
	}

	// Get paginated data with dynamic sorting
	// Map frontend sort keys to DB columns (key = API param, value = DB column)
	allowedSortColumns := map[string]string{
		"started_at":        "started_at",
		"path":              "path",
		"status":            "status",
		"files_scanned":     "files_scanned",
		"corruptions_found": "corruptions_found",
	}
	orderByClause := SafeOrderByClause(p.SortBy, p.SortOrder, allowedSortColumns, "started_at", "desc")
	rows, err := s.scans.ListPaged(c.Request.Context(), orderByClause, p.Limit, p.Offset)
	if err != nil {
		logger.Errorf("Failed to query scans: %v", err)
		respondDatabaseError(c, err)
		return
	}

	scans := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		scans = append(scans, map[string]interface{}{
			"id":                row.ID,
			"path":              row.Path,
			"status":            row.Status,
			"files_scanned":     row.FilesScanned,
			"corruptions_found": row.CorruptionsFound,
			"started_at":        row.StartedAt,
			"completed_at":      row.CompletedAt.String,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data":       scans,
		"pagination": NewPaginationResponse(p, total),
	})
}

func (s *RESTServer) getActiveScans(c *gin.Context) {
	activeScans := s.scanner.GetActiveScans()
	c.JSON(http.StatusOK, activeScans)
}

func (s *RESTServer) cancelScan(c *gin.Context) {
	scanID := c.Param("scan_id")
	if s.scanner.CancelScan(scanID) != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": ErrMsgScanNotFound})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scan cancelled"})
}

func (s *RESTServer) pauseScan(c *gin.Context) {
	scanID := c.Param("scan_id")
	if err := s.scanner.PauseScan(scanID); err != nil {
		respondWithError(c, http.StatusBadRequest, ErrMsgInvalidRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scan paused"})
}

func (s *RESTServer) resumeScan(c *gin.Context) {
	scanID := c.Param("scan_id")
	if err := s.scanner.ResumeScan(scanID); err != nil {
		respondWithError(c, http.StatusBadRequest, ErrMsgInvalidRequest, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scan resumed"})
}

func (s *RESTServer) pauseAllScans(c *gin.Context) {
	activeScans := s.scanner.GetActiveScans()
	paused := 0
	for i := range activeScans {
		if activeScans[i].Status == "running" {
			if s.scanner.PauseScan(activeScans[i].ID) == nil {
				paused++
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scans paused", "paused": paused})
}

func (s *RESTServer) resumeAllScans(c *gin.Context) {
	activeScans := s.scanner.GetActiveScans()
	resumed := 0
	for i := range activeScans {
		if activeScans[i].Status == "paused" {
			if s.scanner.ResumeScan(activeScans[i].ID) == nil {
				resumed++
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scans resumed", "resumed": resumed})
}

func (s *RESTServer) cancelAllScans(c *gin.Context) {
	activeScans := s.scanner.GetActiveScans()
	cancelled := 0
	for i := range activeScans {
		if activeScans[i].Status == "running" || activeScans[i].Status == "paused" {
			if s.scanner.CancelScan(activeScans[i].ID) == nil {
				cancelled++
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scans cancelled", "cancelled": cancelled})
}

func (s *RESTServer) rescanPath(c *gin.Context) {
	scanID := c.Param("scan_id")

	// Get the original scan path from the database
	scanIDInt, parseErr := strconv.ParseInt(scanID, 10, 64)
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid scan ID"})
		return
	}
	scan, err := s.scans.GetByID(c.Request.Context(), scanIDInt)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": ErrMsgScanNotFound})
		return
	}
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	path := scan.Path

	// Don't allow rescanning a currently running scan
	if scan.Status == "running" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Scan is currently running"})
		return
	}

	// Find the scan_path that matches this path (to get the path_id)
	pathID, err := s.scanPaths.FindEnabledIDByLocalPath(c.Request.Context(), path)
	if errors.Is(err, repository.ErrNotFound) {
		// Path might not be in scan_paths (e.g., webhook scan) - scan directly
		safego.Run("rescan-file", func() {
			if scanErr := s.scanner.ScanFile(path); scanErr != nil {
				logger.Errorf("Rescan failed for path %s: %v", path, scanErr)
			}
		})
		c.JSON(http.StatusOK, gin.H{"message": "Rescan started", "path": path, "type": "file"})
		return
	}
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	// Start a new directory scan
	safego.Run("rescan-path", func() {
		if scanErr := s.scanner.ScanPath(pathID, path); scanErr != nil {
			logger.Errorf("Rescan failed for path %s: %v", path, scanErr)
		}
	})

	c.JSON(http.StatusOK, gin.H{"message": "Rescan started", "path": path, "path_id": pathID, "type": "path"})
}

func (s *RESTServer) getScanDetails(c *gin.Context) {
	scanID := c.Param("scan_id")

	var scan struct {
		ID                int    `json:"id"`
		Path              string `json:"path"`
		PathID            int    `json:"path_id"`
		Status            string `json:"status"`
		FilesScanned      int    `json:"files_scanned"`
		CorruptionsFound  int    `json:"corruptions_found"`
		StartedAt         string `json:"started_at"`
		CompletedAt       string `json:"completed_at"`
		HealthyFiles      int    `json:"healthy_files"`
		CorruptFiles      int    `json:"corrupt_files"`
		SkippedFiles      int    `json:"skipped_files"`
		InaccessibleFiles int    `json:"inaccessible_files"`
	}

	scanIDInt, parseErr := strconv.ParseInt(scanID, 10, 64)
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid scan ID"})
		return
	}
	row, err := s.scans.GetByID(c.Request.Context(), scanIDInt)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": ErrMsgScanNotFound})
		return
	}
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	scan.ID = int(row.ID)
	scan.Path = row.Path
	scan.Status = row.Status
	scan.FilesScanned = row.FilesScanned
	scan.CorruptionsFound = row.CorruptionsFound
	scan.StartedAt = row.StartedAt
	scan.CompletedAt = row.CompletedAt.String
	if row.PathID.Valid {
		scan.PathID = int(row.PathID.Int64)
	}

	// Get file counts from scan_files table using single GROUP BY query (performance optimization)
	counts, err := s.scanFiles.CountByStatus(c.Request.Context(), scanIDInt)
	if err != nil {
		logger.Debugf("Failed to query file counts: %v", err)
	} else {
		scan.HealthyFiles = counts["healthy"]
		scan.CorruptFiles = counts["corrupt"]
		scan.SkippedFiles = counts["skipped"]
		scan.InaccessibleFiles = counts["inaccessible"]
	}

	c.JSON(http.StatusOK, scan)
}

func (s *RESTServer) getScanFiles(c *gin.Context) {
	scanID := c.Param("scan_id")
	statusFilter := c.DefaultQuery("status", "all") // 'all', 'healthy', 'corrupt'

	// Parse pagination (no sorting - fixed order by status DESC, file_path ASC)
	p := ParsePagination(c, DefaultPaginationConfig())

	// Verify scan exists
	scanIDInt, parseErr := strconv.ParseInt(scanID, 10, 64)
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid scan ID"})
		return
	}
	exists, err := s.scans.Exists(c.Request.Context(), scanIDInt)
	if err == nil && !exists {
		c.JSON(http.StatusNotFound, gin.H{"error": ErrMsgScanNotFound})
		return
	}

	// Get total count (optionally filtered by status)
	total, err := s.scanFiles.CountForScan(c.Request.Context(), scanIDInt, statusFilter)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	// Get paginated data
	rows, err := s.scanFiles.ListForScan(c.Request.Context(), scanIDInt, statusFilter, p.Limit, p.Offset)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	files := make([]map[string]interface{}, 0, len(rows))
	for _, row := range rows {
		files = append(files, map[string]interface{}{
			"id":              row.ID,
			"file_path":       row.FilePath,
			"status":          row.Status,
			"corruption_type": row.CorruptionType.String,
			"error_details":   row.ErrorDetails.String,
			"file_size":       row.FileSize.Int64,
			"scanned_at":      row.ScannedAt,
		})
	}

	c.JSON(http.StatusOK, gin.H{
		"data":       files,
		"pagination": NewPaginationResponse(p, total),
	})
}

// triggerScanAll triggers scans for all enabled paths
func (s *RESTServer) triggerScanAll(c *gin.Context) {
	paths, err := s.scanPaths.ListEnabled(c.Request.Context())
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	started := 0
	skipped := 0
	for _, p := range paths {
		if s.scanner.IsPathBeingScanned(p.LocalPath) {
			skipped++
			continue
		}

		go func(pid int64, path string) {
			if err := s.scanner.ScanPath(pid, path); err != nil {
				logger.Errorf("Scan failed for path %d (%s): %v", pid, path, err)
			}
		}(p.ID, p.LocalPath)
		started++
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message": fmt.Sprintf("Started %d scan(s), skipped %d already running", started, skipped),
		"started": started,
		"skipped": skipped,
	})
}
