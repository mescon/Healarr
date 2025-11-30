package api

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter implements a token bucket rate limiter per IP address
type RateLimiter struct {
	mu       sync.Mutex
	clients  map[string]*clientBucket
	rate     int           // tokens per interval
	interval time.Duration // refill interval
	burst    int           // max tokens (bucket size)
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
	}

	// Cleanup old entries periodically
	go rl.cleanup()

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

	// Refill tokens based on elapsed time
	elapsed := now.Sub(bucket.lastCheck)
	tokensToAdd := int(elapsed/rl.interval) * rl.rate
	bucket.tokens += tokensToAdd
	if bucket.tokens > rl.burst {
		bucket.tokens = rl.burst
	}
	bucket.lastCheck = now

	if bucket.tokens > 0 {
		bucket.tokens--
		return true
	}

	return false
}

// cleanup removes stale entries older than 10 minutes
func (rl *RateLimiter) cleanup() {
	ticker := time.NewTicker(5 * time.Minute)
	for range ticker.C {
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
