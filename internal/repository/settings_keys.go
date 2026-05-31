package repository

// Tunable setting keys. These rows in the `settings` KV table back the
// /config UI's runtime-editable knobs. Each one has a matching env var
// (HEALARR_*) whose presence locks the field from the UI side; env wins
// over DB wins over default.
//
// Naming convention: lowercase, dotted by area, *_seconds suffix for
// durations stored as Go time.ParseDuration strings ("60s", "10m"),
// *_days/_workers for integers. The keys are stable identifiers - the
// frontend stores no copies, it asks /api/config/tunables for the
// catalog plus values.
const (
	// Scan defaults
	SettingKeyThoroughDuration  = "scan.thorough_duration_seconds"
	SettingKeyThoroughTimeout   = "scan.thorough_timeout_seconds"
	SettingKeyHwAccel           = "scan.hwaccel"
	SettingKeyDefaultMaxRetries = "scan.default_max_retries"
	SettingKeyScannerWorkers    = "scan.workers"
	SettingKeyShutdownTimeout   = "scan.shutdown_timeout_seconds"
	SettingKeyDryRunMode        = "scan.dry_run_mode"

	// Maintenance / lifecycle
	SettingKeyRetentionDays        = "maintenance.retention_days"
	SettingKeyVerificationTimeout  = "maintenance.verification_timeout_seconds"
	SettingKeyVerificationInterval = "maintenance.verification_interval_seconds"
	SettingKeyStaleThreshold       = "maintenance.stale_threshold_seconds"

	// Outbound rate limits
	SettingKeyArrRateLimitRPS   = "limits.arr_rate_limit_rps"
	SettingKeyArrRateLimitBurst = "limits.arr_rate_limit_burst"
)
