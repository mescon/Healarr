package api

import (
	"database/sql"
	"encoding/json"
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
	_, err := s.db.Exec(`
		INSERT INTO settings (key, value, updated_at) VALUES ('base_path', ?, datetime('now'))
		ON CONFLICT(key) DO UPDATE SET value = ?, updated_at = datetime('now')
	`, basePath, basePath)
	if err != nil {
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
	go func() {
		time.Sleep(500 * time.Millisecond)
		logger.Infof("Initiating server restart...")

		// Platform-specific restart (see restart_unix.go and restart_windows.go)
		restartProcess()
	}()
}

// exportArrInstances exports arr instances from the database.
func (s *RESTServer) exportArrInstances() []gin.H {
	rows, err := s.db.Query("SELECT name, type, url, api_key, enabled FROM arr_instances")
	if err != nil {
		logger.Debugf("Failed to query arr instances for export: %v", err)
		return nil
	}
	defer rows.Close()

	var instances []gin.H
	for rows.Next() {
		var name, arrType, url, encryptedKey string
		var enabled bool
		if err := rows.Scan(&name, &arrType, &url, &encryptedKey, &enabled); err != nil {
			logger.Errorf("Failed to scan arr instance for export: %v", err)
			continue
		}
		decryptedKey, err := crypto.Decrypt(encryptedKey)
		if err != nil {
			logger.Errorf("Failed to decrypt API key for export: %v", err)
			decryptedKey = "[DECRYPTION_ERROR]"
		}
		instances = append(instances, gin.H{
			"name": name, "type": arrType, "url": url, "api_key": decryptedKey, "enabled": enabled,
		})
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Error iterating arr instances for export: %v", err)
	}
	return instances
}

// exportScanPaths exports scan paths from the database.
func (s *RESTServer) exportScanPaths() []gin.H {
	rows, err := s.db.Query(`SELECT local_path, arr_path, arr_instance_id, enabled, auto_remediate, dry_run,
		detection_method, detection_args, detection_mode, max_retries, verification_timeout_hours
		FROM scan_paths`)
	if err != nil {
		logger.Debugf("Failed to query scan paths for export: %v", err)
		return nil
	}
	defer rows.Close()

	var paths []gin.H
	for rows.Next() {
		var localPath, arrPath, detectionMethod, detectionMode string
		var arrInstanceID sql.NullInt64
		var enabled, autoRemediate, dryRun bool
		var detectionArgs sql.NullString
		var maxRetries int
		var verificationTimeout sql.NullInt64
		if err := rows.Scan(&localPath, &arrPath, &arrInstanceID, &enabled, &autoRemediate, &dryRun,
			&detectionMethod, &detectionArgs, &detectionMode, &maxRetries, &verificationTimeout); err != nil {
			logger.Errorf("Failed to scan path for export: %v", err)
			continue
		}
		path := gin.H{
			"local_path": localPath, "arr_path": arrPath, "enabled": enabled,
			"auto_remediate": autoRemediate, "dry_run": dryRun, "detection_method": detectionMethod,
			"detection_mode": detectionMode, "max_retries": maxRetries,
		}
		if arrInstanceID.Valid {
			path["arr_instance_id"] = arrInstanceID.Int64
		}
		if detectionArgs.Valid && detectionArgs.String != "" {
			path["detection_args"] = detectionArgs.String
		}
		if verificationTimeout.Valid {
			path["verification_timeout_hours"] = verificationTimeout.Int64
		}
		paths = append(paths, path)
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Error iterating scan paths for export: %v", err)
	}
	return paths
}

// exportSchedules exports scan schedules from the database.
func (s *RESTServer) exportSchedules() []gin.H {
	rows, err := s.db.Query(`
		SELECT ss.cron_expression, ss.enabled, sp.local_path
		FROM scan_schedules ss
		JOIN scan_paths sp ON ss.scan_path_id = sp.id
	`)
	if err != nil {
		logger.Debugf("Failed to query schedules for export: %v", err)
		return nil
	}
	defer rows.Close()

	var schedules []gin.H
	for rows.Next() {
		var cronExpr, localPath string
		var enabled bool
		if err := rows.Scan(&cronExpr, &enabled, &localPath); err != nil {
			logger.Errorf("Failed to scan schedule for export: %v", err)
			continue
		}
		schedules = append(schedules, gin.H{
			"local_path": localPath, "cron_expression": cronExpr, "enabled": enabled,
		})
	}
	if err := rows.Err(); err != nil {
		logger.Errorf("Error iterating schedules for export: %v", err)
	}
	return schedules
}

// exportNotifications exports notification configs.
func (s *RESTServer) exportNotifications() []gin.H {
	if s.notifier == nil {
		return nil
	}
	configs, err := s.notifier.GetAllConfigs()
	if err != nil {
		return nil
	}
	var notifConfigs []gin.H
	for _, cfg := range configs {
		notifConfigs = append(notifConfigs, gin.H{
			"name": cfg.Name, "provider_type": cfg.ProviderType,
			"config": cfg.Config, "events": cfg.Events,
			"enabled": cfg.Enabled, "throttle_seconds": cfg.ThrottleSeconds,
		})
	}
	return notifConfigs
}

// exportConfig exports all configuration as JSON
func (s *RESTServer) exportConfig(c *gin.Context) {
	export := gin.H{
		"exported_at": time.Now().Format(time.RFC3339),
		"version":     config.Version,
	}

	if instances := s.exportArrInstances(); instances != nil {
		export["arr_instances"] = instances
	}
	if paths := s.exportScanPaths(); paths != nil {
		export["scan_paths"] = paths
	}
	if schedules := s.exportSchedules(); schedules != nil {
		export["schedules"] = schedules
	}
	if notifications := s.exportNotifications(); notifications != nil {
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
	LocalPath                string `json:"local_path"`
	ArrPath                  string `json:"arr_path"`
	ArrInstanceID            *int   `json:"arr_instance_id"`
	Enabled                  bool   `json:"enabled"`
	AutoRemediate            bool   `json:"auto_remediate"`
	DryRun                   bool   `json:"dry_run"`
	DetectionMethod          string `json:"detection_method"`
	DetectionArgs            string `json:"detection_args"`
	DetectionMode            string `json:"detection_mode"`
	MaxRetries               int    `json:"max_retries"`
	VerificationTimeoutHours *int   `json:"verification_timeout_hours"`
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
func (s *RESTServer) importArrInstances(instances []importArrInstance) int {
	count := 0
	for _, inst := range instances {
		// Check if an instance with the same URL already exists
		var existingID int
		err := s.db.QueryRow("SELECT id FROM arr_instances WHERE url = ?", inst.URL).Scan(&existingID)
		if err == nil {
			// Instance already exists, skip
			logger.Debugf("Skipping duplicate arr instance with URL %s (existing ID: %d)", inst.URL, existingID)
			continue
		}

		encryptedKey, err := crypto.Encrypt(inst.APIKey)
		if err != nil {
			logger.Errorf("Failed to encrypt API key for import: %v", err)
			continue
		}
		_, err = s.db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
			inst.Name, inst.Type, inst.URL, encryptedKey, inst.Enabled)
		if err == nil {
			count++
		} else {
			logger.Errorf("Failed to import arr instance %s: %v", inst.Name, err)
		}
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

// importScanPaths imports scan paths and returns count and path ID mapping.
// Skips duplicates based on local_path to prevent creating multiple entries for the same path.
func (s *RESTServer) importScanPaths(paths []importScanPath) (int, map[string]int64) {
	count := 0
	pathIDs := make(map[string]int64)
	for i := range paths {
		path := &paths[i]

		// Check if a scan path with the same local_path already exists
		var existingID int64
		err := s.db.QueryRow("SELECT id FROM scan_paths WHERE local_path = ?", path.LocalPath).Scan(&existingID)
		if err == nil {
			// Path already exists, add to mapping but don't count as imported
			logger.Debugf("Skipping duplicate scan path %s (existing ID: %d)", path.LocalPath, existingID)
			pathIDs[path.LocalPath] = existingID
			continue
		}

		normalizeScanPathDefaults(path)

		result, err := s.db.Exec(`INSERT INTO scan_paths
			(local_path, arr_path, arr_instance_id, enabled, auto_remediate, dry_run, detection_method, detection_args, detection_mode, max_retries, verification_timeout_hours)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			path.LocalPath, path.ArrPath, path.ArrInstanceID, path.Enabled, path.AutoRemediate, path.DryRun,
			path.DetectionMethod, path.DetectionArgs, path.DetectionMode, path.MaxRetries, path.VerificationTimeoutHours)
		if err == nil {
			count++
			if newID, idErr := result.LastInsertId(); idErr == nil {
				pathIDs[path.LocalPath] = newID
			} else {
				logger.Warnf("Failed to get ID for imported scan path %s: %v (schedule import will use DB lookup)", path.LocalPath, idErr)
			}
		} else {
			logger.Errorf("Failed to import scan path %s: %v", path.LocalPath, err)
		}
	}
	return count, pathIDs
}

// importSchedules imports schedules using the path ID mapping.
// Skips duplicates based on scan_path_id + cron_expression to prevent duplicate schedules.
func (s *RESTServer) importSchedules(schedules []importSchedule, pathIDs map[string]int64) int {
	count := 0
	for _, sched := range schedules {
		scanPathID, exists := pathIDs[sched.LocalPath]
		if !exists {
			row := s.db.QueryRow("SELECT id FROM scan_paths WHERE local_path = ?", sched.LocalPath)
			if err := row.Scan(&scanPathID); err != nil {
				logger.Errorf("Failed to find scan path for schedule (local_path=%s): %v", sched.LocalPath, err)
				continue
			}
		}

		// Check if a schedule with the same path and cron expression already exists
		var existingID int
		err := s.db.QueryRow("SELECT id FROM scan_schedules WHERE scan_path_id = ? AND cron_expression = ?",
			scanPathID, sched.CronExpression).Scan(&existingID)
		if err == nil {
			logger.Debugf("Skipping duplicate schedule for path ID %d with cron %s", scanPathID, sched.CronExpression)
			continue
		}

		_, err = s.db.Exec("INSERT INTO scan_schedules (scan_path_id, cron_expression, enabled) VALUES (?, ?, ?)",
			scanPathID, sched.CronExpression, sched.Enabled)
		if err == nil {
			count++
		} else {
			logger.Errorf("Failed to import schedule for %s: %v", sched.LocalPath, err)
		}
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

		cfg := &notifierConfig{
			Name:            notif.Name,
			ProviderType:    notif.ProviderType,
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	arrCount := s.importArrInstances(req.ArrInstances)
	pathCount, pathIDs := s.importScanPaths(req.ScanPaths)
	schedCount := s.importSchedules(req.Schedules, pathIDs)
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

	// Clean up the temporary backup file after sending (in background)
	go func() {
		time.Sleep(5 * time.Second) // Wait for download to complete
		if err := os.Remove(backupPath); err != nil {
			logger.Debugf("Failed to remove temporary backup file %s: %v", backupPath, err)
		}
	}()
}
