package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	s := &RESTServer{
		router:    gin.New(),
		startTime: time.Now().Add(-1 * time.Hour), // Started 1 hour ago
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
			s := &RESTServer{
				router:    gin.New(),
				startTime: tt.startTime,
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

	s := &RESTServer{
		router:    gin.New(),
		startTime: time.Now(),
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

func TestSystemInfoLinksAreValid(t *testing.T) {
	gin.SetMode(gin.TestMode)

	config.SetForTesting(&config.Config{
		Port:                 "8080",
		VerificationTimeout:  60 * time.Second,
		VerificationInterval: 4 * time.Hour,
	})

	s := &RESTServer{
		router:    gin.New(),
		startTime: time.Now(),
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
