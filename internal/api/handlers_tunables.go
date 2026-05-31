package api

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/repository"
)

// getTunables returns every catalog entry with its effective value,
// source (env / db / default), the raw env and DB strings (so the UI
// can show "Set by HEALARR_FOO"), and the metadata needed to render
// the form (kind, bounds, enum values, requires_restart).
func (s *RESTServer) getTunables(c *gin.Context) {
	tn := repository.NewTunables(s.settings)
	values, err := tn.ResolveAll(c.Request.Context())
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tunables": values})
}

// updateTunables performs a bulk update of one or more tunables.
//
// Request shape:
//
//	{ "updates": { "scan.thorough_duration_seconds": 60, "scan.hwaccel": "cuda" } }
//
// Validation runs through TunablesValidateAndStore which:
//   - rejects unknown keys
//   - rejects keys whose matching env var is set (env always wins)
//   - enforces per-kind bounds and enum membership
//   - writes the entire batch atomically (one transaction)
//
// Error messages from validation are surfaced directly to the caller so
// the UI can render an inline field error - unlike respondBadRequest
// which masks the error to avoid leaking internals, here the messages
// already describe user-facing input problems ("value 999 out of
// range", "tunable X locked by env var Y").
func (s *RESTServer) updateTunables(c *gin.Context) {
	var req struct {
		Updates map[string]any `json:"updates"`
	}
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}
	if len(req.Updates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "no updates supplied"})
		return
	}

	tn := repository.NewTunables(s.settings)
	if err := tn.ValidateAndStore(c.Request.Context(), req.Updates); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	values, err := tn.ResolveAll(c.Request.Context())
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"tunables": values})
}
