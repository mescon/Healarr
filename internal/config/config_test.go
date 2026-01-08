package config

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite" // Register pure-Go SQLite driver for database/sql
)

// =============================================================================
// Helper functions tests
// =============================================================================

func TestGetEnvOrDefault(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue string
		expected     string
	}{
		{
			name:         "env set",
			key:          "TEST_ENV_VAR",
			envValue:     "custom-value",
			defaultValue: "default",
			expected:     "custom-value",
		},
		{
			name:         "env not set",
			key:          "TEST_ENV_VAR_UNSET",
			envValue:     "",
			defaultValue: "default",
			expected:     "default",
		},
		{
			name:         "empty default",
			key:          "TEST_ENV_VAR_EMPTY",
			envValue:     "",
			defaultValue: "",
			expected:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(tt.key, tt.envValue)
			}

			got := getEnvOrDefault(tt.key, tt.defaultValue)
			if got != tt.expected {
				t.Errorf("getEnvOrDefault() = %q, want %q", got, tt.expected)
			}
		})
	}
}

func TestGetEnvIntOrDefault(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue int
		expected     int
	}{
		{
			name:         "valid int",
			key:          "TEST_INT_VAR",
			envValue:     "42",
			defaultValue: 10,
			expected:     42,
		},
		{
			name:         "invalid int",
			key:          "TEST_INT_INVALID",
			envValue:     "not-a-number",
			defaultValue: 10,
			expected:     10,
		},
		{
			name:         "env not set",
			key:          "TEST_INT_UNSET",
			envValue:     "",
			defaultValue: 10,
			expected:     10,
		},
		{
			name:         "negative int",
			key:          "TEST_INT_NEGATIVE",
			envValue:     "-5",
			defaultValue: 10,
			expected:     -5,
		},
		{
			name:         "zero",
			key:          "TEST_INT_ZERO",
			envValue:     "0",
			defaultValue: 10,
			expected:     0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(tt.key, tt.envValue)
			}

			got := getEnvIntOrDefault(tt.key, tt.defaultValue)
			if got != tt.expected {
				t.Errorf("getEnvIntOrDefault() = %d, want %d", got, tt.expected)
			}
		})
	}
}

func TestGetEnvDurationOrDefault(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue time.Duration
		expected     time.Duration
	}{
		{
			name:         "valid duration seconds",
			key:          "TEST_DUR_VAR",
			envValue:     "30s",
			defaultValue: time.Minute,
			expected:     30 * time.Second,
		},
		{
			name:         "valid duration hours",
			key:          "TEST_DUR_HOURS",
			envValue:     "72h",
			defaultValue: time.Hour,
			expected:     72 * time.Hour,
		},
		{
			name:         "invalid duration",
			key:          "TEST_DUR_INVALID",
			envValue:     "not-duration",
			defaultValue: time.Minute,
			expected:     time.Minute,
		},
		{
			name:         "env not set",
			key:          "TEST_DUR_UNSET",
			envValue:     "",
			defaultValue: time.Minute,
			expected:     time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(tt.key, tt.envValue)
			}

			got := getEnvDurationOrDefault(tt.key, tt.defaultValue)
			if got != tt.expected {
				t.Errorf("getEnvDurationOrDefault() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGetEnvBoolOrDefault(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue bool
		expected     bool
	}{
		{name: "true lowercase", key: "TEST_BOOL_1", envValue: "true", defaultValue: false, expected: true},
		{name: "TRUE uppercase", key: "TEST_BOOL_2", envValue: "TRUE", defaultValue: false, expected: true},
		{name: "1", key: "TEST_BOOL_3", envValue: "1", defaultValue: false, expected: true},
		{name: "yes lowercase", key: "TEST_BOOL_4", envValue: "yes", defaultValue: false, expected: true},
		{name: "YES uppercase", key: "TEST_BOOL_5", envValue: "YES", defaultValue: false, expected: true},
		{name: "false", key: "TEST_BOOL_6", envValue: "false", defaultValue: true, expected: false},
		{name: "0", key: "TEST_BOOL_7", envValue: "0", defaultValue: true, expected: false},
		{name: "no", key: "TEST_BOOL_8", envValue: "no", defaultValue: true, expected: false},
		{name: "random string", key: "TEST_BOOL_9", envValue: "random", defaultValue: true, expected: false},
		{name: "env not set", key: "TEST_BOOL_UNSET", envValue: "", defaultValue: true, expected: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(tt.key, tt.envValue)
			}

			got := getEnvBoolOrDefault(tt.key, tt.defaultValue)
			if got != tt.expected {
				t.Errorf("getEnvBoolOrDefault() = %v, want %v", got, tt.expected)
			}
		})
	}
}

func TestGetEnvFloatOrDefault(t *testing.T) {
	tests := []struct {
		name         string
		key          string
		envValue     string
		defaultValue float64
		expected     float64
	}{
		{name: "valid float", key: "TEST_FLOAT_1", envValue: "5.5", defaultValue: 1.0, expected: 5.5},
		{name: "integer", key: "TEST_FLOAT_2", envValue: "10", defaultValue: 1.0, expected: 10.0},
		{name: "negative", key: "TEST_FLOAT_3", envValue: "-2.5", defaultValue: 1.0, expected: -2.5},
		{name: "invalid", key: "TEST_FLOAT_4", envValue: "not-float", defaultValue: 1.0, expected: 1.0},
		{name: "not set", key: "TEST_FLOAT_UNSET", envValue: "", defaultValue: 1.0, expected: 1.0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envValue != "" {
				t.Setenv(tt.key, tt.envValue)
			}

			got := getEnvFloatOrDefault(tt.key, tt.defaultValue)
			if got != tt.expected {
				t.Errorf("getEnvFloatOrDefault() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// NewTestConfig tests
// =============================================================================

func TestNewTestConfig(t *testing.T) {
	c := NewTestConfig()

	if c == nil {
		t.Fatal("NewTestConfig() should not return nil")
	}

	if c.Port != "8080" {
		t.Errorf("Port = %s, want 8080", c.Port)
	}
	if c.BasePath != "/" {
		t.Errorf("BasePath = %s, want /", c.BasePath)
	}
	if c.BasePathSource != "test" {
		t.Errorf("BasePathSource = %s, want test", c.BasePathSource)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %s, want debug", c.LogLevel)
	}
	if c.VerificationTimeout != 72*time.Hour {
		t.Errorf("VerificationTimeout = %v, want 72h", c.VerificationTimeout)
	}
	if c.VerificationInterval != 30*time.Second {
		t.Errorf("VerificationInterval = %v, want 30s", c.VerificationInterval)
	}
	if c.DefaultMaxRetries != 3 {
		t.Errorf("DefaultMaxRetries = %d, want 3", c.DefaultMaxRetries)
	}
	if c.DryRunMode != false {
		t.Error("DryRunMode should be false")
	}
	if c.ArrRateLimitRPS != 5 {
		t.Errorf("ArrRateLimitRPS = %v, want 5", c.ArrRateLimitRPS)
	}
	if c.ArrRateLimitBurst != 10 {
		t.Errorf("ArrRateLimitBurst = %d, want 10", c.ArrRateLimitBurst)
	}
	if c.RetentionDays != 90 {
		t.Errorf("RetentionDays = %d, want 90", c.RetentionDays)
	}
}

// =============================================================================
// SetForTesting tests
// =============================================================================

func TestSetForTesting(t *testing.T) {
	// Save original
	original := cfg
	defer func() { cfg = original }()

	testCfg := &Config{Port: "9999"}
	SetForTesting(testCfg)

	got := Get()
	if got.Port != "9999" {
		t.Errorf("SetForTesting did not set config, Port = %s, want 9999", got.Port)
	}
}

// =============================================================================
// Get tests
// =============================================================================

func TestGet_PanicsWhenNotLoaded(t *testing.T) {
	// Save and clear global config
	original := cfg
	cfg = nil
	defer func() { cfg = original }()

	defer func() {
		if r := recover(); r == nil {
			t.Error("Get() should panic when config is not loaded")
		}
	}()

	_ = Get()
}

func TestGet_ReturnsConfig(t *testing.T) {
	testCfg := &Config{Port: "7777"}
	original := cfg
	cfg = testCfg
	defer func() { cfg = original }()

	got := Get()
	if got != testCfg {
		t.Error("Get() should return the global config")
	}
}

// =============================================================================
// Load tests
// =============================================================================

func TestLoad_Defaults(t *testing.T) {
	// Clear relevant env vars
	envVars := []string{
		"HEALARR_PORT", "HEALARR_BASE_PATH", "HEALARR_LOG_LEVEL",
		"HEALARR_VERIFICATION_TIMEOUT", "HEALARR_VERIFICATION_INTERVAL",
		"HEALARR_DEFAULT_MAX_RETRIES", "HEALARR_DRY_RUN",
		"HEALARR_ARR_RATE_LIMIT_RPS", "HEALARR_ARR_RATE_LIMIT_BURST",
		"HEALARR_RETENTION_DAYS", "HEALARR_DATA_DIR", "HEALARR_DATABASE_PATH",
		"HEALARR_WEB_DIR",
	}
	for _, v := range envVars {
		t.Setenv(v, "")
	}

	// Use temp directory for data
	tmpDir := t.TempDir()
	t.Setenv("HEALARR_DATA_DIR", tmpDir)

	c := Load()

	if c.Port != "3090" {
		t.Errorf("Default Port = %s, want 3090", c.Port)
	}
	if c.BasePath != "/" {
		t.Errorf("Default BasePath = %s, want /", c.BasePath)
	}
	if c.BasePathSource != "default" {
		t.Errorf("Default BasePathSource = %s, want default", c.BasePathSource)
	}
	if c.LogLevel != "info" {
		t.Errorf("Default LogLevel = %s, want info", c.LogLevel)
	}
	if c.VerificationTimeout != 72*time.Hour {
		t.Errorf("Default VerificationTimeout = %v, want 72h", c.VerificationTimeout)
	}
	if c.VerificationInterval != 30*time.Second {
		t.Errorf("Default VerificationInterval = %v, want 30s", c.VerificationInterval)
	}
	if c.DefaultMaxRetries != 3 {
		t.Errorf("Default DefaultMaxRetries = %d, want 3", c.DefaultMaxRetries)
	}
	if c.DryRunMode != false {
		t.Error("Default DryRunMode should be false")
	}
	if c.ArrRateLimitRPS != 5.0 {
		t.Errorf("Default ArrRateLimitRPS = %v, want 5.0", c.ArrRateLimitRPS)
	}
	if c.ArrRateLimitBurst != 10 {
		t.Errorf("Default ArrRateLimitBurst = %d, want 10", c.ArrRateLimitBurst)
	}
	if c.RetentionDays != 90 {
		t.Errorf("Default RetentionDays = %d, want 90", c.RetentionDays)
	}
}

func TestLoad_CustomEnvVars(t *testing.T) {
	tmpDir := t.TempDir()

	t.Setenv("HEALARR_PORT", "8080")
	t.Setenv("HEALARR_BASE_PATH", "/myapp")
	t.Setenv("HEALARR_LOG_LEVEL", "DEBUG")
	t.Setenv("HEALARR_VERIFICATION_TIMEOUT", "24h")
	t.Setenv("HEALARR_VERIFICATION_INTERVAL", "1m")
	t.Setenv("HEALARR_DEFAULT_MAX_RETRIES", "5")
	t.Setenv("HEALARR_DRY_RUN", "true")
	t.Setenv("HEALARR_ARR_RATE_LIMIT_RPS", "10.5")
	t.Setenv("HEALARR_ARR_RATE_LIMIT_BURST", "20")
	t.Setenv("HEALARR_RETENTION_DAYS", "30")
	t.Setenv("HEALARR_DATA_DIR", tmpDir)

	c := Load()

	if c.Port != "8080" {
		t.Errorf("Port = %s, want 8080", c.Port)
	}
	if c.BasePath != "/myapp" {
		t.Errorf("BasePath = %s, want /myapp", c.BasePath)
	}
	if c.BasePathSource != "environment" {
		t.Errorf("BasePathSource = %s, want environment", c.BasePathSource)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %s, want debug", c.LogLevel)
	}
	if c.VerificationTimeout != 24*time.Hour {
		t.Errorf("VerificationTimeout = %v, want 24h", c.VerificationTimeout)
	}
	if c.VerificationInterval != time.Minute {
		t.Errorf("VerificationInterval = %v, want 1m", c.VerificationInterval)
	}
	if c.DefaultMaxRetries != 5 {
		t.Errorf("DefaultMaxRetries = %d, want 5", c.DefaultMaxRetries)
	}
	if c.DryRunMode != true {
		t.Error("DryRunMode should be true")
	}
	if c.ArrRateLimitRPS != 10.5 {
		t.Errorf("ArrRateLimitRPS = %v, want 10.5", c.ArrRateLimitRPS)
	}
	if c.ArrRateLimitBurst != 20 {
		t.Errorf("ArrRateLimitBurst = %d, want 20", c.ArrRateLimitBurst)
	}
	if c.RetentionDays != 30 {
		t.Errorf("RetentionDays = %d, want 30", c.RetentionDays)
	}
}

func TestLoad_BasePathNormalization(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "with leading slash", input: "/api", expected: "/api"},
		{name: "without leading slash", input: "api", expected: "/api"},
		{name: "with trailing slash", input: "/api/", expected: "/api"},
		{name: "both slashes", input: "/api/", expected: "/api"},
		{name: "root path", input: "/", expected: "/"},
		{name: "nested path", input: "/healarr/v1/", expected: "/healarr/v1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("HEALARR_DATA_DIR", tmpDir)
			t.Setenv("HEALARR_BASE_PATH", tt.input)

			c := Load()
			if c.BasePath != tt.expected {
				t.Errorf("BasePath = %q, want %q", c.BasePath, tt.expected)
			}
		})
	}
}

func TestLoad_InvalidLogLevel(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HEALARR_DATA_DIR", tmpDir)
	t.Setenv("HEALARR_LOG_LEVEL", "invalid")

	c := Load()

	if c.LogLevel != "info" {
		t.Errorf("Invalid log level should fall back to info, got %s", c.LogLevel)
	}
}

func TestLoad_ValidLogLevels(t *testing.T) {
	for _, level := range []string{"debug", "info", "error"} {
		t.Run(level, func(t *testing.T) {
			tmpDir := t.TempDir()
			t.Setenv("HEALARR_DATA_DIR", tmpDir)
			t.Setenv("HEALARR_LOG_LEVEL", level)

			c := Load()
			if c.LogLevel != level {
				t.Errorf("LogLevel = %s, want %s", c.LogLevel, level)
			}
		})
	}
}

// =============================================================================
// LoadBasePathFromDB tests
// =============================================================================

func TestLoadBasePathFromDB_NotLoaded(t *testing.T) {
	t.Helper() // Mark as helper to use t parameter
	// Save and clear global config
	original := cfg
	cfg = nil
	defer func() { cfg = original }()

	// Should not panic
	LoadBasePathFromDB(nil)
}

func TestLoadBasePathFromDB_EnvironmentOverride(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HEALARR_DATA_DIR", tmpDir)
	t.Setenv("HEALARR_BASE_PATH", "/env-path")

	c := Load()
	if c.BasePathSource != "environment" {
		t.Skip("Config source is not environment")
	}

	// Create test database
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer db.Close()

	_, _ = db.Exec("CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)")
	_, _ = db.Exec("INSERT INTO settings (key, value) VALUES ('base_path', '/db-path')")

	// Load should not change value since env is set
	LoadBasePathFromDB(db)

	if c.BasePath != "/env-path" {
		t.Errorf("BasePath should stay /env-path when set via environment, got %s", c.BasePath)
	}
}

func TestLoadBasePathFromDB_LoadsFromDatabase(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HEALARR_DATA_DIR", tmpDir)
	t.Setenv("HEALARR_BASE_PATH", "") // Clear env

	c := Load()
	if c.BasePathSource != "default" {
		t.Skipf("Config source is not default: %s", c.BasePathSource)
	}

	// Create test database
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer db.Close()

	_, _ = db.Exec("CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)")
	_, _ = db.Exec("INSERT INTO settings (key, value) VALUES ('base_path', '/db-path')")

	LoadBasePathFromDB(db)

	if c.BasePath != "/db-path" {
		t.Errorf("BasePath = %s, want /db-path", c.BasePath)
	}
	if c.BasePathSource != "database" {
		t.Errorf("BasePathSource = %s, want database", c.BasePathSource)
	}
}

func TestLoadBasePathFromDB_NormalizesPath(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HEALARR_DATA_DIR", tmpDir)
	t.Setenv("HEALARR_BASE_PATH", "")

	c := Load()

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("Failed to open db: %v", err)
	}
	defer db.Close()

	_, _ = db.Exec("CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT)")
	_, _ = db.Exec("INSERT INTO settings (key, value) VALUES ('base_path', 'no-leading-slash/')")

	LoadBasePathFromDB(db)

	if c.BasePath != "/no-leading-slash" {
		t.Errorf("BasePath should be normalized, got %s", c.BasePath)
	}
}

// =============================================================================
// ApplyFlags tests
// =============================================================================

func TestApplyFlags_NilConfig(t *testing.T) {
	t.Helper() // Mark as helper to use t parameter
	original := cfg
	cfg = nil
	defer func() { cfg = original }()

	// Should not panic
	ApplyFlags(FlagOverrides{})
}

func TestApplyFlags_AllFlags(t *testing.T) {
	c := NewTestConfig()
	SetForTesting(c)
	defer func() { cfg = nil }()

	port := "9999"
	basePath := "/flagged"
	logLevel := "error"
	timeout := 48 * time.Hour
	interval := 1 * time.Minute
	retries := 10
	dryRun := true
	rps := 20.0
	burst := 50
	retention := 7
	dataDir := "/custom/data"
	dbPath := "/custom/db.sqlite"
	webDir := "/custom/web"

	ApplyFlags(FlagOverrides{
		Port:                 &port,
		BasePath:             &basePath,
		LogLevel:             &logLevel,
		VerificationTimeout:  &timeout,
		VerificationInterval: &interval,
		DefaultMaxRetries:    &retries,
		DryRunMode:           &dryRun,
		ArrRateLimitRPS:      &rps,
		ArrRateLimitBurst:    &burst,
		RetentionDays:        &retention,
		DataDir:              &dataDir,
		DatabasePath:         &dbPath,
		WebDir:               &webDir,
	})

	if c.Port != "9999" {
		t.Errorf("Port = %s, want 9999", c.Port)
	}
	if c.BasePath != "/flagged" {
		t.Errorf("BasePath = %s, want /flagged", c.BasePath)
	}
	if c.BasePathSource != "flag" {
		t.Errorf("BasePathSource = %s, want flag", c.BasePathSource)
	}
	if c.LogLevel != "error" {
		t.Errorf("LogLevel = %s, want error", c.LogLevel)
	}
	if c.VerificationTimeout != 48*time.Hour {
		t.Errorf("VerificationTimeout = %v, want 48h", c.VerificationTimeout)
	}
	if c.VerificationInterval != time.Minute {
		t.Errorf("VerificationInterval = %v, want 1m", c.VerificationInterval)
	}
	if c.DefaultMaxRetries != 10 {
		t.Errorf("DefaultMaxRetries = %d, want 10", c.DefaultMaxRetries)
	}
	if c.DryRunMode != true {
		t.Error("DryRunMode should be true")
	}
	if c.ArrRateLimitRPS != 20.0 {
		t.Errorf("ArrRateLimitRPS = %v, want 20.0", c.ArrRateLimitRPS)
	}
	if c.ArrRateLimitBurst != 50 {
		t.Errorf("ArrRateLimitBurst = %d, want 50", c.ArrRateLimitBurst)
	}
	if c.RetentionDays != 7 {
		t.Errorf("RetentionDays = %d, want 7", c.RetentionDays)
	}
	if c.DataDir != "/custom/data" {
		t.Errorf("DataDir = %s, want /custom/data", c.DataDir)
	}
	if c.DatabasePath != "/custom/db.sqlite" {
		t.Errorf("DatabasePath = %s, want /custom/db.sqlite", c.DatabasePath)
	}
	if c.WebDir != "/custom/web" {
		t.Errorf("WebDir = %s, want /custom/web", c.WebDir)
	}
}

func TestApplyFlags_EmptyStringsNotApplied(t *testing.T) {
	c := NewTestConfig()
	c.Port = "original"
	SetForTesting(c)
	defer func() { cfg = nil }()

	empty := ""
	ApplyFlags(FlagOverrides{
		Port: &empty,
	})

	if c.Port != "original" {
		t.Errorf("Empty string should not override, Port = %s, want original", c.Port)
	}
}

func TestApplyFlags_ZeroValuesNotApplied(t *testing.T) {
	c := NewTestConfig()
	c.VerificationTimeout = 72 * time.Hour
	c.DefaultMaxRetries = 5
	SetForTesting(c)
	defer func() { cfg = nil }()

	zero := 0
	zeroDuration := time.Duration(0)
	ApplyFlags(FlagOverrides{
		DefaultMaxRetries:   &zero,
		VerificationTimeout: &zeroDuration,
	})

	if c.DefaultMaxRetries != 5 {
		t.Errorf("Zero should not override, DefaultMaxRetries = %d, want 5", c.DefaultMaxRetries)
	}
	if c.VerificationTimeout != 72*time.Hour {
		t.Errorf("Zero duration should not override, VerificationTimeout = %v, want 72h", c.VerificationTimeout)
	}
}

func TestApplyFlags_BasePathNormalization(t *testing.T) {
	c := NewTestConfig()
	SetForTesting(c)
	defer func() { cfg = nil }()

	path := "no-slash/"
	ApplyFlags(FlagOverrides{
		BasePath: &path,
	})

	if c.BasePath != "/no-slash" {
		t.Errorf("BasePath should be normalized, got %s", c.BasePath)
	}
}

// =============================================================================
// Directory creation tests
// =============================================================================

func TestLoad_CreatesDataDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	dataDir := filepath.Join(tmpDir, "newdir", "healarr")
	t.Setenv("HEALARR_DATA_DIR", dataDir)
	t.Setenv("HEALARR_BASE_PATH", "")

	c := Load()

	if _, err := os.Stat(c.DataDir); os.IsNotExist(err) {
		t.Error("Load() should create data directory")
	}
}

func TestLoad_CreatesLogDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	t.Setenv("HEALARR_DATA_DIR", tmpDir)
	t.Setenv("HEALARR_BASE_PATH", "")

	c := Load()

	if _, err := os.Stat(c.LogDir); os.IsNotExist(err) {
		t.Error("Load() should create log directory")
	}
}
