package api

import (
	"crypto/subtle"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/safego"
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

	// Get instance ID from URL parameter (needed up-front because the
	// per-instance webhook secret is the authoritative credential).
	instanceIDStr := c.Param("instance_id")
	instanceID, err := strconv.ParseInt(instanceIDStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid instance ID"})
		return
	}

	// Load the instance: enabled flag + (encrypted) per-instance webhook secret
	// if one has been generated. Missing webhook_secret column on legacy
	// rows scans into a NULL sql.NullString.
	instance, err := s.arrInstances.GetByID(c.Request.Context(), instanceID)
	if err != nil {
		logger.Errorf("Webhook rejected: Instance %d not found", instanceID)
		c.JSON(http.StatusNotFound, gin.H{"error": "Instance not found"})
		return
	}
	webhookSecret := instance.EncryptedWebhookSecret

	if !instance.Enabled {
		logger.Infof("Webhook rejected: Instance %d is disabled", instanceID)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "This *arr instance is currently disabled",
			"message": "Enable this instance in the Config page to process webhooks",
		})
		return
	}

	// Authentication path (Phase 1.1c):
	//
	// If the instance has a per-instance webhook_secret, that's the ONLY
	// credential accepted. The master API key no longer works for instances
	// that have moved to per-instance secrets — that's the whole point of
	// the separation.
	//
	// If webhook_secret is NULL (legacy instance created before this
	// migration), fall back to master API key. The UI nudges users to
	// generate a secret to close the gap.
	if webhookSecret.Valid && webhookSecret.String != "" {
		decryptedSecret, decErr := crypto.Decrypt(webhookSecret.String)
		if decErr != nil {
			logger.Errorf("Failed to decrypt webhook secret for instance %d: %v", instanceID, decErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication error"})
			return
		}
		if subtle.ConstantTimeCompare([]byte(apiKey), []byte(decryptedSecret)) != 1 {
			logger.Debugf("Webhook rejected: Invalid webhook secret for instance %d", instanceID)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid webhook secret"})
			return
		}
	} else {
		// Legacy fallback: validate against the master API key.
		var storedKey string
		if err := s.db.QueryRow("SELECT value FROM settings WHERE key = 'api_key'").Scan(&storedKey); err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication error"})
			return
		}
		decryptedKey, decErr := crypto.Decrypt(storedKey)
		if decErr != nil {
			logger.Errorf("Failed to decrypt API key: %v", decErr)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication error"})
			return
		}
		if subtle.ConstantTimeCompare([]byte(apiKey), []byte(decryptedKey)) != 1 {
			logger.Debugf("Webhook rejected: Invalid API key (legacy instance %d without webhook_secret)", instanceID)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			return
		}
		logger.Warnf("Webhook for instance %d authenticated via master API key — generate a per-instance webhook secret to close this gap", instanceID)
	}

	var req WebhookRequest
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
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

	// Trigger single file scan. On failure, publish a ScanFailed event so
	// the UI can surface the issue — previously the HTTP 202 "Scan queued"
	// response promised work that silently died, with the failure only
	// visible in backend logs.
	safego.Run("webhook-scan", func() {
		if err := s.scanner.ScanFile(localPath); err != nil {
			logger.Warnf("Webhook-triggered scan failed for %s: %v", localPath, err)
			pubErr := s.eventBus.Publish(domain.Event{
				AggregateType: "scan",
				// No scan_id yet — ScanFile failed before one was assigned.
				// Use the file path as the aggregate ID so duplicate failures
				// collate in the timeline and clients can index by path.
				AggregateID: "webhook:" + localPath,
				EventType:   domain.ScanFailed,
				EventData: map[string]interface{}{
					"file_path": localPath,
					"trigger":   "webhook",
					"error":     err.Error(),
				},
			})
			if pubErr != nil {
				logger.Errorf("Failed to publish ScanFailed event for webhook scan: %v", pubErr)
			}
		}
	})

	c.JSON(http.StatusOK, gin.H{"message": "Scan queued", "local_path": localPath})
}
