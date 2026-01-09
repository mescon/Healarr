package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// =============================================================================
// HealthCheckError tests
// =============================================================================

func TestHealthCheckError_IsRecoverable(t *testing.T) {
	tests := []struct {
		errorType string
		expected  bool
	}{
		{ErrorTypeZeroByte, false},
		{ErrorTypeCorruptHeader, false},
		{ErrorTypeCorruptStream, false},
		{ErrorTypeInvalidFormat, false},
		{ErrorTypeAccessDenied, true},
		{ErrorTypePathNotFound, true},
		{ErrorTypeMountLost, true},
		{ErrorTypeIOError, true},
		{ErrorTypeTimeout, true},
		{ErrorTypeInvalidConfig, true},
	}

	for _, tt := range tests {
		t.Run(tt.errorType, func(t *testing.T) {
			err := &HealthCheckError{Type: tt.errorType, Message: "test"}
			if err.IsRecoverable() != tt.expected {
				t.Errorf("IsRecoverable() = %v, want %v", err.IsRecoverable(), tt.expected)
			}
		})
	}
}

func TestHealthCheckError_IsTrueCorruption(t *testing.T) {
	tests := []struct {
		errorType string
		expected  bool
	}{
		{ErrorTypeZeroByte, true},
		{ErrorTypeCorruptHeader, true},
		{ErrorTypeCorruptStream, true},
		{ErrorTypeInvalidFormat, true},
		{ErrorTypeAccessDenied, false},
		{ErrorTypePathNotFound, false},
		{ErrorTypeMountLost, false},
		{ErrorTypeIOError, false},
		{ErrorTypeTimeout, false},
	}

	for _, tt := range tests {
		t.Run(tt.errorType, func(t *testing.T) {
			err := &HealthCheckError{Type: tt.errorType, Message: "test"}
			if err.IsTrueCorruption() != tt.expected {
				t.Errorf("IsTrueCorruption() = %v, want %v", err.IsTrueCorruption(), tt.expected)
			}
		})
	}
}

// =============================================================================
// CmdHealthChecker constructor tests
// =============================================================================

func TestNewHealthChecker(t *testing.T) {
	hc := NewHealthChecker()

	if hc.FFprobePath != "ffprobe" {
		t.Errorf("Expected FFprobePath='ffprobe', got %q", hc.FFprobePath)
	}
	if hc.FFmpegPath != "ffmpeg" {
		t.Errorf("Expected FFmpegPath='ffmpeg', got %q", hc.FFmpegPath)
	}
	if hc.HandBrakePath != "HandBrakeCLI" {
		t.Errorf("Expected HandBrakePath='HandBrakeCLI', got %q", hc.HandBrakePath)
	}
}

// =============================================================================
// checkZeroByte tests
// =============================================================================

func TestCmdHealthChecker_CheckZeroByte(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("detects zero byte file", func(t *testing.T) {
		tmpDir := t.TempDir()
		emptyFile := filepath.Join(tmpDir, "empty.mkv")
		if err := os.WriteFile(emptyFile, []byte{}, 0644); err != nil {
			t.Fatalf("Failed to create empty file: %v", err)
		}

		healthy, checkErr := hc.CheckWithConfig(emptyFile, DetectionConfig{Method: DetectionZeroByte})
		if healthy {
			t.Error("Expected unhealthy for zero byte file")
		}
		if checkErr == nil {
			t.Error("Expected error for zero byte file")
		}
		if checkErr != nil && checkErr.Type != ErrorTypeZeroByte {
			t.Errorf("Expected ZeroByte error type, got %s", checkErr.Type)
		}
	})

	t.Run("passes non-empty file", func(t *testing.T) {
		tmpDir := t.TempDir()
		nonEmptyFile := filepath.Join(tmpDir, "nonempty.mkv")
		if err := os.WriteFile(nonEmptyFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		healthy, checkErr := hc.CheckWithConfig(nonEmptyFile, DetectionConfig{Method: DetectionZeroByte})
		if !healthy {
			t.Errorf("Expected healthy for non-empty file, got error: %v", checkErr)
		}
	})

	t.Run("detects missing file in empty dir as mount issue", func(t *testing.T) {
		// Empty parent directories are treated as suspicious (possible mount issue)
		tmpDir := t.TempDir()
		missingFile := filepath.Join(tmpDir, "missing.mkv")

		healthy, checkErr := hc.CheckWithConfig(missingFile, DetectionConfig{Method: DetectionZeroByte})
		if healthy {
			t.Error("Expected unhealthy for missing file")
		}
		if checkErr == nil {
			t.Error("Expected error for missing file")
		}
		// Empty parent directory triggers MountLost as a safety measure
		if checkErr != nil && checkErr.Type != ErrorTypeMountLost {
			t.Errorf("Expected MountLost error type for empty parent, got %s", checkErr.Type)
		}
	})

	t.Run("detects missing file in populated dir as path not found", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create a sibling file so parent isn't empty
		siblingFile := filepath.Join(tmpDir, "sibling.txt")
		if err := os.WriteFile(siblingFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create sibling file: %v", err)
		}
		missingFile := filepath.Join(tmpDir, "missing.mkv")

		healthy, checkErr := hc.CheckWithConfig(missingFile, DetectionConfig{Method: DetectionZeroByte})
		if healthy {
			t.Error("Expected unhealthy for missing file")
		}
		if checkErr == nil {
			t.Error("Expected error for missing file")
		}
		if checkErr != nil && checkErr.Type != ErrorTypePathNotFound {
			t.Errorf("Expected PathNotFound error type, got %s", checkErr.Type)
		}
	})
}

// =============================================================================
// checkAccessibility tests
// =============================================================================

func TestCmdHealthChecker_CheckAccessibility(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("passes for accessible file", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.mkv")
		if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		err := hc.checkAccessibility(testFile)
		if err != nil {
			t.Errorf("Expected no error for accessible file, got: %v", err)
		}
	})

	t.Run("detects missing parent directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		missingDir := filepath.Join(tmpDir, "nonexistent", "file.mkv")

		err := hc.checkAccessibility(missingDir)
		if err == nil {
			t.Error("Expected error for missing parent directory")
		}
		if err != nil && err.Type != ErrorTypeMountLost {
			t.Errorf("Expected MountLost error type, got %s", err.Type)
		}
	})

	t.Run("detects missing file in existing parent", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create a sibling file so parent isn't empty
		siblingFile := filepath.Join(tmpDir, "sibling.txt")
		if err := os.WriteFile(siblingFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create sibling file: %v", err)
		}
		missingFile := filepath.Join(tmpDir, "missing.mkv")

		err := hc.checkAccessibility(missingFile)
		if err == nil {
			t.Error("Expected error for missing file")
		}
		if err != nil && err.Type != ErrorTypePathNotFound {
			t.Errorf("Expected PathNotFound error type, got %s", err.Type)
		}
	})

	t.Run("detects empty parent directory as suspicious", func(t *testing.T) {
		tmpDir := t.TempDir()
		emptySubDir := filepath.Join(tmpDir, "emptydir")
		if err := os.Mkdir(emptySubDir, 0755); err != nil {
			t.Fatalf("Failed to create empty subdir: %v", err)
		}
		missingFile := filepath.Join(emptySubDir, "missing.mkv")

		err := hc.checkAccessibility(missingFile)
		if err == nil {
			t.Error("Expected error for file in empty parent")
		}
		if err != nil && err.Type != ErrorTypeMountLost {
			t.Errorf("Expected MountLost error type for empty parent, got %s", err.Type)
		}
	})
}

// =============================================================================
// classifyOSError tests
// =============================================================================

func TestCmdHealthChecker_ClassifyOSError(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("classifies permission error", func(t *testing.T) {
		err := os.ErrPermission
		result := hc.classifyOSError(err, "/test/path", false)
		if result.Type != ErrorTypeAccessDenied {
			t.Errorf("Expected AccessDenied, got %s", result.Type)
		}
	})

	t.Run("classifies not exist error for file", func(t *testing.T) {
		err := os.ErrNotExist
		result := hc.classifyOSError(err, "/test/path", false)
		if result.Type != ErrorTypePathNotFound {
			t.Errorf("Expected PathNotFound, got %s", result.Type)
		}
	})

	t.Run("classifies not exist error for parent as mount issue", func(t *testing.T) {
		err := os.ErrNotExist
		result := hc.classifyOSError(err, "/test/path", true)
		if result.Type != ErrorTypeMountLost {
			t.Errorf("Expected MountLost for parent not exist, got %s", result.Type)
		}
	})
}

// =============================================================================
// classifyDetectorError tests
// =============================================================================

func TestCmdHealthChecker_ClassifyDetectorError(t *testing.T) {
	hc := NewHealthChecker()

	tests := []struct {
		name         string
		errorMsg     string
		expectedType string
	}{
		{"path not found", "No such file or directory: /test/file.mkv", ErrorTypePathNotFound},
		{"does not exist", "File does not exist", ErrorTypePathNotFound},
		{"permission denied", "Permission denied: /test/file.mkv", ErrorTypeAccessDenied},
		{"io error", "Input/output error", ErrorTypeIOError},
		{"connection refused", "Connection refused", ErrorTypeIOError},
		{"timeout", "Operation timed out", ErrorTypeTimeout},
		{"generic error", "Invalid data found when processing input", ErrorTypeCorruptHeader},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &testDetectorError{msg: tt.errorMsg}
			result := hc.classifyDetectorError(err, "/test/path")
			if result.Type != tt.expectedType {
				t.Errorf("Expected %s, got %s", tt.expectedType, result.Type)
			}
		})
	}
}

type testDetectorError struct {
	msg string
}

func (e *testDetectorError) Error() string {
	return e.msg
}

// =============================================================================
// Invalid config tests
// =============================================================================

func TestCmdHealthChecker_InvalidConfig(t *testing.T) {
	hc := NewHealthChecker()

	tmpDir := t.TempDir()
	testFile := filepath.Join(tmpDir, "test.mkv")
	if err := os.WriteFile(testFile, []byte("content"), 0644); err != nil {
		t.Fatalf("Failed to create file: %v", err)
	}

	healthy, checkErr := hc.CheckWithConfig(testFile, DetectionConfig{Method: "invalid_method"})
	if healthy {
		t.Error("Expected unhealthy for invalid config")
	}
	if checkErr == nil {
		t.Error("Expected error for invalid config")
	}
	if checkErr != nil && checkErr.Type != ErrorTypeInvalidConfig {
		t.Errorf("Expected InvalidConfig error type, got %s", checkErr.Type)
	}
}

// =============================================================================
// Integration tests (require ffprobe/ffmpeg installed)
// =============================================================================

func TestCmdHealthChecker_FFprobe_Integration(t *testing.T) {
	// Skip if ffprobe not available
	if _, err := os.Stat("/usr/bin/ffprobe"); os.IsNotExist(err) {
		t.Skip("ffprobe not available, skipping integration test")
	}

	hc := NewHealthChecker()

	t.Run("detects invalid media file", func(t *testing.T) {
		tmpDir := t.TempDir()
		invalidFile := filepath.Join(tmpDir, "invalid.mkv")
		// Create file with garbage content
		if err := os.WriteFile(invalidFile, []byte("this is not a valid media file"), 0644); err != nil {
			t.Fatalf("Failed to create file: %v", err)
		}

		healthy, checkErr := hc.Check(invalidFile, "quick")
		if healthy {
			t.Error("Expected unhealthy for invalid media file")
		}
		if checkErr == nil {
			t.Error("Expected error for invalid media file")
		}
		// FFprobe should detect this as corruption
		if checkErr != nil && !checkErr.IsTrueCorruption() {
			t.Logf("Error type: %s, message: %s", checkErr.Type, checkErr.Message)
		}
	})
}

// =============================================================================
// DetectionConfig mode tests
// =============================================================================

func TestDetectionConfig_Modes(t *testing.T) {
	t.Run("default mode is quick when empty", func(t *testing.T) {
		config := DetectionConfig{Method: DetectionFFprobe}
		if config.Mode != "" {
			t.Errorf("Expected empty mode by default, got %q", config.Mode)
		}
	})

	t.Run("can set mode to quick", func(t *testing.T) {
		config := DetectionConfig{Method: DetectionFFprobe, Mode: "quick"}
		if config.Mode != "quick" {
			t.Errorf("Expected mode='quick', got %q", config.Mode)
		}
	})

	t.Run("can set mode to thorough", func(t *testing.T) {
		config := DetectionConfig{Method: DetectionFFprobe, Mode: "thorough"}
		if config.Mode != "thorough" {
			t.Errorf("Expected mode='thorough', got %q", config.Mode)
		}
	})
}

// =============================================================================
// Detection method constants tests
// =============================================================================

// =============================================================================
// validateMediaPath tests (command injection prevention)
// =============================================================================

func TestValidateMediaPath(t *testing.T) {
	t.Run("accepts valid absolute paths", func(t *testing.T) {
		validPaths := []string{
			"/media/movies/Test Movie (2024)/Test Movie (2024).mkv",
			"/data/tv/Show Name/Season 01/S01E01 - Pilot.mkv",
			"/mnt/nas/Movies/Movie's Title [2023]/movie.mp4",
			"/srv/media/Film #1 - The Beginning!/film.avi",
			// Radarr/Sonarr IMDB naming convention with curly braces - MUST be allowed
			"/media/Movies/The Avengers (2012)/The Avengers (2012) {imdb-tt0848228} [Remux-2160p].mkv",
			"/media/Movies/Film {imdb-tt1234567} {edition-Extended}.mkv",
		}

		for _, path := range validPaths {
			if err := validateMediaPath(path); err != nil {
				t.Errorf("validateMediaPath(%q) = %v, want nil", path, err)
			}
		}
	})

	t.Run("accepts shell metacharacters since exec.Command does not interpret them", func(t *testing.T) {
		// These paths contain shell metacharacters but are safe with exec.Command
		// because arguments are passed directly without shell interpretation.
		// Radarr/Sonarr commonly use {} for IMDB IDs and edition tags.
		safePaths := []string{
			"/media/movies/Movie {imdb-tt0848228}.mkv",
			"/media/movies/test$variable.mkv",
			"/media/movies/test`backtick`.mkv",
			"/media/movies/test;semicolon.mkv",
			"/media/movies/test|pipe.mkv",
			"/media/movies/test&ampersand.mkv",
			"/media/movies/test<angle>brackets.mkv",
			"/media/movies/test\"quotes.mkv",
		}

		for _, path := range safePaths {
			if err := validateMediaPath(path); err != nil {
				t.Errorf("validateMediaPath(%q) = %v, want nil (safe with exec.Command)", path, err)
			}
		}
	})

	t.Run("rejects relative paths", func(t *testing.T) {
		relativePaths := []string{
			"media/movies/test.mkv",
			"./test.mkv",
			"../parent/test.mkv",
		}

		for _, path := range relativePaths {
			err := validateMediaPath(path)
			if err == nil {
				t.Errorf("validateMediaPath(%q) = nil, want error for relative path", path)
			}
			if err != nil && !strings.Contains(err.Error(), "absolute") {
				t.Errorf("validateMediaPath(%q) error should mention 'absolute', got: %v", path, err)
			}
		}
	})

	t.Run("rejects null bytes", func(t *testing.T) {
		path := "/media/movies/test\x00.mkv"
		err := validateMediaPath(path)
		if err == nil {
			t.Error("validateMediaPath with null byte should fail")
		}
		if err != nil && !strings.Contains(err.Error(), "null byte") {
			t.Errorf("Error should mention null byte, got: %v", err)
		}
	})

	t.Run("rejects newlines", func(t *testing.T) {
		paths := []string{
			"/media/movies/test\n.mkv",
			"/media/movies/test\r.mkv",
		}

		for _, path := range paths {
			err := validateMediaPath(path)
			if err == nil {
				t.Errorf("validateMediaPath(%q) = nil, want error for newline", path)
			}
			if err != nil && !strings.Contains(err.Error(), "newline") {
				t.Errorf("Error should mention newline, got: %v", err)
			}
		}
	})
}

func TestCmdHealthChecker_RejectsUnsafePaths(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("rejects relative path", func(t *testing.T) {
		relativePath := "media/movies/test.mkv"

		healthy, checkErr := hc.Check(relativePath, "quick")
		if healthy {
			t.Error("Expected unhealthy for relative path")
		}
		if checkErr == nil {
			t.Fatal("Expected error for relative path")
		}
		if checkErr.Type != ErrorTypeInvalidConfig {
			t.Errorf("Expected InvalidConfig error type, got %s", checkErr.Type)
		}
	})

	t.Run("rejects path with null byte", func(t *testing.T) {
		nullPath := "/media/movies/test\x00.mkv"

		healthy, checkErr := hc.Check(nullPath, "quick")
		if healthy {
			t.Error("Expected unhealthy for path with null byte")
		}
		if checkErr == nil {
			t.Fatal("Expected error for path with null byte")
		}
		if checkErr.Type != ErrorTypeInvalidConfig {
			t.Errorf("Expected InvalidConfig error type, got %s", checkErr.Type)
		}
	})

	t.Run("rejects path with newline", func(t *testing.T) {
		newlinePath := "/media/movies/test\n.mkv"

		healthy, checkErr := hc.Check(newlinePath, "quick")
		if healthy {
			t.Error("Expected unhealthy for path with newline")
		}
		if checkErr == nil {
			t.Fatal("Expected error for path with newline")
		}
		if checkErr.Type != ErrorTypeInvalidConfig {
			t.Errorf("Expected InvalidConfig error type, got %s", checkErr.Type)
		}
	})
}

func TestDetectionMethods(t *testing.T) {
	if DetectionZeroByte != "zero_byte" {
		t.Errorf("DetectionZeroByte = %q, want 'zero_byte'", DetectionZeroByte)
	}
	if DetectionFFprobe != "ffprobe" {
		t.Errorf("DetectionFFprobe = %q, want 'ffprobe'", DetectionFFprobe)
	}
	if DetectionMediaInfo != "mediainfo" {
		t.Errorf("DetectionMediaInfo = %q, want 'mediainfo'", DetectionMediaInfo)
	}
	if DetectionHandBrake != "handbrake" {
		t.Errorf("DetectionHandBrake = %q, want 'handbrake'", DetectionHandBrake)
	}
}

func TestGetCommandPreview(t *testing.T) {
	hc := NewHealthChecker()

	tests := []struct {
		method DetectionMethod
		mode   string
		want   string
	}{
		{DetectionZeroByte, "quick", "stat <file> (checks if file size == 0)"},
		{DetectionFFprobe, "quick", "ffprobe"},
		{DetectionFFprobe, "thorough", "ffmpeg"},
		{DetectionMediaInfo, "quick", "mediainfo"},
		{DetectionHandBrake, "quick", "HandBrakeCLI"},
		{"unknown", "quick", "unknown detection method"},
	}

	for _, tt := range tests {
		t.Run(string(tt.method)+"-"+tt.mode, func(t *testing.T) {
			preview := hc.GetCommandPreview(tt.method, tt.mode, nil)
			if preview == "" {
				t.Error("Expected non-empty preview")
			}
			// Check that the expected pattern is in the result
			if tt.want != "" && !contains(preview, tt.want) {
				t.Errorf("GetCommandPreview(%s, %s) = %q, want to contain %q", tt.method, tt.mode, preview, tt.want)
			}
		})
	}

	// Test with custom args
	customPreview := hc.GetCommandPreview(DetectionFFprobe, "quick", []string{"-extra", "arg"})
	if customPreview == "" {
		t.Error("Expected non-empty preview with custom args")
	}
}

func TestGetTimeoutDescription(t *testing.T) {
	hc := NewHealthChecker()

	tests := []struct {
		method DetectionMethod
		mode   string
		want   string
	}{
		{DetectionZeroByte, "quick", "instant"},
		{DetectionFFprobe, "quick", "30 seconds"},
		{DetectionFFprobe, "thorough", "10 minutes"},
		{DetectionMediaInfo, "quick", "30 seconds"},
		{DetectionMediaInfo, "thorough", "2 minutes"},
		{DetectionHandBrake, "quick", "2 minutes"},
		{DetectionHandBrake, "thorough", "10 minutes"},
		{"unknown", "quick", "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.method)+"-"+tt.mode, func(t *testing.T) {
			desc := hc.GetTimeoutDescription(tt.method, tt.mode)
			if desc == "" {
				t.Error("Expected non-empty description")
			}
			if !contains(desc, tt.want) {
				t.Errorf("GetTimeoutDescription(%s, %s) = %q, want to contain %q", tt.method, tt.mode, desc, tt.want)
			}
		})
	}

	// Test with empty mode (defaults to quick)
	desc := hc.GetTimeoutDescription(DetectionFFprobe, "")
	if !contains(desc, "30 seconds") {
		t.Errorf("Expected default mode to be quick, got %q", desc)
	}
}

// contains checks if s contains substr (case-insensitive)
func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

// =============================================================================
// CheckWithConfig detection method tests (coverage for switch cases)
// =============================================================================

func TestCmdHealthChecker_CheckWithConfig_MediaInfo(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("returns error for missing file", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create a sibling so parent isn't empty
		siblingFile := filepath.Join(tmpDir, "sibling.txt")
		if err := os.WriteFile(siblingFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create sibling file: %v", err)
		}
		missingFile := filepath.Join(tmpDir, "missing.mkv")

		healthy, checkErr := hc.CheckWithConfig(missingFile, DetectionConfig{
			Method: DetectionMediaInfo,
			Mode:   "quick",
		})
		if healthy {
			t.Error("Expected unhealthy for missing file")
		}
		if checkErr == nil {
			t.Error("Expected error for missing file")
		}
		// Should fail at accessibility check
		if checkErr != nil && checkErr.Type != ErrorTypePathNotFound {
			t.Errorf("Expected PathNotFound error type, got %s", checkErr.Type)
		}
	})

	t.Run("returns error for missing file in thorough mode", func(t *testing.T) {
		tmpDir := t.TempDir()
		siblingFile := filepath.Join(tmpDir, "sibling.txt")
		if err := os.WriteFile(siblingFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create sibling file: %v", err)
		}
		missingFile := filepath.Join(tmpDir, "missing.mkv")

		healthy, checkErr := hc.CheckWithConfig(missingFile, DetectionConfig{
			Method: DetectionMediaInfo,
			Mode:   "thorough",
		})
		if healthy {
			t.Error("Expected unhealthy for missing file")
		}
		if checkErr != nil && checkErr.Type != ErrorTypePathNotFound {
			t.Errorf("Expected PathNotFound, got %s", checkErr.Type)
		}
	})
}

func TestCmdHealthChecker_CheckWithConfig_HandBrake(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("returns error for missing file", func(t *testing.T) {
		tmpDir := t.TempDir()
		siblingFile := filepath.Join(tmpDir, "sibling.txt")
		if err := os.WriteFile(siblingFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create sibling file: %v", err)
		}
		missingFile := filepath.Join(tmpDir, "missing.mkv")

		healthy, checkErr := hc.CheckWithConfig(missingFile, DetectionConfig{
			Method: DetectionHandBrake,
			Mode:   "quick",
		})
		if healthy {
			t.Error("Expected unhealthy for missing file")
		}
		if checkErr == nil {
			t.Error("Expected error for missing file")
		}
		if checkErr != nil && checkErr.Type != ErrorTypePathNotFound {
			t.Errorf("Expected PathNotFound, got %s", checkErr.Type)
		}
	})

	t.Run("returns error for missing file in thorough mode", func(t *testing.T) {
		tmpDir := t.TempDir()
		siblingFile := filepath.Join(tmpDir, "sibling.txt")
		if err := os.WriteFile(siblingFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create sibling file: %v", err)
		}
		missingFile := filepath.Join(tmpDir, "missing.mkv")

		healthy, checkErr := hc.CheckWithConfig(missingFile, DetectionConfig{
			Method: DetectionHandBrake,
			Mode:   "thorough",
		})
		if healthy {
			t.Error("Expected unhealthy for missing file")
		}
		if checkErr != nil && checkErr.Type != ErrorTypePathNotFound {
			t.Errorf("Expected PathNotFound, got %s", checkErr.Type)
		}
	})
}

func TestCmdHealthChecker_CheckWithConfig_FFprobe_ThoroughMode(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("returns error for missing file in thorough mode", func(t *testing.T) {
		tmpDir := t.TempDir()
		siblingFile := filepath.Join(tmpDir, "sibling.txt")
		if err := os.WriteFile(siblingFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create sibling file: %v", err)
		}
		missingFile := filepath.Join(tmpDir, "missing.mkv")

		healthy, checkErr := hc.CheckWithConfig(missingFile, DetectionConfig{
			Method: DetectionFFprobe,
			Mode:   "thorough",
		})
		if healthy {
			t.Error("Expected unhealthy for missing file")
		}
		if checkErr != nil && checkErr.Type != ErrorTypePathNotFound {
			t.Errorf("Expected PathNotFound, got %s", checkErr.Type)
		}
	})
}

func TestCmdHealthChecker_CheckWithConfig_EmptyMode(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("empty mode defaults to quick", func(t *testing.T) {
		tmpDir := t.TempDir()
		siblingFile := filepath.Join(tmpDir, "sibling.txt")
		if err := os.WriteFile(siblingFile, []byte("content"), 0644); err != nil {
			t.Fatalf("Failed to create sibling file: %v", err)
		}
		missingFile := filepath.Join(tmpDir, "missing.mkv")

		// Call with empty Mode - should default to quick
		healthy, checkErr := hc.CheckWithConfig(missingFile, DetectionConfig{
			Method: DetectionFFprobe,
			Mode:   "", // Empty - should default to quick
		})
		if healthy {
			t.Error("Expected unhealthy for missing file")
		}
		// Should fail at accessibility check, not mode parsing
		if checkErr != nil && checkErr.Type != ErrorTypePathNotFound {
			t.Errorf("Expected PathNotFound, got %s", checkErr.Type)
		}
	})
}

// =============================================================================
// classifyOSError tests - network error classification
// =============================================================================

func TestCmdHealthChecker_ClassifyOSError_NetworkErrors(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("classifies transport endpoint error as mount lost", func(t *testing.T) {
		err := &testDetectorError{msg: "transport endpoint is not connected"}
		result := hc.classifyOSError(err, "/test/path", false)
		if result.Type != ErrorTypeMountLost {
			t.Errorf("Expected MountLost for transport endpoint error, got %s", result.Type)
		}
	})

	t.Run("classifies stale handle error as mount lost", func(t *testing.T) {
		err := &testDetectorError{msg: "stale file handle"}
		result := hc.classifyOSError(err, "/test/path", false)
		if result.Type != ErrorTypeMountLost {
			t.Errorf("Expected MountLost for stale handle error, got %s", result.Type)
		}
	})

	t.Run("classifies connection refused as mount lost", func(t *testing.T) {
		err := &testDetectorError{msg: "connection refused"}
		result := hc.classifyOSError(err, "/test/path", false)
		if result.Type != ErrorTypeMountLost {
			t.Errorf("Expected MountLost for connection refused, got %s", result.Type)
		}
	})

	t.Run("classifies no route to host as mount lost", func(t *testing.T) {
		err := &testDetectorError{msg: "no route to host"}
		result := hc.classifyOSError(err, "/test/path", false)
		if result.Type != ErrorTypeMountLost {
			t.Errorf("Expected MountLost for no route to host, got %s", result.Type)
		}
	})

	t.Run("classifies network unreachable as mount lost", func(t *testing.T) {
		err := &testDetectorError{msg: "network is unreachable"}
		result := hc.classifyOSError(err, "/test/path", false)
		if result.Type != ErrorTypeMountLost {
			t.Errorf("Expected MountLost for network unreachable, got %s", result.Type)
		}
	})

	t.Run("classifies mount keyword as mount lost", func(t *testing.T) {
		err := &testDetectorError{msg: "mount: /mnt/nas not mounted"}
		result := hc.classifyOSError(err, "/test/path", false)
		if result.Type != ErrorTypeMountLost {
			t.Errorf("Expected MountLost for mount error, got %s", result.Type)
		}
	})

	t.Run("classifies unknown error as IO error", func(t *testing.T) {
		err := &testDetectorError{msg: "some random filesystem error"}
		result := hc.classifyOSError(err, "/test/path", false)
		if result.Type != ErrorTypeIOError {
			t.Errorf("Expected IOError for unknown error, got %s", result.Type)
		}
	})

	t.Run("includes context in error message for parent", func(t *testing.T) {
		err := &testDetectorError{msg: "some error"}
		result := hc.classifyOSError(err, "/test/path", true)
		if !strings.Contains(result.Message, "parent directory") {
			t.Errorf("Expected error message to mention 'parent directory', got: %s", result.Message)
		}
	})
}

// =============================================================================
// classifyDetectorError tests - additional cases
// =============================================================================

func TestCmdHealthChecker_ClassifyDetectorError_AdditionalCases(t *testing.T) {
	hc := NewHealthChecker()

	tests := []struct {
		name         string
		errorMsg     string
		expectedType string
	}{
		{"network unreachable", "Network is unreachable", ErrorTypeIOError},
		{"transport endpoint", "transport endpoint is not connected", ErrorTypeIOError},
		{"file not found", "file not found", ErrorTypePathNotFound},
		{"access denied", "access denied", ErrorTypeAccessDenied},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := &testDetectorError{msg: tt.errorMsg}
			result := hc.classifyDetectorError(err, "/test/path")
			if result.Type != tt.expectedType {
				t.Errorf("Expected %s, got %s", tt.expectedType, result.Type)
			}
		})
	}
}

// =============================================================================
// GetCommandPreview tests - with custom args
// =============================================================================

func TestGetCommandPreview_WithCustomArgs(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("FFprobe quick with custom args", func(t *testing.T) {
		preview := hc.GetCommandPreview(DetectionFFprobe, "quick", []string{"-extra", "arg"})
		if !strings.Contains(preview, "-extra") {
			t.Errorf("Expected custom args in preview, got: %s", preview)
		}
		if !strings.Contains(preview, "ffprobe") {
			t.Errorf("Expected ffprobe in preview, got: %s", preview)
		}
	})

	t.Run("FFprobe thorough with custom args", func(t *testing.T) {
		preview := hc.GetCommandPreview(DetectionFFprobe, "thorough", []string{"-threads", "4"})
		if !strings.Contains(preview, "-threads") {
			t.Errorf("Expected custom args in preview, got: %s", preview)
		}
		if !strings.Contains(preview, "ffmpeg") {
			t.Errorf("Expected ffmpeg in thorough mode, got: %s", preview)
		}
	})

	t.Run("MediaInfo quick with custom args", func(t *testing.T) {
		preview := hc.GetCommandPreview(DetectionMediaInfo, "quick", []string{"--Language=en"})
		if !strings.Contains(preview, "--Language=en") {
			t.Errorf("Expected custom args in preview, got: %s", preview)
		}
	})

	t.Run("MediaInfo thorough with custom args", func(t *testing.T) {
		preview := hc.GetCommandPreview(DetectionMediaInfo, "thorough", []string{"--Inform=Video"})
		if !strings.Contains(preview, "--Inform=Video") {
			t.Errorf("Expected custom args in preview, got: %s", preview)
		}
		if !strings.Contains(preview, "--Full") {
			t.Errorf("Expected --Full in thorough mode, got: %s", preview)
		}
	})

	t.Run("HandBrake quick with custom args", func(t *testing.T) {
		preview := hc.GetCommandPreview(DetectionHandBrake, "quick", []string{"--verbose"})
		if !strings.Contains(preview, "--verbose") {
			t.Errorf("Expected custom args in preview, got: %s", preview)
		}
	})

	t.Run("HandBrake thorough with custom args", func(t *testing.T) {
		preview := hc.GetCommandPreview(DetectionHandBrake, "thorough", []string{"--verbose"})
		if !strings.Contains(preview, "--previews") {
			t.Errorf("Expected --previews in thorough mode, got: %s", preview)
		}
	})
}

// =============================================================================
// checkAccessibility tests - parent not a directory
// =============================================================================

func TestCmdHealthChecker_CheckAccessibility_ParentIsFile(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("detects parent that is a file not directory", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Create a file that will act as "parent"
		fakeParent := filepath.Join(tmpDir, "fakedir")
		if err := os.WriteFile(fakeParent, []byte("I'm a file"), 0644); err != nil {
			t.Fatalf("Failed to create fake parent file: %v", err)
		}
		// Try to access "file" inside the "parent" that's actually a file
		fakeChild := filepath.Join(fakeParent, "child.mkv")

		err := hc.checkAccessibility(fakeChild)
		if err == nil {
			t.Error("Expected error for parent that is a file")
			return
		}
		// Should fail trying to stat parent or child, result in some error
		// The exact error type depends on OS behavior
		t.Logf("Error type: %s, message: %s", err.Type, err.Message)
	})
}

// =============================================================================
// Helper function tests
// =============================================================================

func TestBuildMediaInfoArgs(t *testing.T) {
	tests := []struct {
		name       string
		mode       string
		customArgs []string
		path       string
		wantArgs   []string
		wantMinTO  time.Duration
	}{
		{
			"quick mode no custom args",
			ModeQuick,
			nil,
			"/path/to/file.mkv",
			[]string{"--Output=JSON", "/path/to/file.mkv"},
			30 * time.Second,
		},
		{
			"thorough mode no custom args",
			ModeThorough,
			nil,
			"/path/to/file.mkv",
			[]string{"--Output=JSON", "--Full", "/path/to/file.mkv"},
			2 * time.Minute,
		},
		{
			"quick mode with custom args",
			ModeQuick,
			[]string{"--extra", "--flags"},
			"/path/to/file.mkv",
			[]string{"--Output=JSON", "--extra", "--flags", "/path/to/file.mkv"},
			30 * time.Second,
		},
		{
			"thorough mode with custom args",
			ModeThorough,
			[]string{"--extra"},
			"/path/to/file.mkv",
			[]string{"--Output=JSON", "--Full", "--extra", "/path/to/file.mkv"},
			2 * time.Minute,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, timeout := buildMediaInfoArgs(tt.mode, tt.customArgs, tt.path)
			if len(args) != len(tt.wantArgs) {
				t.Errorf("buildMediaInfoArgs() args len = %d, want %d", len(args), len(tt.wantArgs))
				return
			}
			for i, arg := range args {
				if arg != tt.wantArgs[i] {
					t.Errorf("buildMediaInfoArgs() args[%d] = %v, want %v", i, arg, tt.wantArgs[i])
				}
			}
			if timeout < tt.wantMinTO {
				t.Errorf("buildMediaInfoArgs() timeout = %v, want >= %v", timeout, tt.wantMinTO)
			}
		})
	}
}

func TestHasValidMediaTrack(t *testing.T) {
	tests := []struct {
		name   string
		tracks []interface{}
		want   bool
	}{
		{
			"empty tracks",
			[]interface{}{},
			false,
		},
		{
			"only general track",
			[]interface{}{
				map[string]interface{}{"@type": "General"},
			},
			false,
		},
		{
			"video track present",
			[]interface{}{
				map[string]interface{}{"@type": "General"},
				map[string]interface{}{"@type": "Video"},
			},
			true,
		},
		{
			"audio track present",
			[]interface{}{
				map[string]interface{}{"@type": "General"},
				map[string]interface{}{"@type": "Audio"},
			},
			true,
		},
		{
			"invalid track type",
			[]interface{}{
				"not a map",
			},
			false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := hasValidMediaTrack(tt.tracks); got != tt.want {
				t.Errorf("hasValidMediaTrack() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateMediaInfoOutput(t *testing.T) {
	tests := []struct {
		name    string
		data    []byte
		wantErr bool
	}{
		{
			"valid media with video",
			[]byte(`{"media":{"track":[{"@type":"General"},{"@type":"Video"}]}}`),
			false,
		},
		{
			"valid media with audio only",
			[]byte(`{"media":{"track":[{"@type":"General"},{"@type":"Audio"}]}}`),
			false,
		},
		{
			"invalid JSON",
			[]byte(`{invalid`),
			true,
		},
		{
			"no media field",
			[]byte(`{"other":"field"}`),
			true,
		},
		{
			"no tracks",
			[]byte(`{"media":{}}`),
			true,
		},
		{
			"empty tracks array",
			[]byte(`{"media":{"track":[]}}`),
			true,
		},
		{
			"only general track",
			[]byte(`{"media":{"track":[{"@type":"General"}]}}`),
			true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMediaInfoOutput(tt.data)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateMediaInfoOutput() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestNewHealthCheckerWithPaths(t *testing.T) {
	hc := NewHealthCheckerWithPaths(
		"/custom/ffprobe",
		"/custom/ffmpeg",
		"/custom/mediainfo",
		"/custom/handbrake",
	)

	if hc.FFprobePath != "/custom/ffprobe" {
		t.Errorf("FFprobePath = %q, want /custom/ffprobe", hc.FFprobePath)
	}
	if hc.FFmpegPath != "/custom/ffmpeg" {
		t.Errorf("FFmpegPath = %q, want /custom/ffmpeg", hc.FFmpegPath)
	}
	if hc.MediaInfoPath != "/custom/mediainfo" {
		t.Errorf("MediaInfoPath = %q, want /custom/mediainfo", hc.MediaInfoPath)
	}
	if hc.HandBrakePath != "/custom/handbrake" {
		t.Errorf("HandBrakePath = %q, want /custom/handbrake", hc.HandBrakePath)
	}
}
