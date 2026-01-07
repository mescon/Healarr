package clock

import (
	"sync"
	"testing"
	"time"
)

// =============================================================================
// RealClock tests
// =============================================================================

func TestNewRealClock(t *testing.T) {
	clock := NewRealClock()
	if clock == nil {
		t.Fatal("NewRealClock() should not return nil")
	}
}

func TestRealClock_Now(t *testing.T) {
	clock := NewRealClock()

	before := time.Now()
	got := clock.Now()
	after := time.Now()

	if got.Before(before) {
		t.Errorf("clock.Now() returned %v which is before %v", got, before)
	}
	if got.After(after) {
		t.Errorf("clock.Now() returned %v which is after %v", got, after)
	}
}

func TestRealClock_Now_Advances(t *testing.T) {
	clock := NewRealClock()

	first := clock.Now()
	time.Sleep(10 * time.Millisecond)
	second := clock.Now()

	if !second.After(first) {
		t.Errorf("clock.Now() should advance over time: first=%v, second=%v", first, second)
	}
}

func TestRealClock_AfterFunc(t *testing.T) {
	clock := NewRealClock()

	var wg sync.WaitGroup
	wg.Add(1)

	executed := false
	timer := clock.AfterFunc(10*time.Millisecond, func() {
		executed = true
		wg.Done()
	})

	if timer == nil {
		t.Fatal("AfterFunc should return a non-nil Timer")
	}

	wg.Wait()

	if !executed {
		t.Error("AfterFunc callback should have been executed")
	}
}

func TestRealClock_AfterFunc_Stop_BeforeFiring(t *testing.T) {
	clock := NewRealClock()

	executed := false
	timer := clock.AfterFunc(100*time.Millisecond, func() {
		executed = true
	})

	// Stop immediately before it fires
	stopped := timer.Stop()
	if !stopped {
		t.Error("Stop() should return true when timer hasn't fired yet")
	}

	// Wait to ensure timer doesn't fire
	time.Sleep(150 * time.Millisecond)

	if executed {
		t.Error("Callback should not execute after Stop()")
	}
}

func TestRealClock_AfterFunc_Stop_AfterFiring(t *testing.T) {
	clock := NewRealClock()

	var wg sync.WaitGroup
	wg.Add(1)

	timer := clock.AfterFunc(10*time.Millisecond, func() {
		wg.Done()
	})

	// Wait for the timer to fire
	wg.Wait()

	// Stop after firing should return false
	stopped := timer.Stop()
	if stopped {
		t.Error("Stop() should return false when timer has already fired")
	}
}

func TestRealClock_AfterFunc_ZeroDuration(t *testing.T) {
	clock := NewRealClock()

	var wg sync.WaitGroup
	wg.Add(1)

	executed := false
	clock.AfterFunc(0, func() {
		executed = true
		wg.Done()
	})

	wg.Wait()

	if !executed {
		t.Error("AfterFunc with zero duration should still execute")
	}
}

// =============================================================================
// Interface compliance tests
// =============================================================================

func TestRealClock_ImplementsClock(t *testing.T) {
	t.Helper() // Mark as helper to use t parameter
	var _ Clock = (*RealClock)(nil)
}

func TestRealTimer_ImplementsTimer(t *testing.T) {
	t.Helper() // Mark as helper to use t parameter
	var _ Timer = (*realTimer)(nil)
}

// =============================================================================
// Concurrent usage tests
// =============================================================================

func TestRealClock_ConcurrentNow(t *testing.T) {
	t.Helper() // Mark as helper to use t parameter
	clock := NewRealClock()

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = clock.Now()
		}()
	}

	wg.Wait()
}

func TestRealClock_ConcurrentAfterFunc(t *testing.T) {
	clock := NewRealClock()

	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	executed := make(chan struct{}, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			clock.AfterFunc(5*time.Millisecond, func() {
				executed <- struct{}{}
			})
		}()
	}

	wg.Wait()

	// Wait for all callbacks to complete
	timeout := time.After(500 * time.Millisecond)
	count := 0
	for count < goroutines {
		select {
		case <-executed:
			count++
		case <-timeout:
			t.Errorf("Expected %d callbacks, got %d", goroutines, count)
			return
		}
	}
}
