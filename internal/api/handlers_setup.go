package api

import (
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/logger"
)

// SQL queries and error messages for setup handlers
const (
	sqlCountPasswordHash = "SELECT COUNT(*) FROM settings WHERE key = 'password_hash'"
	sqlCountAPIKey       = "SELECT COUNT(*) FROM settings WHERE key = 'api_key'"
	errMsgDatabaseError  = "Database error"
)

// validatePathWithinDir validates that targetPath is within baseDir (defense in depth)
// Returns the cleaned target path if valid, or an error if path escapes the base directory
func validatePathWithinDir(targetPath, baseDir string) (string, error) {
	cleanPath := filepath.Clean(targetPath)
	cleanDir := filepath.Clean(baseDir)

	// Ensure path starts with directory + separator to prevent directory traversal
	if !strings.HasPrefix(cleanPath, cleanDir+string(filepath.Separator)) {
		return "", fmt.Errorf("path %s is not within %s", cleanPath, cleanDir)
	}
	return cleanPath, nil
}

// SetupStatus represents the current setup state of the application
type SetupStatus struct {
	NeedsSetup          bool `json:"needs_setup"`
	HasPassword         bool `json:"has_password"`
	HasAPIKey           bool `json:"has_api_key"`
	HasInstances        bool `json:"has_instances"`
	HasScanPaths        bool `json:"has_scan_paths"`
	OnboardingDismissed bool `json:"onboarding_dismissed"`
}

// handleSetupStatus returns the current setup status for the onboarding wizard
// This endpoint is public (no auth required) to allow first-time setup
func (s *RESTServer) handleSetupStatus(c *gin.Context) {
	status := SetupStatus{}

	// Check for password
	var passwordExists int
	err := s.db.QueryRow(sqlCountPasswordHash).Scan(&passwordExists)
	if err != nil && err != sql.ErrNoRows {
		logger.Errorf("Failed to check password status: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": errMsgDatabaseError})
		return
	}
	status.HasPassword = passwordExists > 0

	// Check for API key
	var apiKeyExists int
	err = s.db.QueryRow(sqlCountAPIKey).Scan(&apiKeyExists)
	if err != nil && err != sql.ErrNoRows {
		logger.Errorf("Failed to check API key status: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": errMsgDatabaseError})
		return
	}
	status.HasAPIKey = apiKeyExists > 0

	// Check for configured instances
	var instanceCount int
	err = s.db.QueryRow("SELECT COUNT(*) FROM arr_instances").Scan(&instanceCount)
	if err != nil && err != sql.ErrNoRows {
		logger.Errorf("Failed to check instances: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": errMsgDatabaseError})
		return
	}
	status.HasInstances = instanceCount > 0

	// Check for configured scan paths
	var pathCount int
	err = s.db.QueryRow("SELECT COUNT(*) FROM scan_paths").Scan(&pathCount)
	if err != nil && err != sql.ErrNoRows {
		logger.Errorf("Failed to check scan paths: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": errMsgDatabaseError})
		return
	}
	status.HasScanPaths = pathCount > 0

	// Check if onboarding was dismissed
	var dismissed sql.NullString
	err = s.db.QueryRow("SELECT value FROM settings WHERE key = 'onboarding_dismissed'").Scan(&dismissed)
	if err != nil && err != sql.ErrNoRows {
		logger.Errorf("Failed to check onboarding dismissed status: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": errMsgDatabaseError})
		return
	}
	status.OnboardingDismissed = dismissed.Valid && dismissed.String == "true"

	// User needs setup if they have no password (first-time setup)
	status.NeedsSetup = !status.HasPassword

	c.JSON(http.StatusOK, status)
}

// handleSetupDismiss allows power users to skip the onboarding wizard
// This endpoint is public during first-time setup, authenticated otherwise
func (s *RESTServer) handleSetupDismiss(c *gin.Context) {
	// Store the dismissal flag
	_, err := s.db.Exec(`
		INSERT INTO settings (key, value, updated_at) VALUES ('onboarding_dismissed', 'true', datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = 'true', updated_at = datetime('now')
	`)
	if err != nil {
		logger.Errorf("Failed to dismiss onboarding: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save preference"})
		return
	}

	logger.Infof("Onboarding wizard dismissed by user")
	c.JSON(http.StatusOK, gin.H{"message": "Onboarding dismissed"})
}

// handleDatabaseRestore restores the database from an uploaded backup file
// This endpoint is public during first-time setup, authenticated otherwise
// Requires X-Confirm-Restore: true header for safety
func (s *RESTServer) handleDatabaseRestore(c *gin.Context) {
	// Safety check: require explicit confirmation header
	if c.GetHeader("X-Confirm-Restore") != "true" {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "Confirmation required",
			"message": "Database restore is destructive. Set X-Confirm-Restore: true header to confirm.",
		})
		return
	}

	// Get the uploaded file
	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No file uploaded"})
		return
	}
	defer file.Close()

	// Validate file extension
	if filepath.Ext(header.Filename) != ".db" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid file type. Expected .db file"})
		return
	}

	// Create temp file to store the uploaded database
	cfg := config.Get()
	backupDir := filepath.Join(filepath.Dir(cfg.DatabasePath), "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		logger.Errorf("Failed to create backup directory: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create backup directory"})
		return
	}

	tempPath := filepath.Join(backupDir, fmt.Sprintf("restore_temp_%d.db", time.Now().UnixNano()))
	tempFile, err := os.Create(tempPath)
	if err != nil {
		logger.Errorf("Failed to create temp file for restore: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to process upload"})
		return
	}

	// Copy uploaded file to temp location
	_, err = io.Copy(tempFile, file)
	tempFile.Close()
	if err != nil {
		os.Remove(tempPath)
		logger.Errorf("Failed to save uploaded database: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save upload"})
		return
	}

	// Validate the uploaded database
	validationErr := s.validateUploadedDatabase(tempPath)
	if validationErr != nil {
		os.Remove(tempPath)
		logger.Errorf("Uploaded database validation failed: %v", validationErr)
		c.JSON(http.StatusBadRequest, gin.H{"error": validationErr.Error()})
		return
	}

	// Create a backup of the current database before replacing
	timestamp := time.Now().Format("20060102_150405")
	preRestoreBackup := filepath.Join(backupDir, fmt.Sprintf("pre_restore_%s.db", timestamp))

	// Security: Validate backup path is within expected directory (defense in depth)
	// Note: backupDir is derived from config, not user input, but we validate anyway
	cleanBackupPath, pathErr := validatePathWithinDir(preRestoreBackup, backupDir)
	if pathErr != nil {
		logger.Errorf("Backup path validation failed: %v", pathErr)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid backup path configuration"})
		return
	}

	// Use VACUUM INTO for safe backup of current database
	// SQLite VACUUM INTO doesn't support parameterized paths, so we use validated path
	_, backupErr := s.db.Exec(fmt.Sprintf("VACUUM INTO '%s'", cleanBackupPath))
	if backupErr != nil {
		logger.Errorf("Failed to create pre-restore backup: %v", backupErr)
		// Continue anyway - the user explicitly confirmed the restore
	} else {
		logger.Infof("Pre-restore backup created: %s", cleanBackupPath)
	}

	// Mark the pending restore file
	pendingPath := cfg.DatabasePath + ".pending"
	if err := os.Rename(tempPath, pendingPath); err != nil {
		os.Remove(tempPath)
		logger.Errorf("Failed to stage restore file: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to stage restore"})
		return
	}

	logger.Infof("Database restore staged: %s (restart required to apply)", pendingPath)

	c.JSON(http.StatusOK, gin.H{
		"message":          "Database restore staged successfully",
		"restart_required": true,
		"backup_created":   cleanBackupPath,
		"note":             "Restart the server to apply the restored database",
	})
}

// validateUploadedDatabase checks if the uploaded file is a valid Healarr database
func (s *RESTServer) validateUploadedDatabase(dbPath string) error {
	// Open the uploaded database
	db, err := sql.Open("sqlite3", dbPath+"?mode=ro")
	if err != nil {
		return fmt.Errorf("invalid SQLite database: %w", err)
	}
	defer db.Close()

	// Run integrity check
	var result string
	err = db.QueryRow("PRAGMA integrity_check").Scan(&result)
	if err != nil {
		return fmt.Errorf("database integrity check failed: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("database is corrupted: %s", result)
	}

	// Check for required tables (schema_migrations indicates it's a Healarr database)
	var tableCount int
	err = db.QueryRow(`
		SELECT COUNT(*) FROM sqlite_master
		WHERE type='table' AND name IN ('schema_migrations', 'settings', 'arr_instances', 'scan_paths')
	`).Scan(&tableCount)
	if err != nil {
		return fmt.Errorf("failed to check tables: %w", err)
	}
	if tableCount < 3 {
		return fmt.Errorf("not a valid Healarr database (missing required tables)")
	}

	return nil
}

// handleConfigImportPublic is a wrapper for importConfig that can be called during setup
// It checks if setup is needed before allowing unauthenticated access
func (s *RESTServer) handleConfigImportPublic(c *gin.Context) {
	// Check if we're in setup mode (no password set)
	var passwordExists int
	err := s.db.QueryRow(sqlCountPasswordHash).Scan(&passwordExists)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": errMsgDatabaseError})
		return
	}

	if passwordExists > 0 {
		// Password exists, require authentication
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	// No password, allow import
	s.importConfig(c)
}

// handleDatabaseRestorePublic is a wrapper for handleDatabaseRestore that can be called during setup
func (s *RESTServer) handleDatabaseRestorePublic(c *gin.Context) {
	// Check if we're in setup mode (no password set)
	var passwordExists int
	err := s.db.QueryRow(sqlCountPasswordHash).Scan(&passwordExists)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": errMsgDatabaseError})
		return
	}

	if passwordExists > 0 {
		// Password exists, require authentication
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Authentication required"})
		return
	}

	// No password, allow restore
	s.handleDatabaseRestore(c)
}
