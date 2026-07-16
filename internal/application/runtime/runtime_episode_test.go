package runtime

import (
	"context"
	"encoding/json"
	"testing"

	obsInmem "github.com/aijustin/agentflow-go/internal/adapter/observability/inmem"
	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRunPersistsEpisodeCorrelationOnLifecycleEvents(t *testing.T) {
	store := obsInmem.NewStore()
	gateway := llmmock.NewGateway()
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Content: "done"}})
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Events: obspkg.NewEventStoreSink(store),
	})
	if err != nil {
		t.Fatal(err)
	}

	result, err := engine.Run(context.Background(), RunRequest{
		RunID:       "run-episode-1",
		Agent:       "assistant",
		Prompt:      "hello",
		EpisodeID:   "ep-qa-1",
		TriggerKind: "manual",
		SessionID:   "sess-qa-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected status: %s", result.Status)
	}

	events, err := store.ListEvents(context.Background(), "run-episode-1", obspkg.EventQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(events) < 2 {
		t.Fatalf("expected lifecycle events, got %d", len(events))
	}

	var sawStarted, sawCompleted bool
	for _, record := range events {
		if record.Event.EpisodeID != "ep-qa-1" || record.Event.SessionID != "sess-qa-1" || record.Event.TriggerKind != "manual" {
			t.Fatalf("event missing correlation: %+v", record.Event)
		}
		switch record.Event.Type {
		case core.EventRunStarted:
			sawStarted = true
			var payload map[string]string
			if err := json.Unmarshal(record.Event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload["episode_id"] != "ep-qa-1" {
				t.Fatalf("RunStarted payload missing episode_id: %s", record.Event.Payload)
			}
		case core.EventRunCompleted:
			sawCompleted = true
			var payload core.RunTerminalPayload
			if err := json.Unmarshal(record.Event.Payload, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Status != "completed" || payload.EpisodeID != "ep-qa-1" {
				t.Fatalf("unexpected RunCompleted payload: %+v", payload)
			}
		}
	}
	if !sawStarted || !sawCompleted {
		t.Fatalf("missing lifecycle events started=%v completed=%v", sawStarted, sawCompleted)
	}

	scoped, err := store.ListScopedEvents(context.Background(), obspkg.ScopedEventQuery{EpisodeID: "ep-qa-1", Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(scoped) != len(events) {
		t.Fatalf("scoped episode count %d != run events %d", len(scoped), len(events))
	}
}
