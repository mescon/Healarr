package repository

import (
	"context"
	"testing"
	"time"
)

// TestCorruptionRepository_CountUnresolved pins the /api/health semantics
// from issue #331: a corruption stuck mid-remediation (DeletionFailed) must
// count as unresolved, while resolved and user-ignored ones must not.
func TestCorruptionRepository_CountUnresolved(t *testing.T) {
	t.Parallel()
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	seedCorruption(t, db, "c1", "/a.mkv", "DeletionFailed", base)      // the #331 shape
	seedCorruption(t, db, "c2", "/b.mkv", "CorruptionDetected", base)  // pending
	seedCorruption(t, db, "c3", "/c.mkv", "RemediationPaused", base)   // parked, needs a human
	seedCorruption(t, db, "c4", "/d.mkv", "VerificationSuccess", base) // resolved - settled
	seedCorruption(t, db, "c5", "/e.mkv", "CorruptionIgnored", base)   // ignored - settled
	seedCorruption(t, db, "c6", "/f.mkv", "MaxRetriesReached", base)   // orphaned still needs attention

	n, err := repo.CountUnresolved(context.Background())
	if err != nil {
		t.Fatalf("CountUnresolved: %v", err)
	}
	if n != 4 {
		t.Errorf("CountUnresolved = %d, want 4 (DeletionFailed, CorruptionDetected, RemediationPaused, MaxRetriesReached)", n)
	}
}
