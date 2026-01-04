package api

import (
	"bufio"
	"fmt"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/config"
)

// SystemInfo contains runtime environment information
type SystemInfo struct {
	Version     string            `json:"version"`
	Environment string            `json:"environment"` // "docker" or "native"
	OS          string            `json:"os"`
	Arch        string            `json:"arch"`
	GoVersion   string            `json:"go_version"`
	Uptime      string            `json:"uptime"`
	UptimeSecs  int64             `json:"uptime_seconds"`
	StartedAt   time.Time         `json:"started_at"`
	Config      SystemConfigInfo  `json:"config"`
	Mounts      []MountInfo       `json:"mounts,omitempty"`
	Links       SystemLinks       `json:"links"`
}

// SystemConfigInfo contains configuration details
type SystemConfigInfo struct {
	Port                 string `json:"port"`
	BasePath             string `json:"base_path"`
	BasePathSource       string `json:"base_path_source"`
	LogLevel             string `json:"log_level"`
	DataDir              string `json:"data_dir"`
	DatabasePath         string `json:"database_path"`
	LogDir               string `json:"log_dir"`
	DryRunMode           bool   `json:"dry_run_mode"`
	RetentionDays        int    `json:"retention_days"`
	DefaultMaxRetries    int    `json:"default_max_retries"`
	VerificationTimeout  string `json:"verification_timeout"`
	VerificationInterval string `json:"verification_interval"`
	ArrRateLimitRPS      float64 `json:"arr_rate_limit_rps"`
	ArrRateLimitBurst    int    `json:"arr_rate_limit_burst"`
}

// MountInfo contains information about a mounted volume
type MountInfo struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	Type        string `json:"type,omitempty"`
	ReadOnly    bool   `json:"read_only"`
}

// SystemLinks contains useful links
type SystemLinks struct {
	GitHub       string `json:"github"`
	Issues       string `json:"issues"`
	Releases     string `json:"releases"`
	Wiki         string `json:"wiki"`
	Discussions  string `json:"discussions"`
}

// handleSystemInfo returns runtime environment information
func (s *RESTServer) handleSystemInfo(c *gin.Context) {
	cfg := config.Get()
	uptime := time.Since(s.startTime)

	// Determine environment
	environment := "native"
	if isDockerEnvironment() {
		environment = "docker"
	}

	// Format uptime
	days := int(uptime.Hours()) / 24
	hours := int(uptime.Hours()) % 24
	minutes := int(uptime.Minutes()) % 60
	var uptimeStr string
	if days > 0 {
		uptimeStr = fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	} else if hours > 0 {
		uptimeStr = fmt.Sprintf("%dh %dm", hours, minutes)
	} else {
		uptimeStr = fmt.Sprintf("%dm", minutes)
	}

	info := SystemInfo{
		Version:     config.Version,
		Environment: environment,
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		GoVersion:   runtime.Version(),
		Uptime:      uptimeStr,
		UptimeSecs:  int64(uptime.Seconds()),
		StartedAt:   s.startTime,
		Config: SystemConfigInfo{
			Port:                 cfg.Port,
			BasePath:             cfg.BasePath,
			BasePathSource:       cfg.BasePathSource,
			LogLevel:             cfg.LogLevel,
			DataDir:              cfg.DataDir,
			DatabasePath:         cfg.DatabasePath,
			LogDir:               cfg.LogDir,
			DryRunMode:           cfg.DryRunMode,
			RetentionDays:        cfg.RetentionDays,
			DefaultMaxRetries:    cfg.DefaultMaxRetries,
			VerificationTimeout:  cfg.VerificationTimeout.String(),
			VerificationInterval: cfg.VerificationInterval.String(),
			ArrRateLimitRPS:      cfg.ArrRateLimitRPS,
			ArrRateLimitBurst:    cfg.ArrRateLimitBurst,
		},
		Links: SystemLinks{
			GitHub:      "https://github.com/mescon/Healarr",
			Issues:      "https://github.com/mescon/Healarr/issues",
			Releases:    "https://github.com/mescon/Healarr/releases",
			Wiki:        "https://github.com/mescon/Healarr/wiki",
			Discussions: "https://github.com/mescon/Healarr/discussions",
		},
	}

	// Get mount information if in Docker
	if environment == "docker" {
		info.Mounts = getMountInfo()
	}

	c.JSON(http.StatusOK, info)
}

// isDockerEnvironment checks if we're running inside a Docker container
func isDockerEnvironment() bool {
	// Check for .dockerenv file
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	// Check cgroup for docker/containerd
	if data, err := os.ReadFile("/proc/1/cgroup"); err == nil {
		content := string(data)
		if strings.Contains(content, "docker") || strings.Contains(content, "containerd") {
			return true
		}
	}

	// Check for /run/.containerenv (podman)
	if _, err := os.Stat("/run/.containerenv"); err == nil {
		return true
	}

	return false
}

// getMountInfo reads mount information from /proc/mounts
func getMountInfo() []MountInfo {
	var mounts []MountInfo

	file, err := os.Open("/proc/mounts")
	if err != nil {
		return mounts
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) < 4 {
			continue
		}

		device := fields[0]
		mountPoint := fields[1]
		fsType := fields[2]
		options := fields[3]

		// Filter to show only interesting mounts (skip system mounts)
		if !isInterestingMount(mountPoint, fsType) {
			continue
		}

		mount := MountInfo{
			Source:      device,
			Destination: mountPoint,
			Type:        fsType,
			ReadOnly:    strings.Contains(options, "ro"),
		}
		mounts = append(mounts, mount)
	}

	return mounts
}

// isInterestingMount filters out system mounts and shows only user-relevant ones
func isInterestingMount(mountPoint, fsType string) bool {
	// Skip system filesystems
	skipTypes := []string{"proc", "sysfs", "devpts", "tmpfs", "cgroup", "mqueue", "devtmpfs", "securityfs", "debugfs", "hugetlbfs", "pstore", "bpf", "tracefs", "configfs", "fusectl", "efivarfs"}
	for _, t := range skipTypes {
		if fsType == t {
			return false
		}
	}

	// Skip system paths
	skipPaths := []string{"/proc", "/sys", "/dev", "/run", "/etc/resolv.conf", "/etc/hostname", "/etc/hosts"}
	for _, p := range skipPaths {
		if strings.HasPrefix(mountPoint, p) || mountPoint == p {
			return false
		}
	}

	// Include common user mount points
	interestingPaths := []string{"/config", "/data", "/media", "/tv", "/movies", "/downloads", "/mnt", "/storage"}
	for _, p := range interestingPaths {
		if strings.HasPrefix(mountPoint, p) {
			return true
		}
	}

	// Include overlay (Docker layers) and bind mounts that aren't system paths
	if fsType == "overlay" || fsType == "ext4" || fsType == "xfs" || fsType == "btrfs" || fsType == "zfs" || fsType == "nfs" || fsType == "cifs" || fsType == "fuse.mergerfs" {
		// Skip root filesystem
		if mountPoint == "/" {
			return false
		}
		return true
	}

	return false
}
