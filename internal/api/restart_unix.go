//go:build !windows

package api

import (
	"os"
	"syscall"

	"github.com/mescon/Healarr/internal/logger"
)

// restartProcess replaces the current process with a new instance.
// On Unix systems, this uses syscall.Exec for a true in-place restart.
func restartProcess() {
	executable, err := os.Executable()
	if err != nil {
		logger.Errorf("Failed to get executable path: %v", err)
		os.Exit(1)
	}

	// Re-execute self with same args and environment
	execErr := syscall.Exec(executable, os.Args, os.Environ())
	if execErr != nil {
		logger.Errorf("Failed to restart: %v", execErr)
		os.Exit(1)
	}
}
