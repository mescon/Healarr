# Healarr Implementation Validation Report

**Date**: 2025-12-28
**Version Analyzed**: v1.0.3
**Reviewer**: Claude Code (Automated Analysis)

---

## Executive Summary

This report presents a comprehensive validation of the Healarr implementation against its stated goals in GOALS.md. The analysis covers *arr API download state handling, event-driven architecture consistency, error handling robustness, and test coverage.

**Overall Assessment**: The implementation is **production-quality** with a few **edge cases** that warrant attention.

| Category | Status | Score |
|----------|--------|-------|
| Core Functionality | Excellent | 9/10 |
| *arr State Handling | Good | 7/10 |
| Error Handling | Excellent | 9/10 |
| Test Coverage | Fair | 6/10 |
| Architecture | Excellent | 9/10 |

---

## 1. *arr Download State Analysis

### 1.1 Official TrackedDownloadState Values

Based on Sonarr/Radarr source code analysis:

| State | Description | Healarr Handles |
|-------|-------------|-----------------|
| `downloading` | Actively downloading | ✅ Yes - progress tracked |
| `importPending` | Download complete, awaiting import | ✅ Yes - checks history |
| `importing` | Actively importing to library | ⚠️ **NO** - not explicitly handled |
| `imported` | Successfully imported | ✅ Yes - checks history |
| `failedPending` | Failed, awaiting retry | ✅ Yes - emits DownloadFailed |
| `failed` | Download permanently failed | ✅ Yes - emits DownloadFailed |
| `ignored` | User ignored this item | ⚠️ **NO** - not explicitly handled |

### 1.2 Official TrackedDownloadStatus Values

| Status | Description | Healarr Handles |
|--------|-------------|-----------------|
| `ok` | No issues | ✅ Yes - normal flow |
| `warning` | Stalled, quality issues | ✅ Yes - logs warning, continues |
| `error` | Error state | ✅ Yes - logs warning, continues |

### 1.3 Missing State Handling

**Critical Finding #1: `importing` State**

```go
// verifier.go:267 - Current handling
if item.TrackedDownloadState == "importPending" || item.TrackedDownloadState == "imported" {
    if v.checkHistoryForImport(...) {
        return // Import found and handled
    }
}
```

**Issue**: The `importing` state (active import in progress) is not explicitly handled. When a file is being imported, it's neither `importPending` nor `imported` - it's in transition.

**Impact**: Low to Medium - The code will continue monitoring and the next poll will likely catch `imported`. However, there's a window where progress updates won't reflect the true state.

**Recommendation**: Add `importing` to the condition:
```go
if item.TrackedDownloadState == "importPending" ||
   item.TrackedDownloadState == "importing" ||
   item.TrackedDownloadState == "imported" {
```

---

**Critical Finding #2: `ignored` State**

**Issue**: When a user manually ignores a download in *arr UI, the state becomes `ignored`. This is different from `ManuallyRemoved` (item deleted from queue).

**Impact**: Medium - An ignored download will never trigger history check. The verifier will timeout after `verification_timeout` hours.

**Recommendation**: Handle `ignored` state explicitly:
```go
if item.TrackedDownloadState == "ignored" {
    logger.Infof("[WARN] Download ignored by user for %s", corruptionID)
    if err := v.eventBus.Publish(domain.Event{
        AggregateID:   corruptionID,
        EventType:     domain.DownloadIgnored, // New event type
        EventData:     map[string]interface{}{"reason": "User ignored in *arr"},
    }); err != nil { ... }
    return
}
```

---

## 2. Event-Driven Architecture Validation

### 2.1 State Machine Consistency

The corruption lifecycle state machine is **well-defined** but has one potential inconsistency:

```
CorruptionDetected
    └── [auto_remediate=true] → RemediationQueued
            └── DeletionStarted → DeletionCompleted → SearchStarted → SearchCompleted
                                        │                                    │
                                        └── DeletionFailed                   └── SearchFailed

After SearchCompleted:
    └── [Queue monitoring] → DownloadProgress → FileDetected → VerificationStarted
                                    │                                   │
                                    ├── DownloadFailed                  ├── VerificationSuccess ✓
                                    ├── DownloadTimeout                 └── VerificationFailed → RetryScheduled
                                    ├── ImportBlocked
                                    └── ManuallyRemoved
```

**Issue**: `DownloadFailed` and `ImportBlocked` have different termination behaviors:

| Event | Terminates Monitoring | Triggers Retry |
|-------|----------------------|----------------|
| DownloadFailed | ✅ Yes | ✅ Yes (via MonitorService) |
| ImportBlocked | ❌ No - continues monitoring | ❌ No |
| ManuallyRemoved | ✅ Yes | ❌ No |
| DownloadTimeout | ✅ Yes | ✅ Yes (via MonitorService) |

**Observation**: `ImportBlocked` continues monitoring, which is **correct behavior** - the user might fix the issue in *arr. However, there's no notification sent for `ImportBlocked`, unlike `DownloadFailed`.

**Recommendation**: Ensure notifications are configured to include `ImportBlocked` events so users are alerted to required manual intervention.

### 2.2 Event Data Consistency

All events use consistent data structures with type-safe accessors:

```go
// Good: Type-safe parsing
data, ok := event.ParseCorruptionEventData()
if !ok { ... }

// Good: Consistent field naming
EventData: map[string]interface{}{
    "file_path":   filePath,
    "media_id":    mediaID,
    "path_id":     pathID,
}
```

**Validation Result**: ✅ Consistent across all services

---

## 3. Error Handling Validation

### 3.1 Corruption vs Accessibility Distinction

The implementation correctly distinguishes between true corruption and infrastructure issues:

| Error Type | Classification | Remediation |
|------------|---------------|-------------|
| `ZeroByte` | Corruption | ✅ Delete + Re-search |
| `CorruptHeader` | Corruption | ✅ Delete + Re-search |
| `CorruptStream` | Corruption | ✅ Delete + Re-search |
| `InvalidFormat` | Corruption | ✅ Delete + Re-search |
| `AccessDenied` | Accessibility | ❌ Skip, queue for rescan |
| `PathNotFound` | Accessibility | ❌ Skip, log warning |
| `MountLost` | Accessibility | ❌ Abort scan, emit SystemHealthDegraded |
| `IOError` | Accessibility | ❌ Skip, queue for rescan |
| `Timeout` | Accessibility | ❌ Skip, queue for rescan |

**Validation Result**: ✅ Excellent - This is a key safety feature preventing data loss during infrastructure issues.

### 3.2 Remediator Safety Checks

```go
// remediator.go:207-213
func (r *RemediatorService) isInfrastructureError(corruptionType string) bool {
    switch corruptionType {
    case integration.ErrorTypeAccessDenied, integration.ErrorTypePathNotFound,
        integration.ErrorTypeMountLost, integration.ErrorTypeIOError,
        integration.ErrorTypeTimeout, integration.ErrorTypeInvalidConfig:
        return true
    }
    return false
}
```

**Validation Result**: ✅ Double-safety layer - even if scanner emits a corruption event with an accessibility error type, remediator refuses to process it.

---

## 4. Test Coverage Analysis

### 4.1 Current Coverage

| Package | Coverage | Assessment |
|---------|----------|------------|
| `internal/eventbus` | 91.9% | Excellent |
| `internal/domain` | 88.1% | Excellent |
| `internal/services` | 49.5% | Needs improvement |
| `internal/db` | 46.8% | Needs improvement |
| `internal/integration` | 32.2% | Needs improvement |
| `internal/notifier` | 17.8% | Needs improvement |
| `internal/api` | 5.9% | Critical gap |

### 4.2 Identified Test Gaps

**Gap #1: No integration tests for TrackedDownloadState transitions**

Missing test cases:
- `downloading` → `importing` → `imported`
- `downloading` → `failedPending` → `failed`
- `downloading` → `importBlocked` (user action) → `imported`
- `downloading` → `ignored`

**Gap #2: No tests for MonitorService retry logic**

```go
// monitor.go uses time.AfterFunc which is difficult to test
time.AfterFunc(delay, func() {
    if err := m.eventBus.Publish(...); err != nil { ... }
})
```

**Recommendation**: Inject a clock interface for testability.

**Gap #3: No end-to-end workflow tests**

Missing:
- Full corruption → remediation → verification cycle
- Multi-retry scenario with exponential backoff
- Batch throttling under high corruption count

### 4.3 Recommended Test Cases

```go
// Test case: importing state should continue monitoring
func TestVerifier_ImportingState_ContinuesMonitoring(t *testing.T) {
    // Setup: Queue returns item with trackedDownloadState = "importing"
    // Assert: No termination event, continues polling
}

// Test case: ignored state should terminate monitoring
func TestVerifier_IgnoredState_EmitsEvent(t *testing.T) {
    // Setup: Queue returns item with trackedDownloadState = "ignored"
    // Assert: DownloadIgnored event emitted, monitoring stops
}

// Test case: importBlocked continues but warns
func TestVerifier_ImportBlocked_ContinuesWithWarning(t *testing.T) {
    // Setup: Queue returns importBlocked state
    // Assert: ImportBlocked event emitted, continues monitoring
}

// Test case: wasInQueue but disappeared
func TestVerifier_QueueItemDisappeared_WithoutHistory(t *testing.T) {
    // Setup: First poll returns item, second poll empty, no history
    // Assert: ManuallyRemoved event emitted
}
```

---

## 5. Edge Cases and Potential Failure Modes

### 5.1 Race Conditions

| Scenario | Mitigation | Status |
|----------|------------|--------|
| Duplicate webhook events | `filesInProgress` map | ✅ Handled |
| Overlapping scans for same file | `hasActiveCorruption()` check | ✅ Handled |
| File disappears between stat and ffprobe | `classifyDetectorError()` | ✅ Handled |
| Mount drops during scan | `MountLost` detection, scan abort | ✅ Handled |

### 5.2 Unhandled Edge Cases

**Edge Case #1: History API failure during import check**

```go
// verifier.go:342
historyItems, err := v.arrClient.GetRecentHistoryForMediaByPath(arrPath, mediaID, 20)
if err != nil {
    logger.Debugf("History check error for %s: %v", corruptionID, err)
    return false  // Silently fails - doesn't retry history check
}
```

**Impact**: If history API returns transient error, a successful import might be missed.

**Recommendation**: Consider retry logic for history API calls.

---

**Edge Case #2: Multi-episode file replaced with single file**

Current code handles multi→multi replacement well, but what if:
- Original: S01E01-E03 (multi-episode)
- Replacement: S01E01 only (single episode)

```go
// verifier.go:319
if len(existingPaths) == len(allPaths) {
    // All files exist on disk
```

**Impact**: If replacement is partial (fewer episodes), verification would wait forever.

**Recommendation**: Add logic to handle partial replacements.

---

**Edge Case #3: *arr instance becomes unhealthy mid-verification**

If an *arr instance goes down during verification:
- Queue calls fail → continue polling
- No circuit breaker → hammers unhealthy instance

**Recommendation**: Add circuit breaker pattern for *arr API calls.

---

## 6. Architecture Observations

### 6.1 Strengths

1. **Event Sourcing**: Complete audit trail via `events` table
2. **Loose Coupling**: Services only communicate via events
3. **Graceful Shutdown**: `shutdownCh` pattern with `sync.WaitGroup`
4. **Rate Limiting**: Token bucket for *arr API protection
5. **Per-Path Configuration**: Flexible auto-remediate/dry-run settings
6. **Batch Throttling**: Prevents overwhelming *arr during mass corruption

### 6.2 Areas for Improvement

1. **No Circuit Breaker**: Unhealthy *arr instances get hammered
2. **Single-threaded Verification**: Each corruption spawns a goroutine, but no pool limit
3. **No Metrics Export**: No Prometheus/OpenMetrics endpoint
4. **Memory-resident Active Scans**: Lost on restart (mitigated by database persistence)

---

## 7. Recommendations Summary

### Critical (Should Fix)

| Issue | Location | Recommendation |
|-------|----------|----------------|
| Missing `importing` state | `verifier.go:267` | Add to import check condition |
| Missing `ignored` state | `verifier.go` | Add explicit handling with event |

### Important (Should Address)

| Issue | Location | Recommendation |
|-------|----------|----------------|
| No retry for history API | `verifier.go:343` | Add retry with backoff |
| MonitorService untestable | `monitor.go:65` | Inject clock interface |
| Low test coverage for services | `*_test.go` | Add integration tests |

### Nice to Have

| Issue | Location | Recommendation |
|-------|----------|----------------|
| No circuit breaker | `arr_client.go` | Add circuit breaker for *arr calls |
| No Prometheus metrics | N/A | Add `/metrics` endpoint |
| Partial replacement detection | `verifier.go` | Handle fewer files than expected |

---

## 8. Conclusion

The Healarr implementation is **robust and well-architected** for its stated goals. The event-driven design provides excellent auditability, and the corruption-vs-accessibility distinction is a critical safety feature.

The two missing TrackedDownloadState values (`importing` and `ignored`) are edge cases that won't cause data loss but may result in suboptimal user experience (delayed detection or timeout instead of proper notification).

Test coverage in the services layer should be improved, particularly around state transitions and retry logic.

**Overall Recommendation**: Address the two missing states before the next release. The implementation is production-ready as-is, with these being refinements rather than blockers.

---

## Sources

- [Sonarr API Docs](https://sonarr.tv/docs/api/)
- [Radarr API Docs](https://radarr.video/docs/api/)
- [Sonarr Activity Wiki](https://wiki.servarr.com/sonarr/activity)
- [TrackedDownloadState Discussion](https://github.com/onedr0p/radarr-exporter/issues/8)
