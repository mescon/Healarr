package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/mescon/Healarr/internal/config"
	"github.com/mescon/Healarr/internal/logger"
)

// GitHubRelease represents the response from GitHub's releases API
type GitHubRelease struct {
	TagName     string    `json:"tag_name"`
	Name        string    `json:"name"`
	Body        string    `json:"body"`
	HTMLURL     string    `json:"html_url"`
	PublishedAt time.Time `json:"published_at"`
	Assets      []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

// UpdateCheckResponse is the response returned to the frontend
type UpdateCheckResponse struct {
	CurrentVersion   string            `json:"current_version"`
	LatestVersion    string            `json:"latest_version"`
	UpdateAvailable  bool              `json:"update_available"`
	ReleaseURL       string            `json:"release_url"`
	Changelog        string            `json:"changelog"`
	PublishedAt      string            `json:"published_at"`
	DownloadURLs     map[string]string `json:"download_urls"`
	DockerPullCmd    string            `json:"docker_pull_cmd"`
	UpdateInstructions UpdateInstructions `json:"update_instructions"`
}

// UpdateInstructions provides platform-specific upgrade guidance
type UpdateInstructions struct {
	Docker  string `json:"docker"`
	Linux   string `json:"linux"`
	MacOS   string `json:"macos"`
	Windows string `json:"windows"`
}

const (
	githubAPIURL = "https://api.github.com/repos/mescon/Healarr/releases/latest"
	githubRepo   = "mescon/Healarr"
)

// handleCheckUpdate fetches the latest release from GitHub and compares versions
func (s *RESTServer) handleCheckUpdate(c *gin.Context) {
	currentVersion := config.Version

	// Fetch latest release from GitHub
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("GET", githubAPIURL, nil)
	if err != nil {
		logger.Errorf("Failed to create GitHub request: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check for updates"})
		return
	}

	// GitHub recommends setting User-Agent
	req.Header.Set("User-Agent", "Healarr/"+currentVersion)
	req.Header.Set("Accept", "application/vnd.github.v3+json")

	resp, err := client.Do(req)
	if err != nil {
		logger.Errorf("Failed to fetch GitHub release: %v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error":   "Unable to check for updates",
			"details": "Could not connect to GitHub. Please check your internet connection.",
		})
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		// No releases published yet
		c.JSON(http.StatusOK, UpdateCheckResponse{
			CurrentVersion:  currentVersion,
			LatestVersion:   currentVersion,
			UpdateAvailable: false,
			Changelog:       "No releases available yet.",
		})
		return
	}

	if resp.StatusCode != http.StatusOK {
		logger.Errorf("GitHub API returned status %d", resp.StatusCode)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"error": fmt.Sprintf("GitHub API error (status %d)", resp.StatusCode),
		})
		return
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		logger.Errorf("Failed to parse GitHub release: %v", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to parse release information"})
		return
	}

	latestVersion := strings.TrimPrefix(release.TagName, "v")
	currentClean := strings.TrimPrefix(currentVersion, "v")
	updateAvailable := compareVersions(currentClean, latestVersion) < 0

	// Build download URLs from assets
	downloadURLs := make(map[string]string)
	for _, asset := range release.Assets {
		name := strings.ToLower(asset.Name)
		switch {
		case strings.Contains(name, "linux") && strings.Contains(name, "amd64"):
			downloadURLs["linux_amd64"] = asset.BrowserDownloadURL
		case strings.Contains(name, "linux") && strings.Contains(name, "arm64"):
			downloadURLs["linux_arm64"] = asset.BrowserDownloadURL
		case strings.Contains(name, "darwin") && strings.Contains(name, "amd64"):
			downloadURLs["macos_amd64"] = asset.BrowserDownloadURL
		case strings.Contains(name, "darwin") && strings.Contains(name, "arm64"):
			downloadURLs["macos_arm64"] = asset.BrowserDownloadURL
		case strings.Contains(name, "windows"):
			downloadURLs["windows_amd64"] = asset.BrowserDownloadURL
		}
	}

	response := UpdateCheckResponse{
		CurrentVersion:  currentVersion,
		LatestVersion:   release.TagName,
		UpdateAvailable: updateAvailable,
		ReleaseURL:      release.HTMLURL,
		Changelog:       release.Body,
		PublishedAt:     release.PublishedAt.Format("January 2, 2006"),
		DownloadURLs:    downloadURLs,
		DockerPullCmd:   fmt.Sprintf("docker pull ghcr.io/%s:%s", strings.ToLower(githubRepo), release.TagName),
		UpdateInstructions: UpdateInstructions{
			Docker: `To update Healarr in Docker:
1. Stop the container: docker compose down
2. Pull the latest image: docker compose pull
3. Start the container: docker compose up -d`,
			Linux: `To update Healarr on Linux:
1. Stop the running instance
2. Download the new binary from the release page
3. Replace the existing binary
4. Restart the application`,
			MacOS: `To update Healarr on macOS:
1. Stop the running instance
2. Download the new binary from the release page
3. Replace the existing binary
4. Restart the application`,
			Windows: `To update Healarr on Windows:
1. Stop the running instance
2. Download the new .exe from the release page
3. Replace the existing executable
4. Restart the application`,
		},
	}

	c.JSON(http.StatusOK, response)
}

// compareVersions compares two semantic versions
// Returns: -1 if v1 < v2, 0 if equal, 1 if v1 > v2
func compareVersions(v1, v2 string) int {
	// Handle "dev" version - always considered older than any release
	if v1 == "dev" || v1 == "" {
		if v2 == "dev" || v2 == "" {
			return 0
		}
		return -1
	}
	if v2 == "dev" || v2 == "" {
		return 1
	}

	// Parse version components
	parts1 := parseVersion(v1)
	parts2 := parseVersion(v2)

	// Compare each component
	maxLen := len(parts1)
	if len(parts2) > maxLen {
		maxLen = len(parts2)
	}

	for i := 0; i < maxLen; i++ {
		var p1, p2 int
		if i < len(parts1) {
			p1 = parts1[i]
		}
		if i < len(parts2) {
			p2 = parts2[i]
		}

		if p1 < p2 {
			return -1
		}
		if p1 > p2 {
			return 1
		}
	}

	return 0
}

// parseVersion extracts numeric components from a version string
func parseVersion(v string) []int {
	var parts []int
	var current int
	var inNumber bool

	for _, c := range v {
		if c >= '0' && c <= '9' {
			current = current*10 + int(c-'0')
			inNumber = true
		} else if inNumber {
			parts = append(parts, current)
			current = 0
			inNumber = false
		}
	}
	if inNumber {
		parts = append(parts, current)
	}

	return parts
}
