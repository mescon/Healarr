package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (s *RESTServer) getSchedules(c *gin.Context) {
	rows, err := s.db.Query(`
		SELECT s.id, s.scan_path_id, p.local_path, s.cron_expression, s.enabled
		FROM scan_schedules s
		JOIN scan_paths p ON s.scan_path_id = p.id
	`)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	defer rows.Close()

	schedules := make([]gin.H, 0)
	for rows.Next() {
		var id, scanPathID int
		var localPath, cronExpr string
		var enabled bool
		if err := rows.Scan(&id, &scanPathID, &localPath, &cronExpr, &enabled); err != nil {
			continue
		}
		schedules = append(schedules, gin.H{
			"id":              id,
			"scan_path_id":    scanPathID,
			"local_path":      localPath,
			"cron_expression": cronExpr,
			"enabled":         enabled,
		})
	}
	c.JSON(http.StatusOK, schedules)
}

func (s *RESTServer) addSchedule(c *gin.Context) {
	var req struct {
		ScanPathID     int    `json:"scan_path_id"`
		CronExpression string `json:"cron_expression"`
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	id, err := s.scheduler.AddSchedule(req.ScanPathID, req.CronExpression)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"id": id, "message": "Schedule added"})
}

func (s *RESTServer) deleteSchedule(c *gin.Context) {
	idStr := c.Param("id")
	id, err := strconv.Atoi(idStr)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgInvalidID})
		return
	}

	if err := s.scheduler.DeleteSchedule(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Schedule deleted"})
}

func (s *RESTServer) updateSchedule(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": ErrMsgInvalidID})
		return
	}

	var req struct {
		CronExpression string `json:"cron_expression"`
		Enabled        *bool  `json:"enabled"` // Pointer to distinguish between false and missing
	}
	if err := c.BindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// If enabled is missing, default to true (or maybe we should require it?)
	// Actually, for an update, we might want to keep existing if nil.
	// But for simplicity, let's assume the frontend sends everything or we fetch-modify-save.
	// Let's enforce Enabled is present for now, or handle it carefully.
	// In the service, we require enabled boolean.

	// Default enabled to true; if provided, use the provided value
	// Note: Service signature requires explicit enabled, so client must send full state
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	if err := s.scheduler.UpdateSchedule(id, req.CronExpression, enabled); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Schedule updated"})
}
