package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/auth"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/repository"
)

// defaultSessionTTL is how long a newly issued session token remains valid.
// Long enough for normal browser use without daily re-logins; short enough
// that a leaked token expires automatically if logout was missed.
const defaultSessionTTL = 24 * time.Hour

func (s *RESTServer) handleAuthSetup(c *gin.Context) {
	ctx := c.Request.Context()

	// Check if password already exists
	exists, err := s.settings.Exists(ctx, repository.SettingKeyPasswordHash)
	if err != nil {
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
		respondBadRequest(c, err)
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

	// Store both in one transaction so the user never ends up with one
	// half (password but no api_key, or vice versa).
	if err := s.settings.SetMany(ctx, map[string]string{
		repository.SettingKeyPasswordHash: hash,
		repository.SettingKeyAPIKey:       encryptedKey,
	}); err != nil {
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
		respondBadRequest(c, err)
		return
	}

	// Get stored hash
	hash, err := s.settings.Get(ctx, repository.SettingKeyPasswordHash)
	if errors.Is(err, repository.ErrNotFound) {
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

	// Issue a per-login session token rather than returning the master API
	// key. Sessions live in their own table with a 24h expiry and can be
	// invalidated via logout, decoupled from the long-lived integration key
	// used by Sonarr/Radarr webhooks (Phase 1.3 P1 finding).
	token, err := auth.GenerateAPIKey()
	if err != nil {
		logger.Errorf("Login: failed to generate session token: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
		return
	}
	session, err := s.sessions.Create(ctx, token, defaultSessionTTL, c.Request.UserAgent(), c.ClientIP())
	if err != nil {
		logger.Errorf("Login: failed to persist session: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create session"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"token":      session.Token,
		"expires_at": session.ExpiresAt.Format(time.RFC3339),
		"message":    "Login successful",
	})
	logger.Infof("User logged in successfully from %s", c.ClientIP())
}

func (s *RESTServer) handleAuthStatus(c *gin.Context) {
	ctx := c.Request.Context()

	isSetup, err := s.settings.Exists(ctx, repository.SettingKeyPasswordHash)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": ErrMsgDatabaseError})
		return
	}

	c.JSON(http.StatusOK, gin.H{"is_setup": isSetup})
}

func (s *RESTServer) getAPIKey(c *gin.Context) {
	ctx := c.Request.Context()

	encryptedKey, err := s.settings.Get(ctx, repository.SettingKeyAPIKey)
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
	if err := s.settings.Set(c.Request.Context(), repository.SettingKeyAPIKey, encryptedKey); err != nil {
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
		respondBadRequest(c, err)
		return
	}

	if len(req.NewPassword) < 8 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "New password must be at least 8 characters"})
		return
	}

	// Verify current password
	hash, err := s.settings.Get(ctx, repository.SettingKeyPasswordHash)
	if err != nil {
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
	if err := s.settings.Set(ctx, repository.SettingKeyPasswordHash, newHash); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update password"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Password changed successfully"})
}
