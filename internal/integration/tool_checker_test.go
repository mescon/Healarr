package integration

import (
	"os"
	"path/filepath"
	"testing"
)

// =============================================================================
// Constructor tests
// =============================================================================

func TestNewToolChecker(t *testing.T) {
	tc := NewToolChecker()

	if tc == nil {
		t.Fatal("Expected non-nil ToolChecker")
	}
	if tc.tools == nil {
		t.Error("Expected initialized tools map")
	}
	if tc.ffprobePath != "ffprobe" {
		t.Errorf("Expected default ffprobePath='ffprobe', got %q", tc.ffprobePath)
	}
	if tc.ffmpegPath != "ffmpeg" {
		t.Errorf("Expected default ffmpegPath='ffmpeg', got %q", tc.ffmpegPath)
	}
	if tc.mediaInfoPath != "mediainfo" {
		t.Errorf("Expected default mediaInfoPath='mediainfo', got %q", tc.mediaInfoPath)
	}
	if tc.handBrakePath != "HandBrakeCLI" {
		t.Errorf("Expected default handBrakePath='HandBrakeCLI', got %q", tc.handBrakePath)
	}
}

func TestNewToolCheckerWithPaths(t *testing.T) {
	tc := NewToolCheckerWithPaths("/custom/ffprobe", "/custom/ffmpeg", "/custom/mediainfo", "/custom/handbrake")

	if tc == nil {
		t.Fatal("Expected non-nil ToolChecker")
	}
	if tc.ffprobePath != "/custom/ffprobe" {
		t.Errorf("Expected ffprobePath='/custom/ffprobe', got %q", tc.ffprobePath)
	}
	if tc.ffmpegPath != "/custom/ffmpeg" {
		t.Errorf("Expected ffmpegPath='/custom/ffmpeg', got %q", tc.ffmpegPath)
	}
	if tc.mediaInfoPath != "/custom/mediainfo" {
		t.Errorf("Expected mediaInfoPath='/custom/mediainfo', got %q", tc.mediaInfoPath)
	}
	if tc.handBrakePath != "/custom/handbrake" {
		t.Errorf("Expected handBrakePath='/custom/handbrake', got %q", tc.handBrakePath)
	}
}

// =============================================================================
// resolveBinaryPath tests
// =============================================================================

func TestResolveBinaryPath(t *testing.T) {
	t.Run("resolves absolute path that exists", func(t *testing.T) {
		// Create a temporary executable
		tmpDir := t.TempDir()
		fakeBin := filepath.Join(tmpDir, "fake_binary")
		if err := os.WriteFile(fakeBin, []byte("#!/bin/bash\necho test"), 0755); err != nil {
			t.Fatalf("Failed to create fake binary: %v", err)
		}

		resolved, err := resolveBinaryPath(fakeBin)
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
		if resolved != fakeBin {
			t.Errorf("Expected resolved=%q, got %q", fakeBin, resolved)
		}
	})

	t.Run("returns error for absolute path that does not exist", func(t *testing.T) {
		_, err := resolveBinaryPath("/nonexistent/path/to/binary")
		if err == nil {
			t.Error("Expected error for non-existent absolute path")
		}
	})

	t.Run("uses PATH lookup for bare name", func(t *testing.T) {
		// "ls" should be available on any Unix system
		resolved, err := resolveBinaryPath("ls")
		if err != nil {
			t.Skipf("ls not in PATH, skipping: %v", err)
		}
		if resolved == "" {
			t.Error("Expected non-empty resolved path for 'ls'")
		}
		if !filepath.IsAbs(resolved) {
			t.Errorf("Expected absolute path, got %q", resolved)
		}
	})

	t.Run("returns error for bare name not in PATH", func(t *testing.T) {
		_, err := resolveBinaryPath("definitely_not_a_real_binary_12345")
		if err == nil {
			t.Error("Expected error for binary not in PATH")
		}
	})
}

// =============================================================================
// CheckAllTools tests
// =============================================================================

func TestToolChecker_CheckAllTools(t *testing.T) {
	tc := NewToolChecker()
	tools := tc.CheckAllTools()

	if tools == nil {
		t.Fatal("Expected non-nil tools map")
	}

	// Should have entries for all 4 tools
	expectedTools := []string{"ffprobe", "ffmpeg", "mediainfo", "handbrake"}
	for _, name := range expectedTools {
		if _, exists := tools[name]; !exists {
			t.Errorf("Expected tool %q in results", name)
		}
	}

	// Verify tool status fields are populated
	for name, status := range tools {
		if status == nil {
			t.Errorf("Tool %q has nil status", name)
			continue
		}
		if status.Name == "" {
			t.Errorf("Tool %q has empty Name field", name)
		}
		if status.Description == "" {
			t.Errorf("Tool %q has empty Description field", name)
		}
	}

	// ffprobe and ffmpeg should be marked as required
	if tools["ffprobe"] != nil && !tools["ffprobe"].Required {
		t.Error("Expected ffprobe to be marked as required")
	}
	if tools["ffmpeg"] != nil && !tools["ffmpeg"].Required {
		t.Error("Expected ffmpeg to be marked as required")
	}

	// mediainfo and handbrake should NOT be required
	if tools["mediainfo"] != nil && tools["mediainfo"].Required {
		t.Error("Expected mediainfo to NOT be required")
	}
	if tools["handbrake"] != nil && tools["handbrake"].Required {
		t.Error("Expected handbrake to NOT be required")
	}
}

// =============================================================================
// GetToolStatus tests
// =============================================================================

func TestToolChecker_GetToolStatus(t *testing.T) {
	tc := NewToolChecker()

	t.Run("returns empty map before CheckAllTools", func(t *testing.T) {
		status := tc.GetToolStatus()
		if len(status) != 0 {
			t.Errorf("Expected empty status before check, got %d entries", len(status))
		}
	})

	t.Run("returns copy of status after CheckAllTools", func(t *testing.T) {
		tc.CheckAllTools()
		status := tc.GetToolStatus()

		if len(status) != 4 {
			t.Errorf("Expected 4 tools, got %d", len(status))
		}

		// Verify it's a copy by modifying it
		for _, s := range status {
			s.Name = "modified"
		}

		// Original should be unchanged
		status2 := tc.GetToolStatus()
		for name, s := range status2 {
			if s.Name == "modified" {
				t.Errorf("GetToolStatus returned reference instead of copy for %s", name)
			}
		}
	})
}

// =============================================================================
// IsToolAvailable tests
// =============================================================================

func TestToolChecker_IsToolAvailable(t *testing.T) {
	tc := NewToolChecker()

	t.Run("returns false before CheckAllTools", func(t *testing.T) {
		if tc.IsToolAvailable("ffprobe") {
			t.Error("Expected false before checking tools")
		}
	})

	t.Run("returns false for unknown tool", func(t *testing.T) {
		tc.CheckAllTools()
		if tc.IsToolAvailable("unknown_tool") {
			t.Error("Expected false for unknown tool")
		}
	})

	t.Run("returns correct availability after check", func(t *testing.T) {
		tc.CheckAllTools()
		status := tc.GetToolStatus()

		for name, s := range status {
			available := tc.IsToolAvailable(name)
			if available != s.Available {
				t.Errorf("IsToolAvailable(%q) = %v, but status.Available = %v", name, available, s.Available)
			}
		}
	})
}

// =============================================================================
// HasRequiredTools tests
// =============================================================================

func TestToolChecker_HasRequiredTools(t *testing.T) {
	t.Run("returns true when no tools checked", func(t *testing.T) {
		tc := NewToolChecker()
		// Empty tools map means no required tools to check
		if !tc.HasRequiredTools() {
			t.Error("Expected true when no tools are in map")
		}
	})

	t.Run("returns based on required tool availability", func(t *testing.T) {
		tc := NewToolChecker()
		tc.CheckAllTools()

		hasRequired := tc.HasRequiredTools()
		status := tc.GetToolStatus()

		// Manually verify
		allRequiredAvailable := true
		for _, s := range status {
			if s.Required && !s.Available {
				allRequiredAvailable = false
				break
			}
		}

		if hasRequired != allRequiredAvailable {
			t.Errorf("HasRequiredTools() = %v, expected %v", hasRequired, allRequiredAvailable)
		}
	})
}

// =============================================================================
// GetMissingRequiredTools tests
// =============================================================================

func TestToolChecker_GetMissingRequiredTools(t *testing.T) {
	t.Run("returns empty list when no tools checked", func(t *testing.T) {
		tc := NewToolChecker()
		missing := tc.GetMissingRequiredTools()
		if len(missing) != 0 {
			t.Errorf("Expected empty list, got %v", missing)
		}
	})

	t.Run("returns list of missing required tools", func(t *testing.T) {
		tc := NewToolChecker()
		tc.CheckAllTools()

		missing := tc.GetMissingRequiredTools()
		status := tc.GetToolStatus()

		// Verify each missing tool is actually required and unavailable
		for _, name := range missing {
			s, ok := status[name]
			if !ok {
				t.Errorf("Missing tool %q not in status", name)
				continue
			}
			if !s.Required {
				t.Errorf("Tool %q in missing list but not required", name)
			}
			if s.Available {
				t.Errorf("Tool %q in missing list but is available", name)
			}
		}

		// Verify no required unavailable tools are missing from the list
		for name, s := range status {
			if s.Required && !s.Available {
				found := false
				for _, m := range missing {
					if m == name {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Required unavailable tool %q not in missing list", name)
				}
			}
		}
	})
}

// =============================================================================
// RefreshTools tests
// =============================================================================

func TestToolChecker_RefreshTools(t *testing.T) {
	tc := NewToolChecker()

	// First check
	tools1 := tc.CheckAllTools()

	// Refresh
	tools2 := tc.RefreshTools()

	// Both should return the same structure
	if len(tools1) != len(tools2) {
		t.Errorf("RefreshTools returned different number of tools: %d vs %d", len(tools1), len(tools2))
	}

	for name := range tools1 {
		if _, exists := tools2[name]; !exists {
			t.Errorf("Tool %q missing after refresh", name)
		}
	}
}

// =============================================================================
// ToolStatus struct tests
// =============================================================================

func TestToolStatus_Fields(t *testing.T) {
	tc := NewToolChecker()
	tc.CheckAllTools()
	status := tc.GetToolStatus()

	for name, s := range status {
		t.Run(name, func(t *testing.T) {
			// Name should match key
			if s.Name != name {
				t.Errorf("Status.Name=%q doesn't match key=%q", s.Name, name)
			}

			// Description should be non-empty
			if s.Description == "" {
				t.Error("Expected non-empty Description")
			}

			// If available, Path should be non-empty
			if s.Available && s.Path == "" {
				t.Error("Available tool should have non-empty Path")
			}

			// If not available, Path should be empty
			if !s.Available && s.Path != "" {
				t.Errorf("Unavailable tool has Path=%q", s.Path)
			}
		})
	}
}

// =============================================================================
// Concurrent access tests
// =============================================================================

func TestToolChecker_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	tc := NewToolChecker()
	tc.CheckAllTools()

	// Run concurrent reads
	done := make(chan bool, 10)
	for i := 0; i < 10; i++ {
		go func() {
			_ = tc.GetToolStatus()
			_ = tc.IsToolAvailable("ffprobe")
			_ = tc.HasRequiredTools()
			_ = tc.GetMissingRequiredTools()
			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < 10; i++ {
		<-done
	}
}

// =============================================================================
// Integration tests (with actual binaries if available)
// =============================================================================

func TestToolChecker_Integration_FFprobe(t *testing.T) {
	// Skip if ffprobe not available
	if _, err := resolveBinaryPath("ffprobe"); err != nil {
		t.Skip("ffprobe not available, skipping integration test")
	}

	tc := NewToolChecker()
	tc.CheckAllTools()
	status := tc.GetToolStatus()

	ffprobeStatus := status["ffprobe"]
	if ffprobeStatus == nil {
		t.Fatal("Expected ffprobe in status")
	}

	if !ffprobeStatus.Available {
		t.Error("Expected ffprobe to be available")
	}

	if ffprobeStatus.Path == "" {
		t.Error("Expected non-empty path for ffprobe")
	}

	if ffprobeStatus.Version == "" {
		t.Log("Warning: ffprobe available but version not detected")
	} else {
		t.Logf("Detected ffprobe version: %s", ffprobeStatus.Version)
	}
}

func TestToolChecker_Integration_FFmpeg(t *testing.T) {
	if _, err := resolveBinaryPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not available, skipping integration test")
	}

	tc := NewToolChecker()
	tc.CheckAllTools()
	status := tc.GetToolStatus()

	ffmpegStatus := status["ffmpeg"]
	if ffmpegStatus == nil {
		t.Fatal("Expected ffmpeg in status")
	}

	if !ffmpegStatus.Available {
		t.Error("Expected ffmpeg to be available")
	}

	if ffmpegStatus.Version != "" {
		t.Logf("Detected ffmpeg version: %s", ffmpegStatus.Version)
	}
}

func TestToolChecker_WithCustomAbsolutePaths(t *testing.T) {
	// Create temp directory with fake executables
	tmpDir := t.TempDir()

	// Create fake ffprobe that outputs version info
	fakeFfprobe := filepath.Join(tmpDir, "ffprobe")
	ffprobeScript := "#!/bin/bash\necho 'ffprobe version 6.0-test Copyright (c) 2007-2023'"
	if err := os.WriteFile(fakeFfprobe, []byte(ffprobeScript), 0755); err != nil {
		t.Fatalf("Failed to create fake ffprobe: %v", err)
	}

	// Create fake ffmpeg
	fakeFfmpeg := filepath.Join(tmpDir, "ffmpeg")
	ffmpegScript := "#!/bin/bash\necho 'ffmpeg version 6.0-test Copyright (c) 2007-2023'"
	if err := os.WriteFile(fakeFfmpeg, []byte(ffmpegScript), 0755); err != nil {
		t.Fatalf("Failed to create fake ffmpeg: %v", err)
	}

	tc := NewToolCheckerWithPaths(fakeFfprobe, fakeFfmpeg, "nonexistent_mediainfo", "nonexistent_handbrake")
	tools := tc.CheckAllTools()

	// ffprobe and ffmpeg should be available (using our fake scripts)
	if !tools["ffprobe"].Available {
		t.Error("Expected ffprobe to be available with custom path")
	}
	if tools["ffprobe"].Path != fakeFfprobe {
		t.Errorf("Expected ffprobe path=%q, got %q", fakeFfprobe, tools["ffprobe"].Path)
	}

	if !tools["ffmpeg"].Available {
		t.Error("Expected ffmpeg to be available with custom path")
	}

	// mediainfo and handbrake should NOT be available
	if tools["mediainfo"].Available {
		t.Error("Expected mediainfo to NOT be available")
	}
	if tools["handbrake"].Available {
		t.Error("Expected handbrake to NOT be available")
	}
}
