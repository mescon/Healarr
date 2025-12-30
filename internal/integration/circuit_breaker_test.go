package integration

import (
	"testing"
	"time"
)

// =============================================================================
// CircuitState tests
// =============================================================================

func TestCircuitState_String(t *testing.T) {
	tests := []struct {
		state    CircuitState
		expected string
	}{
		{CircuitClosed, "closed"},
		{CircuitOpen, "open"},
		{CircuitHalfOpen, "half-open"},
		{CircuitState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			if got := tt.state.String(); got != tt.expected {
				t.Errorf("CircuitState(%d).String() = %q, want %q", tt.state, got, tt.expected)
			}
		})
	}
}

// =============================================================================
// CircuitBreaker tests
// =============================================================================

func TestNewCircuitBreaker_DefaultConfig(t *testing.T) {
	config := DefaultCircuitBreakerConfig()

	if config.FailureThreshold != 5 {
		t.Errorf("Expected FailureThreshold=5, got %d", config.FailureThreshold)
	}
	if config.ResetTimeout != 30*time.Second {
		t.Errorf("Expected ResetTimeout=30s, got %v", config.ResetTimeout)
	}
	if config.SuccessThreshold != 2 {
		t.Errorf("Expected SuccessThreshold=2, got %d", config.SuccessThreshold)
	}
}

func TestNewCircuitBreaker_OverridesInvalidConfig(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 0,  // Invalid, should become 5
		ResetTimeout:     0,  // Invalid, should become 30s
		SuccessThreshold: -1, // Invalid, should become 2
	})

	stats := cb.Stats()
	if cb.config.FailureThreshold != 5 {
		t.Errorf("Expected FailureThreshold=5, got %d", cb.config.FailureThreshold)
	}
	if cb.config.ResetTimeout != 30*time.Second {
		t.Errorf("Expected ResetTimeout=30s, got %v", cb.config.ResetTimeout)
	}
	if cb.config.SuccessThreshold != 2 {
		t.Errorf("Expected SuccessThreshold=2, got %d", cb.config.SuccessThreshold)
	}
	if stats.State != CircuitClosed {
		t.Errorf("Expected initial state=closed, got %v", stats.State)
	}
}

func TestCircuitBreaker_AllowsRequestsWhenClosed(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig())

	for i := 0; i < 10; i++ {
		if !cb.Allow() {
			t.Errorf("Request %d should be allowed when circuit is closed", i)
		}
	}
}

func TestCircuitBreaker_OpensAfterFailureThreshold(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 3,
		ResetTimeout:     30 * time.Second,
		SuccessThreshold: 2,
	})

	// Record 3 failures - should open the circuit
	for i := 0; i < 3; i++ {
		if !cb.Allow() {
			t.Fatalf("Request should be allowed before threshold")
		}
		cb.RecordFailure()
	}

	if cb.State() != CircuitOpen {
		t.Errorf("Circuit should be open after %d failures, got %v", 3, cb.State())
	}

	// Next request should be rejected
	if cb.Allow() {
		t.Error("Request should be rejected when circuit is open")
	}

	stats := cb.Stats()
	if stats.TotalRejected != 1 {
		t.Errorf("Expected TotalRejected=1, got %d", stats.TotalRejected)
	}
}

func TestCircuitBreaker_SuccessResetsFailureCount(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 3,
		ResetTimeout:     30 * time.Second,
		SuccessThreshold: 2,
	})

	// Record 2 failures
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	// Record a success - should reset failures
	cb.Allow()
	cb.RecordSuccess()

	// Record 2 more failures - should not open circuit yet
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	if cb.State() != CircuitClosed {
		t.Error("Circuit should still be closed after success reset")
	}

	// Third failure should open it
	cb.Allow()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Error("Circuit should be open after 3 consecutive failures")
	}
}

func TestCircuitBreaker_TransitionsToHalfOpenAfterTimeout(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		ResetTimeout:     100 * time.Millisecond, // Short timeout for testing
		SuccessThreshold: 1,
	})

	// Open the circuit
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatal("Circuit should be open")
	}

	// Wait for reset timeout
	time.Sleep(150 * time.Millisecond)

	// Should allow a probe request and transition to half-open
	if !cb.Allow() {
		t.Error("Should allow probe request after reset timeout")
	}

	if cb.State() != CircuitHalfOpen {
		t.Errorf("Circuit should be half-open, got %v", cb.State())
	}
}

func TestCircuitBreaker_ClosesAfterSuccessInHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		ResetTimeout:     100 * time.Millisecond,
		SuccessThreshold: 2,
	})

	// Open the circuit
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	// Wait and transition to half-open
	time.Sleep(150 * time.Millisecond)
	cb.Allow()

	if cb.State() != CircuitHalfOpen {
		t.Fatal("Circuit should be half-open")
	}

	// First success
	cb.RecordSuccess()
	if cb.State() != CircuitHalfOpen {
		t.Error("Circuit should still be half-open after 1 success")
	}

	// Second success - should close
	cb.Allow()
	cb.RecordSuccess()
	if cb.State() != CircuitClosed {
		t.Errorf("Circuit should be closed after %d successes, got %v", 2, cb.State())
	}
}

func TestCircuitBreaker_ReopensOnFailureInHalfOpen(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		ResetTimeout:     100 * time.Millisecond,
		SuccessThreshold: 2,
	})

	// Open the circuit
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	// Wait and transition to half-open
	time.Sleep(150 * time.Millisecond)
	cb.Allow()

	if cb.State() != CircuitHalfOpen {
		t.Fatal("Circuit should be half-open")
	}

	// Failure in half-open should reopen
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Errorf("Circuit should be open after failure in half-open, got %v", cb.State())
	}
}

func TestCircuitBreaker_Reset(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 2,
		ResetTimeout:     30 * time.Second,
		SuccessThreshold: 2,
	})

	// Open the circuit
	cb.Allow()
	cb.RecordFailure()
	cb.Allow()
	cb.RecordFailure()

	if cb.State() != CircuitOpen {
		t.Fatal("Circuit should be open")
	}

	// Reset should close it
	cb.Reset()

	if cb.State() != CircuitClosed {
		t.Errorf("Circuit should be closed after reset, got %v", cb.State())
	}

	// Should allow requests again
	if !cb.Allow() {
		t.Error("Should allow requests after reset")
	}
}

func TestCircuitBreaker_Stats(t *testing.T) {
	cb := NewCircuitBreaker(DefaultCircuitBreakerConfig())

	// Record some activity
	cb.Allow()
	cb.RecordSuccess()
	cb.Allow()
	cb.RecordSuccess()
	cb.Allow()
	cb.RecordFailure()

	stats := cb.Stats()

	if stats.TotalSuccesses != 2 {
		t.Errorf("Expected TotalSuccesses=2, got %d", stats.TotalSuccesses)
	}
	if stats.TotalFailures != 1 {
		t.Errorf("Expected TotalFailures=1, got %d", stats.TotalFailures)
	}
	if stats.ConsecutiveFailures != 1 {
		t.Errorf("Expected ConsecutiveFailures=1, got %d", stats.ConsecutiveFailures)
	}
	if stats.State != CircuitClosed {
		t.Errorf("Expected State=closed, got %v", stats.State)
	}
}

func TestCircuitBreaker_HalfOpenAllowsMultipleRequests(t *testing.T) {
	cb := NewCircuitBreaker(CircuitBreakerConfig{
		FailureThreshold: 1,
		ResetTimeout:     50 * time.Millisecond,
		SuccessThreshold: 2,
	})

	// Open the circuit
	cb.Allow()
	cb.RecordFailure()

	// Wait for transition to half-open
	time.Sleep(100 * time.Millisecond)

	// In half-open, multiple requests are allowed (simplified implementation)
	if !cb.Allow() {
		t.Error("First request in half-open should be allowed")
	}
	if !cb.Allow() {
		t.Error("Subsequent requests in half-open should also be allowed")
	}
}

// =============================================================================
// CircuitBreakerRegistry tests
// =============================================================================

func TestNewCircuitBreakerRegistry(t *testing.T) {
	config := DefaultCircuitBreakerConfig()
	registry := NewCircuitBreakerRegistry(config)

	if registry == nil {
		t.Fatal("Registry should not be nil")
	}
	if registry.breakers == nil {
		t.Fatal("Registry breakers map should be initialized")
	}
}

func TestCircuitBreakerRegistry_Get_CreatesNew(t *testing.T) {
	registry := NewCircuitBreakerRegistry(DefaultCircuitBreakerConfig())

	cb1 := registry.Get(1)
	if cb1 == nil {
		t.Fatal("Should create new circuit breaker")
	}

	cb2 := registry.Get(2)
	if cb2 == nil {
		t.Fatal("Should create new circuit breaker for different ID")
	}

	if cb1 == cb2 {
		t.Error("Different IDs should have different circuit breakers")
	}
}

func TestCircuitBreakerRegistry_Get_ReturnsSame(t *testing.T) {
	registry := NewCircuitBreakerRegistry(DefaultCircuitBreakerConfig())

	cb1 := registry.Get(42)
	cb2 := registry.Get(42)

	if cb1 != cb2 {
		t.Error("Same ID should return same circuit breaker")
	}
}

func TestCircuitBreakerRegistry_Get_Concurrent(t *testing.T) {
	registry := NewCircuitBreakerRegistry(DefaultCircuitBreakerConfig())

	// Concurrent access to the same ID
	done := make(chan *CircuitBreaker, 100)
	for i := 0; i < 100; i++ {
		go func() {
			done <- registry.Get(1)
		}()
	}

	// Collect results
	var first *CircuitBreaker
	for i := 0; i < 100; i++ {
		cb := <-done
		if first == nil {
			first = cb
		} else if cb != first {
			t.Error("Concurrent access should return same instance")
		}
	}
}

func TestCircuitBreakerRegistry_AllStats(t *testing.T) {
	registry := NewCircuitBreakerRegistry(CircuitBreakerConfig{
		FailureThreshold: 2,
		ResetTimeout:     30 * time.Second,
		SuccessThreshold: 1,
	})

	// Create some breakers with different states
	cb1 := registry.Get(1)
	cb1.Allow()
	cb1.RecordSuccess()

	cb2 := registry.Get(2)
	cb2.Allow()
	cb2.RecordFailure()
	cb2.Allow()
	cb2.RecordFailure() // Opens the circuit

	stats := registry.AllStats()

	if len(stats) != 2 {
		t.Errorf("Expected 2 stats, got %d", len(stats))
	}

	if stats[1].State != CircuitClosed {
		t.Errorf("Instance 1 should be closed, got %v", stats[1].State)
	}
	if stats[2].State != CircuitOpen {
		t.Errorf("Instance 2 should be open, got %v", stats[2].State)
	}
}

func TestCircuitBreakerRegistry_ResetAll(t *testing.T) {
	registry := NewCircuitBreakerRegistry(CircuitBreakerConfig{
		FailureThreshold: 1,
		ResetTimeout:     30 * time.Second,
		SuccessThreshold: 1,
	})

	// Open multiple circuits
	cb1 := registry.Get(1)
	cb1.Allow()
	cb1.RecordFailure()

	cb2 := registry.Get(2)
	cb2.Allow()
	cb2.RecordFailure()

	if cb1.State() != CircuitOpen || cb2.State() != CircuitOpen {
		t.Fatal("Both circuits should be open")
	}

	// Reset all
	registry.ResetAll()

	if cb1.State() != CircuitClosed {
		t.Errorf("Circuit 1 should be closed after reset, got %v", cb1.State())
	}
	if cb2.State() != CircuitClosed {
		t.Errorf("Circuit 2 should be closed after reset, got %v", cb2.State())
	}
}

// =============================================================================
// ErrCircuitOpen tests
// =============================================================================

func TestErrCircuitOpen(t *testing.T) {
	if ErrCircuitOpen == nil {
		t.Error("ErrCircuitOpen should be defined")
	}

	expected := "circuit breaker is open: service unavailable"
	if ErrCircuitOpen.Error() != expected {
		t.Errorf("ErrCircuitOpen.Error() = %q, want %q", ErrCircuitOpen.Error(), expected)
	}
}
