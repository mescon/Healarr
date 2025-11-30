//go:build windows

package api

import (
	"os"
	"os/exec"

	"github.com/mescon/Healarr/internal/logger"
)

// restartProcess starts a new instance and exits the current one.
// On Windows, syscall.Exec doesn't exist, so we spawn a new process instead.
func restartProcess() {
	executable, err := os.Executable()
	if err != nil {
		logger.Errorf("Failed to get executable path: %v", err)
		os.Exit(1)
	}

	// Start a new process
	cmd := exec.Command(executable, os.Args[1:]...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		logger.Errorf("Failed to start new process: %v", err)
		os.Exit(1)
	}

	logger.Infof("Started new process (PID: %d), exiting current process", cmd.Process.Pid)
	os.Exit(0)
}
