//go:build !windows

package integration

import (
	"errors"
	"io/fs"
	"syscall"
)

// classifySyscallError checks for Unix-specific syscall errors and returns
// the appropriate error type string, or empty string if not a known syscall error.
func classifySyscallError(err error) (errorType string, message string) {
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		return "", ""
	}

	errno, ok := pathErr.Err.(syscall.Errno)
	if !ok {
		return "", ""
	}

	switch errno {
	case syscall.ESTALE:
		return ErrorTypeMountLost, "stale NFS file handle"
	case syscall.ETIMEDOUT:
		return ErrorTypeTimeout, "filesystem operation timed out"
	case syscall.ENODEV, syscall.ENXIO:
		return ErrorTypeMountLost, "device not available (mount offline)"
	case syscall.EIO:
		return ErrorTypeIOError, "I/O error"
	case syscall.EHOSTDOWN, syscall.EHOSTUNREACH, syscall.ENETDOWN, syscall.ENETUNREACH:
		return ErrorTypeMountLost, "network/host unreachable"
	}

	return "", ""
}
