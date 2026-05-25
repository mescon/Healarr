package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/notifier"
	"github.com/mescon/Healarr/internal/repository"
	"github.com/mescon/Healarr/internal/safego"
)

// Type alias for cleaner code
type notifierConfig = notifier.NotificationConfig

// jsonMarshal is a helper for json.Marshal
var jsonMarshal = json.Marshal

func (s *RESTServer) updateSettings(c *gin.Context) {
	var req struct {
		BasePath string `json:"base_path"`
	}
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}

	// Normalize base path
	basePath := req.BasePath
	if basePath != "/" && basePath != "" {
		if !strings.HasPrefix(basePath, "/") {
			basePath = "/" + basePath
		}
		basePath = strings.TrimSuffix(basePath, "/")
	}
	if basePath == "" {
		basePath = "/"
	}

	// Upsert setting
	if err := s.settings.Set(c.Request.Context(), repository.SettingKeyBasePath, basePath); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save setting"})
		return
	}

	logger.Infof("Base path setting updated to: %s", basePath)
	c.JSON(http.StatusOK, gin.H{
		"message":          "Settings saved. Restart required for changes to take effect.",
		"base_path":        basePath,
		"restart_required": true,
	})
}

func (s *RESTServer) restartServer(c *gin.Context) {
	logger.Infof("Server restart requested via API")

	// Send response before restarting
	c.JSON(http.StatusOK, gin.H{"message": "Server restarting..."})

	// Give time for the response to be sent
	safego.Run("server-restart", func() {
		time.Sleep(500 * time.Millisecond)
		logger.Infof("Initiating server restart...")

		// Platform-specific restart (see restart_unix.go and restart_windows.go)
		restartProcess()
	})
}

// exportArrInstances exports arr instances from the database. Returns the
// rows and any query/iteration error so the caller can fail the export
// instead of returning a partial result the user wouldn't know is broken.
func (s *RESTServer) exportArrInstances(ctx context.Context) ([]gin.H, error) {
	rows, err := s.arrInstances.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("query arr_instances: %w", err)
	}

	var instances []gin.H
	for _, row := range rows {
		decryptedKey, err := crypto.Decrypt(row.EncryptedAPIKey)
		if err != nil {
			// Per-row decrypt failure is recorded as a sentinel; we don't
			// fail the whole export because the rest of the data is still
			// valuable, but the sentinel makes the corrupt row obvious.
			logger.Errorf("Failed to decrypt API key for export: %v", err)
			decryptedKey = "[DECRYPTION_ERROR]"
		}
		instances = append(instances, gin.H{
			"name": row.Name, "type": row.Type, "url": row.URL, "api_key": decryptedKey, "enabled": row.Enabled,
		})
	}
	return instances, nil
}

// exportScanPaths exports scan paths from the database.
func (s *RESTServer) exportScanPaths(ctx context.Context) ([]gin.H, error) {
	rows, err := s.scanPaths.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("query scan_paths: %w", err)
	}

	var paths []gin.H
	for _, sp := range rows {
		path := gin.H{
			"local_path": sp.LocalPath, "arr_path": sp.ArrPath, "enabled": sp.Enabled,
			"auto_remediate": sp.AutoRemediate, "dry_run": sp.DryRun, "detection_method": sp.DetectionMethod,
			"detection_mode": sp.DetectionMode, "max_retries": sp.MaxRetries,
		}
		if sp.ArrInstanceID.Valid {
			path["arr_instance_id"] = sp.ArrInstanceID.Int64
		}
		if sp.DetectionArgs.Valid && sp.DetectionArgs.String != "" {
			var args []string
			if err := json.Unmarshal([]byte(sp.DetectionArgs.String), &args); err == nil {
				path["detection_args"] = args
			} else {
				path["detection_args"] = sp.DetectionArgs.String
			}
		}
		if sp.VerificationTimeoutHours.Valid {
			path["verification_timeout_hours"] = sp.VerificationTimeoutHours.Int64
		}
		paths = append(paths, path)
	}
	return paths, nil
}

// exportSchedules exports scan schedules from the database.
func (s *RESTServer) exportSchedules(ctx context.Context) ([]gin.H, error) {
	rows, err := s.schedules.ListWithPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("query scan_schedules: %w", err)
	}

	var schedules []gin.H
	for _, sched := range rows {
		schedules = append(schedules, gin.H{
			"local_path": sched.LocalPath, "cron_expression": sched.CronExpression, "enabled": sched.Enabled,
		})
	}
	return schedules, nil
}

// exportNotifications exports notification configs. A nil notifier is not
// an error (legitimate "no notifications configured" state); a failure
// loading existing configs is, since it would produce a backup the user
// thinks is complete but secretly omits notifications.
func (s *RESTServer) exportNotifications() ([]gin.H, error) {
	if s.notifier == nil {
		return nil, nil
	}
	configs, err := s.notifier.GetAllConfigs()
	if err != nil {
		return nil, fmt.Errorf("get notification configs: %w", err)
	}
	var notifConfigs []gin.H
	for _, cfg := range configs {
		notifConfigs = append(notifConfigs, gin.H{
			"name": cfg.Name, "provider_type": cfg.ProviderType,
			"config": cfg.Config, "events": cfg.Events,
			"enabled": cfg.Enabled, "throttle_seconds": cfg.ThrottleSeconds,
		})
	}
	return notifConfigs, nil
}

// exportConfig exports all configuration as JSON. If any section fails to
// load (DB error, decrypt failure, notifier failure), the entire export
// is aborted with a 500 — returning a partial backup that the user thinks
// is complete is far worse than failing cleanly.
func (s *RESTServer) exportConfig(c *gin.Context) {
	export := gin.H{
		"exported_at": time.Now().Format(time.RFC3339),
		"version":     config.Version,
	}

	instances, err := s.exportArrInstances(c.Request.Context())
	if err != nil {
		logger.Errorf("exportConfig: %v", err)
		respondWithError(c, http.StatusInternalServerError, "Failed to export configuration", err)
		return
	}
	if instances != nil {
		export["arr_instances"] = instances
	}

	paths, err := s.exportScanPaths(c.Request.Context())
	if err != nil {
		logger.Errorf("exportConfig: %v", err)
		respondWithError(c, http.StatusInternalServerError, "Failed to export configuration", err)
		return
	}
	if paths != nil {
		export["scan_paths"] = paths
	}

	schedules, err := s.exportSchedules(c.Request.Context())
	if err != nil {
		logger.Errorf("exportConfig: %v", err)
		respondWithError(c, http.StatusInternalServerError, "Failed to export configuration", err)
		return
	}
	if schedules != nil {
		export["schedules"] = schedules
	}

	notifications, err := s.exportNotifications()
	if err != nil {
		logger.Errorf("exportConfig: %v", err)
		respondWithError(c, http.StatusInternalServerError, "Failed to export configuration", err)
		return
	}
	if notifications != nil {
		export["notifications"] = notifications
	}

	c.JSON(http.StatusOK, export)
}

// importConfigRequest represents the import request structure.
type importConfigRequest struct {
	ArrInstances  []importArrInstance  `json:"arr_instances"`
	ScanPaths     []importScanPath     `json:"scan_paths"`
	Schedules     []importSchedule     `json:"schedules"`
	Notifications []importNotification `json:"notifications"`
}

type importArrInstance struct {
	Name    string `json:"name"`
	Type    string `json:"type"`
	URL     string `json:"url"`
	APIKey  string `json:"api_key"`
	Enabled bool   `json:"enabled"`
}

type importScanPath struct {
	LocalPath                string          `json:"local_path"`
	ArrPath                  string          `json:"arr_path"`
	ArrInstanceID            *int            `json:"arr_instance_id"`
	Enabled                  bool            `json:"enabled"`
	AutoRemediate            bool            `json:"auto_remediate"`
	DryRun                   bool            `json:"dry_run"`
	DetectionMethod          string          `json:"detection_method"`
	DetectionArgs            json.RawMessage `json:"detection_args"`
	DetectionMode            string          `json:"detection_mode"`
	MaxRetries               int             `json:"max_retries"`
	VerificationTimeoutHours *int            `json:"verification_timeout_hours"`
}

type importSchedule struct {
	LocalPath      string `json:"local_path"`
	CronExpression string `json:"cron_expression"`
	Enabled        bool   `json:"enabled"`
}

type importNotification struct {
	Name            string   `json:"name"`
	ProviderType    string   `json:"provider_type"`
	Config          any      `json:"config"`
	Events          []string `json:"events"`
	Enabled         bool     `json:"enabled"`
	ThrottleSeconds int      `json:"throttle_seconds"`
}

// importArrInstances imports arr instances and returns the count.
// Skips duplicates based on URL to prevent creating multiple entries for the same instance.
func (s *RESTServer) importArrInstances(ctx context.Context, instances []importArrInstance) int {
	count := 0
	for _, inst := range instances {
		// Check if an instance with the same URL already exists
		existingID, err := s.arrInstances.FindIDByURL(ctx, inst.URL)
		if err == nil {
			// Instance already exists, skip
			logger.Debugf("Skipping duplicate arr instance with URL %s (existing ID: %d)", inst.URL, existingID)
			continue
		}
		if !errors.Is(err, repository.ErrNotFound) {
			logger.Errorf("Failed to check for duplicate arr instance %s: %v", inst.Name, err)
			continue
		}

		encryptedKey, err := crypto.Encrypt(inst.APIKey)
		if err != nil {
			logger.Errorf("Failed to encrypt API key for import: %v", err)
			continue
		}
		if _, err := s.arrInstances.Create(ctx, repository.CreateArrInstanceParams{
			Name:            inst.Name,
			Type:            inst.Type,
			URL:             inst.URL,
			EncryptedAPIKey: encryptedKey,
			Enabled:         inst.Enabled,
		}); err != nil {
			logger.Errorf("Failed to import arr instance %s: %v", inst.Name, err)
			continue
		}
		count++
	}
	return count
}

// normalizeScanPathDefaults fills in default values for optional scan path fields
func normalizeScanPathDefaults(path *importScanPath) {
	if path.DetectionMethod == "" {
		path.DetectionMethod = "ffprobe"
	}
	if path.DetectionMode == "" {
		path.DetectionMode = "quick"
	}
	if path.MaxRetries == 0 {
		path.MaxRetries = config.Get().DefaultMaxRetries
	}
	if path.ArrPath == "" {
		path.ArrPath = path.LocalPath
	}
}

// normalizeDetectionArgs converts detection_args from various import formats to the DB storage format.
// Accepts: null/empty, []string (new format), or string containing JSON array (legacy format).
// Returns the JSON string for DB storage, or empty string if not set.
func normalizeDetectionArgs(raw json.RawMessage) string {
	if len(raw) == 0 || string(raw) == "null" {
		return ""
	}

	// Try as []string (new export format: ["--verbose", "--threads 2"])
	var args []string
	if err := json.Unmarshal(raw, &args); err == nil {
		if len(args) == 0 {
			return ""
		}
		b, err := json.Marshal(args)
		if err != nil {
			return ""
		}
		return string(b)
	}

	// Try as string (legacy format: "[\"--verbose\",\"--threads 2\"]")
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}

	return ""
}

// importScanPaths imports scan paths and returns count and path ID mapping.
// Skips duplicates based on local_path to prevent creating multiple entries for the same path.
func (s *RESTServer) importScanPaths(ctx context.Context, paths []importScanPath) (int, map[string]int64) {
	count := 0
	pathIDs := make(map[string]int64)
	for i := range paths {
		path := &paths[i]

		// Check if a scan path with the same local_path already exists
		existingID, err := s.scanPaths.FindIDByLocalPath(ctx, path.LocalPath)
		if err == nil {
			// Path already exists, add to mapping but don't count as imported
			logger.Debugf("Skipping duplicate scan path %s (existing ID: %d)", path.LocalPath, existingID)
			pathIDs[path.LocalPath] = existingID
			continue
		}
		if !errors.Is(err, repository.ErrNotFound) {
			logger.Errorf("Failed to check for duplicate scan path %s: %v", path.LocalPath, err)
			continue
		}

		normalizeScanPathDefaults(path)

		fields := repository.ScanPathFields{
			LocalPath:         path.LocalPath,
			ArrPath:           path.ArrPath,
			Enabled:           path.Enabled,
			AutoRemediate:     path.AutoRemediate,
			DryRun:            path.DryRun,
			DetectionMethod:   path.DetectionMethod,
			DetectionArgsJSON: normalizeDetectionArgs(path.DetectionArgs),
			DetectionMode:     path.DetectionMode,
			MaxRetries:        path.MaxRetries,
		}
		if path.ArrInstanceID != nil {
			fields.ArrInstanceID = sql.NullInt64{Int64: int64(*path.ArrInstanceID), Valid: true}
		}
		if path.VerificationTimeoutHours != nil {
			fields.VerificationTimeoutHours = sql.NullInt64{Int64: int64(*path.VerificationTimeoutHours), Valid: true}
		}
		newID, err := s.scanPaths.Create(ctx, fields)
		if err != nil {
			logger.Errorf("Failed to import scan path %s: %v", path.LocalPath, err)
			continue
		}
		count++
		pathIDs[path.LocalPath] = newID
	}
	return count, pathIDs
}

// importSchedules imports schedules using the path ID mapping.
// Skips duplicates based on scan_path_id + cron_expression to prevent duplicate schedules.
func (s *RESTServer) importSchedules(ctx context.Context, schedules []importSchedule, pathIDs map[string]int64) int {
	count := 0
	for _, sched := range schedules {
		scanPathID, exists := pathIDs[sched.LocalPath]
		if !exists {
			found, err := s.scanPaths.FindIDByLocalPath(ctx, sched.LocalPath)
			if err != nil {
				logger.Errorf("Failed to find scan path for schedule (local_path=%s): %v", sched.LocalPath, err)
				continue
			}
			scanPathID = found
		}

		// Check if a schedule with the same path and cron expression already exists
		if _, err := s.schedules.FindIDByPathAndCron(ctx, scanPathID, sched.CronExpression); err == nil {
			logger.Debugf("Skipping duplicate schedule for path ID %d with cron %s", scanPathID, sched.CronExpression)
			continue
		} else if !errors.Is(err, repository.ErrNotFound) {
			logger.Errorf("Failed to check for duplicate schedule for %s: %v", sched.LocalPath, err)
			continue
		}

		if _, err := s.schedules.Create(ctx, scanPathID, sched.CronExpression, sched.Enabled); err != nil {
			logger.Errorf("Failed to import schedule for %s: %v", sched.LocalPath, err)
			continue
		}
		count++
	}
	return count
}

// importNotifications imports notification configs.
// Skips duplicates based on name to prevent creating multiple entries for the same notification.
func (s *RESTServer) importNotifications(notifications []importNotification) int {
	if s.notifier == nil {
		return 0
	}
	count := 0
	for _, notif := range notifications {
		// Check if a notification with the same name already exists
		existing, _ := s.notifier.GetAllConfigs()
		isDuplicate := false
		for _, e := range existing {
			if e.Name == notif.Name {
				logger.Debugf("Skipping duplicate notification %s", notif.Name)
				isDuplicate = true
				break
			}
		}
		if isDuplicate {
			continue
		}

		configBytes, err := jsonMarshal(notif.Config)
		if err != nil {
			logger.Errorf("Failed to marshal notification config for %s: %v", notif.Name, err)
			continue
		}

		// Boundary validation: reject unknown provider_type at the import
		// edge so the typed enum is the only thing reaching the DB.
		providerType, perr := notifier.ParseProviderType(notif.ProviderType)
		if perr != nil {
			logger.Errorf("Skipping notification %q on import: %v", notif.Name, perr)
			continue
		}

		cfg := &notifierConfig{
			Name:            notif.Name,
			ProviderType:    providerType,
			Config:          configBytes,
			Events:          notif.Events,
			Enabled:         notif.Enabled,
			ThrottleSeconds: notif.ThrottleSeconds,
		}

		if _, err := s.notifier.CreateConfig(cfg); err == nil {
			count++
		} else {
			logger.Errorf("Failed to import notification %s: %v", notif.Name, err)
		}
	}
	return count
}

// importConfig imports configuration from JSON
func (s *RESTServer) importConfig(c *gin.Context) {
	var req importConfigRequest
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}

	arrCount := s.importArrInstances(c.Request.Context(), req.ArrInstances)
	pathCount, pathIDs := s.importScanPaths(c.Request.Context(), req.ScanPaths)
	schedCount := s.importSchedules(c.Request.Context(), req.Schedules, pathIDs)
	notifCount := s.importNotifications(req.Notifications)

	// Reload path mappings and scheduler
	if s.pathMapper != nil {
		if err := s.pathMapper.Reload(); err != nil {
			logger.Errorf("Failed to reload path mappings after import: %v", err)
		}
	}
	if s.scheduler != nil {
		if err := s.scheduler.LoadSchedules(); err != nil {
			logger.Errorf("Failed to reload schedules after import: %v", err)
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Import complete",
		"imported": gin.H{
			"arr_instances": arrCount,
			"scan_paths":    pathCount,
			"schedules":     schedCount,
			"notifications": notifCount,
		},
	})
}

// downloadDatabaseBackup creates a fresh backup of the database and sends it to the client
// Uses VACUUM INTO for atomic, consistent backups that are safe during concurrent access.
func (s *RESTServer) downloadDatabaseBackup(c *gin.Context) {
	cfg := config.Get()
	dbPath := cfg.DatabasePath

	// Create backup directory if it doesn't exist
	backupDir := filepath.Join(filepath.Dir(dbPath), "backups")
	if err := os.MkdirAll(backupDir, 0700); err != nil {
		logger.Errorf("Failed to create backup directory: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create backup directory"})
		return
	}

	// Generate backup filename with timestamp
	timestamp := time.Now().Format("20060102_150405")
	backupFilename := fmt.Sprintf("healarr_backup_%s.db", timestamp)
	backupPath := filepath.Join(backupDir, backupFilename)

	// Use VACUUM INTO for atomic backup - safe during concurrent access
	// Security: backupPath is server-generated from config + timestamp, not user input.
	// SQLite VACUUM INTO does not support parameterized paths.
	_, err := s.db.Exec(fmt.Sprintf("VACUUM INTO '%s'", backupPath)) //nolint:gosec // Path is server-generated, not user input
	if err != nil {
		_ = os.Remove(backupPath)
		logger.Errorf("Failed to create backup: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create backup"})
		return
	}

	logger.Infof("Database backup created for download: %s", backupPath)

	// Send the file to the client
	c.Header("Content-Description", "File Transfer")
	c.Header("Content-Disposition", fmt.Sprintf("attachment; filename=%s", backupFilename))
	c.Header("Content-Type", "application/octet-stream")
	c.File(backupPath)

	// Clean up the temporary backup file after sending (in background).
	// Failures accumulate disk usage in the backup directory over time, so
	// log loud enough that operators notice during diagnostics.
	safego.Run("backup-cleanup", func() {
		time.Sleep(5 * time.Second) // Wait for download to complete
		if err := os.Remove(backupPath); err != nil {
			logger.Warnf("Failed to remove temporary backup file %s: %v", backupPath, err)
		}
	})
}
