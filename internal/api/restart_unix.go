//go:build !windows

package api

import (
	"os"
	"syscall"

	"github.com/mescon/Healarr/internal/logger"
)

// restartProcessFunc is a variable that can be overridden in tests to prevent
// syscall.Exec from replacing the test process.
var restartProcessFunc = restartProcessImpl

// restartProcess calls the restart function (can be mocked in tests).
func restartProcess() {
	restartProcessFunc()
}

// restartProcessImpl replaces the current process with a new instance.
// On Unix systems, this uses syscall.Exec for a true in-place restart.
func restartProcessImpl() {
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
