# Bug Hunting Methodology for Event-Sourced Systems

This document describes systematic approaches for finding state bugs, resource leaks, and silent failures in event-sourced architectures like Healarr.

## Overview

Event-sourced systems have unique failure modes that differ from traditional CRUD applications. This methodology targets:

1. **Orphaned Events**: Events published but never subscribed to
2. **Dead-End States**: State transitions with no recovery path
3. **Silent Failures**: API errors that look like valid responses
4. **Resource Leaks**: Goroutines, timers, or connections not properly tracked

---

## Phase 1: Event Flow Mapping

### Goal
Identify all events that are published but have no subscribers (orphaned events).

### Method

1. **Map all event publishers**:
   ```bash
   # Find all event publish locations
   grep -rn "eventBus.Publish\|eb.Publish" internal/ --include="*.go" | grep -v "_test.go"
   ```

2. **Map all event subscribers**:
   ```bash
   # Find all subscription registrations
   grep -rn "Subscribe\|eventBus.Subscribe\|eb.Subscribe" internal/ --include="*.go" | grep -v "_test.go"
   ```

3. **Cross-reference**:
   - Create a table of all published event types
   - Mark which ones have corresponding subscribers
   - Events without subscribers are orphaned

### What to look for

| Pattern | Risk | Example |
|---------|------|---------|
| Event published, no subscriber | State stuck forever | `DownloadFailed` with no retry handler |
| Multiple subscribers, different expectations | Race conditions | Two services expecting to "own" the event |
| Subscriber exists but filtered out | Conditional orphaning | Handler that returns early on certain conditions |

### Example Finding

```go
// monitor.go - Before fix
m.eventBus.Subscribe(domain.DeletionFailed, m.handleFailure)
m.eventBus.Subscribe(domain.SearchFailed, m.handleFailure)
// DownloadFailed was missing! Items stuck in downloading state forever
```

**Fix**: Add missing subscription:
```go
m.eventBus.Subscribe(domain.DownloadFailed, m.handleFailure)
```

---

## Phase 2: State Machine Analysis

### Goal
Identify dead-end states and unreachable transitions.

### Method

1. **Document all states** (from events):
   ```
   CorruptionDetected → RemediationQueued → DeletionStarted →
   DeletionCompleted → SearchStarted → SearchCompleted →
   VerificationStarted → VerificationSuccess
   ```

2. **Document failure transitions**:
   ```
   DeletionFailed → ?
   SearchFailed → ?
   VerificationFailed → ?
   DownloadFailed → ?
   DownloadTimeout → ?
   ImportBlocked → ?
   SearchExhausted → ?
   ```

3. **For each failure state, verify**:
   - Is there a retry mechanism?
   - Is there a max-retry limit?
   - Is there a terminal "needs attention" state?
   - Is there user notification?

### State Machine Diagram Query

Use this pattern to visualize actual transitions from the database:
```sql
SELECT
    e1.event_type as from_state,
    e2.event_type as to_state,
    COUNT(*) as transitions
FROM events e1
JOIN events e2 ON e1.aggregate_id = e2.aggregate_id
    AND e2.created_at > e1.created_at
WHERE e2.id = (
    SELECT MIN(id) FROM events e3
    WHERE e3.aggregate_id = e1.aggregate_id
    AND e3.created_at > e1.created_at
)
GROUP BY from_state, to_state
ORDER BY transitions DESC;
```

### What to look for

| Pattern | Risk | Example |
|---------|------|---------|
| Failure event with no handler | Stuck forever | `ImportBlocked` with no logging |
| Success path only | Silent failures | `checkHistoryForImport` returning false on API error |
| No max-retry limit | Infinite retry loops | Missing `MaxRetriesReached` check |

---

## Phase 3: Goroutine Lifecycle Analysis

### Goal
Find resource leaks from untracked goroutines.

### Method

1. **Identify all goroutine spawns**:
   ```bash
   grep -rn "go func\|go m\.\|go s\.\|go v\." internal/ --include="*.go" | grep -v "_test.go"
   ```

2. **For each goroutine, verify**:
   - Is it tracked in a WaitGroup?
   - Does it have a shutdown signal (context or channel)?
   - Can it be gracefully stopped?

3. **Check timer/ticker usage**:
   ```bash
   grep -rn "time.AfterFunc\|time.NewTicker\|time.NewTimer" internal/ --include="*.go"
   ```

### Pattern Checklist

```go
// Good pattern
type Service struct {
    wg       sync.WaitGroup
    stopChan chan struct{}
}

func (s *Service) Start() {
    s.wg.Add(1)
    go func() {
        defer s.wg.Done()
        // work...
    }()
}

func (s *Service) Stop() {
    close(s.stopChan)
    s.wg.Wait()
}
```

### What to look for

| Pattern | Risk | Example |
|---------|------|---------|
| `go func()` without WaitGroup | Orphaned goroutine | Timer callbacks without tracking |
| Timer without cancelation | Memory leak | `AfterFunc` not stored for `Stop()` |
| No shutdown signal | Hangs on restart | Event handlers that can't be interrupted |

---

## Phase 4: API Resilience Audit

### Goal
Find silent failures where API errors are treated as valid responses.

### Method

1. **Find all external API calls**:
   ```bash
   grep -rn "client\.\|arrClient\.\|http\." internal/ --include="*.go" | grep -v "_test.go"
   ```

2. **For each API call, verify**:
   - Is the error checked?
   - Is the error distinguishable from "not found"?
   - Is there retry logic for transient failures?

3. **Look for silent return patterns**:
   ```bash
   grep -rn "return false\|return nil\|return 0" internal/ --include="*.go" | head -100
   ```

### Anti-patterns to find

```go
// Bad: API error looks like "not found"
paths, err := client.GetFilePaths(mediaID)
if err != nil {
    return false  // Bug: caller can't distinguish error from empty result
}
if len(paths) == 0 {
    return false
}

// Good: Error is propagated or logged distinctly
paths, err := client.GetFilePaths(mediaID)
if err != nil {
    logger.Warnf("API error (not empty result): %v", err)
    return false, err  // Caller knows this is an error
}
```

### What to look for

| Pattern | Risk | Example |
|---------|------|---------|
| `if err != nil { return false }` | Silent failure | API error treated as "no results" |
| No retry for transient errors | Flaky behavior | Network timeout causes permanent failure |
| Error swallowed in goroutine | Lost errors | `go func() { if err != nil { return } }` |

---

## Phase 5: Notification Coverage

### Goal
Ensure all significant events can be notified to users.

### Method

1. **List all event types**:
   ```bash
   grep -rn "EventType = " internal/domain/events.go
   ```

2. **Check notification event groups**:
   ```go
   // In notifier.go, verify GetEventGroups() covers all events
   ```

3. **Verify message formatters exist**:
   ```go
   // In notifier.go, check messageFormatters map
   ```

### What to look for

| Pattern | Risk | Example |
|---------|------|---------|
| Event not in notification groups | User can't subscribe | `InstanceUnhealthy` missing |
| No message formatter | Generic/confusing notification | Missing `fmtDownloadFailed` |

---

## Quick Reference Queries

### Find potential orphaned events
```sql
-- Events that were published but never followed by another event
SELECT DISTINCT e.event_type, COUNT(*) as stuck_count
FROM events e
WHERE NOT EXISTS (
    SELECT 1 FROM events e2
    WHERE e2.aggregate_id = e.aggregate_id
    AND e2.created_at > e.created_at
)
AND e.event_type NOT IN ('VerificationSuccess', 'MaxRetriesReached')
GROUP BY e.event_type
ORDER BY stuck_count DESC;
```

### Find retry exhaustion
```sql
SELECT
    corruption_id,
    retry_count,
    current_state,
    file_path
FROM corruption_status
WHERE retry_count > 0
AND current_state NOT IN ('VerificationSuccess', 'MaxRetriesReached')
ORDER BY retry_count DESC;
```

### Find items stuck in downloading state
```sql
SELECT
    corruption_id,
    file_path,
    current_state,
    last_event_at,
    julianday('now') - julianday(last_event_at) as days_stuck
FROM corruption_status
WHERE current_state = 'VerificationStarted'
AND julianday('now') - julianday(last_event_at) > 1
ORDER BY days_stuck DESC;
```

---

## Bug Severity Classification

| Severity | Description | Example |
|----------|-------------|---------|
| P0 - Critical | State stuck forever, data loss risk | Missing `DownloadFailed` handler |
| P1 - High | Silent failures affecting reliability | API error treated as empty result |
| P2 - Medium | Resource leaks, gradual degradation | Untracked goroutines |
| P3 - Low | Log spam, minor observability gaps | Missing notification formatter |

---

## Checklist for New Features

When adding new event types:

- [ ] Event type defined in `domain/events.go`
- [ ] Publisher exists (scanner, webhook, etc.)
- [ ] Subscriber exists (remediator, verifier, monitor)
- [ ] Notification group includes the event
- [ ] Message formatter defined for user-facing notifications
- [ ] Event title defined for generic webhooks
- [ ] Tests cover the event flow
- [ ] Documentation updated

When adding new services:

- [ ] WaitGroup tracks all goroutines
- [ ] Stop/Shutdown method exists
- [ ] Timers/tickers can be canceled
- [ ] Shutdown is called from main.go
- [ ] Graceful shutdown tested

---

## Version History

| Date | Changes |
|------|---------|
| 2026-01-13 | Initial methodology based on v1.1.32 bug hunt |
