package main

import (
	"context"
	"database/sql"
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

const logSeparator = "========================================"

// cliFlags holds all parsed command line flags
type cliFlags struct {
	showVersion          *bool
	port                 *string
	basePath             *string
	logLevel             *string
	dataDir              *string
	databasePath         *string
	webDir               *string
	dryRun               *bool
	retentionDays        *int
	maxRetries           *int
	verificationTimeout  *time.Duration
	verificationInterval *time.Duration
	staleThreshold       *time.Duration
	arrRateLimitRPS      *float64
	arrRateLimitBurst    *int
}

// parseFlags defines and parses command line flags
func parseFlags() cliFlags {
	flags := cliFlags{
		showVersion:          flag.Bool("version", false, "Print version and exit"),
		port:                 flag.String("port", "", "HTTP server port (env: HEALARR_PORT, default: 3090)"),
		basePath:             flag.String("base-path", "", "URL base path for reverse proxy (env: HEALARR_BASE_PATH, default: /)"),
		logLevel:             flag.String("log-level", "", "Log level: debug, info, error (env: HEALARR_LOG_LEVEL, default: info)"),
		dataDir:              flag.String("data-dir", "", "Data directory path (env: HEALARR_DATA_DIR)"),
		databasePath:         flag.String("database-path", "", "Database file path (env: HEALARR_DATABASE_PATH)"),
		webDir:               flag.String("web-dir", "", "Web assets directory (env: HEALARR_WEB_DIR)"),
		dryRun:               flag.Bool("dry-run", false, "Dry run mode - no files deleted (env: HEALARR_DRY_RUN)"),
		retentionDays:        flag.Int("retention-days", -1, "Days to keep old data, 0 to disable pruning (env: HEALARR_RETENTION_DAYS, default: 90)"),
		maxRetries:           flag.Int("max-retries", 0, "Default max remediation retries (env: HEALARR_DEFAULT_MAX_RETRIES, default: 3)"),
		verificationTimeout:  flag.Duration("verification-timeout", 0, "Max time to wait for file replacement (env: HEALARR_VERIFICATION_TIMEOUT, default: 72h)"),
		verificationInterval: flag.Duration("verification-interval", 0, "Polling interval for verification (env: HEALARR_VERIFICATION_INTERVAL, default: 30s)"),
		staleThreshold:       flag.Duration("stale-threshold", 0, "Auto-fix items Healarr lost track of after this time (env: HEALARR_STALE_THRESHOLD, default: 24h)"),
		arrRateLimitRPS:      flag.Float64("arr-rate-limit", 0, "Max requests per second to *arr APIs (env: HEALARR_ARR_RATE_LIMIT_RPS, default: 5)"),
		arrRateLimitBurst:    flag.Int("arr-rate-burst", 0, "Burst size for *arr rate limiting (env: HEALARR_ARR_RATE_LIMIT_BURST, default: 10)"),
	}
	flag.BoolVar(flags.showVersion, "v", false, "Print version and exit (shorthand)")
	flag.Parse()
	return flags
}

// applyFlagOverrides applies CLI flags to the configuration
func applyFlagOverrides(flags cliFlags) {
	flagOverrides := config.FlagOverrides{
		Port:                 flags.port,
		BasePath:             flags.basePath,
		LogLevel:             flags.logLevel,
		DataDir:              flags.dataDir,
		DatabasePath:         flags.databasePath,
		WebDir:               flags.webDir,
		DryRunMode:           flags.dryRun,
		DefaultMaxRetries:    flags.maxRetries,
		VerificationTimeout:  flags.verificationTimeout,
		VerificationInterval: flags.verificationInterval,
		StaleThreshold:       flags.staleThreshold,
		ArrRateLimitRPS:      flags.arrRateLimitRPS,
		ArrRateLimitBurst:    flags.arrRateLimitBurst,
	}
	// Special handling for retention days: -1 means not set (use default), 0 means disable
	if *flags.retentionDays >= 0 {
		flagOverrides.RetentionDays = flags.retentionDays
	}
	config.ApplyFlags(flagOverrides)
}

// logConfiguration logs the current configuration
func logConfiguration(cfg *config.Config) {
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
	logger.Infof("  Stale Threshold: %s", cfg.StaleThreshold)
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
}

// serviceDeps holds all initialized services for dependency injection
type serviceDeps struct {
	repo                 *db.Repository
	eb                   *eventbus.EventBus
	pathMapper           integration.PathMapper
	healthChecker        integration.HealthChecker
	arrClient            integration.ArrClient
	scannerService       *services.ScannerService
	remediatorService    *services.RemediatorService
	verifierService      *services.VerifierService
	monitorService       *services.MonitorService
	healthMonitorService *services.HealthMonitorService
	recoveryService      *services.RecoveryService
	schedulerService     *services.SchedulerService
	eventReplayService   *services.EventReplayService
	notifierService      *notifier.Notifier
	metricsService       *metrics.MetricsService
	stopCheckpoint       func()
}

// initDatabase initializes the database and starts background maintenance goroutines.
func initDatabase(cfg *config.Config) (*db.Repository, func()) {
	logger.Infof("Initializing database: %s", cfg.DatabasePath)
	repo, err := db.NewRepository(cfg.DatabasePath)
	if err != nil {
		logger.Errorf("Failed to initialize database: %v", err)
		os.Exit(1)
	}
	logger.Infof("✓ Database initialized successfully")

	// Create a database backup on startup
	if backupPath, err := repo.Backup(cfg.DatabasePath); err != nil {
		logger.Errorf("Failed to create startup backup: %v", err)
	} else {
		logger.Infof("✓ Database backup created: %s", backupPath)
	}

	// Start scheduled backup goroutine (every 6 hours)
	go runScheduledBackups(repo, cfg.DatabasePath)

	// Start periodic WAL checkpoint (every 5 minutes)
	stopCheckpoint := repo.StartPeriodicCheckpoint(5 * time.Minute)
	logger.Debugf("✓ Periodic WAL checkpoint started (every 5 minutes)")

	// Start scheduled maintenance goroutine (daily at 3 AM local time)
	go runScheduledMaintenance(repo, cfg.RetentionDays)

	return repo, stopCheckpoint
}

// runScheduledBackups runs database backups every 6 hours.
func runScheduledBackups(repo *db.Repository, dbPath string) {
	ticker := time.NewTicker(6 * time.Hour)
	defer ticker.Stop()
	for range ticker.C {
		if _, err := repo.Backup(dbPath); err != nil {
			logger.Errorf("Scheduled backup failed: %v", err)
		}
	}
}

// runScheduledMaintenance runs database maintenance daily at 3 AM local time.
func runScheduledMaintenance(repo *db.Repository, retentionDays int) {
	for {
		sleepDuration := timeUntilNext3AM()
		logger.Debugf("Next database maintenance scheduled in %v", sleepDuration)
		time.Sleep(sleepDuration)

		if err := repo.RunMaintenance(retentionDays); err != nil {
			logger.Errorf("Scheduled maintenance failed: %v", err)
		}
	}
}

// timeUntilNext3AM calculates the duration until the next 3 AM local time.
func timeUntilNext3AM() time.Duration {
	now := time.Now()
	next3AM := time.Date(now.Year(), now.Month(), now.Day(), 3, 0, 0, 0, now.Location())
	if now.After(next3AM) {
		next3AM = next3AM.Add(24 * time.Hour)
	}
	return next3AM.Sub(now)
}

// initIntegration initializes integration components (path mapper, health checker, arr client).
func initIntegration(sqlDB *sql.DB, cfg *config.Config) (integration.PathMapper, integration.HealthChecker, integration.ArrClient) {
	logger.Infof("Initializing Path Mapper (maps *arr paths to local paths)...")
	pathMapper, err := integration.NewPathMapper(sqlDB)
	if err != nil {
		logger.Infof("⚠ Path Mapper initialized with no configured paths (configure in /config)")
	} else {
		logger.Infof("✓ Path Mapper initialized")
	}

	logger.Infof("Initializing Health Checker (corruption detection engine)...")
	healthChecker := integration.NewHealthCheckerWithPaths(
		cfg.FFprobePath, cfg.FFmpegPath, cfg.MediaInfoPath, cfg.HandBrakePath,
	)
	logger.Infof("✓ Health Checker initialized (ffprobe, mediainfo, handbrake)")

	logger.Infof("Initializing *arr Client (Sonarr/Radarr/Whisparr integration)...")
	arrClient := integration.NewArrClient(sqlDB)
	logger.Infof("✓ *arr Client initialized")

	return pathMapper, healthChecker, arrClient
}

// initCoreServices initializes all core services.
func initCoreServices(
	sqlDB *sql.DB, eb *eventbus.EventBus,
	healthChecker integration.HealthChecker, pathMapper integration.PathMapper,
	arrClient integration.ArrClient, cfg *config.Config,
) (*services.ScannerService, *services.RemediatorService, *services.VerifierService,
	*services.MonitorService, *services.HealthMonitorService, *services.RecoveryService,
	*services.SchedulerService, *services.EventReplayService) {
	logger.Infof("Initializing core services...")

	scannerService := services.NewScannerService(sqlDB, eb, healthChecker, pathMapper)
	logger.Infof("✓ Scanner Service (detects corrupted files)")

	remediatorService := services.NewRemediatorService(eb, arrClient, pathMapper, sqlDB)
	logger.Infof("✓ Remediator Service (fixes corrupted files via *arr)")

	verifierService := services.NewVerifierService(eb, healthChecker, pathMapper, arrClient, sqlDB)
	logger.Infof("✓ Verifier Service (verifies remediation success)")

	monitorService := services.NewMonitorService(eb, sqlDB)
	logger.Infof("✓ Monitor Service (tracks corruption lifecycle)")

	healthMonitorService := services.NewHealthMonitorService(sqlDB, eb, arrClient, cfg.StaleThreshold)
	logger.Infof("✓ Health Monitor Service (detects stuck remediations)")

	recoveryService := services.NewRecoveryService(sqlDB, eb, arrClient, pathMapper, healthChecker, cfg.StaleThreshold)
	logger.Infof("✓ Recovery Service (recovers stale remediations on startup)")

	schedulerService := services.NewSchedulerService(sqlDB, scannerService)
	logger.Infof("✓ Scheduler Service (cron-based scans)")

	eventReplayService := services.NewEventReplayService(sqlDB, eb)
	logger.Infof("✓ Event Replay Service (replays unprocessed events on startup)")

	return scannerService, remediatorService, verifierService, monitorService,
		healthMonitorService, recoveryService, schedulerService, eventReplayService
}

// initNotifierAndMetrics initializes the notification and metrics services.
func initNotifierAndMetrics(sqlDB *sql.DB, eb *eventbus.EventBus) (*notifier.Notifier, *metrics.MetricsService) {
	logger.Infof("Initializing Notification Service...")
	notifierService := notifier.NewNotifier(sqlDB, eb)
	if err := notifierService.Start(); err != nil {
		logger.Errorf("Failed to start notification service: %v", err)
	} else {
		logger.Infof("✓ Notification Service (alerts for events)")
	}

	logger.Infof("Initializing Metrics Service...")
	metricsService := metrics.NewMetricsService(eb)
	metricsService.Start()
	logger.Infof("✓ Metrics Service (Prometheus endpoint at /metrics)")

	return notifierService, metricsService
}

// startBackgroundServices starts all background services and performs initial recovery.
func startBackgroundServices(deps *serviceDeps) {
	logger.Infof("Starting background services...")
	deps.remediatorService.Start()
	deps.verifierService.Start()
	deps.monitorService.Start()
	deps.healthMonitorService.Start()

	// Clean up orphaned schedules before starting the scheduler
	if cleaned, err := deps.schedulerService.CleanupOrphanedSchedules(); err != nil {
		logger.Errorf("Failed to cleanup orphaned schedules: %v", err)
	} else if cleaned > 0 {
		logger.Debugf("Cleaned up %d orphaned schedules", cleaned)
	}

	logger.Infof("Starting Scheduler Service...")
	deps.schedulerService.Start()
	logger.Infof("✓ All background services started")

	// Replay unprocessed events AFTER subscribers are ready but BEFORE recovery.
	// This ensures events that were persisted but not processed before a restart
	// are delivered to their intended subscribers.
	if err := deps.eventReplayService.ReplayUnprocessedEvents(); err != nil {
		logger.Errorf("Failed to replay unprocessed events: %v", err)
	}

	// Resume any interrupted scans and start rescan worker
	logger.Infof("Checking for interrupted scans to resume...")
	deps.scannerService.ResumeInterruptedScans()
	deps.scannerService.StartRescanWorker()

	// Run recovery service to reconcile stale in-progress items
	deps.recoveryService.Run()
}

// startAPIServer initializes and starts the API server in a goroutine.
func startAPIServer(deps *serviceDeps, cfg *config.Config) *api.RESTServer {
	logger.Infof("Initializing REST API and WebSocket server...")
	apiServer := api.NewRESTServer(api.ServerDeps{
		DB:         deps.repo.DB,
		EventBus:   deps.eb,
		Scanner:    deps.scannerService,
		PathMapper: deps.pathMapper,
		ArrClient:  deps.arrClient,
		Scheduler:  deps.schedulerService,
		Notifier:   deps.notifierService,
		Metrics:    deps.metricsService,
	})

	go func() {
		addr := ":" + cfg.Port
		if err := apiServer.Start(addr); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Errorf("Failed to start API server: %v", err)
			os.Exit(1)
		}
	}()

	return apiServer
}

// logStartupComplete logs the successful startup message.
func logStartupComplete(cfg *config.Config) {
	logger.Infof(logSeparator)
	logger.Infof("✓ Healarr %s started successfully", config.Version)
	logger.Infof("✓ Server listening on port %s", cfg.Port)
	if cfg.BasePath != "/" {
		logger.Infof("✓ Web UI available at base path: %s", cfg.BasePath)
	}
	logger.Infof(logSeparator)
}

// gracefulShutdown handles the graceful shutdown of all services.
func gracefulShutdown(deps *serviceDeps, apiServer *api.RESTServer) {
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	logger.Infof("Stopping Scheduler Service...")
	deps.schedulerService.Stop()
	logger.Infof("✓ Scheduler Service stopped")

	logger.Infof("Stopping Scanner Service (saving state for interrupted scans)...")
	deps.scannerService.Shutdown()
	logger.Infof("✓ Scanner Service stopped")

	logger.Infof("Stopping Notification Service...")
	deps.notifierService.Stop()
	logger.Infof("✓ Notification Service stopped")

	logger.Infof("Stopping Health Monitor Service...")
	deps.healthMonitorService.Shutdown()
	logger.Infof("✓ Health Monitor Service stopped")

	logger.Infof("Stopping Remediator Service (waiting for in-flight remediations)...")
	deps.remediatorService.Stop()
	logger.Infof("✓ Remediator Service stopped")

	logger.Infof("Stopping Monitor Service (canceling pending retries)...")
	deps.monitorService.Stop()
	logger.Infof("✓ Monitor Service stopped")

	logger.Infof("Stopping Event Bus...")
	deps.eb.Shutdown()
	logger.Infof("✓ Event Bus stopped")

	logger.Infof("Stopping API Server...")
	if err := apiServer.Shutdown(shutdownCtx); err != nil {
		logger.Errorf("API Server shutdown error: %v", err)
	} else {
		logger.Infof("✓ API Server stopped")
	}

	logger.Infof("Closing database connection (with final checkpoint)...")
	if err := deps.repo.GracefulClose(); err != nil {
		logger.Errorf("Failed to close database connection: %v", err)
	}

	logger.Infof(logSeparator)
	logger.Infof("✓ Healarr shutdown complete")
	logger.Infof(logSeparator)
}

func main() {
	flags := parseFlags()

	if *flags.showVersion {
		fmt.Printf("Healarr %s\n", config.Version)
		os.Exit(0)
	}

	// Load configuration
	config.Load()
	applyFlagOverrides(flags)
	cfg := config.Get()

	// Initialize logger
	logger.Init(cfg.LogDir)
	logger.SetLevel(cfg.LogLevel)

	logger.Infof(logSeparator)
	logger.Infof("Starting Healarr %s...", config.Version)
	logger.Infof("Health Evaluation And Library Auto-Recovery for *aRR")
	logger.Infof(logSeparator)

	logConfiguration(cfg)
	config.ValidateAndWarn()

	// Initialize database with background maintenance
	repo, stopCheckpoint := initDatabase(cfg)
	defer stopCheckpoint()

	// Load base path from database if not set via environment
	config.LoadBasePathFromDB(repo.DB)
	cfg = config.Get()
	logger.Infof("  Base Path: %s (source: %s)", cfg.BasePath, cfg.BasePathSource)

	// Initialize event bus
	logger.Infof("Initializing Event Bus...")
	eb := eventbus.NewEventBus(repo.DB)
	logger.Infof("✓ Event Bus initialized")

	// Initialize integration components
	pathMapper, healthChecker, arrClient := initIntegration(repo.DB, cfg)

	// Initialize core services
	scannerService, remediatorService, verifierService,
		monitorService, healthMonitorService, recoveryService,
		schedulerService, eventReplayService := initCoreServices(repo.DB, eb, healthChecker, pathMapper, arrClient, cfg)

	// Initialize notification and metrics
	notifierService, metricsService := initNotifierAndMetrics(repo.DB, eb)

	// Bundle all services for dependency injection
	deps := &serviceDeps{
		repo:                 repo,
		eb:                   eb,
		pathMapper:           pathMapper,
		healthChecker:        healthChecker,
		arrClient:            arrClient,
		scannerService:       scannerService,
		remediatorService:    remediatorService,
		verifierService:      verifierService,
		monitorService:       monitorService,
		healthMonitorService: healthMonitorService,
		recoveryService:      recoveryService,
		schedulerService:     schedulerService,
		eventReplayService:   eventReplayService,
		notifierService:      notifierService,
		metricsService:       metricsService,
		stopCheckpoint:       stopCheckpoint,
	}

	// Start all background services
	startBackgroundServices(deps)

	// Start API server
	apiServer := startAPIServer(deps, cfg)
	logStartupComplete(cfg)

	// Wait for shutdown signal
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	sig := <-quit

	logger.Infof(logSeparator)
	logger.Infof("Received signal %v, initiating graceful shutdown...", sig)
	logger.Infof(logSeparator)

	gracefulShutdown(deps, apiServer)
}
