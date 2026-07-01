package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type brokenTool struct{}

func (brokenTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{}, errors.New("tool failed")
}

type failToolOutputRepo struct {
	inner runstate.Repository
}

func (r *failToolOutputRepo) Load(ctx context.Context, runID string) (runstate.RunSnapshot, error) {
	return r.inner.Load(ctx, runID)
}

func (r *failToolOutputRepo) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if _, ok := snapshot.StepOutputs["tool.call-1"]; ok {
		return errors.New("persist failed")
	}
	return r.inner.Save(ctx, snapshot, expectedVersion)
}

func (r *failToolOutputRepo) Delete(ctx context.Context, runID string) error {
	return r.inner.Delete(ctx, runID)
}

func (r *failToolOutputRepo) List(ctx context.Context, filter runstate.ListFilter) ([]runstate.RunSnapshot, error) {
	return r.inner.List(ctx, filter)
}

// When tool execution fails and persisting the tool result also fails, the
// persist_error must be surfaced on the tool-returned event payload.
func TestEngineToolPersistFailureSurfacesInEvent(t *testing.T) {
	inner := runstateinmem.NewRepository()
	repo := &failToolOutputRepo{inner: inner}
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{}`)}},
	})
	events := &captureEvents{}
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:   repo,
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": brokenTool{}},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "use echo"})
	if err == nil {
		t.Fatal("expected tool loop to fail when tool output cannot be persisted")
	}
	var payload map[string]any
	found := false
	for _, event := range events.events {
		if event.Type != core.EventToolReturned {
			continue
		}
		if err := json.Unmarshal(event.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		found = true
	}
	if !found {
		t.Fatalf("expected tool returned event, got %+v", events.types())
	}
	if payload["persist_error"] == nil {
		t.Fatalf("expected persist_error in tool returned event, got %+v", payload)
	}
	if payload["error"] == nil || payload["error"] == "" {
		t.Fatalf("expected original tool error preserved, got %+v", payload)
	}
}
