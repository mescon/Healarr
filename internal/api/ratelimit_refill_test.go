package api

import (
	"testing"
	"time"
)

// The old refill reset bucket.lastCheck to now on EVERY call. Any client
// arriving faster than the interval therefore never accumulated a full
// interval and starved forever once the burst was spent - e.g. a season-pack
// import firing webhooks drained the bucket and every later webhook got a
// 429 the *arrs never retry, so those files were silently never scanned.

// TestRateLimiter_Allow_SubIntervalArrivalsStillRefill hammers the limiter
// with arrivals shorter than the interval and asserts the bucket still
// refills on schedule.
func TestRateLimiter_Allow_SubIntervalArrivalsStillRefill(t *testing.T) {
	rl := NewRateLimiter(1, 100*time.Millisecond, 1)
	defer rl.Shutdown()

	ip := "10.99.0.1"
	if !rl.Allow(ip) {
		t.Fatal("first request should be allowed")
	}

	deadline := time.Now().Add(450 * time.Millisecond)
	allowed := 0
	for time.Now().Before(deadline) {
		if rl.Allow(ip) {
			allowed++
		}
		time.Sleep(20 * time.Millisecond)
	}
	if allowed == 0 {
		t.Error("bucket never refilled under sub-interval arrivals (refill clock reset on every request)")
	}
}

// TestRateLimiter_Allow_RefillConsumesWholeIntervalsOnly verifies the clock
// arithmetic directly: refilling advances lastCheck by exactly the whole
// intervals consumed, preserving the partial-interval remainder.
func TestRateLimiter_Allow_RefillConsumesWholeIntervalsOnly(t *testing.T) {
	const interval = time.Second
	rl := NewRateLimiter(1, interval, 5)
	defer rl.Shutdown()

	ip := "10.99.0.2"
	rl.Allow(ip) // create the bucket

	rl.mu.Lock()
	bucket := rl.clients[ip]
	bucket.tokens = 0
	before := time.Now().Add(-2500 * time.Millisecond) // 2.5 intervals ago
	bucket.lastCheck = before
	rl.mu.Unlock()

	if !rl.Allow(ip) {
		t.Fatal("2 whole intervals elapsed - expected a refilled token")
	}

	rl.mu.Lock()
	tokens := bucket.tokens
	lastCheck := bucket.lastCheck
	rl.mu.Unlock()

	// 2 whole intervals refilled 2 tokens, the request consumed 1.
	if tokens != 1 {
		t.Errorf("tokens = %d, want 1 (2 intervals refilled, 1 consumed)", tokens)
	}
	// The 0.5-interval remainder must keep accruing toward the next refill.
	if want := before.Add(2 * interval); !lastCheck.Equal(want) {
		t.Errorf("lastCheck advanced by %v, want exactly 2 intervals (partial interval must be preserved)", lastCheck.Sub(before))
	}
}
