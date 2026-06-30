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

func TestEngineContinueAfterMultiToolApprovalPause(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	gateway := mock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{
			{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"query":"first"}`)},
			{ID: "call-2", Name: "risky", Input: json.RawMessage(`{"query":"second"}`)},
			{ID: "call-3", Name: "echo", Input: json.RawMessage(`{"query":"third"}`)},
		},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	scenario.Tools["risky"] = core.Tool{
		Name:        "risky",
		Type:        "builtin.echo",
		Description: "Risky echo",
		InputSchema: json.RawMessage(`{"type":"object"}`),
		Approval:    core.ApprovalPause,
		SideEffect:  core.SideEffectWrite,
	}
	agent := scenario.Agents["assistant"]
	agent.Tools = []string{"echo", "risky"}
	scenario.Agents["assistant"] = agent
	engine, err := NewEngine(scenario, Dependencies{
		Runs:      repo,
		LLM:       gateway,
		HumanGate: gate,
		Tools:     mapToolRegistry{"echo": echoTool{}, "risky": echoTool{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	paused, err := engine.Run(context.Background(), RunRequest{RunID: "run-multi", Agent: "assistant", Prompt: "go"})
	if err != nil || paused.Status != runstate.RunStatusPaused {
		t.Fatalf("expected pause, got %+v err=%v", paused, err)
	}
	snapshot, err := repo.Load(context.Background(), "run-multi")
	if err != nil {
		t.Fatal(err)
	}
	var pending []llm.ToolCall
	if raw := snapshot.Variables[checkpointToolCallsVar]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &pending); err != nil {
			t.Fatal(err)
		}
	}
	if len(pending) != 2 || pending[0].ID != "call-2" || pending[1].ID != "call-3" {
		t.Fatalf("expected remaining tool calls persisted, got %+v", pending)
	}
	if _, ok := snapshot.StepOutputs["tool.call-1"]; !ok {
		t.Fatal("expected first tool call to execute before pause")
	}
	token := paused.Token
	if token == "" {
		t.Fatal("expected pause token")
	}
	if err := gate.Resume(context.Background(), token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	result, err := engine.ContinueAfterCheckpoint(context.Background(), "run-multi")
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "done" || result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected continue result: %+v", result)
	}
	snapshot, err = repo.Load(context.Background(), "run-multi")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"call-1", "call-2", "call-3"} {
		if _, ok := snapshot.StepOutputs["tool."+id]; !ok {
			t.Fatalf("expected tool output for %s, got %+v", id, snapshot.StepOutputs)
		}
	}
}
