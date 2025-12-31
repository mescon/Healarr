package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mescon/Healarr/internal/api"
	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/db"
	"github.com/mescon/Healarr/internal/eventbus"
	"github.com/mescon/Healarr/internal/integration"
	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/metrics"
	"github.com/mescon/Healarr/internal/notifier"
	"github.com/mescon/Healarr/internal/services"
	"github.com/mescon/Healarr/internal/web"
)

func main() {
	// Define command line flags (these override environment variables)
	showVersion := flag.Bool("version", false, "Print version and exit")
	flag.BoolVar(showVersion, "v", false, "Print version and exit (shorthand)")

	// Configuration flags - all can also be set via environment variables (HEALARR_*)
	flagPort := flag.String("port", "", "HTTP server port (env: HEALARR_PORT, default: 3090)")
	flagBasePath := flag.String("base-path", "", "URL base path for reverse proxy (env: HEALARR_BASE_PATH, default: /)")
	flagLogLevel := flag.String("log-level", "", "Log level: debug, info, error (env: HEALARR_LOG_LEVEL, default: info)")
	flagDataDir := flag.String("data-dir", "", "Data directory path (env: HEALARR_DATA_DIR)")
	flagDatabasePath := flag.String("database-path", "", "Database file path (env: HEALARR_DATABASE_PATH)")
	flagWebDir := flag.String("web-dir", "", "Web assets directory (env: HEALARR_WEB_DIR)")
	flagDryRun := flag.Bool("dry-run", false, "Dry run mode - no files deleted (env: HEALARR_DRY_RUN)")
	flagRetentionDays := flag.Int("retention-days", -1, "Days to keep old data, 0 to disable pruning (env: HEALARR_RETENTION_DAYS, default: 90)")
	flagMaxRetries := flag.Int("max-retries", 0, "Default max remediation retries (env: HEALARR_DEFAULT_MAX_RETRIES, default: 3)")
	flagVerificationTimeout := flag.Duration("verification-timeout", 0, "Max time to wait for file replacement (env: HEALARR_VERIFICATION_TIMEOUT, default: 72h)")
	flagVerificationInterval := flag.Duration("verification-interval", 0, "Polling interval for verification (env: HEALARR_VERIFICATION_INTERVAL, default: 30s)")
	flagArrRateLimitRPS := flag.Float64("arr-rate-limit", 0, "Max requests per second to *arr APIs (env: HEALARR_ARR_RATE_LIMIT_RPS, default: 5)")
	flagArrRateLimitBurst := flag.Int("arr-rate-burst", 0, "Burst size for *arr rate limiting (env: HEALARR_ARR_RATE_LIMIT_BURST, default: 10)")

	flag.Parse()

	if *showVersion {
		fmt.Printf("Healarr %s\n", config.Version)
		os.Exit(0)
	}

	// Load configuration from environment variables (initial load, refreshed after flags)
	config.Load()

	// Apply command-line flag overrides
	flagOverrides := config.FlagOverrides{
		Port:                 flagPort,
		BasePath:             flagBasePath,
		LogLevel:             flagLogLevel,
		DataDir:              flagDataDir,
		DatabasePath:         flagDatabasePath,
		WebDir:               flagWebDir,
		DryRunMode:           flagDryRun,
		DefaultMaxRetries:    flagMaxRetries,
		VerificationTimeout:  flagVerificationTimeout,
		VerificationInterval: flagVerificationInterval,
		ArrRateLimitRPS:      flagArrRateLimitRPS,
		ArrRateLimitBurst:    flagArrRateLimitBurst,
	}
	// Special handling for retention days: -1 means not set (use default), 0 means disable
	if *flagRetentionDays >= 0 {
		flagOverrides.RetentionDays = flagRetentionDays
	}
	config.ApplyFlags(flagOverrides)

	// Refresh config after applying flags
	cfg := config.Get()

	// Initialize logger with configured log directory
	logger.Init(cfg.LogDir)

	// Set log level from config
	logger.SetLevel(cfg.LogLevel)

	logger.Infof("========================================")
	logger.Infof("Starting Healarr %s...", config.Version)
	logger.Infof("Health Evaluation And Library Auto-Recovery for *aRR")
	logger.Infof("========================================")

	// Log initial configuration (base path may be updated from DB)
	logger.Infof("Configuration:")
	logger.Infof("  Port: %s", cfg.Port)
	logger.Infof("  Log Level: %s", cfg.LogLevel)
	logger.Infof("  Data Directory: %s", cfg.DataDir)
	logger.Infof("  Database: %s", cfg.DatabasePath)
	logger.Infof("  Log Directory: %s", cfg.LogDir)
	if !web.HasEmbeddedAssets() {
		logger.Infof("  Web Directory: %s", cfg.WebDir)
	}
	logger.Infof("  Verification Timeout: %s", cfg.VerificationTimeout)
	logger.Infof("  Verification Interval: %s", cfg.VerificationInterval)
	logger.Infof("  Default Max Retries: %d", cfg.DefaultMaxRetries)
	logger.Infof("  *arr API Rate Limit: %.1f req/s (burst: %d)", cfg.ArrRateLimitRPS, cfg.ArrRateLimitBurst)
	if cfg.RetentionDays > 0 {
		logger.Infof("  Data Retention: %d days", cfg.RetentionDays)
	} else {
		logger.Infof("  Data Retention: disabled (no automatic pruning)")
	}
	if cfg.DryRunMode {
		logger.Infof("  ⚠️  DRY-RUN MODE: ENABLED (no files will be deleted)")
	}

	// Initialize Database
	logger.Infof("Initializing database: %s", cfg.DatabasePath)
	repo, err := db.NewRepository(cfg.DatabasePath)
	if err != nil {
		logger.Errorf("Failed to initialize database: %v", err)
		os.Exit(1)
	}
	defer repo.Close()
	logger.Infof("✓ Database initialized successfully")

	// Create a database backup on startup
	if backupPath, err := repo.Backup(cfg.DatabasePath); err != nil {
		logger.Errorf("Failed to create startup backup: %v", err)
	} else {
		logger.Infof("✓ Database backup created: %s", backupPath)
	}

	// Start scheduled backup goroutine (every 6 hours)
	go func() {
		ticker := time.NewTicker(6 * time.Hour)
		defer ticker.Stop()
		for range ticker.C {
			if _, err := repo.Backup(cfg.DatabasePath); err != nil {
				logger.Errorf("Scheduled backup failed: %v", err)
			}
		}
	}()

	// Start scheduled maintenance goroutine (daily at 3 AM local time)
	go func() {
		retentionDays := cfg.RetentionDays // Capture config value for goroutine
		for {
			// Calculate time until next 3 AM
			now := time.Now()
			next3AM := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
			if now.After(next3AM) {
				next3AM = next3AM.Add(24 * time.Hour)
			}
			sleepDuration := next3AM.Sub(now)
			logger.Debugf("Next database maintenance scheduled in %v", sleepDuration)

			time.Sleep(sleepDuration)

			// Run maintenance with configured retention
			if err := repo.RunMaintenance(retentionDays); err != nil {
				logger.Errorf("Scheduled maintenance failed: %v", err)
			}
		}
	}()

	// Load base path from database if not set via environment
	config.LoadBasePathFromDB(repo.DB)
	cfg = config.Get() // Refresh config after DB load
	logger.Infof("  Base Path: %s (source: %s)", cfg.BasePath, cfg.BasePathSource)

	// Initialize Event Bus
	logger.Infof("Initializing Event Bus...")
	eb := eventbus.NewEventBus(repo.DB)
	logger.Infof("✓ Event Bus initialized")

	// Initialize Integration
	// For verification, we try to initialize the real components
	// In production, we would handle errors properly

	// PathMapper
	logger.Infof("Initializing Path Mapper (maps *arr paths to local paths)...")
	pathMapper, err := integration.NewPathMapper(repo.DB)
	if err != nil {
		logger.Infof("⚠ Path Mapper initialized with no configured paths (configure in /config)")
	} else {
		logger.Infof("✓ Path Mapper initialized")
	}

	// HealthChecker
	logger.Infof("Initializing Health Checker (corruption detection engine)...")
	healthChecker := integration.NewHealthChecker()
	logger.Infof("✓ Health Checker initialized (ffprobe, mediainfo, handbrake)")

	// ArrClient
	logger.Infof("Initializing *arr Client (Sonarr/Radarr/Whisparr integration)...")
	arrClient := integration.NewArrClient(repo.DB)
	logger.Infof("✓ *arr Client initialized")

	// Initialize Services
	logger.Infof("Initializing core services...")
	scannerService := services.NewScannerService(repo.DB, eb, healthChecker, pathMapper)
	logger.Infof("✓ Scanner Service (detects corrupted files)")

	remediatorService := services.NewRemediatorService(eb, arrClient, pathMapper, repo.DB)
	logger.Infof("✓ Remediator Service (fixes corrupted files via *arr)")

	verifierService := services.NewVerifierService(eb, healthChecker, pathMapper, arrClient, repo.DB)
	logger.Infof("✓ Verifier Service (verifies remediation success)")

	monitorService := services.NewMonitorService(eb, repo.DB)
	logger.Infof("✓ Monitor Service (tracks corruption lifecycle)")

	schedulerService := services.NewSchedulerService(repo.DB, scannerService)
	logger.Infof("✓ Scheduler Service (cron-based scans)")

	// Initialize Notifier Service
	logger.Infof("Initializing Notification Service...")
	notifierService := notifier.NewNotifier(repo.DB, eb)
	if err := notifierService.Start(); err != nil {
		logger.Errorf("Failed to start notification service: %v", err)
		// Non-fatal - continue without notifications
	} else {
		logger.Infof("✓ Notification Service (alerts for events)")
	}

	// Initialize Metrics Service (Prometheus metrics)
	logger.Infof("Initializing Metrics Service...")
	metricsService := metrics.NewMetricsService(eb)
	metricsService.Start()
	logger.Infof("✓ Metrics Service (Prometheus endpoint at /metrics)")

	// Start Services
	logger.Infof("Starting background services...")
	remediatorService.Start()
	verifierService.Start()
	monitorService.Start()
	schedulerService.Start()
	logger.Infof("✓ All background services started")

	// Resume any interrupted scans from previous shutdown
	logger.Infof("Checking for interrupted scans to resume...")
	scannerService.ResumeInterruptedScans()

	// Start the rescan worker for files that had infrastructure errors
	scannerService.StartRescanWorker()

	// Start API Server
	logger.Infof("Initializing REST API and WebSocket server...")
	apiServer := api.NewRESTServer(repo.DB, eb, scannerService, pathMapper, schedulerService, notifierService, metricsService)
	go func() {
		addr := ":" + cfg.Port
		if err := apiServer.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Errorf("Failed to start API server: %v", err)
			os.Exit(1)
		}
	}()

	logger.Infof("========================================")
	logger.Infof("✓ Healarr %s started successfully", config.Version)
	logger.Infof("✓ Server listening on port %s", cfg.Port)
	if cfg.BasePath != "/" {
		logger.Infof("✓ Web UI available at base path: %s", cfg.BasePath)
	}
	logger.Infof("========================================")

	// Graceful shutdown handling
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	logger.Infof("========================================")
	logger.Infof("Received signal %v, initiating graceful shutdown...", sig)
	logger.Infof("========================================")

	// Create a context with timeout for graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Shutdown in reverse order of startup
	logger.Infof("Stopping Scheduler Service...")
	schedulerService.Stop()
	logger.Infof("✓ Scheduler Service stopped")

	logger.Infof("Stopping Scanner Service (saving state for interrupted scans)...")
	scannerService.Shutdown()
	logger.Infof("✓ Scanner Service stopped")

	logger.Infof("Stopping Notification Service...")
	notifierService.Stop()
	logger.Infof("✓ Notification Service stopped")

	logger.Infof("Stopping Event Bus...")
	eb.Shutdown()
	logger.Infof("✓ Event Bus stopped")

	logger.Infof("Stopping API Server...")
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		logger.Errorf("API Server shutdown error: %v", err)
	} else {
		logger.Infof("✓ API Server stopped")
	}

	logger.Infof("Closing database connection...")
	if err := repo.Close(); err != nil {
		logger.Errorf("Failed to close database connection: %v", err)
	} else {
		logger.Infof("✓ Database connection closed")
	}

	logger.Infof("========================================")
	logger.Infof("✓ Healarr shutdown complete")
	logger.Infof("========================================")
}
