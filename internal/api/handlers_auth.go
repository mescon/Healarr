package api

import (
	"database/sql"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
)

func (s *RESTServer) handleAuthSetup(c *gin.Context) {
	ctx := c.Request.Context()

	// Check if password already exists
	var exists bool
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM settings WHERE key = 'password_hash')").Scan(&exists); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": ErrMsgDatabaseError})
		return
	}

	if exists {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Setup already completed"})
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.Password) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Password must be at least 8 characters"})
		return
	}

	// Hash password
	hash, err := auth.HashPassword(req.Password)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Generate API key
	apiKey, err := auth.GenerateAPIKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate API key"})
		return
	}

	// Encrypt API key before storage
	encryptedKey, err := crypto.Encrypt(apiKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt API key"})
		return
	}

	// Store both
	_, err = s.db.Exec("INSERT INTO settings (key, value) VALUES ('password_hash', ?), ('api_key', ?)", hash, encryptedKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save settings"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "Setup complete",
		"token":   apiKey,
	})
	logger.Infof("Auth setup completed")
}

func (s *RESTServer) handleLogin(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		Password string `json:"password"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get stored hash
	var hash string
	err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'password_hash'").Scan(&hash)
	if err == sql.ErrNoRows {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Setup required"})
		return
	}
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": ErrMsgDatabaseError})
		return
	}

	// Verify password
	if !auth.CheckPasswordHash(req.Password, hash) {
		logger.Errorf("Login failed: Invalid password attempt from %s", c.ClientIP())
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid password"})
		return
	}

	// Get API key to return as session token
	var encryptedKey string
	err = s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'api_key'").Scan(&encryptedKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve API key"})
		return
	}

	// Decrypt API key
	apiKey, err := crypto.Decrypt(encryptedKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to decrypt API key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":   apiKey, // Use API key as session token for simplicity
		"message": "Login successful",
	})
	logger.Infof("User logged in successfully from %s", c.ClientIP())
}

func (s *RESTServer) handleAuthStatus(c *gin.Context) {
	ctx := c.Request.Context()

	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM settings WHERE key = 'password_hash'").Scan(&count); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": ErrMsgDatabaseError})
		return
	}

	c.JSON(http.StatusOK, gin.H{"is_setup": count > 0})
}

func (s *RESTServer) getAPIKey(c *gin.Context) {
	ctx := c.Request.Context()

	var encryptedKey string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'api_key'").Scan(&encryptedKey); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to retrieve API key"})
		return
	}

	// Decrypt API key
	apiKey, err := crypto.Decrypt(encryptedKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to decrypt API key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"api_key": apiKey})
}

func (s *RESTServer) regenerateAPIKey(c *gin.Context) {
	// Generate new API key
	newKey, err := auth.GenerateAPIKey()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to generate API key"})
		return
	}

	// Encrypt API key before storage
	encryptedKey, err := crypto.Encrypt(newKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to encrypt API key"})
		return
	}

	// Update in database
	_, err = s.db.Exec("UPDATE settings SET value = ? WHERE key = 'api_key'", encryptedKey)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update API key"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"api_key": newKey,
		"message": "API key regenerated successfully. Update your webhook URLs!",
	})
}

func (s *RESTServer) changePassword(c *gin.Context) {
	ctx := c.Request.Context()

	var req struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if len(req.NewPassword) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "New password must be at least 8 characters"})
		return
	}

	// Verify current password
	var hash string
	if err := s.db.QueryRowContext(ctx, "SELECT value FROM settings WHERE key = 'password_hash'").Scan(&hash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": ErrMsgDatabaseError})
		return
	}

	if !auth.CheckPasswordHash(req.CurrentPassword, hash) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid current password"})
		return
	}

	// Hash new password
	newHash, err := auth.HashPassword(req.NewPassword)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to hash password"})
		return
	}

	// Update in database
	_, err = s.db.Exec("UPDATE settings SET value = ? WHERE key = 'password_hash'", newHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Password changed successfully"})
}
