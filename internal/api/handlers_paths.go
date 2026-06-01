package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/repository"
)

// scanPathFieldsFromRequest builds the persistence-layer field bundle
// from the validated scan-path request and its already-canonicalized
// detection_args JSON.
func scanPathFieldsFromRequest(req *scanPathRequest, detectionArgsJSON []byte) repository.ScanPathFields {
	fields := repository.ScanPathFields{
		LocalPath:         req.LocalPath,
		ArrPath:           req.ArrPath,
		Enabled:           req.Enabled,
		AutoRemediate:     req.AutoRemediate,
		DryRun:            req.DryRun,
		DetectionMethod:   req.DetectionMethod,
		DetectionArgsJSON: string(detectionArgsJSON),
		DetectionMode:     req.DetectionMode,
		MaxRetries:        req.MaxRetries,
	}
	if req.ArrInstanceID != nil {
		fields.ArrInstanceID = sql.NullInt64{Int64: int64(*req.ArrInstanceID), Valid: true}
	}
	if req.VerificationTimeoutHours != nil {
		fields.VerificationTimeoutHours = sql.NullInt64{Int64: int64(*req.VerificationTimeoutHours), Valid: true}
	}
	if req.ThoroughDurationSeconds != nil {
		fields.ThoroughDurationSeconds = sql.NullInt64{Int64: *req.ThoroughDurationSeconds, Valid: true}
	}
	if req.ThoroughTimeoutSeconds != nil {
		fields.ThoroughTimeoutSeconds = sql.NullInt64{Int64: *req.ThoroughTimeoutSeconds, Valid: true}
	}
	if req.Hwaccel != nil && *req.Hwaccel != "" {
		fields.Hwaccel = sql.NullString{String: *req.Hwaccel, Valid: true}
	}
	return fields
}

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

// scanPathRequest is the common request structure for creating and updating scan paths.
//
// The three pointer fields at the bottom (ThoroughDurationSeconds /
// ThoroughTimeoutSeconds / Hwaccel) are per-path overrides. nil/empty
// means "inherit the matching global tunable" - the existing behavior
// for every row that predates Phase 2. A non-nil value wins over the
// global for that specific path; the resolver picks per-path > global >
// env > default.
type scanPathRequest struct {
	LocalPath                string   `json:"local_path"`
	ArrPath                  string   `json:"arr_path"`
	ArrInstanceID            *int     `json:"arr_instance_id"`
	Enabled                  bool     `json:"enabled"`
	AutoRemediate            bool     `json:"auto_remediate"`
	DryRun                   bool     `json:"dry_run"`
	DetectionMethod          string   `json:"detection_method"`
	DetectionArgs            []string `json:"detection_args"`
	DetectionMode            string   `json:"detection_mode"`
	MaxRetries               int      `json:"max_retries"`
	VerificationTimeoutHours *int     `json:"verification_timeout_hours"`
	ThoroughDurationSeconds  *int64   `json:"thorough_duration_seconds,omitempty"`
	ThoroughTimeoutSeconds   *int64   `json:"thorough_timeout_seconds,omitempty"`
	Hwaccel                  *string  `json:"hwaccel,omitempty"`
}

// prepareScanPathRequest validates and normalizes a scan path request.
// It applies defaults and marshals detection_args to JSON.
// Returns the JSON bytes for detection_args and any validation error.
func prepareScanPathRequest(req *scanPathRequest, c *gin.Context) ([]byte, bool) {
	// Apply defaults
	if req.DetectionMethod == "" {
		req.DetectionMethod = "ffprobe"
	}
	if req.DetectionMode == "" {
		req.DetectionMode = "quick"
	}
	// Boundary validation: closes T5 from audit. The bare-string fields
	// from JSON could previously persist arbitrary values; reject unknown
	// methods/modes here so the DB only ever holds valid enum members.
	if _, err := integration.ParseDetectionMethod(req.DetectionMethod); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	if _, err := integration.ParseDetectionMode(req.DetectionMode); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	if req.MaxRetries <= 0 || req.MaxRetries > 100 {
		req.MaxRetries = config.Get().DefaultMaxRetries
	}
	if req.ArrPath == "" {
		req.ArrPath = req.LocalPath
	}

	// Validate verification_timeout_hours (1 hour to 1 year)
	if req.VerificationTimeoutHours != nil {
		hours := *req.VerificationTimeoutHours
		if hours < 1 || hours > 8760 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "verification_timeout_hours must be between 1 and 8760"})
			return nil, false
		}
	}

	// Validate per-path overrides. Bounds mirror the catalog entries
	// (internal/repository/tunables.go) so the validation rule is the
	// same whether the value lands in a global setting or a per-path
	// override. nil/zero/empty means "inherit", which is always allowed.
	if req.ThoroughDurationSeconds != nil {
		if v := *req.ThoroughDurationSeconds; v < 0 || v > 24*3600 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "thorough_duration_seconds must be between 0 and 86400"})
			return nil, false
		}
	}
	if req.ThoroughTimeoutSeconds != nil {
		if v := *req.ThoroughTimeoutSeconds; v < 30 || v > 6*3600 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "thorough_timeout_seconds must be between 30 and 21600"})
			return nil, false
		}
	}
	if req.Hwaccel != nil && *req.Hwaccel != "" {
		switch *req.Hwaccel {
		case "auto", "off", "cuda", "vaapi", "qsv", "videotoolbox", "vdpau", "drm":
			// allowed
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hwaccel value"})
			return nil, false
		}
	}

	// Allowlist detection_args so a malicious or compromised admin session
	// cannot persist -i http://... / -f data / etc. and have them spliced
	// into ffmpeg at scan time.
	if err := validateDetectionArgs(req.DetectionArgs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}

	// Marshal detection args to JSON
	var detectionArgsJSON []byte
	if len(req.DetectionArgs) > 0 {
		var err error
		detectionArgsJSON, err = json.Marshal(req.DetectionArgs)
		if err != nil {
			logger.Warnf("Failed to marshal detection_args: %v, using empty array", err)
			detectionArgsJSON = []byte("[]")
		}
	}

	return detectionArgsJSON, true
}

func (s *RESTServer) getScanPaths(c *gin.Context) {
	rows, err := s.scanPaths.ListAll(c.Request.Context())
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	paths := make([]gin.H, 0, len(rows))
	for _, p := range rows {
		path := gin.H{
			"id":               p.ID,
			"local_path":       p.LocalPath,
			"arr_path":         p.ArrPath,
			"enabled":          p.Enabled,
			"auto_remediate":   p.AutoRemediate,
			"dry_run":          p.DryRun,
			"detection_method": p.DetectionMethod,
			"detection_mode":   p.DetectionMode,
			"max_retries":      p.MaxRetries,
		}
		if p.ArrInstanceID.Valid {
			path["arr_instance_id"] = p.ArrInstanceID.Int64
		} else {
			path["arr_instance_id"] = nil
		}
		if p.DetectionArgs.Valid && p.DetectionArgs.String != "" {
			var args []string
			if err := json.Unmarshal([]byte(p.DetectionArgs.String), &args); err == nil {
				path["detection_args"] = args
			} else {
				path["detection_args"] = p.DetectionArgs.String
			}
		} else {
			path["detection_args"] = nil
		}
		if p.VerificationTimeoutHours.Valid {
			path["verification_timeout_hours"] = p.VerificationTimeoutHours.Int64
		} else {
			path["verification_timeout_hours"] = nil
		}
		if p.ThoroughDurationSeconds.Valid {
			path["thorough_duration_seconds"] = p.ThoroughDurationSeconds.Int64
		} else {
			path["thorough_duration_seconds"] = nil
		}
		if p.ThoroughTimeoutSeconds.Valid {
			path["thorough_timeout_seconds"] = p.ThoroughTimeoutSeconds.Int64
		} else {
			path["thorough_timeout_seconds"] = nil
		}
		if p.Hwaccel.Valid {
			path["hwaccel"] = p.Hwaccel.String
		} else {
			path["hwaccel"] = nil
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
	var req scanPathRequest
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}

	detectionArgsJSON, ok := prepareScanPathRequest(&req, c)
	if !ok {
		return
	}

	if _, err := s.scanPaths.Create(c.Request.Context(), scanPathFieldsFromRequest(&req, detectionArgsJSON)); err != nil {
		respondDatabaseError(c, err)
		return
	}
	if err := s.pathMapper.Reload(); err != nil {
		logger.Errorf(errMsgReloadPathMappings, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Scan path created but path mapping update failed"})
		return
	}
	c.Status(http.StatusCreated)
}

func (s *RESTServer) deleteScanPath(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid scan path ID"})
		return
	}
	if err := s.scanPaths.Delete(c.Request.Context(), id); err != nil {
		respondDatabaseError(c, err)
		return
	}
	if err := s.pathMapper.Reload(); err != nil {
		logger.Errorf(errMsgReloadPathMappings, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Scan path deleted but path mapping update failed"})
		return
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
	var req scanPathRequest
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}

	detectionArgsJSON, ok := prepareScanPathRequest(&req, c)
	if !ok {
		return
	}

	idInt, parseErr := strconv.ParseInt(id, 10, 64)
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid scan path ID"})
		return
	}
	if err := s.scanPaths.Update(c.Request.Context(), idInt, scanPathFieldsFromRequest(&req, detectionArgsJSON)); err != nil {
		respondDatabaseError(c, err)
		return
	}
	if err := s.pathMapper.Reload(); err != nil {
		logger.Errorf(errMsgReloadPathMappings, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Scan path updated but path mapping update failed"})
		return
	}
	c.Status(http.StatusOK)
}

// pathValidationResult holds the results of path validation.
type pathValidationResult struct {
	Accessible  bool     `json:"accessible"`
	FileCount   int      `json:"file_count"`
	SampleFiles []string `json:"sample_files"`
	Error       *string  `json:"error"`
}

// classifyPathError returns a user-friendly error message for path access errors.
func classifyPathError(err error) string {
	if os.IsNotExist(err) {
		return "Path does not exist"
	}
	if os.IsPermission(err) {
		return "Permission denied"
	}
	return "Path not accessible"
}

// validationMediaExtensions defines supported media file extensions for validation.
var validationMediaExtensions = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".mov": true,
	".wmv": true, ".flv": true, ".webm": true, ".m4v": true,
	".ts": true, ".m2ts": true, ".mpg": true, ".mpeg": true,
}

// relPathOrName returns the path relative to basePath, falling back to name on error.
func relPathOrName(basePath, fullPath, name string) string {
	rel, err := filepath.Rel(basePath, fullPath)
	if err != nil {
		return name
	}
	return rel
}

// countMediaFiles walks a directory and counts media files, collecting samples.
// maxFiles limits the count to prevent slow responses on very large libraries.
func countMediaFiles(basePath string, maxSamples, maxFiles int) (int, []string, bool) {
	var fileCount int
	var sampleFiles []string
	truncated := false

	_ = filepath.WalkDir(basePath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil // Skip inaccessible directories
		}
		if d.IsDir() && strings.HasPrefix(d.Name(), ".") {
			return filepath.SkipDir
		}
		if d.IsDir() {
			return nil
		}

		ext := strings.ToLower(filepath.Ext(d.Name()))
		if !validationMediaExtensions[ext] {
			return nil
		}

		fileCount++
		if len(sampleFiles) < maxSamples {
			sampleFiles = append(sampleFiles, relPathOrName(basePath, path, d.Name()))
		}

		// Stop early if we've counted enough files (performance optimization)
		if maxFiles > 0 && fileCount >= maxFiles {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})

	return fileCount, sampleFiles, truncated
}

// validateScanPath checks if a scan path is accessible and returns file statistics.
// GET /config/paths/:id/validate
func (s *RESTServer) validateScanPath(c *gin.Context) {
	id := c.Param("id")

	// Get the path from database
	idInt, parseErr := strconv.ParseInt(id, 10, 64)
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid scan path ID"})
		return
	}
	path, err := s.scanPaths.GetByID(c.Request.Context(), idInt)
	if errors.Is(err, repository.ErrNotFound) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Scan path not found"})
		return
	}
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	localPath := path.LocalPath

	// Check if path exists and is accessible
	info, err := os.Stat(localPath)
	if err != nil {
		errMsg := classifyPathError(err)
		c.JSON(http.StatusOK, pathValidationResult{
			Accessible:  false,
			SampleFiles: []string{},
			Error:       &errMsg,
		})
		return
	}

	if !info.IsDir() {
		errMsg := "Path is not a directory"
		c.JSON(http.StatusOK, pathValidationResult{
			Accessible:  false,
			SampleFiles: []string{},
			Error:       &errMsg,
		})
		return
	}

	// Count media files and collect samples (limit to 10000 for performance)
	fileCount, sampleFiles, truncated := countMediaFiles(localPath, 5, 10000)

	result := pathValidationResult{
		Accessible:  true,
		FileCount:   fileCount,
		SampleFiles: sampleFiles,
		Error:       nil,
	}

	// Indicate if count was truncated for very large libraries
	if truncated {
		truncMsg := "Count limited to 10,000 files for performance"
		result.Error = &truncMsg
	}

	c.JSON(http.StatusOK, result)
}
