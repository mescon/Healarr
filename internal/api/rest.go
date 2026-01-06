// Package api provides the REST API handlers and server for Healarr.
// It includes endpoints for managing scans, corruptions, configurations,
// notifications, and real-time updates via WebSocket.
package api

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/crypto"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/metrics"
	"github.com/mescon/Healarr/internal/notifier"
	"github.com/mescon/Healarr/internal/services"
	"github.com/mescon/Healarr/internal/web"
)

type RESTServer struct {
	router      *gin.Engine
	httpServer  *http.Server
	db          *sql.DB
	eventBus    *eventbus.EventBus
	scanner     services.Scanner
	pathMapper  integration.PathMapper
	scheduler   services.Scheduler
	notifier    *notifier.Notifier
	metrics     *metrics.MetricsService
	hub         *WebSocketHub
	startTime   time.Time
	toolChecker *integration.ToolChecker
}

func NewRESTServer(db *sql.DB, eb *eventbus.EventBus, scanner services.Scanner, pm integration.PathMapper, scheduler services.Scheduler, n *notifier.Notifier, m *metrics.MetricsService) *RESTServer {
	// Set Gin to release mode for production (suppresses debug warnings)
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	// Request ID middleware for correlation/tracing
	r.Use(func(c *gin.Context) {
		// Use existing request ID from header if provided, otherwise generate one
		reqID := c.GetHeader("X-Request-ID")
		if reqID == "" {
			reqID = fmt.Sprintf("%d-%d", time.Now().UnixNano(), c.Request.ContentLength)
		}
		c.Set("request_id", reqID)
		c.Header("X-Request-ID", reqID)
		c.Next()
	})

	// Custom recovery middleware with enhanced logging
	r.Use(gin.CustomRecovery(func(c *gin.Context, recovered interface{}) {
		reqID := c.GetString("request_id")
		logger.Errorf("[PANIC RECOVERY] request_id=%s path=%s method=%s error=%v",
			reqID, c.Request.URL.Path, c.Request.Method, recovered)
		c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
			"error":      "Internal server error",
			"request_id": reqID,
		})
	}))

	// CORS middleware - configurable via HEALARR_CORS_ORIGIN env var
	// If not set, defaults to same-origin (no CORS header = browser enforces same-origin)
	// Set to "*" only for development, or specify allowed origins comma-separated
	corsOrigins := os.Getenv("HEALARR_CORS_ORIGIN")
	allowedOrigins := make(map[string]bool)
	if corsOrigins != "" {
		for _, origin := range strings.Split(corsOrigins, ",") {
			allowedOrigins[strings.TrimSpace(origin)] = true
		}
	}

	r.Use(func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// Only set CORS headers if origin is allowed
		if corsOrigins == "*" {
			c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		} else if origin != "" && allowedOrigins[origin] {
			c.Writer.Header().Set("Access-Control-Allow-Origin", origin)
			c.Writer.Header().Set("Vary", "Origin")
		}
		// If no match, don't set Access-Control-Allow-Origin (same-origin policy applies)

		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, X-API-Key, accept, origin, Cache-Control, X-Requested-With")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})

	// Initialize tool checker and check all tools at startup
	toolChecker := integration.NewToolChecker()
	toolChecker.CheckAllTools()

	s := &RESTServer{
		router:      r,
		db:          db,
		eventBus:    eb,
		scanner:     scanner,
		pathMapper:  pm,
		scheduler:   scheduler,
		notifier:    n,
		metrics:     m,
		hub:         NewWebSocketHub(eb),
		startTime:   time.Now(),
		toolChecker: toolChecker,
	}

	s.setupRoutes()

	return s
}

// mustSub returns a sub-filesystem or panics. Used for embedded assets.
func mustSub(fsys fs.FS, dir string) fs.FS {
	sub, err := fs.Sub(fsys, dir)
	if err != nil {
		panic(fmt.Sprintf("failed to get sub-filesystem %q: %v", dir, err))
	}
	return sub
}

func (s *RESTServer) setupRoutes() {
	cfg := config.Get()
	basePath := cfg.BasePath

	// Prometheus metrics endpoint at root level (standard convention, not behind base path)
	// This makes it easy for Prometheus to discover and scrape without knowing the base path
	s.router.GET("/metrics", gin.WrapH(s.metrics.Handler()))

	// Create a group for the base path (or use root if basePath is "/")
	var base *gin.RouterGroup
	if basePath == "/" {
		base = s.router.Group("")
	} else {
		base = s.router.Group(basePath)
		// Redirect root to base path
		s.router.GET("/", func(c *gin.Context) {
			c.Redirect(http.StatusMovedPermanently, basePath)
		})
	}

	api := base.Group("/api")
	{
		// Endpoint to get runtime config (base path) for frontend
		api.GET("/config/runtime", func(c *gin.Context) {
			// Get saved base path from database (if any)
			var savedBasePath sql.NullString
			if err := s.db.QueryRow("SELECT value FROM settings WHERE key = 'base_path'").Scan(&savedBasePath); err != nil && err != sql.ErrNoRows {
				logger.Debugf("Failed to query base_path setting: %v", err)
			}

			// Determine source: env var takes precedence, then database, then default
			envBasePath := os.Getenv("HEALARR_BASE_PATH")
			source := "default"
			effectivePath := basePath

			if envBasePath != "" {
				source = "environment"
			} else if savedBasePath.Valid && savedBasePath.String != "" {
				source = "database"
			}

			c.JSON(http.StatusOK, gin.H{
				"base_path":        effectivePath,
				"base_path_source": source,
			})
		})

		// Health check endpoint (no authentication required)
		api.GET("/health", s.handleHealth)

		// System info endpoint (no authentication required - useful for debugging)
		api.GET("/system/info", s.handleSystemInfo)

		// Prometheus metrics endpoint (no authentication required for scraping)
		api.GET("/metrics", gin.WrapH(s.metrics.Handler()))

		// Public auth endpoints with rate limiting
		api.POST("/auth/setup", SetupLimiter.Middleware(), s.handleAuthSetup)
		api.POST("/auth/login", LoginLimiter.Middleware(), s.handleLogin)
		api.GET("/auth/status", s.handleAuthStatus)
		api.POST("/webhook/:instance_id", WebhookLimiter.Middleware(), s.handleWebhook) // Webhooks use API key in query or header

		// Protected endpoints (require password authentication)
		protected := api.Group("")
		protected.Use(s.authMiddleware())
		{
			// Auth management
			protected.GET("/auth/key", s.getAPIKey)
			protected.POST("/auth/regenerate", s.regenerateAPIKey)
			protected.POST("/auth/password", s.changePassword)

			// Config - Server settings
			protected.PUT("/config/settings", s.updateSettings)
			protected.POST("/config/restart", s.restartServer)

			// Config
			protected.GET("/config/arr", s.getArrInstances)
			protected.POST("/config/arr", s.createArrInstance)
			protected.POST("/config/arr/test", s.testArrConnection)
			protected.PUT("/config/arr/:id", s.updateArrInstance)
			protected.DELETE("/config/arr/:id", s.deleteArrInstance)
			protected.GET("/config/paths", s.getScanPaths)
			protected.POST("/config/paths", s.createScanPath)
			protected.PUT("/config/paths/:id", s.updateScanPath)
			protected.DELETE("/config/paths/:id", s.deleteScanPath)
			protected.GET("/config/browse", s.browseDirectory)

			// Notifications
			protected.GET("/config/notifications", s.getNotifications)
			protected.POST("/config/notifications", s.createNotification)
			protected.PUT("/config/notifications/:id", s.updateNotification)
			protected.DELETE("/config/notifications/:id", s.deleteNotification)
			protected.POST("/config/notifications/test", s.testNotification)
			protected.GET("/config/notifications/events", s.getNotificationEvents)
			protected.GET("/config/notifications/:id/log", s.getNotificationLog)
			protected.GET("/config/notifications/:id", s.getNotification)

			// Config export/import
			protected.GET("/config/export", s.exportConfig)
			protected.POST("/config/import", s.importConfig)
			protected.GET("/config/backup", s.downloadDatabaseBackup)

			// Detection preview - shows what command will be run
			protected.GET("/config/detection-preview", s.getDetectionPreview)

			// Stats & Data
			protected.GET("/stats/dashboard", s.getDashboardStats)
			protected.GET("/stats/history", s.getStatsHistory)
			protected.GET("/stats/types", s.getStatsTypes)
			protected.GET("/corruptions", s.getCorruptions)
			protected.GET("/config/schedules", s.getSchedules)
			protected.POST("/config/schedules", s.addSchedule)
			protected.PUT("/config/schedules/:id", s.updateSchedule)
			protected.DELETE("/config/schedules/:id", s.deleteSchedule)

			protected.GET("/corruptions/:id/history", s.getCorruptionHistory)
			// Corruption bulk actions
			protected.POST("/corruptions/retry", s.retryCorruptions)
			protected.POST("/corruptions/ignore", s.ignoreCorruptions)
			protected.POST("/corruptions/delete", s.deleteCorruptions)
			protected.GET("/remediations", s.getRemediations)
			protected.GET("/scans", s.getScans)
			protected.GET("/scans/active", s.getActiveScans)
			// Specific routes MUST come before :scan_id parameter routes
			protected.POST("/scans/all", s.triggerScanAll) // Scan all enabled paths
			protected.POST("/scans/pause-all", s.pauseAllScans)
			protected.POST("/scans/resume-all", s.resumeAllScans)
			protected.POST("/scans/cancel-all", s.cancelAllScans)
			protected.POST("/scans", s.triggerScan) // RESTful: POST to collection
			protected.POST("/scan", s.triggerScan)  // Legacy: keep for compatibility
			// Parameter routes come after specific routes
			protected.GET("/scans/:scan_id", s.getScanDetails)
			protected.GET("/scans/:scan_id/files", s.getScanFiles)
			protected.DELETE("/scans/:scan_id", s.cancelScan)
			protected.POST("/scans/:scan_id/pause", s.pauseScan)
			protected.POST("/scans/:scan_id/resume", s.resumeScan)
			protected.POST("/scans/:scan_id/rescan", s.rescanPath)
			protected.GET("/ws", func(c *gin.Context) {
				s.hub.HandleConnection(c)
			})

			// Logs
			protected.GET("/logs/recent", s.handleRecentLogs)
			protected.GET("/logs/download", s.handleDownloadLogs)

			// Updates - check for new versions
			protected.GET("/updates/check", s.handleCheckUpdate)
		}
	}

	// Serve static files under the base path
	// Check for embedded assets first, fall back to filesystem
	if web.HasEmbeddedAssets() {
		logger.Infof("Serving web assets from embedded filesystem")
		// Debug: list embedded files
		if files := web.ListEmbeddedFiles(); files != nil {
			logger.Debugf("Embedded files: %v", files)
		}
		webFS := web.GetFS()
		base.StaticFS("/assets", http.FS(mustSub(webFS, "assets")))
		base.StaticFS("/icons", http.FS(mustSub(webFS, "icons")))

		// Helper to serve embedded files directly (avoids http.FS redirect behavior)
		serveEmbeddedFile := func(c *gin.Context, filename string, contentType string) {
			data, err := fs.ReadFile(webFS, filename)
			if err != nil {
				logger.Errorf("Failed to read embedded file %s: %v", filename, err)
				c.Status(http.StatusNotFound)
				return
			}
			c.Data(http.StatusOK, contentType, data)
		}

		// Helper to serve index.html with injected base path for SPA routing
		serveIndexWithBasePath := func(c *gin.Context) {
			data, err := fs.ReadFile(webFS, "index.html")
			if err != nil {
				logger.Errorf("Failed to read embedded index.html: %v", err)
				c.Status(http.StatusNotFound)
				return
			}
			// Inject base path as a script tag before </head>
			// This allows the frontend to know the base path before any API calls
			injectedScript := fmt.Sprintf(`<script>window.__HEALARR_BASE_PATH__=%q;</script></head>`, basePath)
			html := strings.Replace(string(data), "</head>", injectedScript, 1)
			c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
		}

		// Serve individual files from embedded FS
		base.GET("/", serveIndexWithBasePath)
		base.GET("/index.html", serveIndexWithBasePath)
		base.GET("/favicon.png", func(c *gin.Context) {
			serveEmbeddedFile(c, "favicon.png", "image/png")
		})
		base.GET("/healarr.svg", func(c *gin.Context) {
			serveEmbeddedFile(c, "healarr.svg", "image/svg+xml")
		})

		// SPA Routes - serve index.html for client-side routing
		s.router.NoRoute(func(c *gin.Context) {
			if basePath == "/" || strings.HasPrefix(c.Request.URL.Path, basePath) {
				serveIndexWithBasePath(c)
			} else {
				c.Redirect(http.StatusMovedPermanently, basePath)
			}
		})
	} else {
		// Check if web directory exists on filesystem
		webDir := cfg.WebDir
		indexFile := filepath.Join(webDir, "index.html")
		if _, err := os.Stat(indexFile); err == nil {
			// Filesystem mode - web directory exists
			logger.Infof("Serving web assets from filesystem: %s", webDir)
			base.Static("/assets", filepath.Join(webDir, "assets"))
			base.Static("/icons", filepath.Join(webDir, "icons"))
			base.StaticFile("/favicon.png", filepath.Join(webDir, "favicon.png"))
			base.StaticFile("/healarr.svg", filepath.Join(webDir, "healarr.svg"))

			// Helper to serve index.html with injected base path
			serveIndexWithBasePath := func(c *gin.Context) {
				data, err := os.ReadFile(indexFile)
				if err != nil {
					logger.Errorf("Failed to read index.html: %v", err)
					c.Status(http.StatusNotFound)
					return
				}
				// Inject base path as a script tag before </head>
				injectedScript := fmt.Sprintf(`<script>window.__HEALARR_BASE_PATH__=%q;</script></head>`, basePath)
				html := strings.Replace(string(data), "</head>", injectedScript, 1)
				c.Data(http.StatusOK, "text/html; charset=utf-8", []byte(html))
			}

			base.GET("/", serveIndexWithBasePath)
			base.GET("/index.html", serveIndexWithBasePath)

			// SPA Routes - serve index.html for client-side routing under base path
			s.router.NoRoute(func(c *gin.Context) {
				// Check if request is under our base path
				if basePath == "/" || strings.HasPrefix(c.Request.URL.Path, basePath) {
					serveIndexWithBasePath(c)
				} else {
					c.Redirect(http.StatusMovedPermanently, basePath)
				}
			})
		} else {
			// No web assets available - API only mode
			logger.Infof("No web assets found (embedded or filesystem at %s) - running in API-only mode", webDir)
			s.router.NoRoute(func(c *gin.Context) {
				// Return a helpful JSON response instead of redirect loop
				if strings.HasPrefix(c.Request.URL.Path, "/api/") {
					c.JSON(http.StatusNotFound, gin.H{"error": "API endpoint not found"})
				} else {
					c.JSON(http.StatusServiceUnavailable, gin.H{
						"error":   "Web UI not available",
						"message": "This binary was built without embedded web assets. Please download a release binary or run in development mode with a web/ directory.",
						"api":     basePath + "api/",
					})
				}
			})
		}
	}
}

func (s *RESTServer) Start(addr string) error {
	s.httpServer = &http.Server{
		Addr:    addr,
		Handler: s.router,
	}
	return s.httpServer.ListenAndServe()
}

// Shutdown gracefully shuts down the HTTP server
func (s *RESTServer) Shutdown(ctx context.Context) error {
	if s.httpServer == nil {
		return nil
	}
	return s.httpServer.Shutdown(ctx)
}

func (s *RESTServer) authMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get token from header
		token := c.GetHeader("X-API-Key")
		if token == "" {
			token = c.GetHeader("Authorization")
			// Remove "Bearer " prefix if present
			if len(token) > 7 && token[:7] == "Bearer " {
				token = token[7:]
			}
		}

		// Also check query parameter (for WebSockets and simple webhooks)
		if token == "" {
			token = c.Query("token")
		}
		if token == "" {
			token = c.Query("apikey")
		}

		if token == "" {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "No authentication token provided"})
			c.Abort()
			return
		}

		// Verify token matches stored API key
		var encryptedKey string
		err := s.db.QueryRow("SELECT value FROM settings WHERE key = 'api_key'").Scan(&encryptedKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication error"})
			c.Abort()
			return
		}

		// Decrypt the stored API key
		storedKey, err := crypto.Decrypt(encryptedKey)
		if err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Authentication error"})
			c.Abort()
			return
		}

		// Use constant-time comparison to prevent timing attacks
		if subtle.ConstantTimeCompare([]byte(token), []byte(storedKey)) != 1 {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid authentication token"})
			c.Abort()
			return
		}

		c.Next()
	}
}
