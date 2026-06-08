package integration

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync"

	"github.com/mescon/Healarr/internal/repository"
)

// SQLPathMapper translates between local filesystem paths and *arr paths.
type SQLPathMapper struct {
	db        *sql.DB
	scanPaths *repository.ScanPathRepository
	mappings  []PathMapping
	mu        sync.RWMutex
}

// PathMapping defines a mapping between a local path and its *arr equivalent.
type PathMapping struct {
	LocalPath string
	ArrPath   string
}

// NewPathMapper creates a SQLPathMapper and loads mappings from the database.
func NewPathMapper(db *sql.DB) (*SQLPathMapper, error) {
	pm := &SQLPathMapper{
		db:        db,
		scanPaths: repository.NewScanPathRepository(db),
	}
	if err := pm.Reload(); err != nil {
		return nil, err
	}
	return pm, nil
}

// pathSeparators are the path separators we recognize when matching a stored
// scan-path against a path reported by an *arr instance. Forward slash covers
// Linux/macOS and *arr's normalized form on most setups; backslash covers
// Windows UNC paths (e.g. \\server\share\folder) that Sonarr/Radarr report
// verbatim when running on Windows.
const pathSeparators = "/\\"

// trimTrailingSep removes any trailing path separators (both / and \) so the
// stored mapping form is canonical. TrimRight is safe for UNC paths because
// it only strips trailing characters, not the leading \\ that anchors the
// UNC root.
func trimTrailingSep(p string) string {
	return strings.TrimRight(p, pathSeparators)
}

// hasSepPrefix reports whether s begins with a recognized path separator
// (forward slash or backslash). Used after a HasPrefix match to confirm that
// the matched stem ends on a directory boundary, so /mnt/media/TV doesn't
// false-match against /mnt/media/TV2, and \\srv\share\Movies doesn't
// false-match \\srv\share\MoviesArchive.
func hasSepPrefix(s string) bool {
	if s == "" {
		return false
	}
	return strings.ContainsRune(pathSeparators, rune(s[0]))
}

// dominantSeparator returns the path separator that p is written in: backslash
// if p uses backslashes and no forward slashes (a native Windows path like
// D:\Media\Movies), forward slash otherwise. The forward-slash default covers
// Linux/macOS (where Healarr containers run) and any mixed/ambiguous input.
func dominantSeparator(p string) byte {
	if strings.Contains(p, "\\") && !strings.Contains(p, "/") {
		return '\\'
	}
	return '/'
}

// normalizeRemainder rewrites the separators in an *arr-supplied path
// remainder to match the target path's separator convention. Sonarr/Radarr
// running on Windows report paths with backslashes; when the mapped local
// path is a Linux path (the container default), those backslashes must become
// forward slashes or downstream filesystem ops (stat/open) and the scan-path
// matcher treat the whole tail as one nonsensical filename
// (e.g. stat "/media/Movies\Show\file.mkv": invalid argument). Closes #305.
func normalizeRemainder(remainder string, target string) string {
	if dominantSeparator(target) == '\\' {
		return strings.ReplaceAll(remainder, "/", "\\")
	}
	return strings.ReplaceAll(remainder, "\\", "/")
}

func (pm *SQLPathMapper) Reload() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	rows, err := pm.scanPaths.ListEnabled(context.Background())
	if err != nil {
		return fmt.Errorf("failed to query scan_paths: %w", err)
	}

	mappings := make([]PathMapping, 0, len(rows))
	for _, row := range rows {
		mappings = append(mappings, PathMapping{
			LocalPath: trimTrailingSep(row.LocalPath),
			ArrPath:   trimTrailingSep(row.ArrPath),
		})
	}

	pm.mappings = mappings
	return nil
}

func (pm *SQLPathMapper) ToArrPath(localPath string) (string, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var bestMatch *PathMapping
	var longestPrefixLen int

	for i := range pm.mappings {
		m := &pm.mappings[i]
		if strings.HasPrefix(localPath, m.LocalPath) {
			remainder := localPath[len(m.LocalPath):]
			if remainder == "" || hasSepPrefix(remainder) {
				if len(m.LocalPath) > longestPrefixLen {
					longestPrefixLen = len(m.LocalPath)
					bestMatch = m
				}
			}
		}
	}

	if bestMatch == nil {
		return "", fmt.Errorf("no mapping found for local path: %s", localPath)
	}

	relPath := strings.TrimPrefix(localPath, bestMatch.LocalPath)
	return bestMatch.ArrPath + normalizeRemainder(relPath, bestMatch.ArrPath), nil
}

func (pm *SQLPathMapper) ToLocalPath(arrPath string) (string, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var bestMatch *PathMapping
	var longestPrefixLen int

	for i := range pm.mappings {
		m := &pm.mappings[i]
		if strings.HasPrefix(arrPath, m.ArrPath) {
			remainder := arrPath[len(m.ArrPath):]
			if remainder == "" || hasSepPrefix(remainder) {
				if len(m.ArrPath) > longestPrefixLen {
					longestPrefixLen = len(m.ArrPath)
					bestMatch = m
				}
			}
		}
	}

	if bestMatch == nil {
		return "", fmt.Errorf("no mapping found for arr path: %s", arrPath)
	}

	relPath := strings.TrimPrefix(arrPath, bestMatch.ArrPath)
	return bestMatch.LocalPath + normalizeRemainder(relPath, bestMatch.LocalPath), nil
}
