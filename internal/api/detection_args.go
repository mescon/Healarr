package api

import (
	"errors"
	"fmt"
	"strings"
)

// allowedDetectionFlags lists ffmpeg / ffprobe / mediainfo flags that an
// operator may supply via the per-scan-path detection_args setting.
//
// The set is intentionally small: only performance/probing tuning flags
// that take primitive values. Anything that lets ffmpeg open additional
// inputs (-i), choose output formats (-f), bypass protocol restrictions
// (-protocol_whitelist), or reference URLs/files is excluded — those
// would turn a configurable scan into an exfiltration primitive for any
// caller who can edit a scan path.
var allowedDetectionFlags = map[string]bool{
	"-threads":         true, // CPU thread count for decoding
	"-probesize":       true, // bytes to read for format detection
	"-analyzeduration": true, // microseconds to analyze streams
	"-fpsprobesize":    true, // frames to inspect for FPS detection
	"-loglevel":        true, // log verbosity (overrides default -v error)
	"-v":               true, // alias for -loglevel
	"-hide_banner":     true, // suppress ffmpeg startup banner
	"-nostats":         true, // suppress periodic encoding statistics
}

// validateDetectionArgs enforces an allowlist on the per-scan-path
// detection_args values before they are persisted. Called at API write
// time so misconfiguration is caught at edit, not at scan execution
// where it could attempt to exfiltrate data.
//
// Each element in args is one ffmpeg argv slot. Flag elements (those
// starting with "-") must be members of allowedDetectionFlags. Value
// elements (no leading dash) are accepted but must not contain protocol
// markers that turn into URLs or pipes inside ffmpeg.
func validateDetectionArgs(args []string) error {
	for i, arg := range args {
		if arg == "" {
			return fmt.Errorf("detection_args[%d] is empty", i)
		}
		if err := checkForProtocolMarker(arg); err != nil {
			return fmt.Errorf("detection_args[%d]: %w", i, err)
		}
		if strings.HasPrefix(arg, "-") {
			if !allowedDetectionFlags[arg] {
				return fmt.Errorf("detection_args[%d]: flag %q is not permitted (allowed: %s)",
					i, arg, allowedFlagsList())
			}
		}
	}
	return nil
}

// checkForProtocolMarker rejects strings that ffmpeg would interpret as
// non-file inputs: URLs, file: URIs, and ffmpeg's pipe: protocol.
func checkForProtocolMarker(arg string) error {
	lower := strings.ToLower(arg)
	switch {
	case strings.Contains(lower, "://"):
		return errors.New("URLs are not permitted in detection_args")
	case strings.HasPrefix(lower, "file:"):
		return errors.New("file: URIs are not permitted in detection_args")
	case strings.HasPrefix(lower, "pipe:"):
		return errors.New("pipe: redirection is not permitted in detection_args")
	}
	return nil
}

// allowedFlagsList returns the allowlist as a sorted comma-separated
// string, for error messages.
func allowedFlagsList() string {
	flags := make([]string, 0, len(allowedDetectionFlags))
	for f := range allowedDetectionFlags {
		flags = append(flags, f)
	}
	// Insertion sort — N ≤ 10 so the simple algorithm is fine.
	for i := 1; i < len(flags); i++ {
		for j := i; j > 0 && flags[j-1] > flags[j]; j-- {
			flags[j-1], flags[j] = flags[j], flags[j-1]
		}
	}
	return strings.Join(flags, ", ")
}
