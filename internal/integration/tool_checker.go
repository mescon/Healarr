package integration

import (
	"bytes"
	"os/exec"
	"regexp"
	"strings"
	"sync"

	"github.com/mescon/Healarr/internal/logger"
)

// ToolStatus represents the availability status of a detection tool
type ToolStatus struct {
	Name        string `json:"name"`
	Available   bool   `json:"available"`
	Path        string `json:"path,omitempty"`
	Version     string `json:"version,omitempty"`
	Required    bool   `json:"required"`
	Description string `json:"description"`
}

// ToolChecker checks availability of detection tools
type ToolChecker struct {
	mu    sync.RWMutex
	tools map[string]*ToolStatus
}

// NewToolChecker creates a new tool checker instance
func NewToolChecker() *ToolChecker {
	return &ToolChecker{
		tools: make(map[string]*ToolStatus),
	}
}

// CheckAllTools checks availability of all detection tools and caches results
func (tc *ToolChecker) CheckAllTools() map[string]*ToolStatus {
	tc.mu.Lock()
	defer tc.mu.Unlock()

	// Check ffprobe (required - primary detection tool)
	tc.tools["ffprobe"] = tc.checkFFprobe()

	// Check ffmpeg (required for thorough mode)
	tc.tools["ffmpeg"] = tc.checkFFmpeg()

	// Check mediainfo (optional alternative)
	tc.tools["mediainfo"] = tc.checkMediaInfo()

	// Check HandBrakeCLI (optional alternative)
	tc.tools["handbrake"] = tc.checkHandBrake()

	return tc.tools
}

// GetToolStatus returns the cached status of all tools
func (tc *ToolChecker) GetToolStatus() map[string]*ToolStatus {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	// Return copy to prevent external modification
	result := make(map[string]*ToolStatus, len(tc.tools))
	for k, v := range tc.tools {
		copy := *v
		result[k] = &copy
	}
	return result
}

// IsToolAvailable checks if a specific tool is available
func (tc *ToolChecker) IsToolAvailable(name string) bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	if tool, ok := tc.tools[name]; ok {
		return tool.Available
	}
	return false
}

// HasRequiredTools checks if all required tools are available
func (tc *ToolChecker) HasRequiredTools() bool {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	for _, tool := range tc.tools {
		if tool.Required && !tool.Available {
			return false
		}
	}
	return true
}

// GetMissingRequiredTools returns list of missing required tools
func (tc *ToolChecker) GetMissingRequiredTools() []string {
	tc.mu.RLock()
	defer tc.mu.RUnlock()

	var missing []string
	for name, tool := range tc.tools {
		if tool.Required && !tool.Available {
			missing = append(missing, name)
		}
	}
	return missing
}

func (tc *ToolChecker) checkFFprobe() *ToolStatus {
	status := &ToolStatus{
		Name:        "ffprobe",
		Required:    true,
		Description: "Primary tool for media file analysis (quick mode)",
	}

	path, err := exec.LookPath("ffprobe")
	if err != nil {
		logger.Debugf("ffprobe not found in PATH: %v", err)
		return status
	}

	status.Available = true
	status.Path = path

	// Get version
	cmd := exec.Command(path, "-version")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		// Extract version from first line: "ffprobe version 6.1.1 Copyright..."
		firstLine := strings.Split(out.String(), "\n")[0]
		if matches := regexp.MustCompile(`version\s+(\S+)`).FindStringSubmatch(firstLine); len(matches) > 1 {
			status.Version = matches[1]
		}
	}

	return status
}

func (tc *ToolChecker) checkFFmpeg() *ToolStatus {
	status := &ToolStatus{
		Name:        "ffmpeg",
		Required:    true,
		Description: "Required for thorough mode (full file decode)",
	}

	path, err := exec.LookPath("ffmpeg")
	if err != nil {
		logger.Debugf("ffmpeg not found in PATH: %v", err)
		return status
	}

	status.Available = true
	status.Path = path

	// Get version
	cmd := exec.Command(path, "-version")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		firstLine := strings.Split(out.String(), "\n")[0]
		if matches := regexp.MustCompile(`version\s+(\S+)`).FindStringSubmatch(firstLine); len(matches) > 1 {
			status.Version = matches[1]
		}
	}

	return status
}

func (tc *ToolChecker) checkMediaInfo() *ToolStatus {
	status := &ToolStatus{
		Name:        "mediainfo",
		Required:    false,
		Description: "Alternative detection method for detailed media analysis",
	}

	path, err := exec.LookPath("mediainfo")
	if err != nil {
		logger.Debugf("mediainfo not found in PATH: %v", err)
		return status
	}

	status.Available = true
	status.Path = path

	// Get version
	cmd := exec.Command(path, "--Version")
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		// MediaInfo outputs "MediaInfo Command line, MediaInfoLib - v24.01"
		output := strings.TrimSpace(out.String())
		if matches := regexp.MustCompile(`v(\d+\.\d+)`).FindStringSubmatch(output); len(matches) > 1 {
			status.Version = matches[1]
		}
	}

	return status
}

func (tc *ToolChecker) checkHandBrake() *ToolStatus {
	status := &ToolStatus{
		Name:        "handbrake",
		Required:    false,
		Description: "Alternative detection method using HandBrake CLI",
	}

	path, err := exec.LookPath("HandBrakeCLI")
	if err != nil {
		logger.Debugf("HandBrakeCLI not found in PATH: %v", err)
		return status
	}

	status.Available = true
	status.Path = path

	// Get version
	cmd := exec.Command(path, "--version")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out // HandBrake outputs to stderr
	if err := cmd.Run(); err == nil {
		// HandBrake outputs "HandBrake 1.7.3"
		output := strings.TrimSpace(out.String())
		if matches := regexp.MustCompile(`HandBrake\s+(\S+)`).FindStringSubmatch(output); len(matches) > 1 {
			status.Version = matches[1]
		}
	}

	return status
}

// RefreshTools re-checks all tools and updates the cache
func (tc *ToolChecker) RefreshTools() map[string]*ToolStatus {
	return tc.CheckAllTools()
}
