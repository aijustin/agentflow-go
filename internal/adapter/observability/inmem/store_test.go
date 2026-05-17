package inmem

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

func TestStoreAppendsListsRunsAndEvents(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	base := time.Date(2026, 5, 17, 10, 0, 0, 0, time.UTC)

	appendEvent := func(event core.Event) obspkg.EventRecord {
		t.Helper()
		record, err := store.Append(ctx, event)
		if err != nil {
			t.Fatal(err)
		}
		return record
	}

	first := appendEvent(core.Event{Type: core.EventRunStarted, RunID: "run-1", ScenarioName: "sales", Timestamp: base})
	second := appendEvent(core.Event{Type: core.EventLLMCalled, RunID: "run-1", ScenarioName: "sales", Timestamp: base.Add(time.Second), Payload: json.RawMessage(`{"model":"gpt"}`)})
	third := appendEvent(core.Event{Type: core.EventRunCompleted, RunID: "run-1", ScenarioName: "sales", Timestamp: base.Add(2 * time.Second)})
	fourth := appendEvent(core.Event{Type: core.EventRunStarted, RunID: "run-2", ScenarioName: "support", Timestamp: base.Add(3 * time.Second)})

	if first.Sequence != 1 || second.Sequence != 2 || third.Sequence != 3 || fourth.Sequence != 1 {
		t.Fatalf("unexpected per-run sequences: %d %d %d %d", first.Sequence, second.Sequence, third.Sequence, fourth.Sequence)
	}

	runs, err := store.ListRuns(ctx, obspkg.RunQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 2 {
		t.Fatalf("expected 2 runs, got %d", len(runs))
	}
	if runs[0].RunID != "run-2" || runs[0].Status != obspkg.RunStatusRunning || runs[0].EventCount != 1 {
		t.Fatalf("unexpected newest run summary: %+v", runs[0])
	}
	if runs[1].RunID != "run-1" || runs[1].Status != obspkg.RunStatusCompleted || runs[1].EventCount != 3 {
		t.Fatalf("unexpected completed run summary: %+v", runs[1])
	}

	completed, err := store.ListRuns(ctx, obspkg.RunQuery{Status: obspkg.RunStatusCompleted, Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(completed) != 1 || completed[0].RunID != "run-1" {
		t.Fatalf("expected completed run-1, got %+v", completed)
	}

	events, err := store.ListEvents(ctx, "run-1", obspkg.EventQuery{AfterSequence: 1, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Sequence != 2 || events[0].Event.Type != core.EventLLMCalled {
		t.Fatalf("unexpected filtered events: %+v", events)
	}

	second.Event.Payload[0] = '['
	stored, err := store.ListEvents(ctx, "run-1", obspkg.EventQuery{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if string(stored[1].Event.Payload) != `{"model":"gpt"}` {
		t.Fatalf("store should clone payloads, got %s", stored[1].Event.Payload)
	}
}

func TestStoreRejectsEventsWithoutRunID(t *testing.T) {
	_, err := NewStore().Append(context.Background(), core.Event{Type: core.EventRunStarted})
	if err == nil {
		t.Fatal("expected missing run id error")
	}
}
