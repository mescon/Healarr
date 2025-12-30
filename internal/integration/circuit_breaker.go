package integration

import (
	"fmt"
	"sync"
	"time"
)

// CircuitState represents the current state of the circuit breaker.
type CircuitState int

const (
	// CircuitClosed is the normal operating state - requests are allowed.
	CircuitClosed CircuitState = iota
	// CircuitOpen is the failure state - requests are rejected immediately.
	CircuitOpen
	// CircuitHalfOpen allows a single probe request to test if the service has recovered.
	CircuitHalfOpen
)

func (s CircuitState) String() string {
	switch s {
	case CircuitClosed:
		return "closed"
	case CircuitOpen:
		return "open"
	case CircuitHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}

// CircuitBreakerConfig configures the circuit breaker behavior.
type CircuitBreakerConfig struct {
	// FailureThreshold is the number of consecutive failures before opening the circuit.
	// Default: 5
	FailureThreshold int
	// ResetTimeout is how long to wait before allowing a probe request (half-open state).
	// Default: 30 seconds
	ResetTimeout time.Duration
	// SuccessThreshold is the number of consecutive successes in half-open state
	// needed to close the circuit. Default: 2
	SuccessThreshold int
}

// DefaultCircuitBreakerConfig returns sensible defaults for the circuit breaker.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		FailureThreshold: 5,
		ResetTimeout:     30 * time.Second,
		SuccessThreshold: 2,
	}
}

// CircuitBreaker implements the circuit breaker pattern for a single service.
// It prevents cascading failures by rejecting requests when a service is unhealthy.
type CircuitBreaker struct {
	mu              sync.RWMutex
	config          CircuitBreakerConfig
	state           CircuitState
	failures        int       // consecutive failures
	successes       int       // consecutive successes (for half-open state)
	lastFailureTime time.Time // when the last failure occurred
	lastStateChange time.Time // when the state last changed
	totalFailures   int64     // total failures (for stats)
	totalSuccesses  int64     // total successes (for stats)
	totalRejected   int64     // requests rejected due to open circuit
}

// NewCircuitBreaker creates a new circuit breaker with the given configuration.
func NewCircuitBreaker(config CircuitBreakerConfig) *CircuitBreaker {
	if config.FailureThreshold <= 0 {
		config.FailureThreshold = 5
	}
	if config.ResetTimeout <= 0 {
		config.ResetTimeout = 30 * time.Second
	}
	if config.SuccessThreshold <= 0 {
		config.SuccessThreshold = 2
	}
	return &CircuitBreaker{
		config:          config,
		state:           CircuitClosed,
		lastStateChange: time.Now(),
	}
}

// Allow checks if a request should be allowed through.
// Returns true if the request is allowed, false if rejected.
// Call RecordSuccess or RecordFailure after the request completes.
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case CircuitClosed:
		return true

	case CircuitOpen:
		// Check if enough time has passed to try again
		if time.Since(cb.lastFailureTime) >= cb.config.ResetTimeout {
			cb.state = CircuitHalfOpen
			cb.lastStateChange = time.Now()
			cb.successes = 0
			return true // Allow the probe request
		}
		cb.totalRejected++
		return false

	case CircuitHalfOpen:
		// Only allow one request at a time in half-open state
		// The first request that comes through will determine the fate
		return true

	default:
		return true
	}
}

// RecordSuccess records a successful request, potentially closing the circuit.
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.totalSuccesses++

	switch cb.state {
	case CircuitClosed:
		cb.failures = 0 // Reset failure counter on success

	case CircuitHalfOpen:
		cb.successes++
		if cb.successes >= cb.config.SuccessThreshold {
			cb.state = CircuitClosed
			cb.lastStateChange = time.Now()
			cb.failures = 0
			cb.successes = 0
		}

	case CircuitOpen:
		// This shouldn't happen, but handle it gracefully
		cb.state = CircuitHalfOpen
		cb.lastStateChange = time.Now()
		cb.successes = 1
	}
}

// RecordFailure records a failed request, potentially opening the circuit.
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.totalFailures++
	cb.failures++
	cb.lastFailureTime = time.Now()
	cb.successes = 0 // Reset success counter

	switch cb.state {
	case CircuitClosed:
		if cb.failures >= cb.config.FailureThreshold {
			cb.state = CircuitOpen
			cb.lastStateChange = time.Now()
		}

	case CircuitHalfOpen:
		// Failed during probe - go back to open
		cb.state = CircuitOpen
		cb.lastStateChange = time.Now()

	case CircuitOpen:
		// Already open, just update the failure time
	}
}

// State returns the current state of the circuit breaker.
func (cb *CircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Stats returns statistics about the circuit breaker.
func (cb *CircuitBreaker) Stats() CircuitBreakerStats {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return CircuitBreakerStats{
		State:               cb.state,
		ConsecutiveFailures: cb.failures,
		LastFailureTime:     cb.lastFailureTime,
		LastStateChange:     cb.lastStateChange,
		TotalFailures:       cb.totalFailures,
		TotalSuccesses:      cb.totalSuccesses,
		TotalRejected:       cb.totalRejected,
	}
}

// Reset resets the circuit breaker to closed state.
func (cb *CircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = CircuitClosed
	cb.failures = 0
	cb.successes = 0
	cb.lastStateChange = time.Now()
}

// CircuitBreakerStats holds statistics for monitoring.
type CircuitBreakerStats struct {
	State               CircuitState
	ConsecutiveFailures int
	LastFailureTime     time.Time
	LastStateChange     time.Time
	TotalFailures       int64
	TotalSuccesses      int64
	TotalRejected       int64
}

// ErrCircuitOpen is returned when the circuit is open and requests are rejected.
var ErrCircuitOpen = fmt.Errorf("circuit breaker is open: service unavailable")

// CircuitBreakerRegistry manages circuit breakers for multiple instances.
// Each *arr instance gets its own circuit breaker.
type CircuitBreakerRegistry struct {
	mu       sync.RWMutex
	breakers map[int64]*CircuitBreaker
	config   CircuitBreakerConfig
}

// NewCircuitBreakerRegistry creates a registry with the given default configuration.
func NewCircuitBreakerRegistry(config CircuitBreakerConfig) *CircuitBreakerRegistry {
	return &CircuitBreakerRegistry{
		breakers: make(map[int64]*CircuitBreaker),
		config:   config,
	}
}

// Get returns the circuit breaker for an instance, creating one if needed.
func (r *CircuitBreakerRegistry) Get(instanceID int64) *CircuitBreaker {
	r.mu.RLock()
	cb, exists := r.breakers[instanceID]
	r.mu.RUnlock()

	if exists {
		return cb
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Double-check after acquiring write lock
	if cb, exists = r.breakers[instanceID]; exists {
		return cb
	}

	cb = NewCircuitBreaker(r.config)
	r.breakers[instanceID] = cb
	return cb
}

// AllStats returns statistics for all circuit breakers.
func (r *CircuitBreakerRegistry) AllStats() map[int64]CircuitBreakerStats {
	r.mu.RLock()
	defer r.mu.RUnlock()

	stats := make(map[int64]CircuitBreakerStats, len(r.breakers))
	for id, cb := range r.breakers {
		stats[id] = cb.Stats()
	}
	return stats
}

// ResetAll resets all circuit breakers to closed state.
func (r *CircuitBreakerRegistry) ResetAll() {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for _, cb := range r.breakers {
		cb.Reset()
	}
}
