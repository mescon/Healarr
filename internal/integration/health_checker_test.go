package integration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
		}

		for _, path := range validPaths {
			if err := validateMediaPath(path); err != nil {
				t.Errorf("validateMediaPath(%q) = %v, want nil", path, err)
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

	t.Run("rejects command injection attempts", func(t *testing.T) {
		dangerousPaths := []string{
			"/media/movies/$(rm -rf /)/test.mkv",
			"/media/movies/`whoami`/test.mkv",
			"/media/movies/test;ls/test.mkv",
			"/media/movies/test|cat /etc/passwd/test.mkv",
			"/media/movies/test&& echo pwned/test.mkv",
			"/media/movies/test > /tmp/out/test.mkv",
			"/media/movies/test < /etc/passwd/test.mkv",
			"/media/movies/test\"/test.mkv",
			"/media/movies/${PATH}/test.mkv",
			"/media/movies/test{a,b}/test.mkv",
		}

		for _, path := range dangerousPaths {
			err := validateMediaPath(path)
			if err == nil {
				t.Errorf("validateMediaPath(%q) = nil, want error for dangerous path", path)
			}
		}
	})

	t.Run("rejects null bytes", func(t *testing.T) {
		path := "/media/movies/test\x00.mkv"
		err := validateMediaPath(path)
		if err == nil {
			t.Error("validateMediaPath with null byte should fail")
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
		}
	})
}

func TestCmdHealthChecker_RejectsUnsafePaths(t *testing.T) {
	hc := NewHealthChecker()

	t.Run("rejects command injection in path", func(t *testing.T) {
		dangerousPath := "/media/movies/$(rm -rf /)/test.mkv"

		healthy, checkErr := hc.Check(dangerousPath, "quick")
		if healthy {
			t.Error("Expected unhealthy for dangerous path")
		}
		if checkErr == nil {
			t.Fatal("Expected error for dangerous path")
		}
		if checkErr.Type != ErrorTypeInvalidConfig {
			t.Errorf("Expected InvalidConfig error type, got %s", checkErr.Type)
		}
		if !strings.Contains(checkErr.Message, "invalid media path") {
			t.Errorf("Error message should mention invalid path, got: %s", checkErr.Message)
		}
	})

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
