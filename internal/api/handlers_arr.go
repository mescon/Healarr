package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
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
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	// Only allow http and https schemes
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return errInvalidURLScheme
	}

	// Ensure host is present
	if parsed.Host == "" {
		return errors.New("URL must include a host")
	}

	return nil
}

func (s *RESTServer) getArrInstances(c *gin.Context) {
	rows, err := s.db.Query("SELECT id, name, type, url, api_key, enabled FROM arr_instances")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	instances := make([]map[string]interface{}, 0)
	for rows.Next() {
		var id int
		var name, arrType, url, apiKey string
		var enabled bool
		if err := rows.Scan(&id, &name, &arrType, &url, &apiKey, &enabled); err != nil {
			logger.Warnf("Failed to scan arr_instances row: %v", err)
			continue
		}
		// Decrypt API key for display
		decryptedKey, err := crypto.Decrypt(apiKey)
		if err != nil {
			logger.Errorf("Failed to decrypt API key for instance %d: %v", id, err)
			decryptedKey = "[DECRYPTION_ERROR]"
		}
		instances = append(instances, map[string]interface{}{
			"id":      id,
			"name":    name,
			"type":    arrType,
			"url":     url,
			"api_key": decryptedKey,
			"enabled": enabled,
		})
	}

	if err := rows.Err(); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Error reading arr instances"})
		logger.Errorf("Error iterating arr instances: %v", err)
		return
	}

	c.JSON(http.StatusOK, instances)
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

	_, err = s.db.Exec("INSERT INTO arr_instances (name, type, url, api_key, enabled) VALUES (?, ?, ?, ?, ?)",
		req.Name, req.Type, req.URL, encryptedKey, req.Enabled)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Status(http.StatusCreated)
}

func (s *RESTServer) deleteArrInstance(c *gin.Context) {
	id := c.Param("id")
	_, err := s.db.Exec("DELETE FROM arr_instances WHERE id = ?", id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

	_, err = s.db.Exec("UPDATE arr_instances SET name = ?, type = ?, url = ?, api_key = ?, enabled = ? WHERE id = ?",
		req.Name, req.Type, req.URL, encryptedKey, req.Enabled, id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
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
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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

	targetURL := fmt.Sprintf("%s/api/v3/system/status?apikey=%s", baseURL, req.APIKey)
	logger.Debugf("Testing connection to: %s/api/v3/system/status", baseURL)

	resp, err := client.Get(targetURL) // #nosec G107 -- URL is validated above
	if err != nil {
		logger.Debugf("Connection test failed: %v", err)
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Connection failed: %v", err),
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		logger.Debugf("Connection test returned status: %d", resp.StatusCode)
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   fmt.Sprintf("Server returned status %d", resp.StatusCode),
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
