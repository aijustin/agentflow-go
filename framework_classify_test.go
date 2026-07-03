package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkHybridClassifiesExistingRunStatuses(t *testing.T) {
	scenario := core.Scenario{
		Name: "hybrid-classify",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTool, Ref: "echo", Input: json.RawMessage(`{"message":"data"}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(fakeGateway{content: "hybrid ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	runID := "hybrid-dup"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID, Agent: "assistant", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: runID, Agent: "assistant", Prompt: "go"})
	if !errors.Is(err, agentflow.ErrRunAlreadyCompleted) {
		t.Fatalf("expected ErrRunAlreadyCompleted, got %v", err)
	}
}

func TestFrameworkWorkflowAgentNodeUsesEngine(t *testing.T) {
	scenario := core.Scenario{
		Name: "workflow-agent-node",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Instructions: "answer"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "think", Kind: core.NodeAgent, Ref: "assistant", Input: json.RawMessage(`{"prompt":"hello"}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(fakeGateway{content: "agent output"}))
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "wf-agent", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || !strings.Contains(result.Output, "agent output") {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFrameworkRunStructuredHybridWorkflowPhase(t *testing.T) {
	scenario := core.Scenario{
		Name: "structured-hybrid-wf",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {
				Name: "assistant",
				LLM:  "default",
				Policy: core.AgentPolicy{
					OutputSchema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`),
				},
			},
		},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTool, Ref: "echo", Input: json.RawMessage(`{"message":"ctx"}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(structuredFakeGateway{payload: json.RawMessage(`{"answer":"structured hybrid"}`)}),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.RunStructured(context.Background(), agentflow.RunRequest{
		RunID: "structured-hybrid-run", Agent: "assistant", Prompt: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || string(result.StructuredOutput) != `{"answer":"structured hybrid"}` {
		t.Fatalf("unexpected result: %+v", result)
	}
}
