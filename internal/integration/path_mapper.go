package integration

import (
	"database/sql"
	"fmt"
	"strings"
	"sync"
)

type SQLPathMapper struct {
	db       *sql.DB
	mappings []PathMapping
	mu       sync.RWMutex
}

type PathMapping struct {
	LocalPath string
	ArrPath   string
}

func NewPathMapper(db *sql.DB) (*SQLPathMapper, error) {
	pm := &SQLPathMapper{
		db: db,
	}
	if err := pm.Reload(); err != nil {
		return nil, err
	}
	return pm, nil
}

func (pm *SQLPathMapper) Reload() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	rows, err := pm.db.Query("SELECT local_path, arr_path FROM scan_paths WHERE enabled = 1")
	if err != nil {
		return fmt.Errorf("failed to query scan_paths: %w", err)
	}
	defer rows.Close()

	var mappings []PathMapping
	for rows.Next() {
		var m PathMapping
		if err := rows.Scan(&m.LocalPath, &m.ArrPath); err != nil {
			return fmt.Errorf("failed to scan path mapping: %w", err)
		}
		// Ensure paths don't have trailing slashes for consistent matching,
		// unless it's root (which shouldn't happen for media folders)
		m.LocalPath = strings.TrimRight(m.LocalPath, "/")
		m.ArrPath = strings.TrimRight(m.ArrPath, "/")
		mappings = append(mappings, m)
	}

	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating path mappings: %w", err)
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
		// Check if localPath starts with m.LocalPath AND is followed by / or end of string
		// This prevents /mnt/media/TV from matching /mnt/media/TV2
		if strings.HasPrefix(localPath, m.LocalPath) {
			remainder := localPath[len(m.LocalPath):]
			// Valid match only if remainder is empty or starts with /
			if remainder == "" || strings.HasPrefix(remainder, "/") {
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
	return bestMatch.ArrPath + relPath, nil
}

func (pm *SQLPathMapper) ToLocalPath(arrPath string) (string, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	var bestMatch *PathMapping
	var longestPrefixLen int

	for i := range pm.mappings {
		m := &pm.mappings[i]
		// Check if arrPath starts with m.ArrPath AND is followed by / or end of string
		// This prevents /data/movies from matching /data/movies-archive
		if strings.HasPrefix(arrPath, m.ArrPath) {
			remainder := arrPath[len(m.ArrPath):]
			// Valid match only if remainder is empty or starts with /
			if remainder == "" || strings.HasPrefix(remainder, "/") {
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
	return bestMatch.LocalPath + relPath, nil
}
