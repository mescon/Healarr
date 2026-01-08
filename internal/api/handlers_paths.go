package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
)

const errMsgReloadPathMappings = "Failed to reload path mappings: %v"

// errInvalidPath is returned when a path fails security validation.
var errInvalidPath = errors.New("invalid path")

// sanitizeBrowsePath validates and sanitizes a path for directory browsing.
// It prevents path traversal attacks by ensuring the path:
// 1. Is cleaned of any relative path components
// 2. Does not contain path traversal sequences after cleaning
// 3. Is an absolute path
// 4. Contains only valid path characters
func sanitizeBrowsePath(requestedPath string) (string, error) {
	// Clean the path to resolve any . or .. components
	cleanPath := filepath.Clean(requestedPath)

	// Ensure the path is absolute
	if !filepath.IsAbs(cleanPath) {
		cleanPath = "/" + cleanPath
		cleanPath = filepath.Clean(cleanPath)
	}

	// Security: Reject if path still contains traversal sequences
	// This handles edge cases that filepath.Clean might not catch
	if strings.Contains(cleanPath, "..") {
		return "", errInvalidPath
	}

	// Security: Reject null bytes which can be used to bypass checks
	if strings.ContainsRune(cleanPath, 0) {
		return "", errInvalidPath
	}

	return cleanPath, nil
}

func (s *RESTServer) getScanPaths(c *gin.Context) {
	rows, err := s.db.Query("SELECT id, local_path, arr_path, arr_instance_id, enabled, auto_remediate, detection_method, detection_args, detection_mode, max_retries, verification_timeout_hours FROM scan_paths")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	var paths []gin.H
	for rows.Next() {
		var id int
		var localPath, arrPath string
		var arrInstanceID sql.NullInt64
		var enabled, autoRemediate bool
		var detectionMethod, detectionMode string
		var detectionArgs sql.NullString
		var maxRetries int
		var verificationTimeoutHours sql.NullInt64
		if rows.Scan(&id, &localPath, &arrPath, &arrInstanceID, &enabled, &autoRemediate, &detectionMethod, &detectionArgs, &detectionMode, &maxRetries, &verificationTimeoutHours) != nil {
			continue
		}
		path := gin.H{
			"id":               id,
			"local_path":       localPath,
			"arr_path":         arrPath,
			"arr_instance_id":  arrInstanceID.Int64,
			"enabled":          enabled,
			"auto_remediate":   autoRemediate,
			"detection_method": detectionMethod,
			"detection_args":   detectionArgs.String,
			"detection_mode":   detectionMode,
			"max_retries":      maxRetries,
		}
		if verificationTimeoutHours.Valid {
			path["verification_timeout_hours"] = verificationTimeoutHours.Int64
		} else {
			path["verification_timeout_hours"] = nil
		}
		paths = append(paths, path)
	}
	c.JSON(http.StatusOK, paths)
}

// getDetectionPreview returns a preview of the command that will be executed for given detection settings
func (s *RESTServer) getDetectionPreview(c *gin.Context) {
	method := c.DefaultQuery("method", "ffprobe")
	mode := c.DefaultQuery("mode", "quick")
	customArgsStr := c.Query("args") // comma-separated custom args

	// Parse custom args
	var customArgs []string
	if customArgsStr != "" {
		for _, arg := range strings.Split(customArgsStr, ",") {
			arg = strings.TrimSpace(arg)
			if arg != "" {
				customArgs = append(customArgs, arg)
			}
		}
	}

	// Get the health checker to generate preview
	hc := integration.NewHealthChecker()

	// Map string method to DetectionMethod
	var detectionMethod integration.DetectionMethod
	switch method {
	case "ffprobe":
		detectionMethod = integration.DetectionFFprobe
	case "mediainfo":
		detectionMethod = integration.DetectionMediaInfo
	case "handbrake":
		detectionMethod = integration.DetectionHandBrake
	case "zero_byte":
		detectionMethod = integration.DetectionZeroByte
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid detection method"})
		return
	}

	command := hc.GetCommandPreview(detectionMethod, mode, customArgs)
	timeout := hc.GetTimeoutDescription(detectionMethod, mode)

	// Mode descriptions
	var modeDescription string
	switch mode {
	case "thorough":
		switch method {
		case "ffprobe":
			modeDescription = "Decodes the entire file to detect mid-file corruption, bad frames, and stream errors. Much slower but catches issues that header checks miss."
		case "mediainfo":
			modeDescription = "Performs full metadata analysis including all track details and extended properties."
		case "handbrake":
			modeDescription = "Generates multiple preview frames at different points in the file to verify stream integrity throughout."
		case "zero_byte":
			modeDescription = "Simple file size check - only detects completely empty files."
		}
	default: // quick
		switch method {
		case "ffprobe":
			modeDescription = "Checks container headers and stream information. Fast and reliable for obvious corruption."
		case "mediainfo":
			modeDescription = "Basic metadata extraction to verify container structure."
		case "handbrake":
			modeDescription = "Basic container scan to detect audio/video tracks."
		case "zero_byte":
			modeDescription = "Simple file size check - only detects completely empty files."
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"method":           method,
		"mode":             mode,
		"command":          command,
		"timeout":          timeout,
		"mode_description": modeDescription,
	})
}

func (s *RESTServer) createScanPath(c *gin.Context) {
	var req struct {
		LocalPath                string   `json:"local_path"`
		ArrPath                  string   `json:"arr_path"`
		ArrInstanceID            *int     `json:"arr_instance_id"`
		Enabled                  bool     `json:"enabled"`
		AutoRemediate            bool     `json:"auto_remediate"`
		DetectionMethod          string   `json:"detection_method"`
		DetectionArgs            []string `json:"detection_args"`
		DetectionMode            string   `json:"detection_mode"`
		MaxRetries               int      `json:"max_retries"`
		VerificationTimeoutHours *int     `json:"verification_timeout_hours"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set defaults if not provided
	if req.DetectionMethod == "" {
		req.DetectionMethod = "ffprobe"
	}
	if req.DetectionMode == "" {
		req.DetectionMode = "quick"
	}
	if req.MaxRetries == 0 {
		req.MaxRetries = config.Get().DefaultMaxRetries
	}
	// If arr_path is empty, assume same path as local (no mapping needed)
	if req.ArrPath == "" {
		req.ArrPath = req.LocalPath
	}

	// Marshal detection args to JSON
	var detectionArgsJSON []byte
	if len(req.DetectionArgs) > 0 {
		var marshalErr error
		detectionArgsJSON, marshalErr = json.Marshal(req.DetectionArgs)
		if marshalErr != nil {
			logger.Warnf("Failed to marshal detection_args: %v, using empty object", marshalErr)
			detectionArgsJSON = []byte("{}")
		}
	}

	_, err := s.db.Exec(`INSERT INTO scan_paths
		(local_path, arr_path, arr_instance_id, enabled, auto_remediate, detection_method, detection_args, detection_mode, max_retries, verification_timeout_hours)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		req.LocalPath, req.ArrPath, req.ArrInstanceID, req.Enabled, req.AutoRemediate,
		req.DetectionMethod, detectionArgsJSON, req.DetectionMode, req.MaxRetries, req.VerificationTimeoutHours)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := s.pathMapper.Reload(); err != nil {
		logger.Errorf(errMsgReloadPathMappings, err)
	}
	c.Status(http.StatusCreated)
}

func (s *RESTServer) deleteScanPath(c *gin.Context) {
	id := c.Param("id")
	_, err := s.db.Exec("DELETE FROM scan_paths WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := s.pathMapper.Reload(); err != nil {
		logger.Errorf(errMsgReloadPathMappings, err)
	}
	c.Status(http.StatusNoContent)
}

// browseDirectory returns directory contents for the file browser.
// This endpoint is protected by authentication and is used by admins to configure scan paths.
func (s *RESTServer) browseDirectory(c *gin.Context) {
	requestedPath := c.Query("path")
	if requestedPath == "" {
		requestedPath = "/"
	}

	// Security: Sanitize and validate the path to prevent path traversal
	cleanPath, err := sanitizeBrowsePath(requestedPath)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"current_path": "/",
			"parent_path":  nil,
			"entries":      []gin.H{},
			"error":        "Invalid path",
		})
		return
	}

	// Check if path exists and is a directory
	info, err := os.Stat(cleanPath)
	if err != nil {
		if os.IsNotExist(err) {
			c.JSON(http.StatusOK, gin.H{
				"current_path": "/",
				"parent_path":  nil,
				"entries":      []gin.H{},
				"error":        "Directory not found",
			})
			return
		}
		c.JSON(http.StatusOK, gin.H{
			"current_path": "/",
			"parent_path":  nil,
			"entries":      []gin.H{},
			"error":        "Cannot access directory",
		})
		return
	}

	if !info.IsDir() {
		// If it's a file, go to parent directory
		cleanPath = filepath.Dir(cleanPath)
	}

	// Read directory contents
	entries, err := os.ReadDir(cleanPath)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{
			"current_path": cleanPath,
			"parent_path":  nil,
			"entries":      []gin.H{},
			"error":        "Cannot read directory",
		})
		return
	}

	// Build response with only directories
	var dirEntries []gin.H
	for _, entry := range entries {
		if entry.IsDir() {
			// Skip hidden directories (starting with .)
			if strings.HasPrefix(entry.Name(), ".") {
				continue
			}
			dirEntries = append(dirEntries, gin.H{
				"name":   entry.Name(),
				"path":   filepath.Join(cleanPath, entry.Name()),
				"is_dir": true,
			})
		}
	}

	// Calculate parent path
	var parentPath interface{}
	if cleanPath != "/" {
		parentPath = filepath.Dir(cleanPath)
	}

	c.JSON(http.StatusOK, gin.H{
		"current_path": cleanPath,
		"parent_path":  parentPath,
		"entries":      dirEntries,
		"error":        nil,
	})
}

func (s *RESTServer) updateScanPath(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		LocalPath                string   `json:"local_path"`
		ArrPath                  string   `json:"arr_path"`
		ArrInstanceID            *int     `json:"arr_instance_id"`
		Enabled                  bool     `json:"enabled"`
		AutoRemediate            bool     `json:"auto_remediate"`
		DetectionMethod          string   `json:"detection_method"`
		DetectionArgs            []string `json:"detection_args"`
		DetectionMode            string   `json:"detection_mode"`
		MaxRetries               int      `json:"max_retries"`
		VerificationTimeoutHours *int     `json:"verification_timeout_hours"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if req.MaxRetries == 0 {
		req.MaxRetries = config.Get().DefaultMaxRetries
	}
	if req.DetectionMethod == "" {
		req.DetectionMethod = "ffprobe"
	}
	if req.DetectionMode == "" {
		req.DetectionMode = "quick"
	}
	// If arr_path is empty, assume same path as local (no mapping needed)
	if req.ArrPath == "" {
		req.ArrPath = req.LocalPath
	}

	// Marshal detection args to JSON
	var detectionArgsJSON []byte
	if len(req.DetectionArgs) > 0 {
		var marshalErr error
		detectionArgsJSON, marshalErr = json.Marshal(req.DetectionArgs)
		if marshalErr != nil {
			logger.Warnf("Failed to marshal detection_args: %v, using empty object", marshalErr)
			detectionArgsJSON = []byte("{}")
		}
	}

	_, err := s.db.Exec(`UPDATE scan_paths SET
		local_path = ?, arr_path = ?, arr_instance_id = ?, enabled = ?,
		auto_remediate = ?, detection_method = ?, detection_args = ?,
		detection_mode = ?, max_retries = ?, verification_timeout_hours = ?
		WHERE id = ?`,
		req.LocalPath, req.ArrPath, req.ArrInstanceID, req.Enabled,
		req.AutoRemediate, req.DetectionMethod, detectionArgsJSON,
		req.DetectionMode, req.MaxRetries, req.VerificationTimeoutHours, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if err := s.pathMapper.Reload(); err != nil {
		logger.Errorf(errMsgReloadPathMappings, err)
	}
	c.Status(http.StatusOK)
}
