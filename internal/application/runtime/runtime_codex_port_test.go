package runtime

import (
	"context"
	"encoding/json"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

func TestTurnStopHookContinues(t *testing.T) {
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "draft"}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final"}},
	})
	events := &captureEvents{}
	calls := 0
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:   runstateinmem.NewRepository(),
		LLM:    gateway,
		Tools:  mapToolRegistry{"echo": echoTool{}},
		Events: events,
		TurnStopHook: func(ctx context.Context, info core.TurnStopInfo) (core.TurnStopDecision, error) {
			calls++
			if calls == 1 {
				return core.TurnStopDecision{Continue: true, ContinuationPrompt: "polish it"}, nil
			}
			return core.TurnStopDecision{}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-stop-hook", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "final" {
		t.Fatalf("output=%q", result.Output)
	}
	if events.count(core.EventTurnStopContinued) != 1 {
		t.Fatalf("expected TurnStopContinued, got %+v", events.types())
	}
}

func TestSamplingStepContextDeniesUnaadvertisedTool(t *testing.T) {
	step := toolorch.FreezeSamplingStepContext([]llm.ToolSpec{{Name: "echo"}})
	ctx := contextWithSamplingStep(context.Background(), step)
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	engine, err := NewEngine(toolScenario(core.ApprovalNever, core.SideEffectRead, 4), Dependencies{
		Runs:  runstateinmem.NewRepository(),
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.dispatchToolWithOptions(ctx, "run-step", core.Agent{Name: "assistant", Tools: []string{"echo"}}, llm.ToolCall{
		ID: "c1", Name: "http", Input: json.RawMessage(`{}`),
	}, newToolCallTracker(), toolDispatchOptions{skipMemory: true})
	if err != nil {
		t.Fatal(err)
	}
	if result.Error == "" {
		t.Fatal("expected step_context deny")
	}
}

func TestApprovalStoreSkipsSecondPause(t *testing.T) {
	store := toolorch.NewMemoryApprovalStore()
	orch := toolorch.NewStoreOrchestrator(store)
	input := json.RawMessage(`{"q":"x"}`)
	toolorch.RememberAllow(store, "run-cache", "echo", input)

	scenario := toolScenario(core.ApprovalPause, core.SideEffectWrite, 4)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:             runstateinmem.NewRepository(),
		LLM:              llmmock.NewGateway(),
		Tools:            mapToolRegistry{"echo": echoTool{}},
		ToolOrchestrator: orch,
		ApprovalStore:    store,
		HumanGate:        &stubGate{token: "t"},
	})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.maybePauseToolCall(
		context.Background(),
		"run-cache",
		core.Agent{Name: "assistant"},
		[]llm.ToolCall{{ID: "c1", Name: "echo", Input: input}},
		nil,
		newToolCallTracker(),
		"prompt",
		1,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	if paused != nil {
		t.Fatalf("cached allow must skip pause, got %+v", paused)
	}
}

type stubGate struct{ token string }

func (g *stubGate) Pause(ctx context.Context, state core.CheckpointState) (string, error) {
	return g.token, nil
}

func (g *stubGate) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	return nil
}
