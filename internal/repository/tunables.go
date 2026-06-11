package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Source identifies where a tunable's effective value came from.
// Precedence: SourceEnv > SourceDB > SourceDefault.
type Source string

const (
	SourceEnv     Source = "env"
	SourceDB      Source = "db"
	SourceDefault Source = "default"
)

// TunableKind is the wire type for a tunable. The REST API converts to
// JSON accordingly; the frontend uses it to pick a control (number /
// duration string / select / checkbox).
type TunableKind string

const (
	KindDurationSeconds TunableKind = "duration_seconds" // int seconds on the wire; user enters "60s" / "10m" UI-side
	KindInt             TunableKind = "int"
	KindFloat           TunableKind = "float"
	KindBool            TunableKind = "bool"
	KindString          TunableKind = "string"
	KindEnum            TunableKind = "enum"
)

// TunableMeta is the catalog entry for a tunable. The catalog is the
// single source of truth: every tunable has exactly one entry here,
// and both the API and (indirectly) the UI iterate over it.
type TunableMeta struct {
	Key             string      // DB key (SettingKey* constant)
	EnvVar          string      // Matching HEALARR_* var name
	Kind            TunableKind // Wire type
	Default         string      // Default rendered as string for display
	Description     string      // One-line UI help text
	RequiresRestart bool        // True if changing the DB value won't take effect until restart
	EnumValues      []string    // Populated when Kind == KindEnum
	// MinInt / MaxInt / MinFloat / MaxFloat are advisory bounds the API
	// enforces on PUT. Zero values mean "unbounded" for that side.
	MinInt   int64
	MaxInt   int64
	MinFloat float64
	MaxFloat float64
}

// Catalog is the immutable list of tunables. Order is the order shown
// in the UI Advanced section (when the UI iterates this list directly).
//
// IMPORTANT: keep this list in sync with the typed getters below. The
// getters reference Key + EnvVar literally; the catalog provides the
// metadata the REST endpoint exposes.
var Catalog = []TunableMeta{
	{
		Key:         SettingKeyThoroughDuration,
		EnvVar:      "HEALARR_HEALTH_CHECK_THOROUGH_DURATION",
		Kind:        KindDurationSeconds,
		Default:     "0",
		Description: "How much of a file thorough scans decode. 0 = full file. A short prefix (e.g. 60s) catches header / decode-init / early-stream errors in seconds, useful for triaging large AV1 libraries.",
		MinInt:      0,
		MaxInt:      24 * 3600,
	},
	{
		Key:         SettingKeyThoroughTimeout,
		EnvVar:      "HEALARR_HEALTH_CHECK_THOROUGH_TIMEOUT",
		Kind:        KindDurationSeconds,
		Default:     "600",
		Description: "Per-file wall-clock cap for a thorough ffmpeg / mediainfo / HandBrake run. Files that exceed this are parked for rescan.",
		MinInt:      30,
		MaxInt:      6 * 3600,
	},
	{
		Key:         SettingKeyHwAccel,
		EnvVar:      "HEALARR_HEALTH_CHECK_HWACCEL",
		Kind:        KindEnum,
		Default:     "auto",
		Description: "ffmpeg hardware acceleration. \"auto\" probes the build, \"off\" forces software, or pick a named accelerator.",
		EnumValues:  []string{"auto", "off", "cuda", "vaapi", "qsv", "videotoolbox", "vdpau", "drm"},
	},
	{
		Key:         SettingKeyDefaultMaxRetries,
		EnvVar:      "HEALARR_DEFAULT_MAX_RETRIES",
		Kind:        KindInt,
		Default:     "3",
		Description: "Default remediation retry cap. Can still be overridden per scan path.",
		MinInt:      0,
		MaxInt:      100,
	},
	{
		Key:         SettingKeyScannerWorkers,
		EnvVar:      "HEALARR_SCANNER_WORKERS",
		Kind:        KindInt,
		Default:     "auto",
		Description: "Max concurrent file checks, shared across scans, webhook checks, rescans and verification. \"auto\" (0) = min(4, CPU cores, memory budget). Each thorough 4K check is a full ffmpeg decode (GPU VRAM or heavy CPU) - raise only if your hardware has headroom. Applies to new scans immediately; lowering also throttles scans already running.",
		MinInt:      0,
		MaxInt:      32,
	},
	{
		Key:             SettingKeyShutdownTimeout,
		EnvVar:          "HEALARR_SCANNER_SHUTDOWN_TIMEOUT",
		Kind:            KindDurationSeconds,
		Default:         "90",
		Description:     "Graceful drain time when the scanner is stopping.",
		RequiresRestart: true,
		MinInt:          1,
		MaxInt:          3600,
	},
	{
		Key:             SettingKeyDryRunMode,
		EnvVar:          "HEALARR_DRY_RUN",
		Kind:            KindBool,
		Default:         "false",
		Description:     "Global dry-run: log remediation actions without deleting files.",
		RequiresRestart: true,
	},
	{
		Key:             SettingKeyRetentionDays,
		EnvVar:          "HEALARR_RETENTION_DAYS",
		Kind:            KindInt,
		Default:         "90",
		Description:     "Days to keep events and scan history. 0 disables pruning.",
		RequiresRestart: true,
		MinInt:          0,
		MaxInt:          3650,
	},
	{
		Key:             SettingKeyVerificationTimeout,
		EnvVar:          "HEALARR_VERIFICATION_TIMEOUT",
		Kind:            KindDurationSeconds,
		Default:         "259200",
		Description:     "Max wait for the *arr to produce a replacement file (default 72h).",
		RequiresRestart: true,
		MinInt:          60,
		MaxInt:          30 * 24 * 3600,
	},
	{
		Key:             SettingKeyVerificationInterval,
		EnvVar:          "HEALARR_VERIFICATION_INTERVAL",
		Kind:            KindDurationSeconds,
		Default:         "30",
		Description:     "Poll interval while waiting for replacement.",
		RequiresRestart: true,
		MinInt:          5,
		MaxInt:          3600,
	},
	{
		Key:             SettingKeyStaleThreshold,
		EnvVar:          "HEALARR_STALE_THRESHOLD",
		Kind:            KindDurationSeconds,
		Default:         "86400",
		Description:     "Auto-recover items that have been active with no update for this long.",
		RequiresRestart: true,
		MinInt:          300,
		MaxInt:          30 * 24 * 3600,
	},
	{
		Key:             SettingKeyArrRateLimitRPS,
		EnvVar:          "HEALARR_ARR_RATE_LIMIT_RPS",
		Kind:            KindFloat,
		Default:         "5",
		Description:     "Max requests per second to *arr APIs.",
		RequiresRestart: true,
		MinFloat:        0.1,
		MaxFloat:        100,
	},
	{
		Key:             SettingKeyArrRateLimitBurst,
		EnvVar:          "HEALARR_ARR_RATE_LIMIT_BURST",
		Kind:            KindInt,
		Default:         "10",
		Description:     "Burst allowance above the RPS cap.",
		RequiresRestart: true,
		MinInt:          1,
		MaxInt:          1000,
	},
}

// CatalogByKey returns the meta entry for key, or nil if unknown.
func CatalogByKey(key string) *TunableMeta {
	for i := range Catalog {
		if Catalog[i].Key == key {
			return &Catalog[i]
		}
	}
	return nil
}

// Tunables is the live, precedence-aware view over the catalog. Callers
// in hot paths (health_checker, etc.) use the typed getters; the API
// layer uses Resolve to iterate the catalog.
type Tunables struct {
	repo *SettingsRepository
}

// NewTunables wires a resolver onto a SettingsRepository. Pass the same
// repo the rest of the app uses; reads are cheap (single SELECT).
func NewTunables(repo *SettingsRepository) *Tunables {
	return &Tunables{repo: repo}
}

// ResolvedString is a string-typed tunable value with its source.
type ResolvedString struct {
	Value  string
	Source Source
}

// ResolvedDuration is a duration-typed tunable value with its source.
type ResolvedDuration struct {
	Value  time.Duration
	Source Source
}

// ResolvedInt is an int-typed tunable value with its source.
type ResolvedInt struct {
	Value  int
	Source Source
}

// ResolvedFloat is a float-typed tunable value with its source.
type ResolvedFloat struct {
	Value  float64
	Source Source
}

// ResolvedBool is a bool-typed tunable value with its source.
type ResolvedBool struct {
	Value  bool
	Source Source
}

// ----- Typed getters ---------------------------------------------------------
//
// Each follows the same shape: try env, try DB, fall back to default. Parse
// errors at any step fall through to the next layer rather than crashing -
// a corrupt DB value should never lock the operator out of the feature.

// ThoroughDuration is the prefix duration ffmpeg decodes during thorough
// scans. Zero means "decode the whole file".
func (t *Tunables) ThoroughDuration(ctx context.Context) ResolvedDuration {
	return t.resolveDurationSeconds(ctx, "HEALARR_HEALTH_CHECK_THOROUGH_DURATION", SettingKeyThoroughDuration, 0)
}

// ThoroughTimeout is the per-file wall-clock cap for a thorough scan.
func (t *Tunables) ThoroughTimeout(ctx context.Context) ResolvedDuration {
	return t.resolveDurationSeconds(ctx, "HEALARR_HEALTH_CHECK_THOROUGH_TIMEOUT", SettingKeyThoroughTimeout, 10*time.Minute)
}

// HwAccel selects ffmpeg hardware acceleration: "auto" / "off" / accel name.
func (t *Tunables) HwAccel(ctx context.Context) ResolvedString {
	return t.resolveString(ctx, "HEALARR_HEALTH_CHECK_HWACCEL", SettingKeyHwAccel, "auto", true)
}

// ScannerWorkers is the configured max concurrent file checks. 0 means
// "auto" (the consumer derives a prudent default). This getter existed only
// as a catalog row for a long time: the UI showed and stored scan.workers,
// but no code ever read the DB value, so the knob was a silent no-op unless
// set via env (the audit's settings-drift bug class).
func (t *Tunables) ScannerWorkers(ctx context.Context) ResolvedInt {
	return t.resolveInt(ctx, "HEALARR_SCANNER_WORKERS", SettingKeyScannerWorkers, 0)
}

// resolveInt reads env > db > default. "auto" (any case) parses as 0, the
// catalog's auto sentinel. Parse errors at any layer fall through to the
// next rather than crashing.
func (t *Tunables) resolveInt(ctx context.Context, envKey, dbKey string, def int) ResolvedInt {
	parse := func(s string) (int, bool) {
		s = strings.TrimSpace(s)
		if strings.EqualFold(s, "auto") {
			return 0, true
		}
		n, err := strconv.Atoi(s)
		if err != nil || n < 0 {
			return 0, false
		}
		return n, true
	}
	if v, ok := os.LookupEnv(envKey); ok && v != "" {
		if n, ok := parse(v); ok {
			return ResolvedInt{Value: n, Source: SourceEnv}
		}
	}
	if t.repo != nil {
		if v, err := t.repo.Get(ctx, dbKey); err == nil && v != "" {
			if n, ok := parse(v); ok {
				return ResolvedInt{Value: n, Source: SourceDB}
			}
		}
	}
	return ResolvedInt{Value: def, Source: SourceDefault}
}

// resolveString reads env > db > default. If lower is true the value is
// lowercased so callers can compare against constants without re-casing.
func (t *Tunables) resolveString(ctx context.Context, envKey, dbKey, def string, lower bool) ResolvedString {
	norm := func(s string) string {
		if lower {
			return strings.ToLower(strings.TrimSpace(s))
		}
		return s
	}
	if v, ok := os.LookupEnv(envKey); ok && v != "" {
		return ResolvedString{Value: norm(v), Source: SourceEnv}
	}
	if t.repo != nil {
		if v, err := t.repo.Get(ctx, dbKey); err == nil && v != "" {
			return ResolvedString{Value: norm(v), Source: SourceDB}
		}
	}
	return ResolvedString{Value: def, Source: SourceDefault}
}

// resolveDurationSeconds reads env > db > default. Env accepts either
// time.ParseDuration syntax ("60s", "10m") or a bare integer seconds.
// DB values are bare integer seconds (the wire format).
func (t *Tunables) resolveDurationSeconds(ctx context.Context, envKey, dbKey string, def time.Duration) ResolvedDuration {
	if v, ok := os.LookupEnv(envKey); ok && v != "" {
		if d, err := parseDurationLoose(v); err == nil {
			return ResolvedDuration{Value: d, Source: SourceEnv}
		}
	}
	if t.repo != nil {
		if v, err := t.repo.Get(ctx, dbKey); err == nil && v != "" {
			if d, err := parseDurationLoose(v); err == nil {
				return ResolvedDuration{Value: d, Source: SourceDB}
			}
		}
	}
	return ResolvedDuration{Value: def, Source: SourceDefault}
}

// parseDurationLoose accepts either a Go duration string ("60s", "1h30m")
// or a bare integer (interpreted as seconds). The latter is how the API
// serializes durations on the wire.
func parseDurationLoose(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration")
	}
	if n, err := strconv.ParseInt(s, 10, 64); err == nil {
		return time.Duration(n) * time.Second, nil
	}
	return time.ParseDuration(s)
}

// ----- Catalog iteration -----------------------------------------------------

// ResolvedValue is the API-level shape of one tunable's effective value
// plus enough metadata for the UI to render and validate the field.
type ResolvedValue struct {
	Key             string      `json:"key"`
	EnvVar          string      `json:"env_var"`
	Kind            TunableKind `json:"kind"`
	Description     string      `json:"description"`
	RequiresRestart bool        `json:"requires_restart"`
	EnumValues      []string    `json:"enum_values,omitempty"`
	MinInt          int64       `json:"min_int,omitempty"`
	MaxInt          int64       `json:"max_int,omitempty"`
	MinFloat        float64     `json:"min_float,omitempty"`
	MaxFloat        float64     `json:"max_float,omitempty"`
	// Value is the effective value as a JSON-friendly type matched to Kind.
	Value any `json:"value"`
	// EnvValue is the raw string from the environment variable if it was
	// set, otherwise null. The UI uses this to display "Set by env" without
	// having to re-read process env on the client.
	EnvValue *string `json:"env_value"`
	// DBValue is the raw string from the database row if one exists,
	// otherwise null.
	DBValue *string `json:"db_value"`
	Source  Source  `json:"source"`
}

// ResolveAll iterates the catalog and resolves each entry. Returns one
// entry per catalog row, in catalog order. Read-only; safe to call from
// API handlers on every request.
func (t *Tunables) ResolveAll(ctx context.Context) ([]ResolvedValue, error) {
	out := make([]ResolvedValue, 0, len(Catalog))
	for i := range Catalog {
		v, err := t.Resolve(ctx, &Catalog[i])
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, nil
}

// Resolve resolves a single catalog entry. Exported so handlers can
// re-resolve a specific tunable after a PUT without re-reading them all.
func (t *Tunables) Resolve(ctx context.Context, m *TunableMeta) (ResolvedValue, error) {
	rv := ResolvedValue{
		Key:             m.Key,
		EnvVar:          m.EnvVar,
		Kind:            m.Kind,
		Description:     m.Description,
		RequiresRestart: m.RequiresRestart,
		EnumValues:      m.EnumValues,
		MinInt:          m.MinInt,
		MaxInt:          m.MaxInt,
		MinFloat:        m.MinFloat,
		MaxFloat:        m.MaxFloat,
		Source:          SourceDefault,
	}

	if v, ok := os.LookupEnv(m.EnvVar); ok && v != "" {
		envCopy := v
		rv.EnvValue = &envCopy
	}
	if t.repo != nil {
		v, err := t.repo.Get(ctx, m.Key)
		switch {
		case err == nil && v != "":
			dbCopy := v
			rv.DBValue = &dbCopy
		case errors.Is(err, ErrNotFound) || v == "":
			// no DB row, fall through
		case err != nil:
			return ResolvedValue{}, fmt.Errorf("read tunable %q: %w", m.Key, err)
		}
	}

	raw, src := m.Default, SourceDefault
	if rv.DBValue != nil {
		raw, src = *rv.DBValue, SourceDB
	}
	if rv.EnvValue != nil {
		raw, src = *rv.EnvValue, SourceEnv
	}
	rv.Source = src

	parsed, err := parseValueForKind(m.Kind, raw)
	if err != nil {
		// Fall back to default if a stored value is corrupt; the UI shows
		// the default and the operator can re-save to fix.
		parsed, _ = parseValueForKind(m.Kind, m.Default)
		rv.Source = SourceDefault
	}
	rv.Value = parsed
	return rv, nil
}

// parseValueForKind converts a raw string into a JSON-friendly Go value
// matching the declared Kind. For durations the value is integer seconds
// so the wire form is uniform across consumers.
func parseValueForKind(k TunableKind, s string) (any, error) {
	s = strings.TrimSpace(s)
	switch k {
	case KindInt:
		if s == "auto" {
			return 0, nil
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return nil, err
		}
		return n, nil
	case KindFloat:
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return nil, err
		}
		return f, nil
	case KindBool:
		switch strings.ToLower(s) {
		case "1", "true", "yes", "on":
			return true, nil
		case "0", "false", "no", "off", "":
			return false, nil
		}
		return nil, fmt.Errorf("invalid bool %q", s)
	case KindDurationSeconds:
		d, err := parseDurationLoose(s)
		if err != nil {
			return nil, err
		}
		return int64(d.Seconds()), nil
	case KindString, KindEnum:
		return s, nil
	}
	return nil, fmt.Errorf("unknown kind %q", k)
}

// ----- Validation + write ----------------------------------------------------

// ValidateAndStore writes one or more tunable values to the DB after
// validating each against its catalog entry. Env-locked tunables (i.e.
// those whose env var is set) are rejected so the API can never make a
// silent no-op the UI doesn't expect.
//
// Values are accepted as the JSON-decoded shape matching Kind:
//   - KindInt:                int64 or string-parseable as int
//   - KindFloat:              float64 or string-parseable as float
//   - KindBool:               bool
//   - KindDurationSeconds:    int64 seconds, or a Go duration string ("60s")
//   - KindString / KindEnum:  string
func (t *Tunables) ValidateAndStore(ctx context.Context, updates map[string]any) error {
	if t.repo == nil {
		return errors.New("tunables: no settings repository wired")
	}
	stored := make(map[string]string, len(updates))
	for key, raw := range updates {
		m := CatalogByKey(key)
		if m == nil {
			return fmt.Errorf("unknown tunable %q", key)
		}
		if _, ok := os.LookupEnv(m.EnvVar); ok && os.Getenv(m.EnvVar) != "" {
			return fmt.Errorf("tunable %q is locked by env var %s", key, m.EnvVar)
		}
		s, err := serializeForKind(m, raw)
		if err != nil {
			return fmt.Errorf("tunable %q: %w", key, err)
		}
		stored[key] = s
	}
	return t.repo.SetMany(ctx, stored)
}

// serializeForKind validates the inbound JSON shape, enforces bounds, and
// renders the canonical DB string form.
func serializeForKind(m *TunableMeta, raw any) (string, error) {
	switch m.Kind {
	case KindInt:
		n, err := coerceInt64(raw)
		if err != nil {
			return "", err
		}
		if m.MinInt != 0 || m.MaxInt != 0 {
			if n < m.MinInt || n > m.MaxInt {
				return "", fmt.Errorf("value %d out of range [%d,%d]", n, m.MinInt, m.MaxInt)
			}
		}
		return strconv.FormatInt(n, 10), nil
	case KindFloat:
		f, err := coerceFloat64(raw)
		if err != nil {
			return "", err
		}
		if m.MinFloat != 0 || m.MaxFloat != 0 {
			if f < m.MinFloat || f > m.MaxFloat {
				return "", fmt.Errorf("value %g out of range [%g,%g]", f, m.MinFloat, m.MaxFloat)
			}
		}
		return strconv.FormatFloat(f, 'f', -1, 64), nil
	case KindBool:
		b, ok := raw.(bool)
		if !ok {
			return "", fmt.Errorf("expected bool, got %T", raw)
		}
		return strconv.FormatBool(b), nil
	case KindDurationSeconds:
		var d time.Duration
		switch v := raw.(type) {
		case string:
			parsed, err := parseDurationLoose(v)
			if err != nil {
				return "", err
			}
			d = parsed
		default:
			n, err := coerceInt64(raw)
			if err != nil {
				return "", err
			}
			d = time.Duration(n) * time.Second
		}
		secs := int64(d.Seconds())
		if m.MinInt != 0 || m.MaxInt != 0 {
			if secs < m.MinInt || secs > m.MaxInt {
				return "", fmt.Errorf("value %ds out of range [%ds,%ds]", secs, m.MinInt, m.MaxInt)
			}
		}
		return strconv.FormatInt(secs, 10), nil
	case KindString, KindEnum:
		s, ok := raw.(string)
		if !ok {
			return "", fmt.Errorf("expected string, got %T", raw)
		}
		s = strings.TrimSpace(s)
		if m.Kind == KindEnum {
			matched := false
			for _, allowed := range m.EnumValues {
				if strings.EqualFold(allowed, s) {
					s = allowed
					matched = true
					break
				}
			}
			if !matched {
				return "", fmt.Errorf("value %q not in allowed set %v", s, m.EnumValues)
			}
		}
		return s, nil
	}
	return "", fmt.Errorf("unknown kind %q", m.Kind)
}

func coerceInt64(raw any) (int64, error) {
	switch v := raw.(type) {
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	case float64: // JSON numbers decode as float64
		return int64(v), nil
	case string:
		n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("expected int, got %q", v)
		}
		return n, nil
	}
	return 0, fmt.Errorf("expected int, got %T", raw)
}

func coerceFloat64(raw any) (float64, error) {
	switch v := raw.(type) {
	case float64:
		return v, nil
	case int:
		return float64(v), nil
	case int64:
		return float64(v), nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("expected float, got %q", v)
		}
		return f, nil
	}
	return 0, fmt.Errorf("expected float, got %T", raw)
}
