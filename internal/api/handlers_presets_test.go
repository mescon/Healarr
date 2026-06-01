package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mescon/Healarr/internal/testutil"
)

// setupPresetsTestServer wires the four preset routes onto the standard
// auth middleware and seeds the same five built-ins migration 009 ships.
func setupPresetsTestServer(t *testing.T) (*gin.Engine, string, func()) {
	t.Helper()
	db, dbCleanup := setupConfigTestDB(t)
	pm := &testutil.MockPathMapper{}
	r, apiKey, srvCleanup := setupConfigTestServer(t, db, pm, false)

	// Create the scan_presets table + seed builtins. handlers_config_test's
	// shared schema doesn't include it, so we do it here.
	if _, err := db.Exec(`
		CREATE TABLE scan_presets (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL UNIQUE,
			description TEXT NOT NULL DEFAULT '',
			detection_method TEXT NOT NULL DEFAULT 'ffprobe',
			detection_mode TEXT NOT NULL DEFAULT 'quick',
			detection_args TEXT,
			thorough_duration_seconds INTEGER,
			thorough_timeout_seconds INTEGER,
			hwaccel TEXT,
			is_builtin INTEGER NOT NULL DEFAULT 0,
			created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
		INSERT INTO scan_presets (name, detection_method, detection_mode, is_builtin) VALUES
			('Zero-byte only', 'zero_byte', 'quick', 1),
			('Quick',          'ffprobe',   'quick', 1),
			('Fast triage',    'ffprobe',   'thorough', 1),
			('Deep scan',      'ffprobe',   'thorough', 1),
			('Paranoid',       'handbrake', 'thorough', 1);
	`); err != nil {
		t.Fatalf("seed scan_presets: %v", err)
	}

	gin.SetMode(gin.TestMode)
	s := &RESTServer{db: db}
	s.initRepositories()
	api := r.Group("/api")
	protected := api.Group("")
	protected.Use(s.authMiddleware())
	protected.GET("/config/presets", s.getScanPresets)
	protected.POST("/config/presets", s.createScanPreset)
	protected.PUT("/config/presets/:id", s.updateScanPreset)
	protected.DELETE("/config/presets/:id", s.deleteScanPreset)

	cleanup := func() {
		srvCleanup()
		dbCleanup()
	}
	return r, apiKey, cleanup
}

func TestGetScanPresets_ReturnsBuiltins(t *testing.T) {
	r, apiKey, cleanup := setupPresetsTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/config/presets", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())
	var got []map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Len(t, got, 5)
	wantNames := []string{"Zero-byte only", "Quick", "Fast triage", "Deep scan", "Paranoid"}
	for i, want := range wantNames {
		assert.Equal(t, want, got[i]["name"], "position %d", i)
		assert.Equal(t, true, got[i]["is_builtin"], "position %d", i)
	}
}

func TestCreateScanPreset_HappyPath(t *testing.T) {
	r, apiKey, cleanup := setupPresetsTestServer(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]interface{}{
		"name":                      "My Custom",
		"description":               "test",
		"detection_method":          "ffprobe",
		"detection_mode":            "thorough",
		"thorough_duration_seconds": 90,
		"hwaccel":                   "cuda",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/config/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusCreated, w.Code, "body=%s", w.Body.String())
	var got map[string]interface{}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, "My Custom", got["name"])
	assert.Equal(t, false, got["is_builtin"])
	assert.Equal(t, float64(90), got["thorough_duration_seconds"])
	assert.Equal(t, "cuda", got["hwaccel"])
}

func TestUpdateScanPreset_RejectsBuiltin(t *testing.T) {
	r, apiKey, cleanup := setupPresetsTestServer(t)
	defer cleanup()

	// id=2 is "Quick" (built-in).
	body, _ := json.Marshal(map[string]interface{}{
		"name":             "Hijacked",
		"detection_method": "ffprobe",
		"detection_mode":   "quick",
	})
	req := httptest.NewRequest(http.MethodPut, "/api/config/presets/2", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code, "body=%s", w.Body.String())
	assert.Contains(t, w.Body.String(), "built-in")
}

func TestDeleteScanPreset_RejectsBuiltin(t *testing.T) {
	r, apiKey, cleanup := setupPresetsTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodDelete, "/api/config/presets/3", nil) // Fast triage
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusForbidden, w.Code)
}

func TestDeleteScanPreset_AllowsCustom(t *testing.T) {
	r, apiKey, cleanup := setupPresetsTestServer(t)
	defer cleanup()

	// Create then delete.
	body, _ := json.Marshal(map[string]interface{}{
		"name":             "Throwaway",
		"detection_method": "ffprobe",
		"detection_mode":   "quick",
	})
	postReq := httptest.NewRequest(http.MethodPost, "/api/config/presets", bytes.NewReader(body))
	postReq.Header.Set("Content-Type", "application/json")
	postReq.Header.Set("X-API-Key", apiKey)
	postW := httptest.NewRecorder()
	r.ServeHTTP(postW, postReq)
	require.Equal(t, http.StatusCreated, postW.Code)
	var created map[string]interface{}
	require.NoError(t, json.Unmarshal(postW.Body.Bytes(), &created))
	id := int(created["id"].(float64))

	delReq := httptest.NewRequest(http.MethodDelete, "/api/config/presets/"+strconv.Itoa(id), nil)
	delReq.Header.Set("X-API-Key", apiKey)
	delW := httptest.NewRecorder()
	r.ServeHTTP(delW, delReq)
	require.Equal(t, http.StatusNoContent, delW.Code)
}

func TestCreateScanPreset_RejectsBadHwaccel(t *testing.T) {
	r, apiKey, cleanup := setupPresetsTestServer(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]interface{}{
		"name":             "Bad",
		"detection_method": "ffprobe",
		"detection_mode":   "quick",
		"hwaccel":          "not-a-real-accel",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/config/presets", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "hwaccel")
}
