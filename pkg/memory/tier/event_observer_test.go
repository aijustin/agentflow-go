package tier

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

type captureEvents struct {
	events []core.Event
}

func (c *captureEvents) Emit(_ context.Context, event core.Event) error {
	c.events = append(c.events, event)
	return nil
}

func TestEventSinkMigrationObserverEmitsPromotedEvent(t *testing.T) {
	sink := &captureEvents{}
	observer := EventSinkMigrationObserver{Sink: sink, Scenario: "tier-memory"}
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "s1", Agent: "assistant"}
	ctx := WithMigrationRunID(context.Background(), "run-1")
	observer.Promoted(ctx, ns, "rec-1", LevelHot, LevelWarm)
	if len(sink.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(sink.events))
	}
	event := sink.events[0]
	if event.Type != core.EventMemoryPromoted || event.RunID != "run-1" || event.ScenarioName != "tier-memory" {
		t.Fatalf("unexpected event: %+v", event)
	}
	var payload map[string]any
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload["record_id"] != "rec-1" || payload["from_tier"] != "hot" || payload["to_tier"] != "warm" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestChainMigrationObserversSkipsNilAndNoop(t *testing.T) {
	sink := &captureEvents{}
	observer := ChainMigrationObservers(nil, NoopMigrationObserver{}, EventSinkMigrationObserver{Sink: sink, Scenario: "tier-memory"})
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "s1", Agent: "assistant"}
	observer.Evicted(context.Background(), ns, "rec-2", LevelCold)
	if len(sink.events) != 1 || sink.events[0].Type != core.EventMemoryEvicted {
		t.Fatalf("unexpected events: %+v", sink.events)
	}
}
