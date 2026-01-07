package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/mescon/Healarr/internal/logger"
)

// FFmpeg/FFprobe command line argument constants
const (
	argXError      = "-xerror"       // Exit on first decode error
	argShowFormat  = "-show_format"  // Show format information
	argShowStreams = "-show_streams" // Show stream information
)

// HandBrake CLI argument constants
const (
	argScan     = "--scan"     // Scan mode for HandBrakeCLI
	argPreviews = "--previews" // Preview frames argument
)

// MediaInfo CLI argument constants
const (
	argOutputJSON = "--Output=JSON" // JSON output format
	argFull       = "--Full"        // Full output mode
)

// validateMediaPath ensures a file path is safe to pass to subprocess commands.
// Since we use exec.Command directly (not via shell), the main concerns are:
// - Null bytes that could truncate the path
// - Newlines that could interfere with argument parsing
// - Path traversal attempts
// Note: Characters like {}, $, `, etc. are safe because exec.Command doesn't
// interpret them - they're passed directly to the executable as literal characters.
func validateMediaPath(path string) error {
	// Path must be absolute to prevent relative path attacks
	if !filepath.IsAbs(path) {
		return fmt.Errorf("path must be absolute: %s", path)
	}

	// Reject null bytes - these could truncate the path in C-based tools
	if strings.Contains(path, "\x00") {
		return fmt.Errorf("path contains null byte: %s", path)
	}

	// Reject newlines - could interfere with argument parsing
	if strings.Contains(path, "\n") || strings.Contains(path, "\r") {
		return fmt.Errorf("path contains newline: %s", path)
	}

	return nil
}

type DetectionMethod string

const (
	DetectionZeroByte  DetectionMethod = "zero_byte"
	DetectionFFprobe   DetectionMethod = "ffprobe"
	DetectionMediaInfo DetectionMethod = "mediainfo"
	DetectionHandBrake DetectionMethod = "handbrake"
)

// Detection mode constants
const (
	ModeQuick    = "quick"    // Header-only analysis (fast)
	ModeThorough = "thorough" // Full stream decoding (slow)
)

type DetectionConfig struct {
	Method DetectionMethod
	Args   []string
	Mode   string // "quick" or "thorough"
}

type CmdHealthChecker struct {
	// Paths to binaries, can be configured
	FFprobePath   string
	FFmpegPath    string
	MediaInfoPath string
	HandBrakePath string
}

// NewHealthChecker creates a health checker with default binary paths (uses PATH lookup).
func NewHealthChecker() *CmdHealthChecker {
	return &CmdHealthChecker{
		FFprobePath:   "ffprobe",
		FFmpegPath:    "ffmpeg",
		MediaInfoPath: "mediainfo",
		HandBrakePath: "HandBrakeCLI",
	}
}

// NewHealthCheckerWithPaths creates a health checker with custom binary paths.
// This allows using non-standard binary locations (e.g., /config/tools/ffprobe).
func NewHealthCheckerWithPaths(ffprobePath, ffmpegPath, mediainfoPath, handbrakePath string) *CmdHealthChecker {
	return &CmdHealthChecker{
		FFprobePath:   ffprobePath,
		FFmpegPath:    ffmpegPath,
		MediaInfoPath: mediainfoPath,
		HandBrakePath: handbrakePath,
	}
}

func (hc *CmdHealthChecker) Check(path string, mode string) (bool, *HealthCheckError) {
	// Legacy method - use default ffprobe detection
	return hc.CheckWithConfig(path, DetectionConfig{
		Method: DetectionFFprobe,
		Args:   []string{},
		Mode:   mode,
	})
}

func (hc *CmdHealthChecker) CheckWithConfig(path string, config DetectionConfig) (bool, *HealthCheckError) {
	// 0. Validate path to prevent command injection before any subprocess execution
	if err := validateMediaPath(path); err != nil {
		return false, &HealthCheckError{
			Type:    ErrorTypeInvalidConfig,
			Message: fmt.Sprintf("invalid media path: %v", err),
		}
	}

	// 1. Zero byte check (if requested)
	if config.Method == DetectionZeroByte {
		return hc.checkZeroByte(path)
	}

	// 2. Pre-flight accessibility check (distinguishes mount/access issues from corruption)
	if err := hc.checkAccessibility(path); err != nil {
		return false, err
	}

	// Default to ModeQuick if mode not specified
	mode := config.Mode
	if mode == "" {
		mode = ModeQuick
	}

	// 3. Run appropriate detector with mode awareness
	switch config.Method {
	case DetectionFFprobe:
		err := hc.runFFprobeWithArgs(path, config.Args, mode)
		if err != nil {
			return false, hc.classifyDetectorError(err, path)
		}
	case DetectionMediaInfo:
		err := hc.runMediaInfo(path, config.Args, mode)
		if err != nil {
			return false, hc.classifyDetectorError(err, path)
		}
	case DetectionHandBrake:
		err := hc.runHandBrakeWithArgs(path, config.Args, mode)
		if err != nil {
			// HandBrake errors are typically stream-level corruption
			errStr := err.Error()
			if strings.Contains(errStr, "No such file or directory") ||
				strings.Contains(errStr, "does not exist") {
				return false, &HealthCheckError{Type: ErrorTypePathNotFound, Message: errStr}
			}
			return false, &HealthCheckError{Type: ErrorTypeCorruptStream, Message: errStr}
		}
	default:
		return false, &HealthCheckError{Type: ErrorTypeInvalidConfig, Message: "unknown detection method"}
	}

	return true, nil
}

// checkAccessibility performs pre-flight checks to distinguish between
// true file corruption and transient infrastructure issues (mount lost, NAS down, etc.)
func (hc *CmdHealthChecker) checkAccessibility(path string) *HealthCheckError {
	// 1. Check if parent directory exists and is accessible
	parentDir := filepath.Dir(path)
	parentInfo, parentErr := os.Stat(parentDir)
	if parentErr != nil {
		// Parent directory is inaccessible - this is almost certainly a mount/NAS issue
		return hc.classifyOSError(parentErr, parentDir, true)
	}

	// 2. Verify parent is actually a directory (not a file left over from unmount)
	if !parentInfo.IsDir() {
		return &HealthCheckError{
			Type:    ErrorTypeMountLost,
			Message: fmt.Sprintf("parent path is not a directory (possible stale mount): %s", parentDir),
		}
	}

	// 3. Check if we can list the parent directory (verify mount is functional)
	entries, listErr := os.ReadDir(parentDir)
	if listErr != nil {
		return &HealthCheckError{
			Type:    ErrorTypeMountLost,
			Message: fmt.Sprintf("cannot read parent directory (mount may be stale): %v", listErr),
		}
	}

	// 4. Now check the file itself
	fileInfo, fileErr := os.Stat(path)
	if fileErr != nil {
		// File doesn't exist but parent is accessible
		// This could be legitimate (file was deleted) or a partial mount issue
		if os.IsNotExist(fileErr) {
			// Double-check: if parent has entries but file is missing, it might be truly gone
			// vs if parent is empty (suspicious for a media directory)
			if len(entries) == 0 {
				return &HealthCheckError{
					Type:    ErrorTypeMountLost,
					Message: fmt.Sprintf("parent directory is empty (possible mount issue): %s", parentDir),
				}
			}
			// Parent has files, so this file is legitimately missing
			return &HealthCheckError{
				Type:    ErrorTypePathNotFound,
				Message: fileErr.Error(),
			}
		}
		return hc.classifyOSError(fileErr, path, false)
	}

	// 5. Final sanity check: file should have non-negative size
	if fileInfo.Size() < 0 {
		return &HealthCheckError{
			Type:    ErrorTypeIOError,
			Message: "file reports negative size (filesystem corruption or stale handle)",
		}
	}

	return nil // All checks passed
}

// classifyOSError converts an OS error into the appropriate HealthCheckError type
func (hc *CmdHealthChecker) classifyOSError(err error, path string, isParent bool) *HealthCheckError {
	context := "file"
	if isParent {
		context = "parent directory"
	}

	// Check for permission errors
	if os.IsPermission(err) {
		return &HealthCheckError{
			Type:    ErrorTypeAccessDenied,
			Message: fmt.Sprintf("%s access denied: %v", context, err),
		}
	}

	// Check for not-exist errors
	if os.IsNotExist(err) {
		if isParent {
			// Parent directory missing is almost always a mount issue
			return &HealthCheckError{
				Type:    ErrorTypeMountLost,
				Message: fmt.Sprintf("parent directory not found (mount may be offline): %s", path),
			}
		}
		return &HealthCheckError{
			Type:    ErrorTypePathNotFound,
			Message: fmt.Sprintf("file not found: %s", path),
		}
	}

	// Check for platform-specific syscall errors (see errno_unix.go and errno_windows.go)
	if errType, errMsg := classifySyscallError(err); errType != "" {
		return &HealthCheckError{
			Type:    errType,
			Message: fmt.Sprintf("%s: %s", errMsg, path),
		}
	}

	// Check for common mount-related error messages
	errStr := strings.ToLower(err.Error())
	if strings.Contains(errStr, "transport endpoint") ||
		strings.Contains(errStr, "stale") ||
		strings.Contains(errStr, "mount") ||
		strings.Contains(errStr, "connection refused") ||
		strings.Contains(errStr, "no route to host") ||
		strings.Contains(errStr, "network is unreachable") {
		return &HealthCheckError{
			Type:    ErrorTypeMountLost,
			Message: fmt.Sprintf("mount/network error: %v", err),
		}
	}

	// Default: treat as generic I/O error (recoverable)
	return &HealthCheckError{
		Type:    ErrorTypeIOError,
		Message: fmt.Sprintf("filesystem error accessing %s: %v", context, err),
	}
}

// classifyDetectorError analyzes errors from ffprobe/mediainfo and classifies them appropriately.
// This catches cases where files disappear between accessibility check and detector execution (race condition),
// or where the detector sees different paths than Go's os.Stat (e.g., symlink resolution differences).
func (hc *CmdHealthChecker) classifyDetectorError(err error, _ string) *HealthCheckError {
	errStr := err.Error()

	// Check for path-related errors (file disappeared, wrong path, symlink issues)
	if strings.Contains(errStr, "No such file or directory") ||
		strings.Contains(errStr, "does not exist") ||
		strings.Contains(errStr, "not found") {
		return &HealthCheckError{
			Type:    ErrorTypePathNotFound,
			Message: errStr,
		}
	}

	// Check for permission errors
	if strings.Contains(errStr, "Permission denied") ||
		strings.Contains(errStr, "access denied") {
		return &HealthCheckError{
			Type:    ErrorTypeAccessDenied,
			Message: errStr,
		}
	}

	// Check for I/O errors (network/mount issues that manifest during read)
	if strings.Contains(errStr, "Input/output error") ||
		strings.Contains(errStr, "Connection refused") ||
		strings.Contains(errStr, "Network is unreachable") ||
		strings.Contains(errStr, "transport endpoint") {
		return &HealthCheckError{
			Type:    ErrorTypeIOError,
			Message: errStr,
		}
	}

	// Check for timeout
	if strings.Contains(errStr, "timed out") {
		return &HealthCheckError{
			Type:    ErrorTypeTimeout,
			Message: errStr,
		}
	}

	// Default: treat as header/container corruption (the detector ran but found issues with the file content)
	return &HealthCheckError{
		Type:    ErrorTypeCorruptHeader,
		Message: errStr,
	}
}

func (hc *CmdHealthChecker) checkZeroByte(path string) (bool, *HealthCheckError) {
	// First do accessibility check
	if err := hc.checkAccessibility(path); err != nil {
		return false, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return false, hc.classifyOSError(err, path, false)
	}
	if info.Size() == 0 {
		return false, &HealthCheckError{Type: ErrorTypeZeroByte, Message: "file is empty"}
	}
	return true, nil
}

func (hc *CmdHealthChecker) runFFprobeWithArgs(path string, customArgs []string, mode string) error {
	// Mode determines the type of check:
	// - "quick": Only check container headers and stream info (fast, ~1-2 seconds) using ffprobe
	// - "thorough": Decode entire file to detect stream corruption (slow, can take minutes) using ffmpeg

	var args []string
	var cmdPath string
	var cmdName string

	if mode == ModeThorough {
		// Thorough mode: Use ffmpeg to decode the entire file and check for stream corruption
		// This catches issues that header-only checks miss (mid-file corruption, bad frames, etc.)
		// -xerror makes ffmpeg exit on first decode error
		// -f null - outputs to null device (no output file needed)
		cmdPath = hc.FFmpegPath
		cmdName = "ffmpeg"
		args = []string{"-v", "error", argXError, "-i", path, "-f", "null", "-"}

		// Insert custom args before -i (if any)
		if len(customArgs) > 0 {
			newArgs := []string{"-v", "error", argXError}
			newArgs = append(newArgs, customArgs...)
			newArgs = append(newArgs, "-i", path, "-f", "null", "-")
			args = newArgs
		}
	} else {
		// Quick mode (default): Use ffprobe to check container structure and stream headers
		// Fast and reliable for detecting obvious corruption
		cmdPath = hc.FFprobePath
		cmdName = "ffprobe"
		args = []string{"-v", "error", argShowFormat, argShowStreams, path}

		// Insert custom args before path (if any)
		if len(customArgs) > 0 {
			newArgs := []string{"-v", "error", argShowFormat, argShowStreams}
			newArgs = append(newArgs, customArgs...)
			newArgs = append(newArgs, path)
			args = newArgs
		}
	}

	cmd := exec.Command(cmdPath, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Thorough mode needs much longer timeout since it decodes entire file
	timeout := 30 * time.Second
	if mode == ModeThorough {
		timeout = 10 * time.Minute // Large files can take a while to fully decode
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case <-time.After(timeout):
		if cmd.Process != nil {
			// Kill the process - errors expected if process already exited
			if killErr := cmd.Process.Kill(); killErr != nil {
				logger.Debugf("Process kill returned: %v (may be already exited)", killErr)
			}
			// Wait to reap the zombie process - error expected since we killed it
			if waitErr := cmd.Wait(); waitErr != nil {
				logger.Debugf("Process wait after kill: %v", waitErr)
			}
		}
		return fmt.Errorf("%s timed out after %v", cmdName, timeout)
	case err := <-done:
		if err != nil {
			return fmt.Errorf("%s failed: %s", cmdName, stderr.String())
		}
	}

	return nil
}

func (hc *CmdHealthChecker) runHandBrakeWithArgs(path string, customArgs []string, mode string) error {
	// Mode determines the type of check:
	// - "quick": Basic scan of container structure
	// - "thorough": Full stream analysis (HandBrake's default scan is already quite thorough)

	var args []string
	var timeout time.Duration

	if mode == ModeThorough {
		// Thorough mode: Full scan with preview analysis
		// --previews 10:0 generates 10 previews at different points to verify stream integrity
		args = []string{argScan, argPreviews, "10:0", "-i", path}
		timeout = 10 * time.Minute
	} else {
		// Quick mode: Basic container scan
		args = []string{argScan, "-i", path}
		timeout = 2 * time.Minute
	}

	// Insert custom args before -i
	if len(customArgs) > 0 {
		if mode == ModeThorough {
			newArgs := []string{argScan, argPreviews, "10:0"}
			newArgs = append(newArgs, customArgs...)
			newArgs = append(newArgs, "-i", path)
			args = newArgs
		} else {
			newArgs := []string{argScan}
			newArgs = append(newArgs, customArgs...)
			newArgs = append(newArgs, "-i", path)
			args = newArgs
		}
	}

	cmd := exec.Command(hc.HandBrakePath, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case <-time.After(timeout):
		if cmd.Process != nil {
			if killErr := cmd.Process.Kill(); killErr != nil {
				logger.Debugf("HandBrake process kill returned: %v", killErr)
			}
			if waitErr := cmd.Wait(); waitErr != nil {
				logger.Debugf("HandBrake process wait after kill: %v", waitErr)
			}
		}
		return fmt.Errorf("HandBrake scan timed out after %v", timeout)
	case err := <-done:
		if err != nil {
			return fmt.Errorf("HandBrake failed: %s", stderr.String())
		}
	}

	// HandBrake returns exit code 0 even for failures, so check output for error indicators
	combinedOutput := stdout.String() + stderr.String()
	if strings.Contains(combinedOutput, "No title found") ||
		strings.Contains(combinedOutput, "unrecognized file type") ||
		strings.Contains(combinedOutput, "open ") && strings.Contains(combinedOutput, " failed") {
		return fmt.Errorf("HandBrake scan failed: %s", combinedOutput)
	}

	return nil
}

func (hc *CmdHealthChecker) runMediaInfo(path string, customArgs []string, mode string) error {
	// Mode determines the type of check:
	// - "quick": Basic metadata extraction (container info)
	// - "thorough": Full parsing with all details (deeper analysis)

	var args []string
	var timeout time.Duration

	if mode == ModeThorough {
		// Thorough mode: Full details including all track info
		args = []string{argOutputJSON, argFull, path}
		timeout = 2 * time.Minute
	} else {
		// Quick mode: Basic JSON output
		args = []string{argOutputJSON, path}
		timeout = 30 * time.Second
	}

	// Insert custom args before path
	if len(customArgs) > 0 {
		if mode == ModeThorough {
			newArgs := []string{argOutputJSON, argFull}
			newArgs = append(newArgs, customArgs...)
			newArgs = append(newArgs, path)
			args = newArgs
		} else {
			newArgs := []string{argOutputJSON}
			newArgs = append(newArgs, customArgs...)
			newArgs = append(newArgs, path)
			args = newArgs
		}
	}

	cmd := exec.Command(hc.MediaInfoPath, args...)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()

	select {
	case <-time.After(timeout):
		if cmd.Process != nil {
			if killErr := cmd.Process.Kill(); killErr != nil {
				logger.Debugf("mediainfo process kill returned: %v", killErr)
			}
			if waitErr := cmd.Wait(); waitErr != nil {
				logger.Debugf("mediainfo process wait after kill: %v", waitErr)
			}
		}
		return fmt.Errorf("mediainfo timed out after %v", timeout)
	case err := <-done:
		if err != nil {
			return fmt.Errorf("mediainfo failed: %s", stderr.String())
		}
	}

	// Parse JSON output and verify it contains media information
	var result map[string]interface{}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return fmt.Errorf("mediainfo produced invalid JSON: %v", err)
	}

	// Check if media field exists and has tracks
	media, ok := result["media"].(map[string]interface{})
	if !ok || media == nil {
		return fmt.Errorf("mediainfo: no media information found in file")
	}

	// Check for tracks - a valid media file should have at least one track with video or audio
	tracks, ok := media["track"].([]interface{})
	if !ok || len(tracks) == 0 {
		return fmt.Errorf("mediainfo: no tracks found in file")
	}

	// Look for at least one video or audio track (not just General)
	hasMediaTrack := false
	for _, track := range tracks {
		if t, ok := track.(map[string]interface{}); ok {
			trackType, _ := t["@type"].(string)
			if trackType == "Video" || trackType == "Audio" {
				hasMediaTrack = true
				break
			}
		}
	}

	if !hasMediaTrack {
		return fmt.Errorf("mediainfo: no video or audio tracks found in file")
	}

	return nil
}

// GetCommandPreview returns the exact command that would be executed for a given configuration.
// This is useful for displaying to users so they know exactly what will run.
// buildFFprobePreview builds the command preview for ffprobe/ffmpeg detection
func (hc *CmdHealthChecker) buildFFprobePreview(mode string, customArgs []string, filePath string) string {
	var args []string
	if mode == ModeThorough {
		args = []string{hc.FFmpegPath, "-v", "error", argXError}
		args = append(args, customArgs...)
		args = append(args, "-i", filePath, "-f", "null", "-")
	} else {
		args = []string{hc.FFprobePath, "-v", "error", argShowFormat, argShowStreams}
		args = append(args, customArgs...)
		args = append(args, filePath)
	}
	return strings.Join(args, " ")
}

// buildMediaInfoPreview builds the command preview for mediainfo detection
func (hc *CmdHealthChecker) buildMediaInfoPreview(mode string, customArgs []string, filePath string) string {
	var args []string
	if mode == ModeThorough {
		args = []string{hc.MediaInfoPath, argOutputJSON, argFull}
	} else {
		args = []string{hc.MediaInfoPath, argOutputJSON}
	}
	args = append(args, customArgs...)
	args = append(args, filePath)
	return strings.Join(args, " ")
}

// buildHandBrakePreview builds the command preview for HandBrake detection
func (hc *CmdHealthChecker) buildHandBrakePreview(mode string, customArgs []string, filePath string) string {
	var args []string
	if mode == ModeThorough {
		args = []string{hc.HandBrakePath, argScan, argPreviews, "10:0"}
	} else {
		args = []string{hc.HandBrakePath, argScan}
	}
	args = append(args, customArgs...)
	args = append(args, "-i", filePath)
	return strings.Join(args, " ")
}

func (hc *CmdHealthChecker) GetCommandPreview(method DetectionMethod, mode string, customArgs []string) string {
	if mode == "" {
		mode = ModeQuick
	}

	filePath := "<file>"

	switch method {
	case DetectionZeroByte:
		return "stat <file> (checks if file size == 0)"
	case DetectionFFprobe:
		return hc.buildFFprobePreview(mode, customArgs, filePath)
	case DetectionMediaInfo:
		return hc.buildMediaInfoPreview(mode, customArgs, filePath)
	case DetectionHandBrake:
		return hc.buildHandBrakePreview(mode, customArgs, filePath)
	default:
		return "unknown detection method"
	}
}

// GetTimeoutDescription returns a human-readable description of the timeout for a given configuration.
func (hc *CmdHealthChecker) GetTimeoutDescription(method DetectionMethod, mode string) string {
	if mode == "" {
		mode = ModeQuick
	}

	switch method {
	case DetectionZeroByte:
		return "instant"
	case DetectionFFprobe:
		if mode == ModeThorough {
			return "10 minutes (ffmpeg decodes entire file)"
		}
		return "30 seconds (ffprobe header check)"
	case DetectionMediaInfo:
		if mode == ModeThorough {
			return "2 minutes"
		}
		return "30 seconds"
	case DetectionHandBrake:
		if mode == ModeThorough {
			return "10 minutes (with preview generation)"
		}
		return "2 minutes"
	default:
		return "unknown"
	}
}
