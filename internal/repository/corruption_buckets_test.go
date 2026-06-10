package repository

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// allBuckets returns the named lifecycle buckets. Kept as a function so each
// test gets its own copy.
func allBuckets() map[string][]string {
	return map[string][]string{
		"Resolved":           BucketResolved,
		"Orphaned":           BucketOrphaned,
		"InProgress":         BucketInProgress,
		"ManualIntervention": BucketManualIntervention,
		"Pending":            BucketPending,
		"Failed":             BucketFailed,
		"Ignored":            BucketIgnored,
	}
}

// TestCorruptionBuckets_Disjoint pins the bucket invariant: a lifecycle state
// in two buckets would be double-counted by StateCounts and matched by two
// /corruptions filters at once.
func TestCorruptionBuckets_Disjoint(t *testing.T) {
	t.Parallel()
	seen := map[string]string{}
	for name, states := range allBuckets() {
		for _, st := range states {
			if prev, dup := seen[st]; dup {
				t.Errorf("state %q appears in both %s and %s buckets", st, prev, name)
			}
			seen[st] = name
		}
	}
}

// TestInClause verifies the SQL rendering of a bucket.
func TestInClause(t *testing.T) {
	t.Parallel()
	got := InClause([]string{"A", "B"})
	if got != "('A', 'B')" {
		t.Errorf("InClause = %q, want ('A', 'B')", got)
	}
	if !strings.HasPrefix(InClause(BucketResolved), "('VerificationSuccess'") {
		t.Errorf("InClause(BucketResolved) = %q", InClause(BucketResolved))
	}
}

// TestCorruptionRepository_StateCounts_EveryStateIsBucketed seeds one
// corruption per lifecycle state across ALL buckets and asserts the dashboard
// counts them all. Before the shared bucket constants, nine states fell
// through StateCounts' hardcoded lists and silently vanished from the
// dashboard while still matching a /corruptions filter (audit finding 15).
func TestCorruptionRepository_StateCounts_EveryStateIsBucketed(t *testing.T) {
	t.Parallel()
	db := newCorruptionTestDB(t)
	repo := NewCorruptionRepository(db)
	base := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

	total := 0
	for _, bucket := range allBuckets() {
		for _, state := range bucket {
			id := fmt.Sprintf("c%d", total)
			seedCorruption(t, db, id, fmt.Sprintf("/f%d.mkv", total), state, base.Add(time.Duration(total)*time.Hour))
			total++
		}
	}

	c, err := repo.StateCounts(context.Background())
	if err != nil {
		t.Fatalf("StateCounts: %v", err)
	}
	sum := c.Resolved + c.Orphaned + c.InProgress + c.ManualIntervention + c.Pending + c.Failed + c.Ignored
	if sum != total {
		t.Errorf("StateCounts buckets cover %d of %d lifecycle states - some state falls through the dashboard", sum, total)
	}
	if c.InProgress != len(BucketInProgress) {
		t.Errorf("InProgress = %d, want %d", c.InProgress, len(BucketInProgress))
	}
	if c.ManualIntervention != len(BucketManualIntervention) {
		t.Errorf("ManualIntervention = %d, want %d", c.ManualIntervention, len(BucketManualIntervention))
	}
	if c.Failed != len(BucketFailed) {
		t.Errorf("Failed = %d, want %d", c.Failed, len(BucketFailed))
	}
}
