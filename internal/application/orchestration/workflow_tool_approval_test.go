package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	humancli "github.com/aijustin/agentflow-go/internal/adapter/human/cli"
	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type staticTool struct{}

func (staticTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{Tool: "risky", Output: json.RawMessage(`{"ok":true}`)}, nil
}

func TestWorkflowRunnerPausesToolNodeApproval(t *testing.T) {
	repo := runstateinmem.NewRepository()
	signer, err := runstate.NewTokenSigner([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	gate := humancli.NewGate(repo, signer, nil)
	reg := registry.New()
	if err := reg.RegisterTool("risky", staticTool{}); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, repo, nil, WithHumanGate(gate))
	scenario := core.Scenario{
		Name: "wf-tool-approval",
		Tools: map[string]core.Tool{
			"risky": {
				Name:        "risky",
				Type:        "builtin.static",
				Approval:    core.ApprovalPause,
				SideEffect:  core.SideEffectWrite,
				InputSchema: json.RawMessage(`{"type":"object"}`),
			},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "call", Kind: core.NodeTool, Ref: "risky", Input: json.RawMessage(`{"query":"hi"}`)},
				},
			},
		},
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-wf-tool", ScenarioName: "wf-tool-approval", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	err = runner.Run(context.Background(), scenario, "run-wf-tool")
	var paused WorkflowPausedError
	if !errors.As(err, &paused) || paused.NodeID != "call" {
		t.Fatalf("expected tool pause, got %v", err)
	}
	snapshot, err := repo.Load(context.Background(), "run-wf-tool")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused snapshot, got %q", snapshot.Status)
	}
	if variableString(snapshot.Variables, workflowCheckpointKindVar) != workflowToolApprovalKind {
		t.Fatalf("expected workflow tool checkpoint, got %+v", snapshot.Variables)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	if err := runner.Resume(context.Background(), scenario, "run-wf-tool"); err != nil {
		t.Fatal(err)
	}
	snapshot, err = repo.Load(context.Background(), "run-wf-tool")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["call"]; !ok {
		t.Fatalf("expected tool output saved: %+v", snapshot.StepOutputs)
	}
}
