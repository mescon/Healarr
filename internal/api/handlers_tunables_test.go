package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mescon/Healarr/internal/repository"
	"github.com/mescon/Healarr/internal/testutil"
)

// setupTunablesTestServer wires just the two tunable routes for focused
// HTTP-level testing. Reuses setupConfigTestDB / setupConfigTestServer
// because the protected route group already covers authMiddleware.
func setupTunablesTestServer(t *testing.T) (*gin.Engine, string, func()) {
	t.Helper()
	db, dbCleanup := setupConfigTestDB(t)
	pm := &testutil.MockPathMapper{}
	r, apiKey, srvCleanup := setupConfigTestServer(t, db, pm, false)

	// Build one server bound to the shared db, then mount the tunable
	// routes on a protected group that uses *its* authMiddleware. Using
	// the same server instance for middleware and handlers ensures both
	// share the same SettingsRepository (otherwise the middleware can't
	// resolve the API key row).
	gin.SetMode(gin.TestMode)
	s := &RESTServer{db: db}
	s.initRepositories()
	api := r.Group("/api")
	protected := api.Group("")
	protected.Use(s.authMiddleware())
	protected.GET("/config/tunables", s.getTunables)
	protected.PUT("/config/tunables", s.updateTunables)

	cleanup := func() {
		srvCleanup()
		dbCleanup()
	}
	return r, apiKey, cleanup
}

func TestGetTunables_ReturnsFullCatalog(t *testing.T) {
	r, apiKey, cleanup := setupTunablesTestServer(t)
	defer cleanup()

	for _, m := range repository.Catalog {
		t.Setenv(m.EnvVar, "")
	}

	req := httptest.NewRequest(http.MethodGet, "/api/config/tunables", nil)
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	var resp struct {
		Tunables []repository.ResolvedValue `json:"tunables"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))
	assert.Len(t, resp.Tunables, len(repository.Catalog))
	for _, v := range resp.Tunables {
		assert.Equal(t, repository.SourceDefault, v.Source,
			"key %s should be default with no env or db", v.Key)
	}
}

func TestUpdateTunables_HappyPath(t *testing.T) {
	r, apiKey, cleanup := setupTunablesTestServer(t)
	defer cleanup()
	for _, m := range repository.Catalog {
		t.Setenv(m.EnvVar, "")
	}

	body, _ := json.Marshal(map[string]any{
		"updates": map[string]any{
			repository.SettingKeyThoroughDuration: 60,
			repository.SettingKeyHwAccel:          "cuda",
		},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/config/tunables", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code, "body=%s", w.Body.String())

	// Re-read via GET and verify the new effective values came back.
	getReq := httptest.NewRequest(http.MethodGet, "/api/config/tunables", nil)
	getReq.Header.Set("X-API-Key", apiKey)
	getW := httptest.NewRecorder()
	r.ServeHTTP(getW, getReq)

	var resp struct {
		Tunables []repository.ResolvedValue `json:"tunables"`
	}
	require.NoError(t, json.Unmarshal(getW.Body.Bytes(), &resp))

	values := make(map[string]repository.ResolvedValue, len(resp.Tunables))
	for _, v := range resp.Tunables {
		values[v.Key] = v
	}
	assert.Equal(t, float64(60), values[repository.SettingKeyThoroughDuration].Value)
	assert.Equal(t, repository.SourceDB, values[repository.SettingKeyThoroughDuration].Source)
	assert.Equal(t, "cuda", values[repository.SettingKeyHwAccel].Value)
	assert.Equal(t, repository.SourceDB, values[repository.SettingKeyHwAccel].Source)
}

func TestUpdateTunables_RejectsEnvLocked(t *testing.T) {
	r, apiKey, cleanup := setupTunablesTestServer(t)
	defer cleanup()

	t.Setenv("HEALARR_HEALTH_CHECK_HWACCEL", "cuda")

	body, _ := json.Marshal(map[string]any{
		"updates": map[string]any{
			repository.SettingKeyHwAccel: "off",
		},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/config/tunables", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "locked by env var")
}

func TestUpdateTunables_RejectsUnknownKey(t *testing.T) {
	r, apiKey, cleanup := setupTunablesTestServer(t)
	defer cleanup()
	for _, m := range repository.Catalog {
		t.Setenv(m.EnvVar, "")
	}

	body, _ := json.Marshal(map[string]any{
		"updates": map[string]any{
			"not.a.real.key": 42,
		},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/config/tunables", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "unknown tunable")
}

func TestUpdateTunables_RejectsOutOfRange(t *testing.T) {
	r, apiKey, cleanup := setupTunablesTestServer(t)
	defer cleanup()
	for _, m := range repository.Catalog {
		t.Setenv(m.EnvVar, "")
	}

	body, _ := json.Marshal(map[string]any{
		"updates": map[string]any{
			repository.SettingKeyDefaultMaxRetries: 999,
		},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/config/tunables", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
	assert.True(t, strings.Contains(w.Body.String(), "out of range"),
		"want range error, got body=%s", w.Body.String())
}

func TestUpdateTunables_EmptyBodyRejected(t *testing.T) {
	r, apiKey, cleanup := setupTunablesTestServer(t)
	defer cleanup()

	body, _ := json.Marshal(map[string]any{
		"updates": map[string]any{},
	})
	req := httptest.NewRequest(http.MethodPut, "/api/config/tunables", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", apiKey)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	require.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetTunables_RequiresAuth(t *testing.T) {
	r, _, cleanup := setupTunablesTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/api/config/tunables", nil)
	// no API key header
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	assert.NotEqual(t, http.StatusOK, w.Code)
}
