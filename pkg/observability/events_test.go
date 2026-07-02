package observability

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
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
	now := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	normalized := NormalizeEvent(core.Event{Type: core.EventRunCompleted}, now)
	if normalized.Timestamp != now {
		t.Fatalf("expected timestamp set: %+v", normalized)
	}
	if StatusAfterEvent(RunStatusRunning, core.EventRunFailed) != RunStatusFailed {
		t.Fatal("expected failed status")
	}
}

func TestNoopRecorderAndTracer(t *testing.T) {
	ctx := context.Background()
	NoopRecorder{}.IncCounter(ctx, MetricRuntimeEventsTotal)
	_, span := NoopTracer{}.Start(ctx, SpanRun)
	span.End()
}
