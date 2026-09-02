package notifier

import (
	"sync"
	"testing"

	"github.com/mescon/Healarr/internal/domain"
	"github.com/mescon/Healarr/internal/eventbus"
)

// TestNotifier_notificationData_DoesNotMutateEvent guards the shared-map
// contract behind #374. The event bus fans a single Event value out to every
// subscriber, and EventData is a map, so all subscribers share one underlying
// map. The notifier must therefore never write into ev.EventData: the
// remediator and metrics subscribers read the same map concurrently in their
// own goroutines, and the Go runtime aborts the whole process with
// "fatal error: concurrent map read and map write" (safego cannot recover a
// runtime fatal error). This surfaced in production at startup, when the event
// replay service re-dispatched two unprocessed CorruptionDetected events back
// to back.
func TestNotifier_notificationData_DoesNotMutateEvent(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	ev := domain.Event{
		AggregateType: "corruption",
		AggregateID:   "agg-1",
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path":       "/tv/show.mkv",
			"corruption_type": "CorruptHeader",
		},
	}

	data := n.notificationData(ev)

	// The provider payload carries the correlation fields plus the original data.
	if got := data["aggregate_id"]; got != "agg-1" {
		t.Errorf("aggregate_id = %v, want agg-1", got)
	}
	if got := data["aggregate_type"]; got != "corruption" {
		t.Errorf("aggregate_type = %v, want corruption", got)
	}
	if got := data["file_path"]; got != "/tv/show.mkv" {
		t.Errorf("file_path = %v, want /tv/show.mkv", got)
	}
	if got := data["corruption_type"]; got != "CorruptHeader" {
		t.Errorf("corruption_type = %v, want CorruptHeader", got)
	}

	// The event's own map, shared with every other subscriber, must be untouched.
	if len(ev.EventData) != 2 {
		t.Fatalf("ev.EventData was mutated, now %d keys: %v", len(ev.EventData), ev.EventData)
	}
	for _, key := range []string{"aggregate_id", "aggregate_type"} {
		if _, ok := ev.EventData[key]; ok {
			t.Errorf("ev.EventData gained key %q; the payload must be copied, not shared", key)
		}
	}

	// A nil payload must still yield a usable map.
	nilData := n.notificationData(domain.Event{AggregateID: "agg-2", AggregateType: "health"})
	if nilData == nil {
		t.Fatal("notificationData returned nil for an event without EventData")
	}
	if got := nilData["aggregate_id"]; got != "agg-2" {
		t.Errorf("aggregate_id = %v, want agg-2", got)
	}
	if got := nilData["aggregate_type"]; got != "health" {
		t.Errorf("aggregate_type = %v, want health", got)
	}

	// Empty correlation fields are not added as empty strings.
	bare := n.notificationData(domain.Event{EventData: map[string]interface{}{"k": "v"}})
	if _, ok := bare["aggregate_id"]; ok {
		t.Error("empty AggregateID must not be added to the payload")
	}
	if _, ok := bare["aggregate_type"]; ok {
		t.Error("empty AggregateType must not be added to the payload")
	}
}

// TestNotifier_notificationData_NoRaceWithConcurrentReader is meaningful under
// `go test -race` (as CI runs it): another subscriber reads ev.EventData while
// the notifier builds its payload from the same event. The pre-fix
// implementation wrote aggregate_id/aggregate_type into ev.EventData, which the
// race detector reports as a data race.
func TestNotifier_notificationData_NoRaceWithConcurrentReader(t *testing.T) {
	tdb := newTestDB(t)
	defer tdb.Close()

	eb := eventbus.NewEventBus(tdb.DB)
	defer eb.Shutdown()

	n := NewNotifier(tdb.DB, eb)

	ev := domain.Event{
		AggregateType: "corruption",
		AggregateID:   "agg-race",
		EventType:     domain.CorruptionDetected,
		EventData: map[string]interface{}{
			"file_path":       "/tv/show.mkv",
			"corruption_type": "CorruptHeader",
		},
	}

	const iterations = 200
	var start, done sync.WaitGroup
	start.Add(1)
	done.Add(2)

	// Notifier subscriber: builds the provider payload from the shared event.
	go func() {
		defer done.Done()
		start.Wait()
		for i := 0; i < iterations; i++ {
			_ = n.notificationData(ev)
		}
	}()

	// Any other subscriber (metrics, remediator): reads the shared map.
	go func() {
		defer done.Done()
		start.Wait()
		for i := 0; i < iterations; i++ {
			_, _ = ev.EventData["corruption_type"].(string)
		}
	}()

	start.Done()
	done.Wait()
}
