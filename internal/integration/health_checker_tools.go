package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/mescon/Healarr/internal/logger"
	"github.com/mescon/Healarr/internal/safego"
)

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
func runCommandWithTimeout(cmd *exec.Cmd, timeout time.Duration, toolName string) ([]byte, error) {
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
			return nil, fmt.Errorf("%s failed: %s", toolName, stderr.String())
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
