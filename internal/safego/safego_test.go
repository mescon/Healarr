package safego_test

import (
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mescon/Healarr/internal/safego"
)

// TestRun_NormalCompletion verifies that a non-panicking goroutine executes its body.
func TestRun_NormalCompletion(t *testing.T) {
	done := make(chan struct{})
	safego.Run("test-normal", func() {
		close(done)
	})

	select {
	case <-done:
		// expected
	case <-time.After(time.Second):
		t.Fatal("goroutine did not complete within 1s")
	}
}

// TestRun_RecoversFromPanic verifies that a panic inside the goroutine is recovered
// and does not propagate to crash the test process.
func TestRun_RecoversFromPanic(t *testing.T) {
	// If recover() were missing, the panic would crash the entire test binary.
	// Reaching the assertion below proves recovery happened.
	finished := make(chan struct{})
	safego.Run("test-panic", func() {
		defer close(finished)
		panic(errors.New("intentional"))
	})

	select {
	case <-finished:
		// expected: deferred close ran during panic unwind, then recover absorbed the panic
	case <-time.After(time.Second):
		t.Fatal("goroutine did not finish within 1s; panic may have escaped")
	}
}

// TestRun_DeferredCleanupRunsOnPanic verifies that defers inside the wrapped
// function still execute on panic (so wg.Done patterns remain correct).
func TestRun_DeferredCleanupRunsOnPanic(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)

	safego.Run("test-defer", func() {
		defer wg.Done()
		panic("simulated handler failure")
	})

	doneCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(doneCh)
	}()

	select {
	case <-doneCh:
		// expected: wg.Done was deferred, ran during unwind
	case <-time.After(time.Second):
		t.Fatal("WaitGroup was not released by deferred cleanup after panic")
	}
}
