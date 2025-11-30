package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/notifier"
)

func (s *RESTServer) getNotifications(c *gin.Context) {
	if s.notifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Notification service not initialized"})
		return
	}

	configs, err := s.notifier.GetAllConfigs()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, configs)
}

func (s *RESTServer) createNotification(c *gin.Context) {
	if s.notifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Notification service not initialized"})
		return
	}

	var req notifier.NotificationConfig
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Set defaults
	if req.ThrottleSeconds == 0 {
		req.ThrottleSeconds = 5
	}

	id, err := s.notifier.CreateConfig(&req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"id": id, "message": "Notification created"})
}

func (s *RESTServer) updateNotification(c *gin.Context) {
	if s.notifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Notification service not initialized"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	var req notifier.NotificationConfig
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	req.ID = id

	if err := s.notifier.UpdateConfig(&req); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Notification updated"})
}

func (s *RESTServer) deleteNotification(c *gin.Context) {
	if s.notifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Notification service not initialized"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	if err := s.notifier.DeleteConfig(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Notification deleted"})
}

func (s *RESTServer) testNotification(c *gin.Context) {
	if s.notifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Notification service not initialized"})
		return
	}

	var req notifier.NotificationConfig
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
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
	if s.notifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Notification service not initialized"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	entries, err := s.notifier.GetNotificationLog(id, limit)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, entries)
}

// getNotification returns a single notification config
func (s *RESTServer) getNotification(c *gin.Context) {
	if s.notifier == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Notification service not initialized"})
		return
	}

	idStr := c.Param("id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	cfg, err := s.notifier.GetConfig(id)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Notification not found"})
		return
	}

	c.JSON(http.StatusOK, cfg)
}
