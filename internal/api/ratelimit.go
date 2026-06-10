package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/mescon/Healarr/internal/safego"
)

// RateLimiter implements a token bucket rate limiter per IP address
type RateLimiter struct {
	mu       sync.Mutex
	clients  map[string]*clientBucket
	rate     int           // tokens per interval
	interval time.Duration // refill interval
	burst    int           // max tokens (bucket size)
	shutdown chan struct{}
}

type clientBucket struct {
	tokens    int
	lastCheck time.Time
}

// NewRateLimiter creates a rate limiter with specified rate (requests per interval) and burst size
func NewRateLimiter(rate int, interval time.Duration, burst int) *RateLimiter {
	rl := &RateLimiter{
		clients:  make(map[string]*clientBucket),
		rate:     rate,
		interval: interval,
		burst:    burst,
		shutdown: make(chan struct{}),
	}

	// Cleanup old entries periodically
	safego.Run("ratelimit-cleanup", rl.cleanup)

	return rl
}

// Allow checks if a request from the given IP should be allowed
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()

	bucket, exists := rl.clients[ip]
	if !exists {
		// New client starts with full bucket
		rl.clients[ip] = &clientBucket{
			tokens:    rl.burst - 1, // -1 for this request
			lastCheck: now,
		}
		return true
	}

	// Refill tokens based on elapsed time. lastCheck advances ONLY by the
	// whole intervals actually consumed: resetting it to now on every
	// request meant any stream with sub-interval arrivals never refilled at
	// all (each request restarted the clock), so a burst — e.g. a season-
	// pack import firing webhooks — drained the bucket and then starved it
	// forever, with the *arrs never retrying the 429'd webhooks and those
	// files silently going unscanned.
	elapsed := now.Sub(bucket.lastCheck)
	intervals := int(elapsed / rl.interval)
	if intervals > 0 {
		bucket.tokens += intervals * rl.rate
		if bucket.tokens > rl.burst {
			bucket.tokens = rl.burst
		}
		bucket.lastCheck = bucket.lastCheck.Add(time.Duration(intervals) * rl.interval)
	}

	if bucket.tokens > 0 {
		bucket.tokens--
		return true
	}

	return false
}

// cleanup removes stale entries older than 10 minutes
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-rl.shutdown:
			return
		case <-ticker.C:
			rl.mu.Lock()
			threshold := time.Now().Add(-10 * time.Minute)
			for ip, bucket := range rl.clients {
				if bucket.lastCheck.Before(threshold) {
					delete(rl.clients, ip)
				}
			}
			rl.mu.Unlock()
		}
	}
}

// Shutdown stops the rate limiter's cleanup goroutine
func (rl *RateLimiter) Shutdown() {
	close(rl.shutdown)
}

// Middleware returns a Gin middleware that rate limits requests
func (rl *RateLimiter) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		ip := c.ClientIP()

		if !rl.Allow(ip) {
			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":       "Too many requests",
				"retry_after": rl.interval.Seconds(),
			})
			c.Abort()
			return
		}

		c.Next()
	}
}

// Global rate limiters for different endpoints
var (
	// LoginLimiter: 5 attempts per minute, burst of 5
	// Protects against brute force login attempts
	LoginLimiter = NewRateLimiter(5, time.Minute, 5)

	// SetupLimiter: 3 attempts per minute, burst of 3
	// Setup should only happen once, strict limiting
	SetupLimiter = NewRateLimiter(3, time.Minute, 3)

	// WebhookLimiter: 60 requests per minute, burst of 30
	// Webhooks can be frequent but need some protection
	WebhookLimiter = NewRateLimiter(60, time.Minute, 30)

	// APILimiter: 120 requests per minute per IP, burst of 60
	// General API protection against abuse
	APILimiter = NewRateLimiter(120, time.Minute, 60)
)
