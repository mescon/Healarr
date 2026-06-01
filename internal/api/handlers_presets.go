package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/repository"
)

// scanPresetRequest mirrors the editable columns of scan_presets. is_builtin
// is intentionally absent: the only path to a built-in row is migration 009.
type scanPresetRequest struct {
	Name                    string   `json:"name"`
	Description             string   `json:"description"`
	DetectionMethod         string   `json:"detection_method"`
	DetectionMode           string   `json:"detection_mode"`
	DetectionArgs           []string `json:"detection_args"`
	ThoroughDurationSeconds *int64   `json:"thorough_duration_seconds,omitempty"`
	ThoroughTimeoutSeconds  *int64   `json:"thorough_timeout_seconds,omitempty"`
	Hwaccel                 *string  `json:"hwaccel,omitempty"`
}

// presetToJSON returns the response shape used by GET / POST / PUT.
// Built-ins surface is_builtin=true so the UI can render them with a
// lock icon and disabled trash button.
func presetToJSON(p repository.ScanPreset) gin.H {
	out := gin.H{
		"id":               p.ID,
		"name":             p.Name,
		"description":      p.Description,
		"detection_method": p.DetectionMethod,
		"detection_mode":   p.DetectionMode,
		"is_builtin":       p.IsBuiltin,
	}
	if p.DetectionArgs.Valid && p.DetectionArgs.String != "" {
		var args []string
		if err := json.Unmarshal([]byte(p.DetectionArgs.String), &args); err == nil {
			out["detection_args"] = args
		} else {
			out["detection_args"] = p.DetectionArgs.String
		}
	} else {
		out["detection_args"] = nil
	}
	if p.ThoroughDurationSeconds.Valid {
		out["thorough_duration_seconds"] = p.ThoroughDurationSeconds.Int64
	} else {
		out["thorough_duration_seconds"] = nil
	}
	if p.ThoroughTimeoutSeconds.Valid {
		out["thorough_timeout_seconds"] = p.ThoroughTimeoutSeconds.Int64
	} else {
		out["thorough_timeout_seconds"] = nil
	}
	if p.Hwaccel.Valid {
		out["hwaccel"] = p.Hwaccel.String
	} else {
		out["hwaccel"] = nil
	}
	return out
}

// validatePresetRequest enforces the same bounds the scan_paths handler
// applies plus the preset-specific constraints (non-empty name, valid
// enums). On error it writes a 400 with a human-readable message; on
// success it returns the JSON-encoded detection_args (or nil) and ok=true.
func validatePresetRequest(req *scanPresetRequest, c *gin.Context) ([]byte, bool) {
	if req.Name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name is required"})
		return nil, false
	}
	if req.DetectionMethod == "" {
		req.DetectionMethod = "ffprobe"
	}
	if req.DetectionMode == "" {
		req.DetectionMode = "quick"
	}
	if _, err := integration.ParseDetectionMethod(req.DetectionMethod); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	if _, err := integration.ParseDetectionMode(req.DetectionMode); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	if err := validateDetectionArgs(req.DetectionArgs); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return nil, false
	}
	if req.ThoroughDurationSeconds != nil {
		if v := *req.ThoroughDurationSeconds; v < 0 || v > 24*3600 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "thorough_duration_seconds must be between 0 and 86400"})
			return nil, false
		}
	}
	if req.ThoroughTimeoutSeconds != nil {
		if v := *req.ThoroughTimeoutSeconds; v < 30 || v > 6*3600 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "thorough_timeout_seconds must be between 30 and 21600"})
			return nil, false
		}
	}
	if req.Hwaccel != nil && *req.Hwaccel != "" {
		switch *req.Hwaccel {
		case "auto", "off", "cuda", "vaapi", "qsv", "videotoolbox", "vdpau", "drm":
			// allowed
		default:
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid hwaccel value"})
			return nil, false
		}
	}

	var detectionArgsJSON []byte
	if len(req.DetectionArgs) > 0 {
		b, err := json.Marshal(req.DetectionArgs)
		if err != nil {
			logger.Warnf("Failed to marshal preset detection_args: %v", err)
			detectionArgsJSON = []byte("[]")
		} else {
			detectionArgsJSON = b
		}
	}
	return detectionArgsJSON, true
}

// presetFieldsFromRequest builds the persistence-layer field bundle from
// the validated request shape.
func presetFieldsFromRequest(req *scanPresetRequest, detectionArgsJSON []byte) repository.ScanPresetFields {
	fields := repository.ScanPresetFields{
		Name:              req.Name,
		Description:       req.Description,
		DetectionMethod:   req.DetectionMethod,
		DetectionMode:     req.DetectionMode,
		DetectionArgsJSON: string(detectionArgsJSON),
	}
	if req.ThoroughDurationSeconds != nil {
		fields.ThoroughDurationSeconds = sql.NullInt64{Int64: *req.ThoroughDurationSeconds, Valid: true}
	}
	if req.ThoroughTimeoutSeconds != nil {
		fields.ThoroughTimeoutSeconds = sql.NullInt64{Int64: *req.ThoroughTimeoutSeconds, Valid: true}
	}
	if req.Hwaccel != nil && *req.Hwaccel != "" {
		fields.Hwaccel = sql.NullString{String: *req.Hwaccel, Valid: true}
	}
	return fields
}

func (s *RESTServer) getScanPresets(c *gin.Context) {
	rows, err := s.scanPresets.ListAll(c.Request.Context())
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, p := range rows {
		out = append(out, presetToJSON(p))
	}
	c.JSON(http.StatusOK, out)
}

func (s *RESTServer) createScanPreset(c *gin.Context) {
	var req scanPresetRequest
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}
	detectionArgsJSON, ok := validatePresetRequest(&req, c)
	if !ok {
		return
	}
	id, err := s.scanPresets.Create(c.Request.Context(), presetFieldsFromRequest(&req, detectionArgsJSON))
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	p, err := s.scanPresets.GetByID(c.Request.Context(), id)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	c.JSON(http.StatusCreated, presetToJSON(p))
}

// updateScanPreset rejects mutation of built-in rows so the operator
// cannot rewrite the meaning of "Quick" or "Deep scan" out from under
// existing UI references. Custom (is_builtin=0) presets are fully
// mutable through this endpoint.
func (s *RESTServer) updateScanPreset(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid preset id"})
		return
	}
	existing, err := s.scanPresets.GetByID(c.Request.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respondNotFound(c, "Preset")
		return
	}
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	if existing.IsBuiltin {
		c.JSON(http.StatusForbidden, gin.H{"error": "built-in presets cannot be modified; duplicate to a custom preset instead"})
		return
	}

	var req scanPresetRequest
	if err := c.BindJSON(&req); err != nil {
		respondBadRequest(c, err)
		return
	}
	detectionArgsJSON, ok := validatePresetRequest(&req, c)
	if !ok {
		return
	}
	if err := s.scanPresets.Update(c.Request.Context(), id, presetFieldsFromRequest(&req, detectionArgsJSON)); err != nil {
		respondDatabaseError(c, err)
		return
	}
	p, err := s.scanPresets.GetByID(c.Request.Context(), id)
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	c.JSON(http.StatusOK, presetToJSON(p))
}

// deleteScanPreset rejects deletion of built-in rows.
func (s *RESTServer) deleteScanPreset(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid preset id"})
		return
	}
	existing, err := s.scanPresets.GetByID(c.Request.Context(), id)
	if errors.Is(err, repository.ErrNotFound) {
		respondNotFound(c, "Preset")
		return
	}
	if err != nil {
		respondDatabaseError(c, err)
		return
	}
	if existing.IsBuiltin {
		c.JSON(http.StatusForbidden, gin.H{"error": "built-in presets cannot be deleted"})
		return
	}
	if err := s.scanPresets.Delete(c.Request.Context(), id); err != nil {
		respondDatabaseError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
