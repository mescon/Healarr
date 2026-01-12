# Healarr Production Audit - 2026-01-13

## Executive Summary

Production analysis of Healarr v1.1.32 on Osiris reveals **5 critical bugs** preventing successful remediation completion. Zero items have reached `VerificationSuccess` state despite multiple completed downloads.

## System State

| Metric | Value | Expected |
|--------|-------|----------|
| Total Events | 23,413 | N/A |
| VerificationSuccess | **0** | >0 |
| VerificationStarted | **0** | >0 |
| ManuallyRemoved | 4 | <1 (false positives) |
| StuckRemediation | 3 | 0 |
| Concurrent goroutines/item | 2-3 | 1 |

## Bugs Identified

### BUG-1: False ManuallyRemoved Race Condition [CRITICAL]

**Severity:** Critical
**Impact:** All completed downloads incorrectly marked as manually removed
**Affected File:** `internal/services/verifier.go:608-622`

**Evidence:**
```
21:24:09 - Download at 100% importPending (COMPLETE)
21:24:39 - Marked as ManuallyRemoved (30s later)
```

**Root Cause:**
- `handleDisappearedQueueItem()` checks history API once
- Sonarr hasn't registered import in history yet (API update delay)
- No retry mechanism for history check

**Fix:**
1. Add retry loop when `wasInQueue=true` and last status was `importPending`
2. Wait up to 2 minutes with 10s intervals for import to appear in history
3. Only emit ManuallyRemoved after exhausting retries

```go
// Proposed fix in handleDisappearedQueueItem
if state.lastStatus == "importPending" || state.lastProgress >= 99 {
    // Download was complete - give history API time to update
    for i := 0; i < 12; i++ { // 12 * 10s = 2 min
        if v.waitWithShutdown(10 * time.Second) {
            return monitorStop // shutdown
        }
        hasImport, err := v.hasImportEventInHistory(state.arrPath, state.mediaID)
        if err == nil && hasImport {
            return monitorContinue // Will be picked up by checkHistoryForImport
        }
    }
}
```

---

### BUG-2: Multiple Concurrent Verification Goroutines [MEDIUM]

**Severity:** Medium
**Impact:** Resource leak, log pollution, race conditions
**Affected File:** `internal/services/verifier.go:427-467`

**Evidence:**
```
14:54:32 - Starting monitoring (goroutine 1)
17:39:19 - Starting monitoring (goroutine 2)
23:29:05 - Starting monitoring (goroutine 3)
// All 3 still running at poll #80, #120, #180
```

**Root Cause:**
- `handleSearchCompleted()` spawns goroutine on each `SearchCompleted`
- No cancellation of existing goroutines for same corruptionID

**Fix:**
1. Add `activeVerifications map[string]context.CancelFunc` to VerifierService
2. Before starting new goroutine, cancel existing one for same corruptionID
3. Create context with cancellation for each verification

```go
func (v *VerifierService) handleSearchCompleted(event domain.Event) {
    corruptionID := event.AggregateID

    // Cancel any existing verification for this corruption
    v.verifyMu.Lock()
    if cancel, exists := v.activeVerifications[corruptionID]; exists {
        cancel()
        logger.Debugf("Cancelled existing verification for %s", corruptionID)
    }
    ctx, cancel := context.WithCancel(context.Background())
    v.activeVerifications[corruptionID] = cancel
    v.verifyMu.Unlock()

    // Pass context to monitoring functions
    v.startVerificationWithSemaphore(ctx, corruptionID, ...)
}
```

---

### BUG-3: No StuckRemediation Handler [MEDIUM]

**Severity:** Medium
**Impact:** Stuck items detected but never auto-recovered
**Affected File:** `internal/services/health_monitor.go`

**Root Cause:**
- HealthMonitor emits `StuckRemediation` events
- No service subscribes to handle them

**Fix:**
1. Add handler in MonitorService or RecoveryService for `StuckRemediation`
2. Handler should emit `RetryScheduled` to trigger new attempt

```go
func (m *MonitorService) handleStuckRemediation(event domain.Event) {
    // Emit RetryScheduled to trigger recovery
    m.eventBus.Publish(domain.Event{
        AggregateID:   event.AggregateID,
        AggregateType: "corruption",
        EventType:     domain.RetryScheduled,
        EventData:     map[string]interface{}{"reason": "stuck_remediation_recovery"},
    })
}
```

---

### BUG-4: History Check Timing [HIGH]

**Severity:** High
**Impact:** Import detection fails due to API timing
**Affected File:** `internal/services/verifier.go:798-804`

**Root Cause:**
- `hasImportEventInHistory` called immediately after queue item disappears
- Sonarr's history API has 10-30s delay updating after import

**Fix:**
1. If `wasInQueue` and download appeared complete, add delay before history check
2. Increase history limit from 20 to 50 for better coverage
3. Add specific delay for importPending transitions

---

### BUG-5: Verification Timeout Reset [HIGH]

**Severity:** High
**Impact:** Old goroutines never timeout
**Affected File:** `internal/services/verifier.go:655-668`

**Root Cause:**
- Each goroutine has `startTime: time.Now()`
- Old goroutines' timeout never reached because BUG-2 spawns new ones
- New goroutines have fresh timeout

**Fix:**
- BUG-2 fix (cancellation) resolves this
- Additionally, store verification start time in database per corruptionID
- Use original start time for timeout calculation

---

## Implementation Priority

| Bug | Priority | Effort | Fix Order |
|-----|----------|--------|-----------|
| BUG-1 | P0 | Medium | 1st |
| BUG-2 | P1 | Medium | 2nd |
| BUG-4 | P1 | Low | 3rd (with BUG-1) |
| BUG-3 | P2 | Low | 4th |
| BUG-5 | P2 | Low | Fixed by BUG-2 |

## Test Plan

1. **BUG-1 Fix Verification:**
   - Delete a monitored file
   - Let Sonarr grab replacement
   - Verify import detected after history delay
   - Should reach VerificationSuccess, not ManuallyRemoved

2. **BUG-2 Fix Verification:**
   - Trigger multiple retries for same corruption
   - Verify only ONE goroutine running (single poll sequence)

3. **BUG-3 Fix Verification:**
   - Let item get stuck for 24+ hours
   - Verify StuckRemediation triggers retry

## Current Workaround

Until fixes are deployed, manually retry stuck items via API:
```bash
curl -X POST "http://healarr:3090/api/corruptions/{id}/retry"
```

Or use the "Retry All" button in Manual Intervention banner.
