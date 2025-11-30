package config

import (
	"database/sql"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Version is set at build time via -ldflags
// Default "dev" is used for development builds
var Version = "dev"

// Config holds all application configuration loaded from environment variables.
// All fields have sensible defaults if environment variables are not set.
type Config struct {
	// Port is the HTTP server listen port (default: 3090)
	Port string

	// BasePath is the URL base path for reverse proxy setups (default: "/")
	// Example: "/healarr" if hosting at domain.com/healarr/
	BasePath string

	// BasePathSource indicates where the base path came from: "environment", "database", or "default"
	BasePathSource string

	// LogLevel controls logging verbosity: "debug", "info", "error" (default: "info")
	LogLevel string

	// VerificationTimeout is the maximum time to wait for *arr to replace a corrupt file (default: 72h)
	VerificationTimeout time.Duration

	// VerificationInterval is the polling interval when checking for file replacement (default: 30s)
	VerificationInterval time.Duration

	// DefaultMaxRetries is the default retry limit for failed remediations (default: 3)
	// Can be overridden per scan path in the UI
	DefaultMaxRetries int

	// DryRunMode when true, logs remediation actions without actually deleting files (default: false)
	// Useful for testing and verification before enabling auto-remediation
	DryRunMode bool

	// ArrRateLimitRPS is the maximum requests per second to *arr APIs (default: 5)
	// Prevents hammering *arr instances during large scans
	ArrRateLimitRPS float64

	// ArrRateLimitBurst is the burst size for *arr API rate limiting (default: 10)
	// Allows short bursts above the RPS limit
	ArrRateLimitBurst int

	// RetentionDays is the number of days to keep old events and scan history (default: 90)
	// Set to 0 to disable automatic pruning
	RetentionDays int

	// DataDir is the directory for persistent data (database, logs, backups, pid file)
	// Default: /config in Docker, ./config locally
	DataDir string

	// DatabasePath is the SQLite database file path (default: <DataDir>/healarr.db)
	DatabasePath string

	// LogDir is the directory for log files (default: <DataDir>/logs)
	LogDir string

	// WebDir is the directory containing web assets (index.html, assets/, etc.)
	WebDir string
}

// Global singleton
var cfg *Config

// Load reads configuration from environment variables with sensible defaults.
// Should be called once at application startup.
func Load() *Config {
	basePath := getEnvOrDefault("HEALARR_BASE_PATH", "")
	basePathSource := "default"

	if basePath != "" {
		basePathSource = "environment"
	} else {
		basePath = "/"
	}

	// Normalize base path: ensure it starts with / and doesn't end with /
	if basePath != "/" {
		if !strings.HasPrefix(basePath, "/") {
			basePath = "/" + basePath
		}
		basePath = strings.TrimSuffix(basePath, "/")
	}

	// Determine DataDir - this is where all persistent data lives
	// Default: ./config (relative to executable or cwd)
	// In Docker: /config is created automatically
	dataDir := getEnvOrDefault("HEALARR_DATA_DIR", "")
	if dataDir == "" {
		// Check if we're in Docker (has /config directory)
		if info, err := os.Stat("/config"); err == nil && info.IsDir() {
			dataDir = "/config"
		} else {
			// Local/bare-metal - use ./config relative to executable or cwd
			if execPath, err := os.Executable(); err == nil {
				execDir := filepath.Dir(execPath)
				dataDir = filepath.Join(execDir, "config")
			} else if cwd, err := os.Getwd(); err == nil {
				dataDir = filepath.Join(cwd, "config")
			} else {
				dataDir = "./config"
			}
		}
	}

	// Ensure dataDir is absolute
	if absDataDir, err := filepath.Abs(dataDir); err == nil {
		dataDir = absDataDir
	}

	// Create data directory if it doesn't exist
	os.MkdirAll(dataDir, 0755)

	// Determine WebDir - find the web directory
	webDir := getEnvOrDefault("HEALARR_WEB_DIR", "")
	if webDir == "" {
		// Build list of candidate directories to check
		candidates := []string{
			"/app/web", // Docker container default
		}

		// Add paths relative to current working directory
		if cwd, err := os.Getwd(); err == nil {
			candidates = append(candidates,
				filepath.Join(cwd, "web"),             // cwd/web
				filepath.Join(cwd, "..", "web"),       // For running from cmd/server (../web goes to project root)
				filepath.Join(cwd, "..", "..", "web"), // Two levels up
			)
		}

		// Add paths relative to executable location
		if execPath, err := os.Executable(); err == nil {
			execDir := filepath.Dir(execPath)
			candidates = append(candidates,
				filepath.Join(execDir, "web"),             // Same dir as executable
				filepath.Join(execDir, "..", "..", "web"), // For go run from cmd/server
				filepath.Join(execDir, "..", "web"),       // One level up
			)
		}

		// Check each candidate
		for _, candidate := range candidates {
			if info, err := os.Stat(candidate); err == nil && info.IsDir() {
				// Verify it contains index.html
				indexPath := filepath.Join(candidate, "index.html")
				if _, err := os.Stat(indexPath); err == nil {
					if absPath, err := filepath.Abs(candidate); err == nil {
						webDir = absPath
						break
					}
				}
			}
		}

		// Fall back to relative path if nothing found
		if webDir == "" {
			webDir = "./web"
		}
	}

	// Database path - inside data directory unless explicitly set
	dbPath := getEnvOrDefault("HEALARR_DATABASE_PATH", "")
	if dbPath == "" {
		dbPath = filepath.Join(dataDir, "healarr.db")
	}

	// Log directory - inside data directory
	logDir := filepath.Join(dataDir, "logs")
	os.MkdirAll(logDir, 0755)

	cfg = &Config{
		Port:                 getEnvOrDefault("HEALARR_PORT", "3090"),
		BasePath:             basePath,
		BasePathSource:       basePathSource,
		LogLevel:             strings.ToLower(getEnvOrDefault("HEALARR_LOG_LEVEL", "info")),
		VerificationTimeout:  getEnvDurationOrDefault("HEALARR_VERIFICATION_TIMEOUT", 72*time.Hour),
		VerificationInterval: getEnvDurationOrDefault("HEALARR_VERIFICATION_INTERVAL", 30*time.Second),
		DefaultMaxRetries:    getEnvIntOrDefault("HEALARR_DEFAULT_MAX_RETRIES", 3),
		DryRunMode:           getEnvBoolOrDefault("HEALARR_DRY_RUN", false),
		ArrRateLimitRPS:      getEnvFloatOrDefault("HEALARR_ARR_RATE_LIMIT_RPS", 5.0),
		ArrRateLimitBurst:    getEnvIntOrDefault("HEALARR_ARR_RATE_LIMIT_BURST", 10),
		RetentionDays:        getEnvIntOrDefault("HEALARR_RETENTION_DAYS", 90),
		DataDir:              dataDir,
		DatabasePath:         dbPath,
		LogDir:               logDir,
		WebDir:               webDir,
	}

	// Validate log level
	switch cfg.LogLevel {
	case "debug", "info", "error":
		// Valid
	default:
		cfg.LogLevel = "info" // Fall back to info for invalid values
	}

	return cfg
}

// LoadBasePathFromDB loads the base path from the database if not set via environment.
// Should be called after database is initialized.
func LoadBasePathFromDB(db *sql.DB) {
	if cfg == nil {
		return
	}

	// Only load from DB if not set via environment variable
	if cfg.BasePathSource == "environment" {
		return
	}

	var basePath string
	err := db.QueryRow("SELECT value FROM settings WHERE key = 'base_path'").Scan(&basePath)
	if err != nil || basePath == "" {
		return // Keep default
	}

	// Normalize
	if basePath != "/" {
		if !strings.HasPrefix(basePath, "/") {
			basePath = "/" + basePath
		}
		basePath = strings.TrimSuffix(basePath, "/")
	}

	cfg.BasePath = basePath
	cfg.BasePathSource = "database"
}

// Get returns the current configuration. Panics if Load() hasn't been called.
func Get() *Config {
	if cfg == nil {
		panic("config.Load() must be called before config.Get()")
	}
	return cfg
}

// SetForTesting allows tests to set the global config without calling Load().
// This should ONLY be used in test code.
func SetForTesting(c *Config) {
	cfg = c
}

// NewTestConfig returns a minimal Config suitable for unit tests.
func NewTestConfig() *Config {
	return &Config{
		Port:                 "8080",
		BasePath:             "/",
		BasePathSource:       "test",
		LogLevel:             "debug",
		VerificationTimeout:  72 * time.Hour,
		VerificationInterval: 30 * time.Second,
		DefaultMaxRetries:    3,
		DryRunMode:           false,
		ArrRateLimitRPS:      5,
		ArrRateLimitBurst:    10,
		RetentionDays:        90,
		DataDir:              "/tmp/healarr-test",
		DatabasePath:         "/tmp/healarr-test/healarr.db",
		LogDir:               "/tmp/healarr-test/logs",
		WebDir:               "",
	}
}

// getEnvOrDefault returns the environment variable value or the default if not set.
func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

// getEnvIntOrDefault returns the environment variable as an int or the default if not set/invalid.
func getEnvIntOrDefault(key string, defaultValue int) int {
	if value := os.Getenv(key); value != "" {
		if i, err := strconv.Atoi(value); err == nil {
			return i
		}
	}
	return defaultValue
}

// getEnvDurationOrDefault returns the environment variable as a duration or the default if not set/invalid.
// Accepts Go duration strings like "30s", "5m", "72h".
func getEnvDurationOrDefault(key string, defaultValue time.Duration) time.Duration {
	if value := os.Getenv(key); value != "" {
		if d, err := time.ParseDuration(value); err == nil {
			return d
		}
	}
	return defaultValue
}

// getEnvBoolOrDefault returns the environment variable as a bool or the default if not set.
// Accepts "true", "1", "yes" as true values (case-insensitive).
func getEnvBoolOrDefault(key string, defaultValue bool) bool {
	if value := os.Getenv(key); value != "" {
		lower := strings.ToLower(value)
		return lower == "true" || lower == "1" || lower == "yes"
	}
	return defaultValue
}

// getEnvFloatOrDefault returns the environment variable as a float64 or the default if not set/invalid.
func getEnvFloatOrDefault(key string, defaultValue float64) float64 {
	if value := os.Getenv(key); value != "" {
		if f, err := strconv.ParseFloat(value, 64); err == nil {
			return f
		}
	}
	return defaultValue
}

// FlagOverrides holds command-line flag values that can override environment variables
type FlagOverrides struct {
	Port                 *string
	BasePath             *string
	LogLevel             *string
	VerificationTimeout  *time.Duration
	VerificationInterval *time.Duration
	DefaultMaxRetries    *int
	DryRunMode           *bool
	ArrRateLimitRPS      *float64
	ArrRateLimitBurst    *int
	RetentionDays        *int
	DataDir              *string
	DatabasePath         *string
	WebDir               *string
}

// ApplyFlags applies command-line flag overrides to the configuration.
// Should be called after Load() and after flag parsing.
// Only non-nil values with non-default flag values will override.
func ApplyFlags(flags FlagOverrides) {
	if cfg == nil {
		return
	}

	if flags.Port != nil && *flags.Port != "" {
		cfg.Port = *flags.Port
	}
	if flags.BasePath != nil && *flags.BasePath != "" {
		basePath := *flags.BasePath
		// Normalize base path
		if basePath != "/" {
			if !strings.HasPrefix(basePath, "/") {
				basePath = "/" + basePath
			}
			basePath = strings.TrimSuffix(basePath, "/")
		}
		cfg.BasePath = basePath
		cfg.BasePathSource = "flag"
	}
	if flags.LogLevel != nil && *flags.LogLevel != "" {
		cfg.LogLevel = strings.ToLower(*flags.LogLevel)
	}
	if flags.VerificationTimeout != nil && *flags.VerificationTimeout != 0 {
		cfg.VerificationTimeout = *flags.VerificationTimeout
	}
	if flags.VerificationInterval != nil && *flags.VerificationInterval != 0 {
		cfg.VerificationInterval = *flags.VerificationInterval
	}
	if flags.DefaultMaxRetries != nil && *flags.DefaultMaxRetries != 0 {
		cfg.DefaultMaxRetries = *flags.DefaultMaxRetries
	}
	if flags.DryRunMode != nil {
		cfg.DryRunMode = *flags.DryRunMode
	}
	if flags.ArrRateLimitRPS != nil && *flags.ArrRateLimitRPS != 0 {
		cfg.ArrRateLimitRPS = *flags.ArrRateLimitRPS
	}
	if flags.ArrRateLimitBurst != nil && *flags.ArrRateLimitBurst != 0 {
		cfg.ArrRateLimitBurst = *flags.ArrRateLimitBurst
	}
	if flags.RetentionDays != nil {
		cfg.RetentionDays = *flags.RetentionDays
	}
	if flags.DataDir != nil && *flags.DataDir != "" {
		cfg.DataDir = *flags.DataDir
	}
	if flags.DatabasePath != nil && *flags.DatabasePath != "" {
		cfg.DatabasePath = *flags.DatabasePath
	}
	if flags.WebDir != nil && *flags.WebDir != "" {
		cfg.WebDir = *flags.WebDir
	}
}
