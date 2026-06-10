package db

import (
	"testing"
	"time"
)

// TestRepository_RunMaintenance_PrunesAbortedAndInterrupted pins the
// retention fix: MarkAborted/MarkInterrupted never stamp completed_at, and
// the old prune predicate (status IN ('completed','cancelled','error') AND
// completed_at < cutoff) therefore never matched those rows - the scans
// table only grew. The fix prunes all five terminal statuses and falls back
// to started_at when completed_at is NULL.
func TestRepository_RunMaintenance_PrunesAbortedAndInterrupted(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	oldTime := time.Now().AddDate(0, 0, -10).Format(time.RFC3339)

	for _, status := range []string{"aborted", "interrupted"} {
		_, err := repo.DB.Exec(`
			INSERT INTO scans (path, status, started_at, completed_at)
			VALUES (?, ?, ?, NULL)
		`, "/test/old-"+status, status, oldTime)
		if err != nil {
			t.Fatalf("insert old %s scan: %v", status, err)
		}
	}

	// A fresh interrupted scan is a resume candidate and must survive.
	_, err := repo.DB.Exec(`
		INSERT INTO scans (path, status, started_at, completed_at)
		VALUES (?, 'interrupted', ?, NULL)
	`, "/test/fresh-interrupted", time.Now().Format(time.RFC3339))
	if err != nil {
		t.Fatalf("insert fresh interrupted scan: %v", err)
	}

	if err := repo.RunMaintenance(7); err != nil {
		t.Fatalf("RunMaintenance: %v", err)
	}

	var oldCount int
	if err := repo.DB.QueryRow(`SELECT COUNT(*) FROM scans WHERE path LIKE '/test/old-%'`).Scan(&oldCount); err != nil {
		t.Fatalf("count old scans: %v", err)
	}
	if oldCount != 0 {
		t.Errorf("old aborted/interrupted scans not pruned: %d rows remain (completed_at is NULL for these statuses)", oldCount)
	}

	var freshCount int
	if err := repo.DB.QueryRow(`SELECT COUNT(*) FROM scans WHERE path = '/test/fresh-interrupted'`).Scan(&freshCount); err != nil {
		t.Fatalf("count fresh scan: %v", err)
	}
	if freshCount != 1 {
		t.Errorf("fresh interrupted scan pruned - it is a resume candidate and must be kept")
	}
}
