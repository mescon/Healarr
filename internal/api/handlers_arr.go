package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/network"
	"github.com/mescon/Healarr/internal/repository"
)

// errInvalidURLScheme is returned when a URL has an invalid scheme.
var errInvalidURLScheme = errors.New("only http and https schemes are allowed")

// formatInvalidURLError formats an error message for invalid URL responses.
func formatInvalidURLError(err error) string {
	return fmt.Sprintf("Invalid URL: %v", err)
}

// validateArrURL validates that a URL is safe to use for *arr API requests.
// It ensures:
// 1. The URL is parseable
// 2. The scheme is http or https (prevents file://, gopher://, etc.)
// 3. The host is not empty
func validateArrURL(rawURL string) error {
	// Check if URL has a scheme - provide a clear error message if not
	if !strings.HasPrefix(strings.ToLower(rawURL), "http://") && !strings.HasPrefix(strings.ToLower(rawURL), "https://") {
		return errors.New("URL must start with http:// or https://")
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// Only allow http and https schemes (redundant check but keeps defense in depth)
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errInvalidURLScheme
	}

	// Ensure host is present
	if parsed.Host == "" {
		return errors.New("URL must include a host")
	}

	// SSRF guard: when HEALARR_BLOCK_PRIVATE_TARGETS=true, refuse RFC1918 /
	// loopback / link-local / multicast destinations. No-op by default to
	// preserve the homelab use case where *arr lives on the same LAN.
	if err := network.ValidateDestination(rawURL); err != nil {
		return err
	}

	return nil
}

func (s *RESTServer) getArrInstances(c *gin.Context) {
	rows, err := s.arrInstances.ListAll(c.Request.Context())
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	instances := make([]map[string]interface{}, 0, len(rows))
	for _, inst := range rows {
		decryptedKey, err := crypto.Decrypt(inst.EncryptedAPIKey)
		if err != nil {
			logger.Errorf("Failed to decrypt API key for instance %d: %v", inst.ID, err)
			decryptedKey = "[DECRYPTION_ERROR]"
		}
		entry := map[string]interface{}{
			"id":      inst.ID,
			"name":    inst.Name,
			"type":    inst.Type,
			"url":     inst.URL,
			"api_key": decryptedKey,
			"enabled": inst.Enabled,
		}
		// Webhook secret may be NULL for legacy instances; surface a sentinel
		// so the UI can show a "no webhook secret configured — generate one"
		// affordance without confusing it for a normal value.
		if inst.EncryptedWebhookSecret.Valid && inst.EncryptedWebhookSecret.String != "" {
			decryptedSecret, derr := crypto.Decrypt(inst.EncryptedWebhookSecret.String)
			if derr != nil {
				logger.Errorf("Failed to decrypt webhook secret for instance %d: %v", inst.ID, derr)
				decryptedSecret = "[DECRYPTION_ERROR]"
			}
			entry["webhook_secret"] = decryptedSecret
		} else {
			entry["webhook_secret"] = nil
		}
		instances = append(instances, entry)
	}

	c.JSON(http.StatusOK, instances)
}

// generateInstanceName creates a human-friendly name for an *arr instance.
// Returns "Sonarr" for the first instance, "Sonarr 2" for the second, etc.
func (s *RESTServer) generateInstanceName(arrType string) string {
	// Map type to display name
	baseName := strings.TrimSuffix(strings.TrimSuffix(arrType, "-v2"), "-v3")
	displayNames := map[string]string{
		"sonarr":   "Sonarr",
		"radarr":   "Radarr",
		"whisparr": "Whisparr",
	}
	displayName, ok := displayNames[baseName]
	if !ok {
		// Fallback: capitalize first letter
		if len(baseName) > 0 {
			displayName = strings.ToUpper(baseName[:1]) + baseName[1:]
		} else {
			displayName = "Instance"
		}
	}

	// Count existing instances of this base type
	var count int
	ctx := context.Background()
	if strings.HasPrefix(arrType, "whisparr") {
		// Count all whisparr variants together
		count, _ = s.arrInstances.CountByTypePrefix(ctx, "whisparr")
	} else {
		count, _ = s.arrInstances.CountByType(ctx, arrType)
	}

	if count == 0 {
		return displayName
	}
	return fmt.Sprintf("%s %d", displayName, count+1)
}

func (s *RESTServer) createArrInstance(c *gin.Context) {
	var req struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		URL     string `json:"url"`
		APIKey  string `json:"api_key"`
		Enabled bool   `json:"enabled"`
	}
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}

	// Security: Validate URL to prevent SSRF attacks
	if err := validateArrURL(req.URL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": formatInvalidURLError(err)})
		return
	}

	// Boundary validation: reject unknown ArrType strings here so the
	// typed enum is the only thing ever persisted (closes T2 from audit).
	if _, err := integration.ParseArrType(req.Type); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Auto-generate a friendly name if not provided
	instanceName := strings.TrimSpace(req.Name)
	if instanceName == "" || strings.Contains(instanceName, "-") && len(instanceName) > 15 {
		// Generate name if empty or looks like auto-generated timestamp (e.g., "sonarr-1234567890")
		instanceName = s.generateInstanceName(req.Type)
	}

	// Encrypt API key before storage
	encryptedKey, err := crypto.Encrypt(req.APIKey)
	if err != nil {
		logger.Errorf("Failed to encrypt API key: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt API key"})
		return
	}

	// Generate a per-instance webhook secret so this instance's webhook
	// credential is decoupled from the master admin API key (Phase 1.1c).
	// We return the plaintext secret in the response so the user can paste
	// it into Sonarr/Radarr; the DB stores it encrypted.
	webhookSecret, err := auth.GenerateAPIKey()
	if err != nil {
		logger.Errorf("Failed to generate webhook secret: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate webhook secret"})
		return
	}
	encryptedSecret, err := crypto.Encrypt(webhookSecret)
	if err != nil {
		logger.Errorf("Failed to encrypt webhook secret: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt webhook secret"})
		return
	}

	id, err := s.arrInstances.Create(c.Request.Context(), repository.CreateArrInstanceParams{
		Name:                   instanceName,
		Type:                   req.Type,
		URL:                    req.URL,
		EncryptedAPIKey:        encryptedKey,
		Enabled:                req.Enabled,
		EncryptedWebhookSecret: encryptedSecret,
	})
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"id":             id,
		"webhook_secret": webhookSecret,
	})
}

// regenerateWebhookSecret generates a fresh per-instance webhook secret,
// invalidating the old one. The new secret is returned plaintext so the user
// can update their *arr webhook configuration; stored encrypted at rest.
func (s *RESTServer) regenerateWebhookSecret(c *gin.Context) {
	id := c.Param("id")
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing instance ID"})
		return
	}

	secret, err := auth.GenerateAPIKey()
	if err != nil {
		logger.Errorf("Failed to generate webhook secret: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate webhook secret"})
		return
	}
	encryptedSecret, err := crypto.Encrypt(secret)
	if err != nil {
		logger.Errorf("Failed to encrypt webhook secret: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt webhook secret"})
		return
	}

	idInt, parseErr := strconv.ParseInt(id, 10, 64)
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid instance ID"})
		return
	}
	switch err := s.arrInstances.UpdateWebhookSecret(c.Request.Context(), idInt, encryptedSecret); {
	case errors.Is(err, repository.ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "Instance not found"})
		return
	case err != nil:
		respondDatabaseError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"webhook_secret": secret})
}

func (s *RESTServer) deleteArrInstance(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid instance ID"})
		return
	}
	if err := s.arrInstances.Delete(c.Request.Context(), id); err != nil {
		respondDatabaseError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (s *RESTServer) updateArrInstance(c *gin.Context) {
	id := c.Param("id")
	var req struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		URL     string `json:"url"`
		APIKey  string `json:"api_key"`
		Enabled bool   `json:"enabled"`
	}
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}

	// Security: Validate URL to prevent SSRF attacks
	if err := validateArrURL(req.URL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": formatInvalidURLError(err)})
		return
	}

	// Encrypt API key before storage
	encryptedKey, err := crypto.Encrypt(req.APIKey)
	if err != nil {
		logger.Errorf("Failed to encrypt API key: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt API key"})
		return
	}

	idInt, parseErr := strconv.ParseInt(id, 10, 64)
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid instance ID"})
		return
	}
	if err := s.arrInstances.Update(c.Request.Context(), idInt, repository.UpdateArrInstanceParams{
		Name:            req.Name,
		Type:            req.Type,
		URL:             req.URL,
		EncryptedAPIKey: encryptedKey,
		Enabled:         req.Enabled,
	}); err != nil {
		respondDatabaseError(c, err)
		return
	}
	c.Status(http.StatusOK)
}

func (s *RESTServer) testArrConnection(c *gin.Context) {
	var req struct {
		URL    string `json:"url"`
		APIKey string `json:"api_key"`
	}
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}

	// Security: Validate URL to prevent SSRF attacks
	if err := validateArrURL(req.URL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   formatInvalidURLError(err),
		})
		return
	}

	// Create client with short timeout
	client := &http.Client{
		Timeout: 5 * time.Second,
	}

	// Try system status endpoint
	// Handle trailing slash in URL
	baseURL := req.URL
	if len(baseURL) > 0 && baseURL[len(baseURL)-1] == '/' {
		baseURL = baseURL[:len(baseURL)-1]
	}

	targetURL := fmt.Sprintf("%s/api/v3/system/status", baseURL)
	logger.Debugf("Testing connection to: %s", targetURL)

	httpReq, err := http.NewRequest("GET", targetURL, nil) // #nosec G107 -- URL is validated above
	if err != nil {
		logger.Debugf("testArrConnection create request error: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"error":   "Failed to create connection request",
		})
		return
	}
	httpReq.Header.Set("X-Api-Key", req.APIKey)

	resp, err := client.Do(httpReq)
	if err != nil {
		// Don't echo the underlying error; that lets an attacker probe internal
		// hosts via the failure message ("connection refused on 10.0.0.5:22").
		logger.Debugf("Connection test failed: %v", err)
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"error":   "Connection failed",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Don't echo the upstream status; it lets an attacker fingerprint
		// what kind of service is at the destination.
		logger.Debugf("Connection test returned status: %d", resp.StatusCode)
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"error":   "Server did not respond with a successful status",
		})
		return
	}

	logger.Debugf("Connection test successful for %s", baseURL)
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Connection successful",
	})
}

// getArrRootFolders returns the root folders configured in a *arr instance.
// These are the library paths (e.g., /data/media/Movies) that can be used as scan paths.
func (s *RESTServer) getArrRootFolders(c *gin.Context) {
	idStr := c.Param("id")
	var instanceID int64
	if _, err := fmt.Sscanf(idStr, "%d", &instanceID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid instance ID"})
		return
	}

	if s.arrClient == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Arr client not available"})
		return
	}

	folders, err := s.arrClient.GetRootFolders(instanceID)
	if err != nil {
		logger.Errorf("Failed to get root folders for instance %d: %v", instanceID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("Failed to get root folders: %v", err)})
		return
	}

	// Convert to response format with additional metadata
	type rootFolderResponse struct {
		ID         int64  `json:"id"`
		Path       string `json:"path"`
		FreeSpace  int64  `json:"free_space"`
		TotalSpace int64  `json:"total_space"`
	}

	response := make([]rootFolderResponse, len(folders))
	for i, folder := range folders {
		response[i] = rootFolderResponse{
			ID:         folder.ID,
			Path:       folder.Path,
			FreeSpace:  folder.FreeSpace,
			TotalSpace: folder.TotalSpace,
		}
	}

	c.JSON(http.StatusOK, response)
}
