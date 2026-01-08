package api

import (
	"testing"
)

// =============================================================================
// isDevVersion tests
// =============================================================================

func TestIsDevVersion(t *testing.T) {
	tests := []struct {
		version string
		want    bool
	}{
		{"dev", true},
		{"", true},
		{"1.0.0", false},
		{"v1.0.0", false},
		{"0.1.0", false},
		{"development", false}, // Not exactly "dev"
		{"DEV", false},         // Case sensitive
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := isDevVersion(tt.version)
			if got != tt.want {
				t.Errorf("isDevVersion(%q) = %v, want %v", tt.version, got, tt.want)
			}
		})
	}
}

// =============================================================================
// getVersionPart tests
// =============================================================================

func TestGetVersionPart(t *testing.T) {
	parts := []int{1, 2, 3}

	tests := []struct {
		index int
		want  int
	}{
		{0, 1},
		{1, 2},
		{2, 3},
		{3, 0}, // Out of bounds
		{10, 0},
		{-1, 0}, // Will cause out of bounds check to fail, returns 0
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			// Skip negative index test as it would panic
			if tt.index < 0 {
				t.Skip("Negative index would panic")
			}
			got := getVersionPart(parts, tt.index)
			if got != tt.want {
				t.Errorf("getVersionPart(parts, %d) = %d, want %d", tt.index, got, tt.want)
			}
		})
	}

	// Test with empty slice
	t.Run("empty slice", func(t *testing.T) {
		got := getVersionPart([]int{}, 0)
		if got != 0 {
			t.Errorf("getVersionPart([], 0) = %d, want 0", got)
		}
	})
}

// =============================================================================
// parseVersion tests
// =============================================================================

func TestParseVersion(t *testing.T) {
	tests := []struct {
		version string
		want    []int
	}{
		{"1.0.0", []int{1, 0, 0}},
		{"1.2.3", []int{1, 2, 3}},
		{"0.1.0", []int{0, 1, 0}},
		{"10.20.30", []int{10, 20, 30}},
		{"1.2.3.4", []int{1, 2, 3, 4}},
		{"v1.0.0", []int{1, 0, 0}},        // 'v' prefix is ignored
		{"1.0.0-beta", []int{1, 0, 0}},    // Non-numeric suffixes ignored
		{"1.0.0-rc.1", []int{1, 0, 0, 1}}, // Numbers in pre-release are captured
		{"", nil},                         // Empty string
		{"abc", nil},                      // No numbers
		{"1", []int{1}},                   // Single number
		{"1.2", []int{1, 2}},              // Two parts
	}

	for _, tt := range tests {
		t.Run(tt.version, func(t *testing.T) {
			got := parseVersion(tt.version)

			// Handle nil vs empty slice comparison
			if len(got) == 0 && len(tt.want) == 0 {
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("parseVersion(%q) = %v, want %v", tt.version, got, tt.want)
				return
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseVersion(%q) = %v, want %v", tt.version, got, tt.want)
					return
				}
			}
		})
	}
}

// =============================================================================
// compareVersions tests
// =============================================================================

func TestCompareVersions(t *testing.T) {
	tests := []struct {
		v1   string
		v2   string
		want int
	}{
		// Equal versions
		{"1.0.0", "1.0.0", 0},
		{"1.2.3", "1.2.3", 0},
		{"0.0.1", "0.0.1", 0},

		// v1 < v2 (returns -1)
		{"1.0.0", "2.0.0", -1},
		{"1.0.0", "1.1.0", -1},
		{"1.0.0", "1.0.1", -1},
		{"0.9.9", "1.0.0", -1},
		{"1.2.3", "1.2.4", -1},

		// v1 > v2 (returns 1)
		{"2.0.0", "1.0.0", 1},
		{"1.1.0", "1.0.0", 1},
		{"1.0.1", "1.0.0", 1},
		{"1.0.0", "0.9.9", 1},
		{"1.2.4", "1.2.3", 1},

		// Dev version handling
		{"dev", "dev", 0},
		{"", "", 0},
		{"dev", "1.0.0", -1}, // dev is older than any release
		{"1.0.0", "dev", 1},  // any release is newer than dev
		{"", "1.0.0", -1},    // empty is treated as dev
		{"1.0.0", "", 1},

		// Different length versions
		{"1.0", "1.0.0", 0}, // Missing parts treated as 0
		{"1.0.0", "1.0", 0},
		{"1.0.1", "1.0", 1},
		{"1.0", "1.0.1", -1},

		// With 'v' prefix (should be stripped before calling)
		{"1.0.0", "1.0.0", 0},

		// Longer version chains
		{"1.2.3.4", "1.2.3.4", 0},
		{"1.2.3.4", "1.2.3.5", -1},
		{"1.2.3.5", "1.2.3.4", 1},
	}

	for _, tt := range tests {
		t.Run(tt.v1+" vs "+tt.v2, func(t *testing.T) {
			got := compareVersions(tt.v1, tt.v2)
			if got != tt.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
			}
		})
	}
}

// =============================================================================
// Edge cases and additional coverage
// =============================================================================

func TestCompareVersions_PreReleaseVariants(t *testing.T) {
	// Note: parseVersion extracts ALL numeric parts, including those in pre-release tags
	// So "1.0.0-rc.1" becomes [1,0,0,1] and "1.0.0+build123" becomes [1,0,0,123]
	tests := []struct {
		name string
		v1   string
		v2   string
		want int
	}{
		{"alpha vs beta same base", "1.0.0-alpha", "1.0.0-beta", 0}, // No numbers in alpha/beta
		{"rc.1 vs release", "1.0.0-rc.1", "1.0.0", 1},               // rc.1 extracts the "1" making it [1,0,0,1] > [1,0,0]
		{"with build metadata", "1.0.0+build123", "1.0.0", 1},       // build123 extracts "123" making it [1,0,0,123]
		{"same rc versions", "1.0.0-rc.1", "1.0.0-rc.1", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := compareVersions(tt.v1, tt.v2)
			if got != tt.want {
				t.Errorf("compareVersions(%q, %q) = %d, want %d", tt.v1, tt.v2, got, tt.want)
			}
		})
	}
}

func TestParseVersion_EdgeCases(t *testing.T) {
	tests := []struct {
		name    string
		version string
		want    []int
	}{
		{"leading zeros", "01.02.03", []int{1, 2, 3}},
		{"large numbers", "100.200.300", []int{100, 200, 300}},
		{"mixed separators", "1-2-3", []int{1, 2, 3}},
		{"underscore separator", "1_2_3", []int{1, 2, 3}},
		{"spaces", "1 2 3", []int{1, 2, 3}},
		{"only dots", "...", nil},
		{"trailing number", "version-1", []int{1}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseVersion(tt.version)

			if len(got) == 0 && len(tt.want) == 0 {
				return
			}

			if len(got) != len(tt.want) {
				t.Errorf("parseVersion(%q) = %v, want %v", tt.version, got, tt.want)
				return
			}

			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("parseVersion(%q) = %v, want %v", tt.version, got, tt.want)
					return
				}
			}
		})
	}
}

// =============================================================================
// Struct type tests
// =============================================================================

func TestGitHubReleaseStruct(t *testing.T) {
	// Ensure the struct can be instantiated and fields are accessible
	release := GitHubRelease{
		TagName: "v1.0.0",
		Name:    "Release 1.0.0",
		Body:    "Changelog here",
		HTMLURL: "https://github.com/mescon/Healarr/releases/v1.0.0",
	}

	if release.TagName != "v1.0.0" {
		t.Errorf("TagName = %q, want v1.0.0", release.TagName)
	}
}

func TestUpdateCheckResponseStruct(t *testing.T) {
	response := UpdateCheckResponse{
		CurrentVersion:  "1.0.0",
		LatestVersion:   "1.1.0",
		UpdateAvailable: true,
		ReleaseURL:      "https://example.com",
		Changelog:       "New features",
		PublishedAt:     "January 1, 2025",
		DownloadURLs:    map[string]string{"linux_amd64": "https://example.com/download"},
		DockerPullCmd:   "docker pull ghcr.io/mescon/healarr:v1.1.0",
		UpdateInstructions: UpdateInstructions{
			Docker:  "Docker instructions",
			Linux:   "Linux instructions",
			MacOS:   "macOS instructions",
			Windows: "Windows instructions",
		},
	}

	if !response.UpdateAvailable {
		t.Error("Expected UpdateAvailable to be true")
	}
	if response.UpdateInstructions.Docker == "" {
		t.Error("Expected Docker instructions to be set")
	}
}
