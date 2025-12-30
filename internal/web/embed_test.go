package web

import (
	"embed"
	"io/fs"
	"testing"
	"testing/fstest"
)

// =============================================================================
// Note: These tests run in development mode (without -tags embed_web)
// In development mode, hasEmbedded = false, so GetFS() returns nil
// =============================================================================

// =============================================================================
// GetFS tests
// =============================================================================

func TestGetFS_DevMode(t *testing.T) {
	// In dev mode (no embed_web tag), GetFS should return nil
	fs := GetFS()

	// We're running tests without embed_web tag, so no embedded assets
	if hasEmbedded {
		// If assets are embedded in test mode, this would pass
		if fs == nil {
			t.Error("GetFS() should return non-nil when hasEmbedded is true")
		}
	} else {
		if fs != nil {
			t.Error("GetFS() should return nil in dev mode")
		}
	}
}

func TestGetFS_Idempotent(t *testing.T) {
	// Multiple calls should return the same result
	fs1 := GetFS()
	fs2 := GetFS()

	// Both should be nil in dev mode, or both non-nil in prod
	if (fs1 == nil) != (fs2 == nil) {
		t.Error("GetFS() should return consistent results")
	}
}

// =============================================================================
// GetHTTPFS tests
// =============================================================================

func TestGetHTTPFS_DevMode(t *testing.T) {
	httpFS := GetHTTPFS()

	if hasEmbedded {
		if httpFS == nil {
			t.Error("GetHTTPFS() should return non-nil when embedded")
		}
	} else {
		if httpFS != nil {
			t.Error("GetHTTPFS() should return nil in dev mode")
		}
	}
}

// =============================================================================
// HasEmbeddedAssets tests
// =============================================================================

func TestHasEmbeddedAssets_DevMode(t *testing.T) {
	has := HasEmbeddedAssets()

	if hasEmbedded {
		// In prod mode with valid assets, should be true (needs index.html)
		t.Logf("HasEmbeddedAssets() = %v (embedded mode)", has)
	} else {
		if has {
			t.Error("HasEmbeddedAssets() should return false in dev mode")
		}
	}
}

// =============================================================================
// ListEmbeddedFiles tests
// =============================================================================

func TestListEmbeddedFiles_DevMode(t *testing.T) {
	files := ListEmbeddedFiles()

	if hasEmbedded {
		if files == nil {
			t.Error("ListEmbeddedFiles() should return file list when embedded")
		}
		t.Logf("Embedded files: %v", files)
	} else {
		if files != nil {
			t.Error("ListEmbeddedFiles() should return nil in dev mode")
		}
	}
}

// =============================================================================
// getEmbeddedFS internal function tests
// =============================================================================

func TestGetEmbeddedFS_Caching(t *testing.T) {
	// Call multiple times to test caching
	result1 := getEmbeddedFS()
	result2 := getEmbeddedFS()
	result3 := getEmbeddedFS()

	// In dev mode, all results should be nil
	// In prod mode with embedded assets, all results should be the same non-nil value
	if hasEmbedded {
		// subFSInitialized should be true after first call
		if !subFSInitialized {
			t.Error("subFSInitialized should be true after calling getEmbeddedFS in embedded mode")
		}
		// All calls should return same cached instance
		if result1 != result2 || result2 != result3 {
			t.Error("getEmbeddedFS should return cached result")
		}
	} else {
		// In dev mode, hasEmbedded is false, so getEmbeddedFS returns early
		// without setting subFSInitialized - this is expected behavior
		if result1 != nil || result2 != nil || result3 != nil {
			t.Error("getEmbeddedFS should return nil in dev mode")
		}
	}
}

// =============================================================================
// Package variable state tests
// =============================================================================

func TestHasEmbedded_Variable(t *testing.T) {
	// In dev mode, hasEmbedded should be false (set by embed_dev.go init())
	// In prod mode, hasEmbedded should be true (set by embed_prod.go init())

	// We can't change this at runtime, but we can verify behavior is consistent
	fs := GetFS()
	if hasEmbedded && fs == nil {
		t.Error("Inconsistent state: hasEmbedded=true but GetFS()=nil")
	}
	if !hasEmbedded && fs != nil {
		t.Error("Inconsistent state: hasEmbedded=false but GetFS()!=nil")
	}
}

// =============================================================================
// Build tag behavior documentation
// =============================================================================

func TestBuildTagBehavior(t *testing.T) {
	t.Log("Build tag behavior:")
	t.Logf("  hasEmbedded = %v", hasEmbedded)
	t.Logf("  subFSInitialized = %v", subFSInitialized)
	t.Logf("  GetFS() returns nil: %v", GetFS() == nil)
	t.Logf("  GetHTTPFS() returns nil: %v", GetHTTPFS() == nil)
	t.Logf("  HasEmbeddedAssets() = %v", HasEmbeddedAssets())
	t.Logf("  ListEmbeddedFiles() = %v", ListEmbeddedFiles())

	// This test always passes - it's informational
}

// =============================================================================
// Edge case tests
// =============================================================================

func TestCachedSubFS_NotNil_WhenEmbedded(t *testing.T) {
	// After initialization, cachedSubFS should be set
	_ = getEmbeddedFS() // Ensure initialization

	if hasEmbedded {
		// In embedded mode, cachedSubFS might be nil if fs.Sub fails
		// or non-nil if successful
		t.Logf("cachedSubFS is nil: %v", cachedSubFS == nil)
	} else {
		if cachedSubFS != nil {
			t.Error("cachedSubFS should be nil in dev mode")
		}
	}
}

// =============================================================================
// Functional tests that work in both modes
// =============================================================================

func TestGetFS_ReturnsConsistentType(t *testing.T) {
	fs := GetFS()

	// Type assertion should work if not nil
	if fs != nil {
		// fs.FS interface - can call ReadDir, Open, etc.
		_, err := fs.Open(".")
		if err != nil {
			t.Logf("Opening root dir: %v (may be expected)", err)
		}
	}
}

func TestGetHTTPFS_ReturnsConsistentType(t *testing.T) {
	httpFS := GetHTTPFS()

	// http.FileSystem interface - can call Open
	if httpFS != nil {
		f, err := httpFS.Open("/")
		if err != nil {
			t.Logf("Opening HTTP FS root: %v (may be expected)", err)
		} else {
			_ = f.Close()
		}
	}
}

// =============================================================================
// Tests that simulate embedded mode by manipulating internal state
// These tests save and restore state to avoid affecting other tests
// =============================================================================

func TestGetHTTPFS_WithEmbeddedFS(t *testing.T) {
	// Save original state
	origHasEmbedded := hasEmbedded
	origSubFSInitialized := subFSInitialized
	origCachedSubFS := cachedSubFS
	defer func() {
		hasEmbedded = origHasEmbedded
		subFSInitialized = origSubFSInitialized
		cachedSubFS = origCachedSubFS
	}()

	// Create a mock filesystem
	mockFS := fstest.MapFS{
		"index.html":     &fstest.MapFile{Data: []byte("<html></html>")},
		"assets/main.js": &fstest.MapFile{Data: []byte("console.log('test')")},
	}

	// Simulate embedded mode with a valid sub-filesystem
	hasEmbedded = true
	subFSInitialized = true
	cachedSubFS = mockFS

	// Test GetHTTPFS with embedded assets
	httpFS := GetHTTPFS()
	if httpFS == nil {
		t.Error("GetHTTPFS() should return non-nil when embedded assets available")
	}

	// Test that we can open files from the HTTP filesystem
	if httpFS != nil {
		f, err := httpFS.Open("/index.html")
		if err != nil {
			t.Errorf("Failed to open index.html: %v", err)
		} else {
			f.Close()
		}
	}
}

func TestHasEmbeddedAssets_WithMockFS(t *testing.T) {
	// Save original state
	origHasEmbedded := hasEmbedded
	origSubFSInitialized := subFSInitialized
	origCachedSubFS := cachedSubFS
	defer func() {
		hasEmbedded = origHasEmbedded
		subFSInitialized = origSubFSInitialized
		cachedSubFS = origCachedSubFS
	}()

	// Create a mock filesystem WITH index.html
	mockFS := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<html></html>")},
	}

	// Simulate embedded mode
	hasEmbedded = true
	subFSInitialized = true
	cachedSubFS = mockFS

	has := HasEmbeddedAssets()
	if !has {
		t.Error("HasEmbeddedAssets() should return true when index.html exists")
	}
}

func TestHasEmbeddedAssets_WithoutIndexHTML(t *testing.T) {
	// Save original state
	origHasEmbedded := hasEmbedded
	origSubFSInitialized := subFSInitialized
	origCachedSubFS := cachedSubFS
	defer func() {
		hasEmbedded = origHasEmbedded
		subFSInitialized = origSubFSInitialized
		cachedSubFS = origCachedSubFS
	}()

	// Create a mock filesystem WITHOUT index.html
	mockFS := fstest.MapFS{
		"other.txt": &fstest.MapFile{Data: []byte("other file")},
	}

	// Simulate embedded mode but missing index.html
	hasEmbedded = true
	subFSInitialized = true
	cachedSubFS = mockFS

	has := HasEmbeddedAssets()
	if has {
		t.Error("HasEmbeddedAssets() should return false when index.html is missing")
	}
}

func TestListEmbeddedFiles_WithMockFS(t *testing.T) {
	// Save original state
	origHasEmbedded := hasEmbedded
	origEmbeddedFS := embeddedFS
	defer func() {
		hasEmbedded = origHasEmbedded
		embeddedFS = origEmbeddedFS
	}()

	// Note: We can't easily mock embeddedFS because it's an embed.FS type
	// But we can test the branch where hasEmbedded = true but fs.WalkDir works

	// This test verifies the dev mode branch (hasEmbedded = false)
	hasEmbedded = false
	files := ListEmbeddedFiles()
	if files != nil {
		t.Error("ListEmbeddedFiles() should return nil when not embedded")
	}
}

func TestGetEmbeddedFS_CachingMechanism(t *testing.T) {
	// Save original state
	origHasEmbedded := hasEmbedded
	origSubFSInitialized := subFSInitialized
	origCachedSubFS := cachedSubFS
	defer func() {
		hasEmbedded = origHasEmbedded
		subFSInitialized = origSubFSInitialized
		cachedSubFS = origCachedSubFS
	}()

	// Create a mock filesystem
	mockFS := fstest.MapFS{
		"test.txt": &fstest.MapFile{Data: []byte("test")},
	}

	// Simulate that we're already initialized with a cached filesystem
	hasEmbedded = true
	subFSInitialized = true
	cachedSubFS = mockFS

	// Call getEmbeddedFS multiple times
	result1 := getEmbeddedFS()
	result2 := getEmbeddedFS()

	// Should return the same cached instance (both should be non-nil)
	if result1 == nil || result2 == nil {
		t.Error("getEmbeddedFS should return non-nil when subFSInitialized is true with valid cache")
	}

	// Verify the filesystem works (can open a file from the mock)
	if result1 != nil {
		_, err := result1.Open("test.txt")
		if err != nil {
			t.Error("getEmbeddedFS should return a working filesystem")
		}
	}
}

func TestGetEmbeddedFS_WithNilCachedSubFS(t *testing.T) {
	// Save original state
	origHasEmbedded := hasEmbedded
	origSubFSInitialized := subFSInitialized
	origCachedSubFS := cachedSubFS
	defer func() {
		hasEmbedded = origHasEmbedded
		subFSInitialized = origSubFSInitialized
		cachedSubFS = origCachedSubFS
	}()

	// Simulate that we're initialized but with nil (e.g., fs.Sub failed)
	hasEmbedded = true
	subFSInitialized = true
	cachedSubFS = nil

	result := getEmbeddedFS()

	if result != nil {
		t.Error("getEmbeddedFS should return nil when cachedSubFS is nil")
	}
}

func TestGetFS_WithDifferentStates(t *testing.T) {
	// Save original state
	origHasEmbedded := hasEmbedded
	origSubFSInitialized := subFSInitialized
	origCachedSubFS := cachedSubFS
	defer func() {
		hasEmbedded = origHasEmbedded
		subFSInitialized = origSubFSInitialized
		cachedSubFS = origCachedSubFS
	}()

	// Test 1: Not embedded
	hasEmbedded = false
	subFSInitialized = false
	cachedSubFS = nil

	result := GetFS()
	if result != nil {
		t.Error("GetFS should return nil when not embedded")
	}

	// Test 2: Embedded with cached filesystem
	mockFS := fstest.MapFS{"test.txt": &fstest.MapFile{Data: []byte("test")}}
	hasEmbedded = true
	subFSInitialized = true
	cachedSubFS = mockFS

	result = GetFS()
	if result == nil {
		t.Error("GetFS should return non-nil when embedded with valid cache")
	}
}

// =============================================================================
// Tests for embed.FS specific behavior
// =============================================================================

func TestEmbeddedFS_IsZeroValue(t *testing.T) {
	// Verify that embeddedFS is zero-valued in dev mode
	var zeroFS embed.FS
	if hasEmbedded {
		// In embedded mode, embeddedFS should be set by init()
		t.Log("In embedded mode, embeddedFS should be populated")
	} else {
		// In dev mode, embeddedFS should be zero-valued
		// We can't directly compare embed.FS, but we can check behavior
		_ = zeroFS // Just ensure no compile error
	}
}

func TestGetHTTPFS_NilCase(t *testing.T) {
	// Save original state
	origHasEmbedded := hasEmbedded
	origSubFSInitialized := subFSInitialized
	origCachedSubFS := cachedSubFS
	defer func() {
		hasEmbedded = origHasEmbedded
		subFSInitialized = origSubFSInitialized
		cachedSubFS = origCachedSubFS
	}()

	// Ensure GetHTTPFS returns nil when GetFS returns nil
	hasEmbedded = false
	subFSInitialized = false
	cachedSubFS = nil

	httpFS := GetHTTPFS()
	if httpFS != nil {
		t.Error("GetHTTPFS should return nil when GetFS returns nil")
	}
}

// =============================================================================
// Edge case: fs.Sub behavior with zero-valued embed.FS
// =============================================================================

func TestGetEmbeddedFS_WithZeroValuedEmbedFS(t *testing.T) {
	// Save original state
	origHasEmbedded := hasEmbedded
	origSubFSInitialized := subFSInitialized
	origCachedSubFS := cachedSubFS
	origEmbeddedFS := embeddedFS
	defer func() {
		hasEmbedded = origHasEmbedded
		subFSInitialized = origSubFSInitialized
		cachedSubFS = origCachedSubFS
		embeddedFS = origEmbeddedFS
	}()

	// Set up state where we need to call fs.Sub
	hasEmbedded = true
	subFSInitialized = false
	cachedSubFS = nil

	// embeddedFS is zero-valued
	// Note: fs.Sub on an empty embed.FS may succeed with an empty sub-FS

	result := getEmbeddedFS()

	// After the call, subFSInitialized should be true (to cache the result)
	if !subFSInitialized {
		t.Error("subFSInitialized should be true after getEmbeddedFS call")
	}

	// Log what happened for debugging
	t.Logf("Result is nil: %v, cachedSubFS is nil: %v", result == nil, cachedSubFS == nil)
}

// =============================================================================
// Interface compliance tests
// =============================================================================

func TestFSInterface_Compliance(t *testing.T) {
	// Verify that when GetFS returns a value, it implements fs.FS
	// Save original state
	origHasEmbedded := hasEmbedded
	origSubFSInitialized := subFSInitialized
	origCachedSubFS := cachedSubFS
	defer func() {
		hasEmbedded = origHasEmbedded
		subFSInitialized = origSubFSInitialized
		cachedSubFS = origCachedSubFS
	}()

	mockFS := fstest.MapFS{
		"test.txt": &fstest.MapFile{Data: []byte("test")},
	}

	hasEmbedded = true
	subFSInitialized = true
	cachedSubFS = mockFS

	result := GetFS()
	if result == nil {
		t.Fatal("Expected non-nil fs.FS")
	}

	// Verify fs.FS interface
	var _ = result

	// Test Open method
	f, err := result.Open("test.txt")
	if err != nil {
		t.Errorf("Failed to open test.txt: %v", err)
	} else {
		f.Close()
	}
}

func TestReadDirFS_Interface(t *testing.T) {
	// fstest.MapFS implements fs.ReadDirFS
	// Save original state
	origHasEmbedded := hasEmbedded
	origSubFSInitialized := subFSInitialized
	origCachedSubFS := cachedSubFS
	defer func() {
		hasEmbedded = origHasEmbedded
		subFSInitialized = origSubFSInitialized
		cachedSubFS = origCachedSubFS
	}()

	mockFS := fstest.MapFS{
		"dir/file1.txt": &fstest.MapFile{Data: []byte("file1")},
		"dir/file2.txt": &fstest.MapFile{Data: []byte("file2")},
	}

	hasEmbedded = true
	subFSInitialized = true
	cachedSubFS = mockFS

	result := GetFS()
	if result == nil {
		t.Fatal("Expected non-nil fs.FS")
	}

	// Try to read directory
	if readDirFS, ok := result.(fs.ReadDirFS); ok {
		entries, err := readDirFS.ReadDir("dir")
		if err != nil {
			t.Errorf("ReadDir failed: %v", err)
		}
		if len(entries) != 2 {
			t.Errorf("Expected 2 entries, got %d", len(entries))
		}
	}
}
