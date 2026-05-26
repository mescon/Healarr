package services

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/mescon/Healarr/internal/logger"
)

// verifyPathAccessible performs pre-flight checks to ensure a scan path is accessible
// before starting enumeration. This prevents false positives when mounts are offline.
func (s *ScannerService) verifyPathAccessible(path string) error {
	// 1. Check if path exists
	info, err := os.Stat(path)
	if err != nil {
		return s.classifyStatError(path, err)
	}

	// 2. Verify it's a directory
	if !info.IsDir() {
		return fmt.Errorf("path is not a directory: %s", path)
	}

	// 3. Try to list the directory (verifies mount is functional)
	entries, err := os.ReadDir(path)
	if err != nil {
		return fmt.Errorf("cannot read directory (mount may be stale): %v", err)
	}

	// 4. Sanity check: a media directory should usually have some entries
	// (but we don't fail on empty - it could be intentionally empty)
	if len(entries) == 0 {
		logger.Infof("Warning: scan path %s is empty", path)
	}

	// 5. Try to access a random file to verify read capability (if entries exist)
	return s.testFileAccess(path, entries)
}

// classifyStatError returns an appropriate error based on the type of stat failure
func (s *ScannerService) classifyStatError(path string, err error) error {
	if os.IsNotExist(err) {
		return fmt.Errorf("path does not exist: %s", path)
	}
	if os.IsPermission(err) {
		return fmt.Errorf("permission denied: %s", path)
	}
	if s.isMountError(err) {
		return fmt.Errorf("mount appears offline: %v", err)
	}
	return fmt.Errorf("cannot access path: %v", err)
}

// isMountError checks if an error indicates a mount-related problem
func (s *ScannerService) isMountError(err error) bool {
	errStr := strings.ToLower(err.Error())
	return strings.Contains(errStr, "stale") ||
		strings.Contains(errStr, "transport endpoint") ||
		strings.Contains(errStr, "no such device")
}

// testFileAccess tries to access a file in the directory to verify read capability
func (s *ScannerService) testFileAccess(path string, entries []os.DirEntry) error {
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		testPath := filepath.Join(path, entry.Name())
		if _, err := os.Stat(testPath); err != nil {
			return fmt.Errorf("can list directory but cannot access files (partial mount?): %v", err)
		}
		return nil // One successful test is enough
	}
	return nil
}

// hasActiveCorruption checks if a file already has an unresolved corruption record
// This prevents duplicate processing from webhook replays, overlapping scans, etc.
func (s *ScannerService) hasActiveCorruption(filePath string) bool {
	// Check for any CorruptionDetected event for this file that hasn't been resolved
	// A corruption is "active" if it has no VerificationSuccess or has MaxRetriesReached
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	active, err := s.corruptionRepo().HasActive(ctx, filePath)
	if err != nil {
		logger.Debugf("Error checking for active corruption: %v", err)
		return false // Err on the side of processing
	}
	return active
}

// LoadActiveCorruptionsForPath preloads all active corruptions for a given root path.
// This fixes the N+1 query problem during path scans by doing a single query upfront.
// Returns a map of file_path -> true for files with active corruptions.
func (s *ScannerService) LoadActiveCorruptionsForPath(rootPath string) map[string]bool {
	ctx, cancel := context.WithTimeout(context.Background(), scannerQueryTimeout)
	defer cancel()

	// Get all active corruptions for files under this path in a single query
	result, err := s.corruptionRepo().ListActiveFilePathsUnderRoot(ctx, rootPath)
	if err != nil {
		logger.Debugf("Error loading active corruptions for path %s: %v", rootPath, err)
		return make(map[string]bool)
	}

	logger.Debugf("Preloaded %d active corruptions for path %s", len(result), rootPath)
	return result
}
