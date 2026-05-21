package runtime

import (
	"context"
	"encoding/json"
	"testing"

	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	"github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestEngineContinueAfterBeforeFinalAnswer(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat)
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "final"}})
	scenario := core.Scenario{
		Name: "hitl",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode:        core.OrchestrationAutonomous,
			HumanInLoop: core.HumanInLoopPolicy{Enabled: true, Checkpoints: []string{"before_final_answer"}},
		},
	}
	engine, err := NewEngine(scenario, Dependencies{Runs: repo, LLM: gateway, HumanGate: gate})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.Run(context.Background(), RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "hello"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause, got %+v err=%v", paused, err)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	result, err := engine.ContinueAfterCheckpoint(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "final" || result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected continue result: %+v", result)
	}
}

func TestEnginePlanningExecuteTracksPlanSteps(t *testing.T) {
	repo := runstateinmem.NewRepository()
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall, llm.CapStructuredOutput)
	gateway.QueueStructured("default", json.RawMessage(`{"steps":[{"goal":"echo","tool":"echo"}]}`))
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"message":"hi"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Orchestration.Planning = core.PlanningPolicy{Enabled: true, Execute: true, MaxSteps: 2}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:  repo,
		LLM:   gateway,
		Tools: mapToolRegistry{"echo": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-plan", Agent: "assistant", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Load(context.Background(), "run-plan")
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := snapshot.StepOutputs["plan"]
	if !ok {
		t.Fatal("expected plan step output")
	}
	var state planExecutionState
	if err := json.Unmarshal(ref.Inline, &state); err != nil {
		t.Fatal(err)
	}
	if len(state.Steps) != 1 || state.Steps[0].Status != "done" {
		t.Fatalf("expected completed plan step, got %+v", state)
	}
}
