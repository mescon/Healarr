package integration

import (
	"context"
	"database/sql"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/repository"
)

// TestAutoScanWorkers pins the prudent "auto" sizing: min(4, cpus, memory
// budget), floored at 1. The previous auto (min(mem/512MB, cores, 32))
// chose 32 on a 32-core host and ran 32 thorough 4K decodes at once.
func TestAutoScanWorkers(t *testing.T) {
	t.Parallel()
	const mb = 1024 * 1024
	const gb = 1024 * mb
	tests := []struct {
		name string
		mem  uint64
		cpus int
		want int
	}{
		{"big host is capped at the prudent default", 64 * gb, 32, 4},
		{"huge host is still capped", 1024 * gb, 128, 4},
		{"unknown memory uses default (capped by cpu)", 0, 8, 4},
		{"unknown memory capped by low cpu", 0, 2, 2},
		{"512MB -> 1 worker", 512 * mb, 8, 1},
		{"under one budget floors at 1", 256 * mb, 8, 1},
		{"1GB -> 2 workers", 1 * gb, 8, 2},
		{"2GB -> 4 workers", 2 * gb, 8, 4},
		{"zero cpus treated as one", 8 * gb, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := autoScanWorkers(tt.mem, tt.cpus); got != tt.want {
				t.Errorf("autoScanWorkers(%d, %d) = %d, want %d", tt.mem, tt.cpus, got, tt.want)
			}
		})
	}
}

// TestEffectiveScanWorkers_EnvOverride verifies env precedence and clamping.
func TestEffectiveScanWorkers_EnvOverride(t *testing.T) {
	t.Setenv("HEALARR_SCANNER_WORKERS", "8")
	if got := EffectiveScanWorkers(); got != 8 {
		t.Errorf("explicit 8 = %d, want 8", got)
	}

	t.Setenv("HEALARR_SCANNER_WORKERS", "100")
	if got := EffectiveScanWorkers(); got != maxScanWorkers {
		t.Errorf("over-max = %d, want %d (clamped)", got, maxScanWorkers)
	}

	t.Setenv("HEALARR_SCANNER_WORKERS", "garbage")
	if got := EffectiveScanWorkers(); got < 1 || got > autoScanWorkersDefault {
		t.Errorf("invalid value should fall back to the prudent auto default, got %d", got)
	}

	t.Setenv("HEALARR_SCANNER_WORKERS", "")
	if got := EffectiveScanWorkers(); got < 1 || got > autoScanWorkersDefault {
		t.Errorf("unset should yield the prudent auto default, got %d", got)
	}
}

// TestEffectiveScanWorkers_UISettingApplies wires a real settings repo into
// the live tunables and verifies the UI-stored scan.workers value reaches
// the limiter's sizing - the end-to-end path that silently did not exist.
func TestEffectiveScanWorkers_UISettingApplies(t *testing.T) {
	t.Setenv("HEALARR_SCANNER_WORKERS", "") // env must not lock the UI value

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
	)`); err != nil {
		t.Fatalf("create settings table: %v", err)
	}
	repo := repository.NewSettingsRepository(db)
	if err := repo.Set(context.Background(), repository.SettingKeyScannerWorkers, "2"); err != nil {
		t.Fatalf("store UI value: %v", err)
	}

	config.SetLiveTunables(repository.NewTunables(repo))
	defer config.SetLiveTunables(nil)

	if got := EffectiveScanWorkers(); got != 2 {
		t.Errorf("EffectiveScanWorkers with UI value 2 = %d, want 2", got)
	}
}

// TestConcurrencyLimiter_CapsActive hammers the limiter with more goroutines
// than the limit and asserts the in-flight count never exceeds it.
func TestConcurrencyLimiter_CapsActive(t *testing.T) {
	t.Parallel()
	l := newConcurrencyLimiter()
	const limit = 2
	const workers = 12

	var active, maxSeen atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l.acquire(func() int { return limit })
			defer l.release()

			n := active.Add(1)
			for {
				m := maxSeen.Load()
				if n <= m || maxSeen.CompareAndSwap(m, n) {
					break
				}
			}
			time.Sleep(5 * time.Millisecond)
			active.Add(-1)
		}()
	}
	wg.Wait()

	if got := maxSeen.Load(); got > limit {
		t.Errorf("max concurrent holders = %d, want <= %d", got, limit)
	}
}

// TestConcurrencyLimiter_DynamicLimit verifies that a lowered limit applies
// to queued acquirers: with the limit dropped to 1 while 2 hold slots, the
// next acquirer proceeds only after enough releases.
func TestConcurrencyLimiter_DynamicLimit(t *testing.T) {
	t.Parallel()
	l := newConcurrencyLimiter()
	var limit atomic.Int64
	limit.Store(2)
	limitFn := func() int { return int(limit.Load()) }

	l.acquire(limitFn)
	l.acquire(limitFn)

	// Lower the limit to 1 while both slots are held.
	limit.Store(1)

	acquired := make(chan struct{})
	go func() {
		l.acquire(limitFn)
		close(acquired)
	}()

	// One release brings active to 1, which still meets the new limit of 1 -
	// the waiter must NOT get in.
	l.release()
	select {
	case <-acquired:
		t.Fatal("acquire succeeded while active >= lowered limit")
	case <-time.After(50 * time.Millisecond):
	}

	// Second release frees the last slot; now the waiter proceeds.
	l.release()
	select {
	case <-acquired:
	case <-time.After(2 * time.Second):
		t.Fatal("acquire did not proceed after enough releases")
	}
	l.release()

	// A limit that resolves to 0 or negative must be floored at 1, never
	// deadlocking every caller.
	limit.Store(0)
	done := make(chan struct{})
	go func() {
		l.acquire(limitFn)
		l.release()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("zero limit deadlocked the limiter (must floor at 1)")
	}
}
