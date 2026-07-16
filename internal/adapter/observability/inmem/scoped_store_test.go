package inmem

import (
	"context"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

func TestStoreListScopedEventsByEpisodeAndSession(t *testing.T) {
	ctx := context.Background()
	store := NewStore()
	base := time.Date(2026, 7, 16, 10, 0, 0, 0, time.UTC)

	mustAppend := func(event core.Event) {
		t.Helper()
		if _, err := store.Append(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	mustAppend(core.Event{Type: core.EventRunStarted, RunID: "run-a", EpisodeID: "ep-1", SessionID: "sess-1", Timestamp: base})
	mustAppend(core.Event{Type: core.EventLLMCalled, RunID: "run-a", EpisodeID: "ep-1", SessionID: "sess-1", Timestamp: base.Add(time.Second)})
	mustAppend(core.Event{Type: core.EventRunStarted, RunID: "run-b", EpisodeID: "ep-1", SessionID: "sess-1", Timestamp: base.Add(2 * time.Second)})
	mustAppend(core.Event{Type: core.EventRunStarted, RunID: "run-c", EpisodeID: "ep-2", SessionID: "sess-2", Timestamp: base.Add(3 * time.Second)})
	mustAppend(core.Event{Type: core.EventMemoryRead, RunID: "run-a", EpisodeID: "ep-1", SessionID: "sess-1", Timestamp: base.Add(4 * time.Second)})

	episodeEvents, err := store.ListScopedEvents(ctx, obspkg.ScopedEventQuery{EpisodeID: "ep-1", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(episodeEvents) != 4 {
		t.Fatalf("expected 4 episode events, got %d", len(episodeEvents))
	}

	productUI, err := store.ListScopedEvents(ctx, obspkg.ScopedEventQuery{
		EpisodeID: "ep-1",
		Limit:     100,
		Preset:    core.EventFilterProductUI,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(productUI) != 3 {
		t.Fatalf("expected product_ui to hide MemoryRead, got %d events", len(productUI))
	}

	sessionEvents, err := store.ListScopedEvents(ctx, obspkg.ScopedEventQuery{SessionID: "sess-1", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessionEvents) != 4 {
		t.Fatalf("expected 4 session events, got %d", len(sessionEvents))
	}
}
