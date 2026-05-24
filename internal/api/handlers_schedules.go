package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
)

func (s *RESTServer) getSchedules(c *gin.Context) {
	rows, err := s.schedules.ListWithPaths(c.Request.Context())
	if err != nil {
		respondDatabaseError(c, err)
		return
	}

	schedules := make([]gin.H, 0, len(rows))
	for _, sched := range rows {
		schedules = append(schedules, gin.H{
			"id":              sched.ID,
			"scan_path_id":    sched.ScanPathID,
			"local_path":      sched.LocalPath,
			"cron_expression": sched.CronExpression,
			"enabled":         sched.Enabled,
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
		respondBadRequest(c, err)
		return
	}

	id, err := s.scheduler.AddSchedule(req.ScanPathID, req.CronExpression)
	if err != nil {
		respondWithError(c, http.StatusInternalServerError, ErrMsgInternalError, err)
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
		respondWithError(c, http.StatusInternalServerError, ErrMsgInternalError, err)
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
		respondBadRequest(c, err)
		return
	}

	// Default enabled to true when omitted; UpdateSchedule requires explicit value.
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}

	if err := s.scheduler.UpdateSchedule(id, req.CronExpression, enabled); err != nil {
		respondWithError(c, http.StatusInternalServerError, ErrMsgInternalError, err)
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "Schedule updated"})
}
