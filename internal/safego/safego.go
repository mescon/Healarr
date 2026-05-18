// Package safego launches goroutines with panic recovery.
//
// A panic in any goroutine without recover() crashes the entire process.
// Use safego.Run for every long-lived or user-data-handling goroutine so
// that a single unexpected panic is logged and contained rather than
// terminating the service.
package safego

import (
	"runtime/debug"

	"github.com/mescon/Healarr/internal/logger"
)

// Run executes fn in a new goroutine with panic recovery. If fn panics,
// the panic value and stack trace are logged at error level with the
// provided name for identification, and the goroutine exits cleanly.
func Run(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf("panic in goroutine %s: %v\n%s", name, r, debug.Stack())
			}
		}()
		fn()
	}()
}
