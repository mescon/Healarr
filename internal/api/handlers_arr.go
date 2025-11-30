package api

import (
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
)

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

	resp, err := client.Get(targetURL)
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
