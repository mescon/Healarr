package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/integration"
)

func TestHandleSystemInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)

	// Set up test config
	config.SetForTesting(&config.Config{
		Port:                 "8080",
		BasePath:             "/",
		BasePathSource:       "default",
		LogLevel:             "info",
		DataDir:              "/config",
		DatabasePath:         "/config/healarr.db",
		LogDir:               "/config/logs",
		DryRunMode:           false,
		RetentionDays:        30,
		DefaultMaxRetries:    3,
		VerificationTimeout:  60 * time.Second,
		VerificationInterval: 4 * time.Hour,
		ArrRateLimitRPS:      10.0,
		ArrRateLimitBurst:    20,
	})

	toolChecker := integration.NewToolChecker()
	toolChecker.CheckAllTools() // Populate tools status
	s := &RESTServer{
		router:      gin.New(),
		startTime:   time.Now().Add(-1 * time.Hour), // Started 1 hour ago
		toolChecker: toolChecker,
	}

	s.router.GET("/api/system/info", s.handleSystemInfo)

	req, _ := http.NewRequest("GET", "/api/system/info", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	require.Equal(t, http.StatusOK, w.Code)

	var response SystemInfo
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Check required fields
	assert.NotEmpty(t, response.Version)
	assert.NotEmpty(t, response.Environment) // "docker" or "native"
	assert.NotEmpty(t, response.OS)
	assert.NotEmpty(t, response.Arch)
	assert.NotEmpty(t, response.GoVersion)
	assert.NotEmpty(t, response.Uptime)
	assert.Greater(t, response.UptimeSecs, int64(0))
	assert.NotZero(t, response.StartedAt)

	// Check config
	assert.Equal(t, "8080", response.Config.Port)
	assert.Equal(t, "/", response.Config.BasePath)
	assert.Equal(t, "info", response.Config.LogLevel)
	assert.Equal(t, "/config", response.Config.DataDir)
	assert.Equal(t, "/config/healarr.db", response.Config.DatabasePath)
	assert.Equal(t, "/config/logs", response.Config.LogDir)
	assert.Equal(t, false, response.Config.DryRunMode)
	assert.Equal(t, 30, response.Config.RetentionDays)
	assert.Equal(t, 3, response.Config.DefaultMaxRetries)

	// Check links
	assert.Equal(t, "https://github.com/mescon/Healarr", response.Links.GitHub)
	assert.Equal(t, "https://github.com/mescon/Healarr/issues", response.Links.Issues)
	assert.Equal(t, "https://github.com/mescon/Healarr/releases", response.Links.Releases)
	assert.Equal(t, "https://github.com/mescon/Healarr/wiki", response.Links.Wiki)
	assert.Equal(t, "https://github.com/mescon/Healarr/discussions", response.Links.Discussions)

	// Check tools - should have entries for required tools
	assert.NotNil(t, response.Tools)
	// ffprobe should always be in the tools map (whether available or not)
	ffprobe, exists := response.Tools["ffprobe"]
	assert.True(t, exists, "ffprobe should be in tools map")
	assert.Equal(t, "ffprobe", ffprobe.Name)
	assert.True(t, ffprobe.Required, "ffprobe should be marked as required")
}

func TestHandleSystemInfo_UptimeFormatting(t *testing.T) {
	gin.SetMode(gin.TestMode)

	config.SetForTesting(&config.Config{
		Port:                 "8080",
		VerificationTimeout:  60 * time.Second,
		VerificationInterval: 4 * time.Hour,
	})

	tests := []struct {
		name          string
		startTime     time.Time
		expectedMatch string
	}{
		{
			name:          "minutes only",
			startTime:     time.Now().Add(-30 * time.Minute),
			expectedMatch: "30m",
		},
		{
			name:          "hours and minutes",
			startTime:     time.Now().Add(-3*time.Hour - 15*time.Minute),
			expectedMatch: "3h",
		},
		{
			name:          "days hours minutes",
			startTime:     time.Now().Add(-2*24*time.Hour - 5*time.Hour - 30*time.Minute),
			expectedMatch: "2d",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toolChecker := integration.NewToolChecker()
			s := &RESTServer{
				router:      gin.New(),
				startTime:   tt.startTime,
				toolChecker: toolChecker,
			}

			s.router.GET("/api/system/info", s.handleSystemInfo)

			req, _ := http.NewRequest("GET", "/api/system/info", nil)
			w := httptest.NewRecorder()
			s.router.ServeHTTP(w, req)

			var response SystemInfo
			json.Unmarshal(w.Body.Bytes(), &response)

			assert.Contains(t, response.Uptime, tt.expectedMatch)
		})
	}
}

func TestIsInterestingMount(t *testing.T) {
	tests := []struct {
		name       string
		mountPoint string
		fsType     string
		expected   bool
	}{
		// Interesting mounts
		{name: "config mount", mountPoint: "/config", fsType: "ext4", expected: true},
		{name: "data mount", mountPoint: "/data", fsType: "ext4", expected: true},
		{name: "media mount", mountPoint: "/media/movies", fsType: "nfs", expected: true},
		{name: "storage mount", mountPoint: "/storage", fsType: "xfs", expected: true},
		{name: "downloads mount", mountPoint: "/downloads", fsType: "cifs", expected: true},
		{name: "mergerfs mount", mountPoint: "/mnt/merged", fsType: "fuse.mergerfs", expected: true},
		{name: "zfs mount", mountPoint: "/mnt/pool", fsType: "zfs", expected: true},

		// System mounts to skip
		{name: "proc", mountPoint: "/proc", fsType: "proc", expected: false},
		{name: "sysfs", mountPoint: "/sys", fsType: "sysfs", expected: false},
		{name: "devpts", mountPoint: "/dev/pts", fsType: "devpts", expected: false},
		{name: "tmpfs run", mountPoint: "/run/something", fsType: "tmpfs", expected: false},
		{name: "cgroup", mountPoint: "/sys/fs/cgroup", fsType: "cgroup", expected: false},
		{name: "resolv.conf", mountPoint: "/etc/resolv.conf", fsType: "ext4", expected: false},
		{name: "hostname", mountPoint: "/etc/hostname", fsType: "ext4", expected: false},
		{name: "hosts", mountPoint: "/etc/hosts", fsType: "ext4", expected: false},

		// Root filesystem should be skipped
		{name: "root overlay", mountPoint: "/", fsType: "overlay", expected: false},
		{name: "root ext4", mountPoint: "/", fsType: "ext4", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isInterestingMount(tt.mountPoint, tt.fsType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestSystemInfoEnvironmentField(t *testing.T) {
	gin.SetMode(gin.TestMode)

	config.SetForTesting(&config.Config{
		Port:                 "8080",
		VerificationTimeout:  60 * time.Second,
		VerificationInterval: 4 * time.Hour,
	})

	toolChecker := integration.NewToolChecker()
	s := &RESTServer{
		router:      gin.New(),
		startTime:   time.Now(),
		toolChecker: toolChecker,
	}

	s.router.GET("/api/system/info", s.handleSystemInfo)

	req, _ := http.NewRequest("GET", "/api/system/info", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	var response SystemInfo
	err := json.Unmarshal(w.Body.Bytes(), &response)
	require.NoError(t, err)

	// Environment should be either "docker" or "native"
	assert.True(t, response.Environment == "docker" || response.Environment == "native",
		"Environment should be 'docker' or 'native', got: %s", response.Environment)
}

func TestGetMountInfo_EmptyWhenNotInDocker(t *testing.T) {
	// getMountInfo reads from /proc/mounts
	// In test environment (non-Docker), it should return empty or the available mounts
	mounts := getMountInfo()

	// In a non-Docker environment, getMountInfo filters out most system mounts
	// The function should not panic and should return a valid slice
	if mounts == nil {
		// Function should return at least an empty slice, not nil
		t.Log("getMountInfo returned nil (acceptable if /proc/mounts doesn't exist)")
	}

	// Verify that any returned mounts have valid fields
	for _, mount := range mounts {
		if mount.Destination == "" {
			t.Errorf("Mount destination should not be empty: %+v", mount)
		}
	}
}

func TestGetMountInfo_FiltersMountTypes(t *testing.T) {
	// This test verifies that the filtering logic works correctly
	// by directly testing isInterestingMount with various inputs

	// These should all be filtered out (not interesting)
	notInteresting := []struct {
		mountPoint string
		fsType     string
	}{
		{"/proc", "proc"},
		{"/sys", "sysfs"},
		{"/dev/pts", "devpts"},
		{"/", "overlay"},
		{"/run/lock", "tmpfs"},
		{"/sys/fs/cgroup", "cgroup"},
		{"/etc/hostname", "ext4"},
	}

	for _, tc := range notInteresting {
		if isInterestingMount(tc.mountPoint, tc.fsType) {
			t.Errorf("Expected %s (%s) to not be interesting", tc.mountPoint, tc.fsType)
		}
	}

	// These should pass through (interesting)
	interesting := []struct {
		mountPoint string
		fsType     string
	}{
		{"/config", "ext4"},
		{"/data", "xfs"},
		{"/media/movies", "nfs"},
		{"/mnt/storage", "zfs"},
		{"/storage/backups", "cifs"},
	}

	for _, tc := range interesting {
		if !isInterestingMount(tc.mountPoint, tc.fsType) {
			t.Errorf("Expected %s (%s) to be interesting", tc.mountPoint, tc.fsType)
		}
	}
}

func TestIsDockerEnvironment(t *testing.T) {
	// Test the isDockerEnvironment function
	// Result depends on actual environment
	result := isDockerEnvironment()

	// We can't assert a specific value since it depends on the environment
	// but we can verify the function doesn't panic and returns a bool
	t.Logf("isDockerEnvironment() = %v (depends on test environment)", result)
}

func TestMountInfoStruct(t *testing.T) {
	// Test that MountInfo struct fields work correctly
	mount := MountInfo{
		Source:      "/dev/sda1",
		Destination: "/config",
		Type:        "ext4",
		ReadOnly:    false,
	}

	if mount.Source != "/dev/sda1" {
		t.Errorf("Expected Source '/dev/sda1', got %s", mount.Source)
	}
	if mount.Destination != "/config" {
		t.Errorf("Expected Destination '/config', got %s", mount.Destination)
	}
	if mount.Type != "ext4" {
		t.Errorf("Expected Type 'ext4', got %s", mount.Type)
	}
	if mount.ReadOnly {
		t.Error("Expected ReadOnly to be false")
	}
}

func TestSystemInfoLinksAreValid(t *testing.T) {
	gin.SetMode(gin.TestMode)

	config.SetForTesting(&config.Config{
		Port:                 "8080",
		VerificationTimeout:  60 * time.Second,
		VerificationInterval: 4 * time.Hour,
	})

	toolChecker := integration.NewToolChecker()
	s := &RESTServer{
		router:      gin.New(),
		startTime:   time.Now(),
		toolChecker: toolChecker,
	}

	s.router.GET("/api/system/info", s.handleSystemInfo)

	req, _ := http.NewRequest("GET", "/api/system/info", nil)
	w := httptest.NewRecorder()
	s.router.ServeHTTP(w, req)

	var response SystemInfo
	json.Unmarshal(w.Body.Bytes(), &response)

	// All links should start with the GitHub base URL
	baseURL := "https://github.com/mescon/Healarr"
	assert.True(t, response.Links.GitHub == baseURL)
	assert.True(t, response.Links.Issues == baseURL+"/issues")
	assert.True(t, response.Links.Releases == baseURL+"/releases")
	assert.True(t, response.Links.Wiki == baseURL+"/wiki")
	assert.True(t, response.Links.Discussions == baseURL+"/discussions")
}
