package agentflow_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkResumeAndContinueAutonomousHITL(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithHITLTokenSecret([]byte("secret"), nil),
		agentflow.WithLLMGateway(fakeGateway{content: "approved answer"}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-continue", Agent: "assistant", Prompt: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused, got %+v", result)
	}
	result, err = fw.ResumeAndContinue(context.Background(), result.Token, core.DecisionApprove, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "approved answer" {
		t.Fatalf("unexpected continue result: %+v", result)
	}
}

func TestFrameworkResumeAndContinueWorkflowHITL(t *testing.T) {
	scenario := core.Scenario{
		Name: "workflow-hitl",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "approve", Kind: core.NodeHumanGate},
					{ID: "done", Kind: core.NodeTransform, DependsOn: []string{"approve"}, Input: json.RawMessage(`{"set":{"ok":true}}`)},
				},
			},
			HumanInLoop: core.HumanInLoopPolicy{Enabled: true},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithHITLTokenSecret([]byte("secret"), nil))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-wf", Prompt: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused workflow, got %+v", result)
	}
	result, err = fw.ResumeAndContinue(context.Background(), result.Token, core.DecisionApprove, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed workflow, got %+v", result)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-wf")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["done"]; !ok {
		t.Fatalf("expected transform step output: %+v", snapshot.StepOutputs)
	}
}

func TestFrameworkResumeRunByIDDeclarativeInterrupt(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalDeclarativeInterrupt(),
		agentflow.WithHITLTokenSecret([]byte("secret"), nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-interrupt", Prompt: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused workflow, got %+v", result)
	}
	steps, err := fw.ListRunSteps(context.Background(), "run-interrupt")
	if err != nil {
		t.Fatal(err)
	}
	if steps.PendingHITL == nil || !steps.PendingHITL.Interrupt || steps.PendingHITL.NodeID != "prepare" {
		t.Fatalf("unexpected pending hitl: %+v", steps.PendingHITL)
	}
	result, err = fw.ResumeRunByID(context.Background(), "run-interrupt", core.DecisionApprove, nil, true)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed workflow, got %+v", result)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-interrupt")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["continue"]; !ok {
		t.Fatalf("expected continue step output: %+v", snapshot.StepOutputs)
	}
}

func TestFrameworkResumeAndContinueToolApprovalPause(t *testing.T) {
	scenario := core.Scenario{
		Name: "tool-pause",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalPause},
		},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"echo"}},
		},
		Orchestration: core.Orchestration{Mode: core.OrchestrationAutonomous},
	}
	gateway := &toolPauseGateway{}
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo", Input: json.RawMessage(`{"message":"hi"}`)}},
	})
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}},
	})
	fw, err := agentflow.New(
		scenario,
		agentflow.WithHITLTokenSecret([]byte("secret"), nil),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-tool", Agent: "assistant", Prompt: "call echo"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused tool approval, got %+v", result)
	}
	result, err = fw.ResumeAndContinue(context.Background(), result.Token, core.DecisionApprove, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "done" {
		t.Fatalf("unexpected continue result: %+v", result)
	}
}

type toolPauseGateway struct {
	queue []llm.ToolCallResponse
}

func (g *toolPauseGateway) QueueToolCall(profile string, resp llm.ToolCallResponse) {
	g.queue = append(g.queue, resp)
}

func (g *toolPauseGateway) ChatWithTools(_ context.Context, _ string, _ llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	if len(g.queue) == 0 {
		return llm.ToolCallResponse{}, nil
	}
	resp := g.queue[0]
	g.queue = g.queue[1:]
	return resp, nil
}

func (g *toolPauseGateway) Chat(_ context.Context, _ string, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

func (g *toolPauseGateway) Supports(_ string, _ llm.Capability) bool { return true }

func TestFrameworkResumeAndContinueReject(t *testing.T) {
	var tokenOut bytes.Buffer
	fw, err := agentflow.New(
		builder.MinimalHumanInLoop("assistant"),
		agentflow.WithHITLTokenSecret([]byte("secret"), &tokenOut),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-reject", Agent: "assistant", Prompt: "approve"})
	if err != nil {
		t.Fatal(err)
	}
	result, err = fw.ResumeAndContinue(context.Background(), result.Token, core.DecisionReject, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCancelled {
		t.Fatalf("expected cancelled, got %+v", result)
	}
}
