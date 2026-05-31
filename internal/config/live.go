package config

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/mescon/Healarr/internal/repository"
)

// liveTunables is the optional runtime resolver for values that the UI
// (via PUT /api/config/tunables) can change without restarting Healarr.
// When set, the LiveX() accessors below consult it; when nil, they fall
// back to the static Config struct fields that Load() populated from env
// at startup.
//
// The nil-fallback path matters for two reasons: tests that build a
// Config without a database, and the boot window between cfg = Load()
// and SetLiveTunables() being called by the server.
var liveTunables atomic.Pointer[repository.Tunables]

// SetLiveTunables registers t as the live source for runtime-editable
// settings. Called once at server startup with a Tunables backed by the
// shared SettingsRepository.
func SetLiveTunables(t *repository.Tunables) {
	liveTunables.Store(t)
}

// LiveHealthCheckThoroughDuration returns the per-file prefix duration
// ffmpeg decodes during thorough scans. Zero means "decode the whole
// file". Resolves env > DB > default at every call so a UI-side update
// takes effect on the next scan tick.
func LiveHealthCheckThoroughDuration() time.Duration {
	if tn := liveTunables.Load(); tn != nil {
		return tn.ThoroughDuration(context.Background()).Value
	}
	return Get().HealthCheckThoroughDuration
}

// LiveHealthCheckThoroughTimeout returns the per-file wall-clock cap for
// thorough scans. Same precedence rules as LiveHealthCheckThoroughDuration.
func LiveHealthCheckThoroughTimeout() time.Duration {
	if tn := liveTunables.Load(); tn != nil {
		return tn.ThoroughTimeout(context.Background()).Value
	}
	return Get().HealthCheckThoroughTimeout
}

// LiveHealthCheckHwAccel returns the resolved hardware acceleration
// mode ("auto", "off", or an accelerator name like "cuda"). Same
// precedence rules as the duration accessors.
func LiveHealthCheckHwAccel() string {
	if tn := liveTunables.Load(); tn != nil {
		return tn.HwAccel(context.Background()).Value
	}
	return Get().HealthCheckHwAccel
}
