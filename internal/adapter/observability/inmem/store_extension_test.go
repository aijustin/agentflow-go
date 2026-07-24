package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

func TestAppendSequencedStoresAndDedupes(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	event := core.Event{Type: core.EventRunCompleted, RunID: "run-1", Timestamp: time.Now().UTC()}
	record, err := store.AppendSequenced(ctx, 5, event)
	if err != nil {
		t.Fatal(err)
	}
	if record.Sequence != 5 {
		t.Fatalf("expected sequence 5, got %d", record.Sequence)
	}
	again, err := store.AppendSequenced(ctx, 5, event)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != record.ID {
		t.Fatalf("duplicate sequenced append must return the existing record, got %+v", again)
	}
	// A regular Append continues past the sequenced insert.
	next, err := store.Append(ctx, core.Event{Type: core.EventRunPaused, RunID: "run-1", Timestamp: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if next.Sequence != 6 {
		t.Fatalf("expected Append to continue at 6, got %d", next.Sequence)
	}
	if _, err := store.AppendSequenced(ctx, 0, event); err == nil {
		t.Fatal("expected error for non-positive sequence")
	}
}

func TestDeleteEventsForRunAndPurgeBefore(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	old := time.Now().UTC().Add(-time.Hour)
	fresh := time.Now().UTC()
	for _, event := range []core.Event{
		{Type: core.EventRunStarted, RunID: "run-a", Timestamp: old},
		{Type: core.EventRunCompleted, RunID: "run-a", Timestamp: fresh},
		{Type: core.EventRunStarted, RunID: "run-b", Timestamp: fresh},
	} {
		if _, err := store.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := store.DeleteEventsForRun(ctx, "run-b")
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 event removed, got %d", removed)
	}
	events, err := store.ListEvents(ctx, "run-b", obspkg.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 0 {
		t.Fatalf("run-b events must be gone, got %+v", events)
	}
	removed, err = store.PurgeEventsBefore(ctx, time.Now().UTC().Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected 1 old event purged, got %d", removed)
	}
	events, err = store.ListEvents(ctx, "run-a", obspkg.EventQuery{})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 1 || events[0].Event.Type != core.EventRunCompleted {
		t.Fatalf("only the fresh run-a event must remain, got %+v", events)
	}
}
