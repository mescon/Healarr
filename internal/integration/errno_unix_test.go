//go:build !windows

package integration

import (
	"io/fs"
	"syscall"
	"testing"
)

func TestClassifySyscallError(t *testing.T) {
	tests := []struct {
		name         string
		err          error
		wantType     string
		wantMessage  string
	}{
		{
			name:        "nil error returns empty",
			err:         nil,
			wantType:    "",
			wantMessage: "",
		},
		{
			name:        "non-PathError returns empty",
			err:         syscall.ESTALE,
			wantType:    "",
			wantMessage: "",
		},
		{
			name: "ESTALE returns MountLost",
			err: &fs.PathError{
				Op:   "stat",
				Path: "/test/path",
				Err:  syscall.ESTALE,
			},
			wantType:    ErrorTypeMountLost,
			wantMessage: "stale NFS file handle",
		},
		{
			name: "ETIMEDOUT returns Timeout",
			err: &fs.PathError{
				Op:   "open",
				Path: "/test/path",
				Err:  syscall.ETIMEDOUT,
			},
			wantType:    ErrorTypeTimeout,
			wantMessage: "filesystem operation timed out",
		},
		{
			name: "ENODEV returns MountLost",
			err: &fs.PathError{
				Op:   "stat",
				Path: "/test/path",
				Err:  syscall.ENODEV,
			},
			wantType:    ErrorTypeMountLost,
			wantMessage: "device not available (mount offline)",
		},
		{
			name: "ENXIO returns MountLost",
			err: &fs.PathError{
				Op:   "open",
				Path: "/test/path",
				Err:  syscall.ENXIO,
			},
			wantType:    ErrorTypeMountLost,
			wantMessage: "device not available (mount offline)",
		},
		{
			name: "EIO returns IOError",
			err: &fs.PathError{
				Op:   "read",
				Path: "/test/path",
				Err:  syscall.EIO,
			},
			wantType:    ErrorTypeIOError,
			wantMessage: "I/O error",
		},
		{
			name: "EHOSTDOWN returns MountLost",
			err: &fs.PathError{
				Op:   "stat",
				Path: "/test/path",
				Err:  syscall.EHOSTDOWN,
			},
			wantType:    ErrorTypeMountLost,
			wantMessage: "network/host unreachable",
		},
		{
			name: "EHOSTUNREACH returns MountLost",
			err: &fs.PathError{
				Op:   "stat",
				Path: "/test/path",
				Err:  syscall.EHOSTUNREACH,
			},
			wantType:    ErrorTypeMountLost,
			wantMessage: "network/host unreachable",
		},
		{
			name: "ENETDOWN returns MountLost",
			err: &fs.PathError{
				Op:   "stat",
				Path: "/test/path",
				Err:  syscall.ENETDOWN,
			},
			wantType:    ErrorTypeMountLost,
			wantMessage: "network/host unreachable",
		},
		{
			name: "ENETUNREACH returns MountLost",
			err: &fs.PathError{
				Op:   "stat",
				Path: "/test/path",
				Err:  syscall.ENETUNREACH,
			},
			wantType:    ErrorTypeMountLost,
			wantMessage: "network/host unreachable",
		},
		{
			name: "unknown syscall error returns empty",
			err: &fs.PathError{
				Op:   "stat",
				Path: "/test/path",
				Err:  syscall.EACCES, // not specifically handled
			},
			wantType:    "",
			wantMessage: "",
		},
		{
			name: "PathError with non-Errno returns empty",
			err: &fs.PathError{
				Op:   "stat",
				Path: "/test/path",
				Err:  fs.ErrNotExist,
			},
			wantType:    "",
			wantMessage: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotType, gotMessage := classifySyscallError(tt.err)
			if gotType != tt.wantType {
				t.Errorf("classifySyscallError() type = %q, want %q", gotType, tt.wantType)
			}
			if gotMessage != tt.wantMessage {
				t.Errorf("classifySyscallError() message = %q, want %q", gotMessage, tt.wantMessage)
			}
		})
	}
}
