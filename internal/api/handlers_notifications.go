package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/notifier"
)

// requireNotifier checks if the notifier is available, returning false and sending error if not
func (s *RESTServer) requireNotifier(c *gin.Context) bool {
	if s.notifier == nil {
		respondServiceUnavailable(c, "Notification service")
		return false
	}
	return true
}

func (s *RESTServer) getNotifications(c *gin.Context) {
	if !s.requireNotifier(c) {
		return
	}

	configs, err := s.notifier.GetAllConfigs()
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	c.JSON(http.StatusOK, configs)
}

func (s *RESTServer) createNotification(c *gin.Context) {
	if !s.requireNotifier(c) {
		return
	}

	var req notifier.NotificationConfig
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err, true)
		return
	}

	// Validate and set defaults for ThrottleSeconds (1-3600 seconds)
	if req.ThrottleSeconds <= 0 || req.ThrottleSeconds > 3600 {
		req.ThrottleSeconds = 5
	}

	id, err := s.notifier.CreateConfig(&req)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "Notification created"})
}

func (s *RESTServer) updateNotification(c *gin.Context) {
	if !s.requireNotifier(c) {
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgInvalidID})
		return
	}

	var req notifier.NotificationConfig
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err, true)
		return
	}
	req.ID = id

	if err := s.notifier.UpdateConfig(&req); err != nil {
		respondDatabaseError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Notification updated"})
}

func (s *RESTServer) deleteNotification(c *gin.Context) {
	if !s.requireNotifier(c) {
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgInvalidID})
		return
	}

	if err := s.notifier.DeleteConfig(id); err != nil {
		respondDatabaseError(c, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Notification deleted"})
}

func (s *RESTServer) testNotification(c *gin.Context) {
	if !s.requireNotifier(c) {
		return
	}

	var req notifier.NotificationConfig
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err, true)
		return
	}

	if err := s.notifier.SendTestNotification(&req); err != nil {
		c.JSON(http.StatusOK, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Test notification sent successfully",
	})
}

func (s *RESTServer) getNotificationEvents(c *gin.Context) {
	groups := notifier.GetEventGroups()
	c.JSON(http.StatusOK, groups)
}

func (s *RESTServer) getNotificationLog(c *gin.Context) {
	if !s.requireNotifier(c) {
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgInvalidID})
		return
	}

	// Use parseInt helper with bounds checking (consistent with pagination.go)
	limit := parseInt(c.DefaultQuery("limit", "50"), 50)
	if limit < 1 || limit > 500 {
		limit = 50
	}
	entries, err := s.notifier.GetNotificationLog(id, limit)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	c.JSON(http.StatusOK, entries)
}

// getNotification returns a single notification config
func (s *RESTServer) getNotification(c *gin.Context) {
	if !s.requireNotifier(c) {
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgInvalidID})
		return
	}

	cfg, err := s.notifier.GetConfig(id)
	if err != nil {
		respondNotFound(c, "Notification")
		return
	}

	c.JSON(http.StatusOK, cfg)
}
