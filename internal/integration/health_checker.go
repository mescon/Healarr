package integration

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/safego"
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

// DetectionMethod specifies which tool to use for media file health checking.
type DetectionMethod string

const (
	// DetectionZeroByte checks if a file has zero bytes (empty file).
	DetectionZeroByte DetectionMethod = "zero_byte"
	// DetectionFFprobe uses ffprobe/ffmpeg for media file validation.
	DetectionFFprobe DetectionMethod = "ffprobe"
	// DetectionMediaInfo uses MediaInfo for media file validation.
	DetectionMediaInfo DetectionMethod = "mediainfo"
	// DetectionHandBrake uses HandBrakeCLI for media file validation.
	DetectionHandBrake DetectionMethod = "handbrake"
)

// DetectionMode is the typed enum of scan depth modes. Promoted to a typed
// string in Phase 2.1.d so the DB scan / API boundary can reject unknown
// values rather than silently storing them (closes T5).
//
// ModeQuick/ModeThorough are deliberately UNTYPED string constants so the
// many existing comparison sites (`mode == ModeThorough`) keep compiling
// against both bare `string` and `DetectionMode` variables. The typed
// enum is the boundary primitive; internal uses can stay as bare strings
// until a future PR threads the type through more sites.
type DetectionMode string

const (
	// ModeQuick performs header-only analysis (fast).
	ModeQuick = "quick"
	// ModeThorough performs full stream decoding (slow but comprehensive).
	ModeThorough = "thorough"
)

var validDetectionMethods = map[DetectionMethod]bool{
	DetectionZeroByte:  true,
	DetectionFFprobe:   true,
	DetectionMediaInfo: true,
	DetectionHandBrake: true,
}

var validDetectionModes = map[DetectionMode]bool{
	ModeQuick:    true,
	ModeThorough: true,
}

// ParseDetectionMethod validates and converts a raw string to DetectionMethod.
// Use at API write boundaries and config-import edges.
func ParseDetectionMethod(s string) (DetectionMethod, error) {
	m := DetectionMethod(s)
	if !validDetectionMethods[m] {
		return "", fmt.Errorf("unknown detection_method %q (must be zero_byte, ffprobe, mediainfo, or handbrake)", s)
	}
	return m, nil
}

// ParseDetectionMode validates and converts a raw string to DetectionMode.
func ParseDetectionMode(s string) (DetectionMode, error) {
	m := DetectionMode(s)
	if !validDetectionModes[m] {
		return "", fmt.Errorf("unknown detection_mode %q (must be quick or thorough)", s)
	}
	return m, nil
}

// Scan implements sql.Scanner so DetectionMethod can be passed to rows.Scan.
func (m *DetectionMethod) Scan(value any) error {
	if value == nil {
		return fmt.Errorf("DetectionMethod: cannot scan NULL")
	}
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("DetectionMethod: expected string DB value, got %T", value)
	}
	parsed, err := ParseDetectionMethod(s)
	if err != nil {
		return fmt.Errorf("DetectionMethod.Scan: %w", err)
	}
	*m = parsed
	return nil
}

// Value implements driver.Valuer for DetectionMethod.
func (m DetectionMethod) Value() (driver.Value, error) {
	return string(m), nil
}

// Scan implements sql.Scanner so DetectionMode can be passed to rows.Scan.
func (m *DetectionMode) Scan(value any) error {
	if value == nil {
		return fmt.Errorf("DetectionMode: cannot scan NULL")
	}
	var s string
	switch v := value.(type) {
	case string:
		s = v
	case []byte:
		s = string(v)
	default:
		return fmt.Errorf("DetectionMode: expected string DB value, got %T", value)
	}
	parsed, err := ParseDetectionMode(s)
	if err != nil {
		return fmt.Errorf("DetectionMode.Scan: %w", err)
	}
	*m = parsed
	return nil
}

// Value implements driver.Valuer for DetectionMode.
func (m DetectionMode) Value() (driver.Value, error) {
	return string(m), nil
}

// Content analysis constants
const contentAnalysisThreshold = 0.90 // Flag if >90% of duration is affected

// Compiled regexes for parsing ffmpeg detection filter output
var (
	blackDurationRe   = regexp.MustCompile(`black_duration:\s*([\d.]+)`)
	freezeDurationRe  = regexp.MustCompile(`freeze_duration:\s*([\d.]+)`)
	silenceDurationRe = regexp.MustCompile(`silence_duration:\s*([\d.]+)`)
)

// parseDurations sums all duration values matching the given regex in ffmpeg stderr output.
func parseDurations(re *regexp.Regexp, stderr string) float64 {
	var total float64
	for _, match := range re.FindAllStringSubmatch(stderr, -1) {
		if val, err := strconv.ParseFloat(match[1], 64); err == nil {
			total += val
		}
	}
	return total
}

// (DetectionConfig.Mode below uses the bare string type for now to avoid
// rippling through all CheckWithConfig call sites in this PR; a follow-up
// can swap Mode to DetectionMode. The Parse/Scan/Value helpers above are
// what matter for the boundary-validation guarantee.)

// DetectionConfig specifies how to check media file health.
type DetectionConfig struct {
	Method DetectionMethod
	Args   []string
	Mode   string // "quick" or "thorough"
}

// CmdHealthChecker validates media files using external command-line tools.
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

// Check validates a media file using the default ffprobe detection method.
// Retained for the HealthChecker interface; new code should call CheckWithConfig directly.
func (hc *CmdHealthChecker) Check(path, mode string) (bool, *HealthCheckError) {
	return hc.CheckWithConfig(path, DetectionConfig{
		Method: DetectionFFprobe,
		Args:   []string{},
		Mode:   mode,
	})
}

// CheckWithConfig validates a media file using the specified detection configuration.
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

	// Last-resort string match. classifySyscallError above already covers these
	// conditions by errno (locale-independent); this catches the rare case where
	// the error isn't a *fs.PathError wrapping a syscall.Errno (e.g. an error
	// that lost its syscall identity through wrapping). Substring matching is
	// locale-fragile, so it must remain the fallback, not the primary path.
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
//
// The known-infra cases are matched on strings: the error wraps a subprocess's
// stderr text (ffprobe/mediainfo), not a Go syscall error, so there is no errno
// to match. To stay locale-robust where it matters most, the fallthrough does
// NOT blindly assume corruption (which would route to deletion) — it re-checks
// accessibility first, so a mount that dropped mid-probe or any unrecognized
// infra error is classified recoverable rather than as a corrupt file.
func (hc *CmdHealthChecker) classifyDetectorError(err error, path string) *HealthCheckError {
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

	// Fallthrough: the detector exited non-zero but the message matched no known
	// infra pattern. Before concluding the file is corrupt — which routes to the
	// destructive remediation path — re-verify the file is actually accessible.
	// If a mount dropped mid-probe (or the message was localized/unrecognized),
	// accessibility now fails and we return that recoverable classification
	// (errno-based, locale-independent) instead of deleting a healthy file.
	if accessErr := hc.checkAccessibility(path); accessErr != nil {
		return accessErr
	}

	// File is still accessible, so the detector's failure is about its content:
	// treat as container/header corruption.
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

// buildFFprobePreview builds the command preview for ffprobe/ffmpeg detection.
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

// GetCommandPreview returns the exact command that would be executed for a given configuration.
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
		return "instant (file metadata only)"
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

// contentAnalysisResult holds parsed durations for threshold evaluation.
type contentAnalysisResult struct {
	BlackDuration   float64
	FreezeDuration  float64
	SilenceDuration float64
	TotalDuration   float64
	HasVideo        bool
	HasAudio        bool
}

// evaluateContentAnalysis checks if any content issue exceeds the corruption threshold.
// Priority: black > frozen > silent (returns first match).
func evaluateContentAnalysis(r contentAnalysisResult) (bool, *HealthCheckError) {
	if r.TotalDuration <= 0 {
		return true, nil
	}

	if r.HasVideo {
		if r.BlackDuration/r.TotalDuration > contentAnalysisThreshold {
			return false, &HealthCheckError{
				Type: ErrorTypeBlackVideo,
				Message: fmt.Sprintf("video is %.0f%% black (%.1fs of %.1fs)",
					r.BlackDuration/r.TotalDuration*100, r.BlackDuration, r.TotalDuration),
			}
		}
		if r.FreezeDuration/r.TotalDuration > contentAnalysisThreshold {
			return false, &HealthCheckError{
				Type: ErrorTypeFrozenVideo,
				Message: fmt.Sprintf("video is %.0f%% frozen (%.1fs of %.1fs)",
					r.FreezeDuration/r.TotalDuration*100, r.FreezeDuration, r.TotalDuration),
			}
		}
	}

	if r.HasAudio {
		if r.SilenceDuration/r.TotalDuration > contentAnalysisThreshold {
			return false, &HealthCheckError{
				Type: ErrorTypeSilentAudio,
				Message: fmt.Sprintf("audio is %.0f%% silent (%.1fs of %.1fs)",
					r.SilenceDuration/r.TotalDuration*100, r.SilenceDuration, r.TotalDuration),
			}
		}
	}

	return true, nil
}

// mediaProbeInfo holds duration and stream type information from ffprobe.
type mediaProbeInfo struct {
	Duration float64
	HasVideo bool
	HasAudio bool
}

// getMediaProbeInfo uses ffprobe to get file duration and stream types in a single call.
func (hc *CmdHealthChecker) getMediaProbeInfo(path string) (*mediaProbeInfo, error) {
	cmd := exec.Command(hc.FFprobePath, "-v", "error",
		"-show_entries", "format=duration:stream=codec_type",
		"-of", "json", path)

	output, err := runCommandWithTimeout(cmd, 30*time.Second, "ffprobe")
	if err != nil {
		return nil, err
	}

	var result struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
		} `json:"streams"`
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}
	if err := json.Unmarshal(output, &result); err != nil {
		return nil, fmt.Errorf("failed to parse ffprobe JSON: %v", err)
	}

	info := &mediaProbeInfo{}
	for _, s := range result.Streams {
		switch s.CodecType {
		case "video":
			info.HasVideo = true
		case "audio":
			info.HasAudio = true
		}
	}

	if result.Format.Duration == "" {
		return nil, fmt.Errorf("no duration in ffprobe output")
	}
	dur, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil {
		return nil, fmt.Errorf("failed to parse duration %q: %v", result.Format.Duration, err)
	}
	info.Duration = dur

	return info, nil
}

// AnalyzeContent checks for content-level issues (black video, frozen video, silent audio)
// in files that have already passed structural health checks.
// Only meaningful in thorough mode — call after CheckWithConfig passes.
func (hc *CmdHealthChecker) AnalyzeContent(path string) (bool, *HealthCheckError) {
	if err := validateMediaPath(path); err != nil {
		return false, &HealthCheckError{
			Type:    ErrorTypeInvalidConfig,
			Message: fmt.Sprintf("invalid media path: %v", err),
		}
	}

	// Probe file for duration and stream types
	info, err := hc.getMediaProbeInfo(path)
	if err != nil {
		logger.Warnf("Content analysis skipped (probe failed): %s: %v", path, err)
		return true, nil
	}

	if info.Duration <= 0 {
		logger.Warnf("Content analysis skipped (invalid duration %.2f): %s", info.Duration, path)
		return true, nil
	}

	if !info.HasVideo && !info.HasAudio {
		return true, nil
	}

	// Build ffmpeg command with appropriate filters
	ffmpegArgs := []string{"-nostats", "-v", "info", "-i", path}

	var vf []string
	if info.HasVideo {
		vf = append(vf, "blackdetect=d=10:pix_th=0.10", "freezedetect=n=0.003:d=10")
	}
	if len(vf) > 0 {
		ffmpegArgs = append(ffmpegArgs, "-vf", strings.Join(vf, ","))
	}

	if info.HasAudio {
		ffmpegArgs = append(ffmpegArgs, "-af", "silencedetect=n=-50dB:d=10")
	}

	ffmpegArgs = append(ffmpegArgs, "-f", "null", "-")

	// Run ffmpeg with detection filters
	cmd := exec.Command(hc.FFmpegPath, ffmpegArgs...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	timeout := 10 * time.Minute
	done := make(chan error, 1)
	safego.Run("content-analysis-cmd", func() {
		done <- cmd.Run()
	})

	select {
	case <-time.After(timeout):
		if cmd.Process != nil {
			if killErr := cmd.Process.Kill(); killErr != nil {
				logger.Debugf("Content analysis kill returned: %v", killErr)
			}
			<-done
		}
		logger.Warnf("Content analysis timed out after %v: %s", timeout, path)
		return true, nil
	case err := <-done:
		if err != nil {
			logger.Warnf("Content analysis ffmpeg error (treating as healthy): %s: %v", path, err)
			return true, nil
		}
	}

	// Parse results and evaluate against threshold
	output := stderr.String()
	return evaluateContentAnalysis(contentAnalysisResult{
		BlackDuration:   parseDurations(blackDurationRe, output),
		FreezeDuration:  parseDurations(freezeDurationRe, output),
		SilenceDuration: parseDurations(silenceDurationRe, output),
		TotalDuration:   info.Duration,
		HasVideo:        info.HasVideo,
		HasAudio:        info.HasAudio,
	})
}
