package api

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/logger"
)

func (s *RESTServer) triggerScan(c *gin.Context) {
	var req struct {
		PathID int64 `json:"path_id"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Look up path
	var localPath string
	err := s.db.QueryRow("SELECT local_path FROM scan_paths WHERE id = ?", req.PathID).Scan(&localPath)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Path not found"})
		return
	}

	// Check if scan is already in progress
	if s.scanner.IsPathBeingScanned(localPath) {
		c.JSON(http.StatusConflict, gin.H{"error": "Scan already in progress for this path"})
		return
	}

	// Trigger scan in background
	go func() {
		if err := s.scanner.ScanPath(req.PathID, localPath); err != nil {
			logger.Errorf("Scan failed for path %d (%s): %v", req.PathID, localPath, err)
		}
	}()

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
	var total int
	err := s.db.QueryRow("SELECT COUNT(*) FROM scans").Scan(&total)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
	query := fmt.Sprintf("SELECT id, path, status, files_scanned, corruptions_found, started_at, completed_at FROM scans %s LIMIT ? OFFSET ?", orderByClause)
	rows, err := s.db.Query(query, p.Limit, p.Offset)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	scans := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var path, status, startedAt string
		var completedAt sql.NullString
		var filesScanned, corruptionsFound int

		if err := rows.Scan(&id, &path, &status, &filesScanned, &corruptionsFound, &startedAt, &completedAt); err != nil {
			continue
		}

		scans = append(scans, map[string]interface{}{
			"id":                id,
			"path":              path,
			"status":            status,
			"files_scanned":     filesScanned,
			"corruptions_found": corruptionsFound,
			"started_at":        startedAt,
			"completed_at":      completedAt.String,
		})
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading scan results"})
		logger.Errorf("Error iterating scans: %v", err)
		return
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
	if err := s.scanner.CancelScan(scanID); err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": ErrMsgScanNotFound})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scan cancelled"})
}

func (s *RESTServer) pauseScan(c *gin.Context) {
	scanID := c.Param("scan_id")
	if err := s.scanner.PauseScan(scanID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scan paused"})
}

func (s *RESTServer) resumeScan(c *gin.Context) {
	scanID := c.Param("scan_id")
	if err := s.scanner.ResumeScan(scanID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scan resumed"})
}

func (s *RESTServer) pauseAllScans(c *gin.Context) {
	activeScans := s.scanner.GetActiveScans()
	paused := 0
	for _, scan := range activeScans {
		if scan.Status == "running" {
			if err := s.scanner.PauseScan(scan.ID); err == nil {
				paused++
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scans paused", "paused": paused})
}

func (s *RESTServer) resumeAllScans(c *gin.Context) {
	activeScans := s.scanner.GetActiveScans()
	resumed := 0
	for _, scan := range activeScans {
		if scan.Status == "paused" {
			if err := s.scanner.ResumeScan(scan.ID); err == nil {
				resumed++
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scans resumed", "resumed": resumed})
}

func (s *RESTServer) cancelAllScans(c *gin.Context) {
	activeScans := s.scanner.GetActiveScans()
	cancelled := 0
	for _, scan := range activeScans {
		if scan.Status == "running" || scan.Status == "paused" {
			if err := s.scanner.CancelScan(scan.ID); err == nil {
				cancelled++
			}
		}
	}
	c.JSON(http.StatusOK, gin.H{"message": "Scans cancelled", "cancelled": cancelled})
}

func (s *RESTServer) rescanPath(c *gin.Context) {
	scanID := c.Param("scan_id")

	// Get the original scan path from the database
	var path string
	var status string
	err := s.db.QueryRow("SELECT path, status FROM scans WHERE id = ?", scanID).Scan(&path, &status)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": ErrMsgScanNotFound})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Don't allow rescanning a currently running scan
	if status == "running" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Scan is currently running"})
		return
	}

	// Find the scan_path that matches this path (to get the path_id)
	var pathID int64
	err = s.db.QueryRow("SELECT id FROM scan_paths WHERE local_path = ? AND enabled = 1", path).Scan(&pathID)
	if err == sql.ErrNoRows {
		// Path might not be in scan_paths (e.g., webhook scan) - scan directly
		go func() {
			if scanErr := s.scanner.ScanFile(path); scanErr != nil {
				logger.Errorf("Rescan failed for path %s: %v", path, scanErr)
			}
		}()
		c.JSON(http.StatusOK, gin.H{"message": "Rescan started", "path": path, "type": "file"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Start a new directory scan
	go func() {
		if scanErr := s.scanner.ScanPath(pathID, path); scanErr != nil {
			logger.Errorf("Rescan failed for path %s: %v", path, scanErr)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Rescan started", "path": path, "path_id": pathID, "type": "path"})
}

func (s *RESTServer) getScanDetails(c *gin.Context) {
	scanID := c.Param("scan_id")

	var scan struct {
		ID               int    `json:"id"`
		Path             string `json:"path"`
		PathID           int    `json:"path_id"`
		Status           string `json:"status"`
		FilesScanned     int    `json:"files_scanned"`
		CorruptionsFound int    `json:"corruptions_found"`
		StartedAt        string `json:"started_at"`
		CompletedAt      string `json:"completed_at"`
		HealthyFiles     int    `json:"healthy_files"`
		CorruptFiles     int    `json:"corrupt_files"`
	}

	var completedAt sql.NullString
	var pathID sql.NullInt64
	err := s.db.QueryRow(`
		SELECT id, path, path_id, status, files_scanned, corruptions_found, started_at, completed_at
		FROM scans WHERE id = ?
	`, scanID).Scan(&scan.ID, &scan.Path, &pathID, &scan.Status, &scan.FilesScanned, &scan.CorruptionsFound, &scan.StartedAt, &completedAt)

	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": ErrMsgScanNotFound})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	scan.CompletedAt = completedAt.String
	if pathID.Valid {
		scan.PathID = int(pathID.Int64)
	}

	// Get file counts from scan_files table
	if err := s.db.QueryRow("SELECT COUNT(*) FROM scan_files WHERE scan_id = ? AND status = 'healthy'", scanID).Scan(&scan.HealthyFiles); err != nil {
		logger.Debugf("Failed to query healthy files count: %v", err)
	}
	if err := s.db.QueryRow("SELECT COUNT(*) FROM scan_files WHERE scan_id = ? AND status = 'corrupt'", scanID).Scan(&scan.CorruptFiles); err != nil {
		logger.Debugf("Failed to query corrupt files count: %v", err)
	}

	c.JSON(http.StatusOK, scan)
}

func (s *RESTServer) getScanFiles(c *gin.Context) {
	scanID := c.Param("scan_id")
	statusFilter := c.DefaultQuery("status", "all") // 'all', 'healthy', 'corrupt'

	// Parse pagination (no sorting - fixed order by status DESC, file_path ASC)
	p := ParsePagination(c, DefaultPaginationConfig())

	// Verify scan exists
	var scanExists int
	err := s.db.QueryRow("SELECT id FROM scans WHERE id = ?", scanID).Scan(&scanExists)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusNotFound, gin.H{"error": ErrMsgScanNotFound})
		return
	}

	// Build query with optional status filter
	whereClause := "WHERE scan_id = ?"
	args := []interface{}{scanID}

	if statusFilter != "all" {
		whereClause += " AND status = ?"
		args = append(args, statusFilter)
	}

	// Get total count
	var total int
	countQuery := "SELECT COUNT(*) FROM scan_files " + whereClause
	err = s.db.QueryRow(countQuery, args...).Scan(&total)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Get paginated data
	query := fmt.Sprintf(`
		SELECT id, file_path, status, corruption_type, error_details, file_size, scanned_at
		FROM scan_files %s
		ORDER BY status DESC, file_path ASC
		LIMIT ? OFFSET ?
	`, whereClause)
	args = append(args, p.Limit, p.Offset)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	files := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var filePath, status, scannedAt string
		var corruptionType, errorDetails sql.NullString
		var fileSize sql.NullInt64

		if err := rows.Scan(&id, &filePath, &status, &corruptionType, &errorDetails, &fileSize, &scannedAt); err != nil {
			continue
		}

		files = append(files, map[string]interface{}{
			"id":              id,
			"file_path":       filePath,
			"status":          status,
			"corruption_type": corruptionType.String,
			"error_details":   errorDetails.String,
			"file_size":       fileSize.Int64,
			"scanned_at":      scannedAt,
		})
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading scan files"})
		logger.Errorf("Error iterating scan files: %v", err)
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"data":       files,
		"pagination": NewPaginationResponse(p, total),
	})
}

// triggerScanAll triggers scans for all enabled paths
func (s *RESTServer) triggerScanAll(c *gin.Context) {
	rows, err := s.db.Query("SELECT id, local_path FROM scan_paths WHERE enabled = 1")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	started := 0
	skipped := 0
	for rows.Next() {
		var pathID int64
		var localPath string
		if err := rows.Scan(&pathID, &localPath); err != nil {
			logger.Errorf("Failed to scan row in triggerScanAll: %v", err)
			continue
		}

		if s.scanner.IsPathBeingScanned(localPath) {
			skipped++
			continue
		}

		go func(pid int64, path string) {
			if err := s.scanner.ScanPath(pid, path); err != nil {
				logger.Errorf("Scan failed for path %d (%s): %v", pid, path, err)
			}
		}(pathID, localPath)
		started++
	}

	if err := rows.Err(); err != nil {
		logger.Errorf("Error iterating scan paths: %v", err)
		// Continue with partial results since some scans may have started
	}

	c.JSON(http.StatusAccepted, gin.H{
		"message": fmt.Sprintf("Started %d scan(s), skipped %d already running", started, skipped),
		"started": started,
		"skipped": skipped,
	})
}
