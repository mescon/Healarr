package api

import (
	"crypto/subtle"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
)

// WebhookRequest represents the payload from Sonarr/Radarr
type WebhookRequest struct {
	EventType string `json:"eventType"` // Download, Upgrade, etc.
	Series    struct {
		Path string `json:"path"`
	} `json:"series"`
	Movie struct {
		Path string `json:"path"`
	} `json:"movie"`
	EpisodeFile struct {
		Path string `json:"path"`
	} `json:"episodeFile"`
	MovieFile struct {
		Path string `json:"path"`
	} `json:"movieFile"`
}

func (s *RESTServer) handleWebhook(c *gin.Context) {
	// Validate API key (from query param or header for Sonarr/Radarr compatibility)
	apiKey := c.Query("apikey")
	if apiKey == "" {
		apiKey = c.GetHeader("X-API-Key")
	}

	if apiKey == "" {
		logger.Debugf("Webhook rejected: Missing API key")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "API key required"})
		return
	}

	// Verify API key - need to decrypt stored key for comparison
	var storedKey string
	err := s.db.QueryRow("SELECT value FROM settings WHERE key = 'api_key'").Scan(&storedKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication error"})
		return
	}

	// Decrypt stored key if encrypted
	decryptedKey, err := crypto.Decrypt(storedKey)
	if err != nil {
		logger.Errorf("Failed to decrypt API key: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication error"})
		return
	}

	// Use constant-time comparison to prevent timing attacks
	if subtle.ConstantTimeCompare([]byte(apiKey), []byte(decryptedKey)) != 1 {
		logger.Debugf("Webhook rejected: Invalid API key")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
		return
	}

	// Get instance ID from URL parameter
	instanceIDStr := c.Param("instance_id")
	instanceID, err := strconv.ParseInt(instanceIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid instance ID"})
		return
	}

	// Verify instance exists and is enabled
	var enabled bool
	err = s.db.QueryRow("SELECT enabled FROM arr_instances WHERE id = ?", instanceID).Scan(&enabled)
	if err != nil {
		logger.Errorf("Webhook rejected: Instance %d not found", instanceID)
		c.JSON(http.StatusNotFound, gin.H{"error": "Instance not found"})
		return
	}

	if !enabled {
		logger.Infof("Webhook rejected: Instance %d is disabled", instanceID)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "This *arr instance is currently disabled",
			"message": "Enable this instance in the Config page to process webhooks",
		})
		return
	}

	var req WebhookRequest
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Determine file path
	var filePath string
	if req.EpisodeFile.Path != "" {
		filePath = req.EpisodeFile.Path
	} else if req.MovieFile.Path != "" {
		filePath = req.MovieFile.Path
	}

	if filePath == "" {
		c.JSON(http.StatusOK, gin.H{"message": "Ignored: No file path"})
		return
	}

	// Map to local path
	localPath, err := s.pathMapper.ToLocalPath(filePath)
	if err != nil {
		// Log error so user can identify configuration issues
		logger.Errorf("Webhook path mapping failed: *arr reported path '%s' but no matching scan path found. Configure a scan path in /config to monitor this directory.", filePath)
		c.JSON(http.StatusOK, gin.H{"message": "Ignored: Path not mapped", "path": filePath, "error": "No matching scan path configured. Please add this path in Config > Scan Paths."})
		return
	}

	// Trigger single file scan
	go func() {
		if err := s.scanner.ScanFile(localPath); err != nil {
			logger.Warnf("Webhook-triggered scan failed for %s: %v", localPath, err)
		}
	}()

	c.JSON(http.StatusOK, gin.H{"message": "Scan queued", "local_path": localPath})
}
