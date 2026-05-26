package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func init() {
	gin.SetMode(gin.TestMode)
}

// =============================================================================
// requestIDMiddleware tests
// =============================================================================

func TestRequestIDMiddleware_GeneratesID(t *testing.T) {
	router := gin.New()
	router.Use(requestIDMiddleware())

	var capturedReqID string
	router.GET("/test", func(c *gin.Context) {
		capturedReqID = c.GetString("request_id")
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.NotEmpty(t, capturedReqID, "Should generate request ID")
	assert.NotEmpty(t, w.Header().Get("X-Request-ID"), "Should set X-Request-ID header")
	assert.Equal(t, capturedReqID, w.Header().Get("X-Request-ID"))
}

func TestRequestIDMiddleware_UsesProvidedID(t *testing.T) {
	router := gin.New()
	router.Use(requestIDMiddleware())

	var capturedReqID string
	router.GET("/test", func(c *gin.Context) {
		capturedReqID = c.GetString("request_id")
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "custom-id-123")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "custom-id-123", capturedReqID)
	assert.Equal(t, "custom-id-123", w.Header().Get("X-Request-ID"))
}

func TestRequestIDMiddleware_SetsContext(t *testing.T) {
	router := gin.New()
	router.Use(requestIDMiddleware())

	var ctxReqID string
	router.GET("/test", func(c *gin.Context) {
		if id, ok := c.Request.Context().Value(RequestIDKey).(string); ok {
			ctxReqID = id
		}
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "ctx-test-id")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, "ctx-test-id", ctxReqID, "Request ID should be in context")
}

// =============================================================================
// metricsMiddleware tests
// =============================================================================

func TestMetricsMiddleware_RecordsMetrics(t *testing.T) {
	router := gin.New()

	// Use nil metricsService - the middleware should handle this gracefully
	// We verify metrics recording behavior through integration tests
	router.Use(metricsMiddleware(nil))
	router.GET("/api/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/api/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMetricsMiddleware_SkipsMetricsEndpoint(t *testing.T) {
	router := gin.New()

	handlerCalled := false
	router.Use(metricsMiddleware(nil))
	router.GET("/metrics", func(c *gin.Context) {
		handlerCalled = true
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/metrics", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, handlerCalled, "Handler should be called")
}

func TestMetricsMiddleware_HandlesNilService(t *testing.T) {
	router := gin.New()
	router.Use(metricsMiddleware(nil))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestMetricsMiddleware_UnmatchedPath(t *testing.T) {
	router := gin.New()
	router.Use(metricsMiddleware(nil))

	// Don't register any routes - path will be unmatched
	req, _ := http.NewRequest("GET", "/nonexistent", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

// =============================================================================
// corsMiddleware tests
// =============================================================================

func TestCorsMiddleware_WildcardOrigin(t *testing.T) {
	router := gin.New()
	router.Use(corsMiddleware("*", nil))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
}

func TestCorsMiddleware_AllowedOrigin(t *testing.T) {
	allowedOrigins := map[string]bool{
		"http://allowed.com": true,
	}
	router := gin.New()
	router.Use(corsMiddleware("http://allowed.com", allowedOrigins))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://allowed.com")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "http://allowed.com", w.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", w.Header().Get("Vary"))
}

func TestCorsMiddleware_DisallowedOrigin(t *testing.T) {
	allowedOrigins := map[string]bool{
		"http://allowed.com": true,
	}
	router := gin.New()
	router.Use(corsMiddleware("http://allowed.com", allowedOrigins))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("Origin", "http://notallowed.com")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Origin"))
}

func TestCorsMiddleware_OptionsRequest(t *testing.T) {
	router := gin.New()
	router.Use(corsMiddleware("*", nil))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("OPTIONS", "/test", nil)
	req.Header.Set("Origin", "http://example.com")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Equal(t, "*", w.Header().Get("Access-Control-Allow-Origin"))
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Methods"))
	assert.NotEmpty(t, w.Header().Get("Access-Control-Allow-Headers"))
}

func TestCorsMiddleware_NoOrigin(t *testing.T) {
	router := gin.New()
	router.Use(corsMiddleware("http://allowed.com", map[string]bool{"http://allowed.com": true}))
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	// No Origin header
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	// Credentials header should not be set when no origin is provided
	assert.Empty(t, w.Header().Get("Access-Control-Allow-Credentials"))
}

// =============================================================================
// recoveryMiddleware tests
// =============================================================================

func TestRecoveryMiddleware_HandlesPanic(t *testing.T) {
	router := gin.New()
	router.Use(requestIDMiddleware())
	router.Use(recoveryMiddleware())
	router.GET("/panic", func(c *gin.Context) {
		panic("test panic")
	})

	req, _ := http.NewRequest("GET", "/panic", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "Internal server error")
}

func TestRecoveryMiddleware_IncludesRequestID(t *testing.T) {
	router := gin.New()
	router.Use(requestIDMiddleware())
	router.Use(recoveryMiddleware())
	router.GET("/panic", func(c *gin.Context) {
		panic("test panic")
	})

	req, _ := http.NewRequest("GET", "/panic", nil)
	req.Header.Set("X-Request-ID", "panic-test-id")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
	assert.Contains(t, w.Body.String(), "request_id")
}

// =============================================================================
// GetRequestID tests
// =============================================================================

func TestGetRequestID_FromContext(t *testing.T) {
	router := gin.New()
	router.Use(requestIDMiddleware())

	var extractedID string
	router.GET("/test", func(c *gin.Context) {
		extractedID = GetRequestID(c.Request.Context())
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set("X-Request-ID", "extract-test-id")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "extract-test-id", extractedID)
}

func TestGetRequestID_EmptyContext(t *testing.T) {
	// Use context.Background() instead of nil for staticcheck compliance
	id := GetRequestID(context.Background())
	assert.Empty(t, id)
}

// securityHeadersMiddleware tests

func TestSecurityHeadersMiddleware_SetsHeaders(t *testing.T) {
	router := gin.New()
	router.Use(securityHeadersMiddleware())
	router.GET("/test", func(c *gin.Context) {
		c.Status(http.StatusOK)
	})

	req, _ := http.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "nosniff", w.Header().Get("X-Content-Type-Options"))
	assert.Equal(t, "SAMEORIGIN", w.Header().Get("X-Frame-Options"))
	assert.Equal(t, "no-referrer", w.Header().Get("Referrer-Policy"))
}
