//go:build windows

package integration

import (
	"errors"
	"io/fs"
	"syscall"
)

// Windows error codes
const (
	ERROR_PATH_NOT_FOUND      syscall.Errno = 3
	ERROR_ACCESS_DENIED       syscall.Errno = 5
	ERROR_BAD_NETPATH         syscall.Errno = 53
	ERROR_NETWORK_BUSY        syscall.Errno = 54
	ERROR_DEV_NOT_EXIST       syscall.Errno = 55
	ERROR_NETNAME_DELETED     syscall.Errno = 64
	ERROR_NETWORK_ACCESS_DENIED syscall.Errno = 65
	ERROR_BAD_NET_NAME        syscall.Errno = 67
	ERROR_SEM_TIMEOUT         syscall.Errno = 121
	ERROR_UNEXP_NET_ERR       syscall.Errno = 59
	ERROR_REM_NOT_LIST        syscall.Errno = 51
)

// classifySyscallError checks for Windows-specific syscall errors and returns
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
	case ERROR_BAD_NETPATH, ERROR_BAD_NET_NAME, ERROR_NETNAME_DELETED:
		return ErrorTypeMountLost, "network path not found"
	case ERROR_SEM_TIMEOUT:
		return ErrorTypeTimeout, "network operation timed out"
	case ERROR_DEV_NOT_EXIST, ERROR_REM_NOT_LIST:
		return ErrorTypeMountLost, "remote device not available"
	case ERROR_NETWORK_BUSY, ERROR_UNEXP_NET_ERR:
		return ErrorTypeIOError, "network error"
	case ERROR_NETWORK_ACCESS_DENIED:
		return ErrorTypeAccessDenied, "network access denied"
	}

	return "", ""
}
