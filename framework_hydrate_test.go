package agentflow_test

import (
	"context"
	"encoding/json"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkHybridRunMergesUserContextWithWorkflowSteps(t *testing.T) {
	scenario := core.Scenario{
		Name: "hydrate-merge",
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
					{ID: "prep", Kind: core.NodeTool, Ref: "echo", Input: json.RawMessage(`{"message":"wf-data"}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(fakeGateway{content: "merged ok"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	userCtx, _ := json.Marshal(map[string]any{"steps": map[string]any{"user": true}, "topic": "billing"})
	result, err := fw.Run(context.Background(), agentflow.RunRequest{
		RunID:   "hydrate-merge-run",
		Agent:   "assistant",
		Prompt:  "continue",
		Context: userCtx,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "merged ok" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFrameworkResumeFromCheckpointRejectsAutonomousMode(t *testing.T) {
	fw, err := agentflow.New(
		builder.MinimalAutonomous("assistant"),
		agentflow.WithCheckpointHistory(agentflow.NewInMemoryCheckpointHistory()),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.ResumeFromCheckpoint(context.Background(), "run-1", 1)
	if err == nil {
		t.Fatal("expected orchestration mode error")
	}
}
