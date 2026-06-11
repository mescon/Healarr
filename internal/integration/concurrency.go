package integration

import (
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"

	"github.com/mescon/Healarr/internal/config"
)

// Scan concurrency is bounded GLOBALLY here, at the tool-subprocess layer,
// because the scan worker pool was never the only spawner: webhook-triggered
// ScanFile calls, the deferred-rescan worker and post-remediation
// verification all start detector runs outside the pool. On a 32-core host
// the pool alone ran 32 thorough 4K decodes while webhooks stacked more on
// top - VRAM filled, ffmpeg fell back to CPU software decode, load >120.
// Every ffmpeg/ffprobe/HandBrake/mediainfo run now acquires a slot from one
// shared limiter, whatever service asked for it.

const (
	// maxScanWorkers caps operator-configured concurrency to avoid thrashing
	// storage or spawning an unreasonable number of subprocesses.
	maxScanWorkers = 32

	// autoScanWorkersDefault is the prudent "auto" concurrency. Deliberately
	// low: a thorough check of a 4K file is a full ffmpeg decode (~1 GiB GPU
	// VRAM, or many CPU threads in software fallback), so the safe default
	// is one a modest host survives. Operators with strong GPUs raise
	// scan.workers in the UI or via HEALARR_SCANNER_WORKERS.
	autoScanWorkersDefault = 4

	// perWorkerMemoryBudget is the RAM each concurrent detection worker may
	// need; it lowers the auto default further on small containers so the
	// default concurrency can't push them into an OOM kill.
	perWorkerMemoryBudget = 512 * 1024 * 1024 // 512 MB
)

// EffectiveScanWorkers resolves the max number of concurrent file checks:
// HEALARR_SCANNER_WORKERS > the scan.workers UI setting > auto (both via
// config.LiveScannerWorkers), clamped to [1, maxScanWorkers]. Re-resolved on
// every call so a UI change applies to the next acquisition / next scan
// without a restart.
func EffectiveScanWorkers() int {
	if n := config.LiveScannerWorkers(); n > 0 {
		if n > maxScanWorkers {
			return maxScanWorkers
		}
		return n
	}
	return autoScanWorkers(availableMemoryBytes(), runtime.NumCPU())
}

// autoScanWorkers derives the "auto" worker count: min(prudent default, CPU
// count, memory budget), floored at 1. Pure function for testability.
func autoScanWorkers(memBytes uint64, cpus int) int {
	if cpus < 1 {
		cpus = 1
	}
	n := autoScanWorkersDefault
	if cpus < n {
		n = cpus
	}
	if memBytes > 0 {
		if m := int(memBytes / perWorkerMemoryBudget); m < n {
			n = m
		}
	}
	if n < 1 {
		n = 1
	}
	return n
}

// availableMemoryBytes best-effort reports the memory ceiling the process
// runs under: the container's cgroup memory limit when present, else total
// system memory. Returns 0 when it can't be determined (e.g. non-Linux), so
// callers skip the memory cap. Linux-specific paths simply don't exist
// elsewhere, so this stays portable without build tags.
func availableMemoryBytes() uint64 {
	// cgroup v2: a single limit file, "max" means unlimited.
	if b, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		if s := strings.TrimSpace(string(b)); s != "max" {
			if v, err := strconv.ParseUint(s, 10, 64); err == nil && v > 0 {
				return v
			}
		}
	}
	// cgroup v1: "unlimited" is a near-max sentinel, so ignore implausibly large values.
	if b, err := os.ReadFile("/sys/fs/cgroup/memory/memory.limit_in_bytes"); err == nil {
		if v, err := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64); err == nil && v > 0 && v < (1<<62) {
			return v
		}
	}
	// Fallback: total system memory from /proc/meminfo (kB).
	if b, err := os.ReadFile("/proc/meminfo"); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			if kb, ok := strings.CutPrefix(line, "MemTotal:"); ok {
				fields := strings.Fields(kb)
				if len(fields) >= 1 {
					if v, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
						return v * 1024
					}
				}
			}
		}
	}
	return 0
}

// concurrencyLimiter is a counting semaphore whose limit is re-evaluated on
// every acquisition attempt, so lowering scan.workers in the UI takes effect
// on running workloads (queued acquirers see the new limit when they wake).
type concurrencyLimiter struct {
	mu     sync.Mutex
	cond   *sync.Cond
	active int
}

func newConcurrencyLimiter() *concurrencyLimiter {
	l := &concurrencyLimiter{}
	l.cond = sync.NewCond(&l.mu)
	return l
}

// acquire blocks until the number of active holders is below limit().
// limit is re-evaluated each time the goroutine wakes, and is floored at 1
// so a misconfigured limit can never deadlock every caller.
func (l *concurrencyLimiter) acquire(limit func() int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for {
		n := limit()
		if n < 1 {
			n = 1
		}
		if l.active < n {
			break
		}
		l.cond.Wait()
	}
	l.active++
}

func (l *concurrencyLimiter) release() {
	l.mu.Lock()
	l.active--
	l.cond.Broadcast()
	l.mu.Unlock()
}

// toolLimiter is the process-wide limiter every detection subprocess goes
// through (see runCommandWithTimeout).
var toolLimiter = newConcurrencyLimiter()
