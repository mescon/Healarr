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
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
)

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

// exportConfig exports all configuration as JSON
func (s *RESTServer) exportConfig(c *gin.Context) {
	export := gin.H{
		"exported_at": time.Now().Format(time.RFC3339),
		"version":     config.Version,
	}

	// Export arr instances (without IDs - they'll be regenerated on import)
	arrRows, _ := s.db.Query("SELECT name, type, url, api_key, enabled FROM arr_instances")
	if arrRows != nil {
		defer arrRows.Close()
		var instances []gin.H
		for arrRows.Next() {
			var name, arrType, url, encryptedKey string
			var enabled bool
			if err := arrRows.Scan(&name, &arrType, &url, &encryptedKey, &enabled); err != nil {
				logger.Errorf("Failed to scan arr instance for export: %v", err)
				continue
			}
			// Decrypt API key for export (so it can be imported elsewhere)
			decryptedKey, err := crypto.Decrypt(encryptedKey)
			if err != nil {
				logger.Errorf("Failed to decrypt API key for export: %v", err)
				decryptedKey = "[DECRYPTION_ERROR]"
			}
			instances = append(instances, gin.H{
				"name": name, "type": arrType, "url": url, "api_key": decryptedKey, "enabled": enabled,
			})
		}
		export["arr_instances"] = instances
	}

	// Export scan paths
	pathRows, _ := s.db.Query(`SELECT local_path, arr_path, arr_instance_id, enabled, auto_remediate, dry_run,
		detection_method, detection_args, detection_mode, max_retries, verification_timeout_hours
		FROM scan_paths`)
	if pathRows != nil {
		defer pathRows.Close()
		var paths []gin.H
		for pathRows.Next() {
			var localPath, arrPath, detectionMethod, detectionMode string
			var arrInstanceID sql.NullInt64
			var enabled, autoRemediate, dryRun bool
			var detectionArgs sql.NullString
			var maxRetries int
			var verificationTimeout sql.NullInt64
			if err := pathRows.Scan(&localPath, &arrPath, &arrInstanceID, &enabled, &autoRemediate, &dryRun,
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
		export["scan_paths"] = paths
	}

	// Export schedules
	schedRows, _ := s.db.Query("SELECT scan_path_id, cron_expression, enabled FROM scan_schedules")
	if schedRows != nil {
		defer schedRows.Close()
		var schedules []gin.H
		for schedRows.Next() {
			var scanPathID int
			var cronExpr string
			var enabled bool
			if err := schedRows.Scan(&scanPathID, &cronExpr, &enabled); err != nil {
				logger.Errorf("Failed to scan schedule for export: %v", err)
				continue
			}
			schedules = append(schedules, gin.H{
				"scan_path_id": scanPathID, "cron_expression": cronExpr, "enabled": enabled,
			})
		}
		export["schedules"] = schedules
	}

	// Export notification configs
	if s.notifier != nil {
		if configs, err := s.notifier.GetAllConfigs(); err == nil {
			var notifConfigs []gin.H
			for _, cfg := range configs {
				notifConfigs = append(notifConfigs, gin.H{
					"name": cfg.Name, "provider_type": cfg.ProviderType,
					"config": cfg.Config, "events": cfg.Events,
					"enabled": cfg.Enabled, "throttle_seconds": cfg.ThrottleSeconds,
				})
			}
			export["notifications"] = notifConfigs
		}
	}

	c.JSON(http.StatusOK, export)
}

// importConfig imports configuration from JSON
func (s *RESTServer) importConfig(c *gin.Context) {
	var req struct {
		ArrInstances []struct {
			Name    string `json:"name"`
			Type    string `json:"type"`
			URL     string `json:"url"`
			APIKey  string `json:"api_key"`
			Enabled bool   `json:"enabled"`
		} `json:"arr_instances"`
		ScanPaths []struct {
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
		} `json:"scan_paths"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	imported := gin.H{"arr_instances": 0, "scan_paths": 0}

	for _, inst := range req.ArrInstances {
		// Encrypt API key before storage
		encryptedKey, err := crypto.Encrypt(inst.APIKey)
		if err != nil {
			logger.Errorf("Failed to encrypt API key for import: %v", err)
			continue
		}
		_, err = s.db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
			inst.Name, inst.Type, inst.URL, encryptedKey, inst.Enabled)
		if err == nil {
			imported["arr_instances"] = imported["arr_instances"].(int) + 1
		} else {
			logger.Errorf("Failed to import arr instance %s: %v", inst.Name, err)
		}
	}

	for _, path := range req.ScanPaths {
		method := path.DetectionMethod
		if method == "" {
			method = "ffprobe"
		}
		mode := path.DetectionMode
		if mode == "" {
			mode = "quick"
		}
		retries := path.MaxRetries
		if retries == 0 {
			retries = config.Get().DefaultMaxRetries
		}
		// If arr_path is empty, assume same path as local (no mapping needed)
		arrPath := path.ArrPath
		if arrPath == "" {
			arrPath = path.LocalPath
		}

		_, err := s.db.Exec(`INSERT INTO scan_paths
			(local_path, arr_path, arr_instance_id, enabled, auto_remediate, dry_run, detection_method, detection_args, detection_mode, max_retries, verification_timeout_hours)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			path.LocalPath, arrPath, path.ArrInstanceID, path.Enabled, path.AutoRemediate, path.DryRun,
			method, path.DetectionArgs, mode, retries, path.VerificationTimeoutHours)
		if err == nil {
			imported["scan_paths"] = imported["scan_paths"].(int) + 1
		} else {
			logger.Errorf("Failed to import scan path %s: %v", path.LocalPath, err)
		}
	}

	s.pathMapper.Reload()
	c.JSON(http.StatusOK, gin.H{"message": "Import complete", "imported": imported})
}

// downloadDatabaseBackup creates a fresh backup of the database and sends it to the client
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

	// Force a WAL checkpoint to ensure all data is in the main database file
	_, err := s.db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	if err != nil {
		logger.Debugf("WAL checkpoint failed (might not be in WAL mode): %v", err)
	}

	// Copy the database file
	srcFile, err := os.Open(dbPath)
	if err != nil {
		logger.Errorf("Failed to open source database: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open database"})
		return
	}
	defer srcFile.Close()

	dstFile, err := os.Create(backupPath)
	if err != nil {
		logger.Errorf("Failed to create backup file: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create backup file"})
		return
	}

	_, err = io.Copy(dstFile, srcFile)
	dstFile.Close() // Close before sending
	if err != nil {
		os.Remove(backupPath)
		logger.Errorf("Failed to copy database: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to copy database"})
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
		os.Remove(backupPath)
	}()
}
