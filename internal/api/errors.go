package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/logger"
)

// Standard error messages (don't leak internal details)
const (
	ErrMsgDatabaseError       = "Database error"
	ErrMsgAuthenticationError = "Authentication error"
	ErrMsgInvalidRequest      = "Invalid request"
	ErrMsgNotFound            = "Not found"
	ErrMsgServiceUnavailable  = "Service unavailable"
	ErrMsgInternalError       = "Internal server error"
	ErrMsgScanNotFound        = "Scan not found"
	ErrMsgNoIDsProvided       = "No IDs provided"
	ErrMsgInvalidID           = "Invalid ID"
)

// respondWithError sends a JSON error response and logs the actual error
func respondWithError(c *gin.Context, status int, publicMsg string, err error) {
	if err != nil {
		logger.Debugf("%s: %v", publicMsg, err)
	}
	c.JSON(status, gin.H{"error": publicMsg})
}

// respondDatabaseError handles database errors consistently
func respondDatabaseError(c *gin.Context, err error) {
	respondWithError(c, http.StatusInternalServerError, ErrMsgDatabaseError, err)
}

// respondAuthError handles authentication errors consistently
func respondAuthError(c *gin.Context, err error) {
	respondWithError(c, http.StatusInternalServerError, ErrMsgAuthenticationError, err)
}

// respondBadRequest handles bad request errors, optionally exposing the error message
// Use exposeError=true only for validation errors safe to show users
func respondBadRequest(c *gin.Context, err error, exposeError bool) {
	if exposeError && err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	respondWithError(c, http.StatusBadRequest, ErrMsgInvalidRequest, err)
}

// respondNotFound handles not found errors
func respondNotFound(c *gin.Context, resource string) {
	c.JSON(http.StatusNotFound, gin.H{"error": resource + " not found"})
}

// respondServiceUnavailable handles service unavailable errors
func respondServiceUnavailable(c *gin.Context, service string) {
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": service + " not available"})
}
