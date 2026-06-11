package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/safego"
)

// runFFprobeWithArgs runs the configured detector (ffprobe in quick mode,
// ffmpeg in thorough mode). For thorough mode it has a defensive retry
// path: if the first attempt fails AND the failure pattern looks like a
// hardware-decoder / hwaccel infrastructure problem (SIGSEGV, libcuvid
// missing, "Failed to setup hwaccel"), it retries the same file once
// with hwaccel forced off. Only if BOTH attempts fail does the error
// propagate to classifyDetectorError - which means the corruption /
// remediation pipeline never deletes a file that "failed" only because
// the GPU runtime is broken. See #276 for the bug this prevents.
func (hc *CmdHealthChecker) runFFprobeWithArgs(path string, customArgs []string, mode string, ov *ScanOverrides) error {
	err := hc.runDetectorAttempt(path, customArgs, mode, ov)
	if err == nil {
		return nil
	}
	// Only the thorough path uses hwaccel, so the retry only makes sense there.
	if mode != ModeThorough {
		return err
	}
	if !isLikelyHwAccelFailure(err) {
		return err
	}
	logger.Warnf("Thorough decode failed with a hwaccel/decoder-init pattern on %s, retrying with hwaccel disabled: %v", path, err)
	return hc.runDetectorAttempt(path, customArgs, mode, withHwAccelOff(ov))
}

// runDetectorAttempt is a single ffmpeg/ffprobe invocation against
// `path`. The runFFprobeWithArgs wrapper handles the retry-without-hwaccel
// fallback; this is the lower-level "build args, run, propagate result"
// that the wrapper composes.
func (hc *CmdHealthChecker) runDetectorAttempt(path string, customArgs []string, mode string, ov *ScanOverrides) error {
	var args []string
	var cmdPath string
	var cmdName string

	if mode == ModeThorough {
		// Thorough mode: Use ffmpeg to decode the file and check for stream
		// corruption. Catches issues that header-only checks miss (mid-file
		// corruption, bad frames, etc.). -xerror exits on first decode error.
		// "-f null -" discards output. Hardware-accel args go first (they are
		// input options). When HEALARR_HEALTH_CHECK_THOROUGH_DURATION > 0 we
		// also inject "-t <seconds>" so the decode walks only the prefix - far
		// faster on slow codecs like AV1, at the cost of not catching mid/late
		// corruption (a trade the operator opts into).
		cmdPath = hc.FFmpegPath
		cmdName = "ffmpeg"
		args = append([]string{"-v", "error", argXError}, hc.hwAccelInputArgs(ov, path)...)
		if duration := effectiveThoroughDuration(ov); duration > 0 {
			args = append(args, "-t", strconv.FormatFloat(duration.Seconds(), 'f', -1, 64))
		}
		args = append(args, customArgs...)
		args = append(args, "-i", path, "-f", "null", "-")
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

	// Thorough mode needs a much longer timeout since it decodes (some or all
	// of) the file. The thorough cap is configurable via
	// HEALARR_HEALTH_CHECK_THOROUGH_TIMEOUT (raise it for slow codecs / large
	// files, or shrink it once you have set THOROUGH_DURATION to a short prefix).
	timeout := 30 * time.Second
	if mode == ModeThorough {
		timeout = effectiveThoroughTimeout(ov)
	}

	done := make(chan error, 1)
	safego.Run("ffprobe-cmd", func() {
		done <- cmd.Run()
	})

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
			// Include the wait-error description (which carries signal info like
			// "signal: segmentation fault" for crashed processes) AND the stderr
			// content. The signal info is what isLikelyHwAccelFailure pattern-
			// matches against to detect a crashed decoder vs. a corrupt file.
			return fmt.Errorf("%s failed (%v): %s", cmdName, err, stderr.String())
		}
	}

	return nil
}

// isLikelyHwAccelFailure returns true if the error looks like a problem
// with the hardware-decode path (driver lib missing, decoder crashed,
// hwaccel init failed) rather than the input file being corrupt. Healarr
// uses this to know when to retry with hwaccel disabled instead of
// letting the failure cascade into classifyDetectorError and from there
// into the remediation/delete pipeline.
//
// The patterns are deliberately broad: a false positive here just costs
// one extra ffmpeg invocation; a false negative could route a healthy
// file to deletion (see #276 for the failure mode this prevents).
func isLikelyHwAccelFailure(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())

	// Process-level crashes. Go's exec wraps these as "signal: <name>" or
	// "exit status 139" (128 + SIGSEGV). A crashed decoder is never the
	// file's fault - cleanly-broken files exit non-zero with a message.
	if strings.Contains(s, "signal: segmentation fault") ||
		strings.Contains(s, "signal: bus error") ||
		strings.Contains(s, "signal: aborted") ||
		strings.Contains(s, "signal: killed") ||
		strings.Contains(s, "exit status 139") {
		return true
	}

	// Explicit hwaccel-init failure messages from ffmpeg. Match on the
	// shared "hardware decoder" substring so we catch both the US and
	// UK "initializ/initialis" spellings ffmpeg has used in various
	// release branches without tripping the misspell linter on our own
	// source.
	if strings.Contains(s, "failed to setup hwaccel") ||
		strings.Contains(s, "hardware decoder") ||
		strings.Contains(s, "device creation failed") ||
		strings.Contains(s, "no supported hwaccel") {
		return true
	}

	// CUDA / NVDEC userspace library load failures (the #276 symptom on
	// our Alpine + NVIDIA Container Toolkit combo: libcuda.so missing).
	if strings.Contains(s, "cannot load libcuda") ||
		strings.Contains(s, "cannot load libnvcuvid") ||
		strings.Contains(s, "cannot load nvcuvid") ||
		strings.Contains(s, "cuvid initialization failed") ||
		strings.Contains(s, "could not initialize cuda") ||
		strings.Contains(s, "cuda_error_") {
		return true
	}

	return false
}

// withHwAccelOff returns a copy of ov with Hwaccel forced to "off". Used
// by the retry path in runFFprobeWithArgs to disable hardware decode
// for the second attempt without mutating the caller's overrides.
func withHwAccelOff(ov *ScanOverrides) *ScanOverrides {
	off := "off"
	if ov == nil {
		return &ScanOverrides{Hwaccel: &off}
	}
	out := *ov
	out.Hwaccel = &off
	return &out
}

func (hc *CmdHealthChecker) runHandBrakeWithArgs(path string, customArgs []string, mode string, ov *ScanOverrides) error {
	// Mode determines the type of check:
	// - "quick": Basic scan of container structure
	// - "thorough": Full stream analysis (HandBrake's default scan is already quite thorough)

	var args []string
	var timeout time.Duration

	if mode == ModeThorough {
		// Thorough mode: Full scan with preview analysis
		// --previews 10:0 generates 10 previews at different points to verify stream integrity
		args = []string{argScan, argPreviews, "10:0", "-i", path}
		timeout = effectiveThoroughTimeout(ov)
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
	safego.Run("handbrake-cmd", func() {
		done <- cmd.Run()
	})

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
			// Include the wait/exec error itself (%v), not just stderr: a
			// missing binary or signal death has empty stderr, and the
			// classifier needs the "fork/exec ..." / "signal: ..." text to
			// map the failure to a recoverable ToolFailure instead of
			// corruption.
			return fmt.Errorf("HandBrake failed (%v): %s", err, stderr.String())
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

// buildMediaInfoArgs constructs the command arguments and timeout for MediaInfo.
func buildMediaInfoArgs(mode string, customArgs []string, path string) ([]string, time.Duration) {
	var baseArgs []string
	var timeout time.Duration

	if mode == ModeThorough {
		baseArgs = []string{argOutputJSON, argFull}
		timeout = 2 * time.Minute
	} else {
		baseArgs = []string{argOutputJSON}
		timeout = 30 * time.Second
	}

	args := append(baseArgs, customArgs...)
	args = append(args, path)
	return args, timeout
}

// runCommandWithTimeout executes a command with a timeout, returning stdout or an error.
//
// Every detection subprocess passes through here, which makes it the one
// choke point where global scan concurrency can actually be enforced: the
// scan pool, webhook-triggered checks, deferred rescans and verification all
// end up in this function. The slot is acquired BEFORE the timeout clock
// starts, so queueing behind the limiter never eats a tool's time budget.
func runCommandWithTimeout(cmd *exec.Cmd, timeout time.Duration, toolName string) ([]byte, error) {
	toolLimiter.acquire(EffectiveScanWorkers)
	defer toolLimiter.release()

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Start command in main goroutine to avoid race on cmd.Process
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("%s failed to start: %s", toolName, err)
	}

	done := make(chan error, 1)
	safego.Run("mediainfo-wait", func() {
		done <- cmd.Wait()
	})

	select {
	case <-time.After(timeout):
		_ = cmd.Process.Kill()
		// Wait for goroutine to complete before returning
		<-done
		return nil, fmt.Errorf("%s timed out after %v", toolName, timeout)
	case err := <-done:
		if err != nil {
			// Include the wait/exec error (%v) alongside stderr so signal
			// deaths and launch failures (empty stderr) carry the text the
			// classifier maps to recoverable ToolFailure.
			return nil, fmt.Errorf("%s failed (%v): %s", toolName, err, stderr.String())
		}
	}
	return stdout.Bytes(), nil
}

// validateMediaInfoOutput parses MediaInfo JSON and verifies it contains valid media tracks.
func validateMediaInfoOutput(data []byte) error {
	var result map[string]interface{}
	if err := json.Unmarshal(data, &result); err != nil {
		return fmt.Errorf("mediainfo produced invalid JSON: %v", err)
	}

	media, ok := result["media"].(map[string]interface{})
	if !ok || media == nil {
		return fmt.Errorf("mediainfo: no media information found in file")
	}

	tracks, ok := media["track"].([]interface{})
	if !ok || len(tracks) == 0 {
		return fmt.Errorf("mediainfo: no tracks found in file")
	}

	if !hasValidMediaTrack(tracks) {
		return fmt.Errorf("mediainfo: no video or audio tracks found in file")
	}
	return nil
}

// hasValidMediaTrack checks if any track is a Video or Audio track.
func hasValidMediaTrack(tracks []interface{}) bool {
	for _, track := range tracks {
		t, ok := track.(map[string]interface{})
		if !ok {
			continue
		}
		trackType, _ := t["@type"].(string)
		if trackType == "Video" || trackType == "Audio" {
			return true
		}
	}
	return false
}

func (hc *CmdHealthChecker) runMediaInfo(path string, customArgs []string, mode string) error {
	args, timeout := buildMediaInfoArgs(mode, customArgs, path)
	cmd := exec.Command(hc.MediaInfoPath, args...)

	output, err := runCommandWithTimeout(cmd, timeout, "mediainfo")
	if err != nil {
		return err
	}

	return validateMediaInfoOutput(output)
}
