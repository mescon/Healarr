// Package clock provides an abstraction over time operations for testability.
// Production code uses RealClock, tests can inject MockClock for deterministic behavior.
package clock

import "time"

// Clock provides an abstraction over time operations for testability.
type Clock interface {
	// AfterFunc waits for the duration to elapse and then calls f in its own goroutine.
	// Returns a Timer that can be used to cancel the call.
	AfterFunc(d time.Duration, f func()) Timer
	// Now returns the current time.
	Now() time.Time
}

// Timer represents a pending AfterFunc callback.
type Timer interface {
	// Stop prevents the Timer from firing. Returns true if the call was stopped,
	// false if the timer has already expired or been stopped.
	Stop() bool
}

// RealClock implements Clock using the standard time package.
type RealClock struct{}

// NewRealClock creates a new RealClock.
func NewRealClock() *RealClock {
	return &RealClock{}
}

// AfterFunc implements Clock.AfterFunc using time.AfterFunc.
func (c *RealClock) AfterFunc(d time.Duration, f func()) Timer {
	return &realTimer{timer: time.AfterFunc(d, f)}
}

// Now implements Clock.Now using time.Now.
func (c *RealClock) Now() time.Time {
	return time.Now()
}

// realTimer wraps time.Timer to implement Timer interface.
type realTimer struct {
	timer *time.Timer
}

// Stop implements Timer.Stop.
func (t *realTimer) Stop() bool {
	return t.timer.Stop()
}
