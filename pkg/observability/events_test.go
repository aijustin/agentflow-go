package observability

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
)

type memoryEventStore struct {
	mu      sync.Mutex
	records []EventRecord
	nextID  int64
}

func (s *memoryEventStore) Append(_ context.Context, event core.Event) (EventRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	record := EventRecord{ID: s.nextID, Sequence: s.nextID, Event: event, CreatedAt: time.Now().UTC()}
	s.records = append(s.records, record)
	return record, nil
}

func (s *memoryEventStore) ListRuns(context.Context, RunQuery) ([]RunSummary, error) {
	return nil, nil
}

func (s *memoryEventStore) ListEvents(context.Context, string, EventQuery) ([]EventRecord, error) {
	return nil, nil
}

func (s *memoryEventStore) ListScopedEvents(context.Context, ScopedEventQuery) ([]EventRecord, error) {
	return nil, nil
}

func TestEventStoreSinkAndFanout(t *testing.T) {
	ctx := context.Background()
	store := &memoryEventStore{}
	hub := NewEventHub()
	sink := NewEventStoreSink(store, hub)
	sub := hub.Subscribe(ctx, EventSubscriptionFilter{RunID: "run-1"})
	defer sub.Cancel()

	event := core.Event{Type: core.EventRunStarted, RunID: "run-1", ScenarioName: "demo"}
	if err := sink.Emit(ctx, event); err != nil {
		t.Fatal(err)
	}
	select {
	case record := <-sub.Events:
		if record.Event.RunID != "run-1" {
			t.Fatalf("unexpected event: %+v", record)
		}
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for published event")
	}

	var count int
	fanout := NewEventFanoutSink(
		core.EventSinkFunc(func(context.Context, core.Event) error {
			count++
			return nil
		}),
		core.EventSinkFunc(func(context.Context, core.Event) error {
			count++
			return nil
		}),
	)
	if err := fanout.Emit(ctx, event); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("expected fanout to both sinks, got %d", count)
	}
}

func TestNormalizeQueriesAndEvents(t *testing.T) {
	runQuery := NormalizeRunQuery(RunQuery{Limit: 0, Offset: -1})
	if runQuery.Limit != DefaultRunQueryLimit || runQuery.Offset != 0 {
		t.Fatalf("unexpected run query: %+v", runQuery)
	}
	eventQuery := NormalizeEventQuery(EventQuery{Limit: 9999, AfterSequence: -1})
	if eventQuery.Limit != MaxEventQueryLimit || eventQuery.AfterSequence != 0 {
		t.Fatalf("unexpected event query: %+v", eventQuery)
	}
	if eventQuery.Preset != core.EventFilterDiagnostic {
		t.Fatalf("expected default diagnostic preset, got %q", eventQuery.Preset)
	}
	if !EventAllowedByPreset(core.EventMemoryRead, core.EventFilterDiagnostic) {
		t.Fatal("diagnostic should allow MemoryRead")
	}
	if EventAllowedByPreset(core.EventMemoryRead, core.EventFilterProductUI) {
		t.Fatal("product_ui should hide MemoryRead")
	}
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	normalized := NormalizeEvent(core.Event{Type: core.EventRunCompleted}, now)
	if normalized.Timestamp != now {
		t.Fatalf("expected timestamp set: %+v", normalized)
	}
	if StatusAfterEvent(RunStatusRunning, core.EventRunFailed) != RunStatusFailed {
		t.Fatal("expected failed status")
	}
	if StatusAfterEvent("", core.EventToolCalled) != RunStatusRunning {
		t.Fatal("expected default running status for empty current")
	}
	if StatusAfterEvent(RunStatusPaused, core.EventToolCalled) != RunStatusPaused {
		t.Fatal("expected current status preserved for unrelated event")
	}
	raw := []byte(`{"x":1}`)
	clone := CloneRawMessage(raw)
	clone[0] = 'X'
	if raw[0] != '{' {
		t.Fatal("clone should not alias source bytes")
	}
	cloned := CloneEvent(core.Event{Payload: raw})
	if len(cloned.Payload) != len(raw) {
		t.Fatal("expected cloned payload")
	}
}

func TestNoopRecorderAndTracer(t *testing.T) {
	ctx := context.Background()
	NoopRecorder{}.IncCounter(ctx, MetricRuntimeEventsTotal)
	NoopRecorder{}.AddCounter(ctx, MetricLLMTokensTotal, 5)
	NoopRecorder{}.ObserveHistogram(ctx, MetricRunDurationSeconds, 1.0)
	NoopRecorder{}.SetGauge(ctx, MetricQueueJobsQueued, 2)
	rec := RecorderFunc(func(context.Context, MetricName, float64, ...Attribute) {})
	rec.ObserveHistogram(ctx, MetricRunDurationSeconds, 1.0)
	rec.SetGauge(ctx, MetricQueueJobsRunning, 3)
	_, span := NoopTracer{}.Start(ctx, SpanRun)
	span.RecordError(errors.New("boom"))
	span.SetAttributes(Attribute{Key: "run_id", Value: "run-1"})
	span.End()
}

// TestEventHubCountsDroppedDeliveries: a subscriber that falls behind must
// not lose events silently — the hub's monotonic drop counter exposes them.
func TestEventHubCountsDroppedDeliveries(t *testing.T) {
	hub := NewEventHub()
	subscription := hub.Subscribe(context.Background(), EventSubscriptionFilter{Buffer: 1})
	defer subscription.Cancel()
	record := EventRecord{Event: core.Event{Type: core.EventRunStarted, RunID: "run-1"}}
	for i := 0; i < 3; i++ {
		if err := hub.PublishEvent(context.Background(), record); err != nil {
			t.Fatal(err)
		}
	}
	if got := hub.DroppedEvents(); got != 2 {
		t.Fatalf("expected 2 dropped deliveries with a 1-deep buffer and no reader, got %d", got)
	}
}

func TestEventStoreSinkStampsAndHubFiltersTenant(t *testing.T) {
	store := &memoryEventStore{}
	hub := NewEventHub()
	sink := NewEventStoreSink(store, hub)
	subA := hub.Subscribe(context.Background(), EventSubscriptionFilter{TenantID: "tenant-a", Buffer: 1})
	defer subA.Cancel()
	subB := hub.Subscribe(context.Background(), EventSubscriptionFilter{TenantID: "tenant-b", Buffer: 1})
	defer subB.Cancel()
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "service-a", Type: identity.PrincipalService, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	if err := sink.Emit(ctx, core.Event{Type: core.EventRunStarted, RunID: "run-a"}); err != nil {
		t.Fatal(err)
	}
	select {
	case record := <-subA.Events:
		if record.Event.TenantID != "tenant-a" {
			t.Fatalf("event tenant was not stamped: %+v", record.Event)
		}
	default:
		t.Fatal("tenant A did not receive its event")
	}
	select {
	case record := <-subB.Events:
		t.Fatalf("tenant B received tenant A event: %+v", record.Event)
	default:
	}
}
