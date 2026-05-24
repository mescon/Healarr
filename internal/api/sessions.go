package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/logger"
)

// handleLogout invalidates the session token from the request. Returns 200
// in both the "session deleted" and "no such session" cases — logout should
// be idempotent from the client's perspective; whether or not the token
// was a known session is not useful information for the caller.
func (s *RESTServer) handleLogout(c *gin.Context) {
	token := s.extractAPIToken(c)
	if token == "" {
		c.JSON(http.StatusOK, gin.H{"message": "Already logged out"})
		return
	}

	if err := s.sessions.Delete(c.Request.Context(), token); err != nil {
		// DB failure during logout is logged but doesn't change the
		// response — the token might still be valid if we say so.
		logger.Errorf("Logout: failed to delete session: %v", err)
	}
	c.JSON(http.StatusOK, gin.H{"message": "Logged out"})
}
