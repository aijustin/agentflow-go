package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/internal/adapter/tool/builtin"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestWorkflowRunnerConditionExistsAndMissing(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("on-exists", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterTool("on-missing", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "conditions",
		Orchestration: core.Orchestration{Workflow: &core.Workflow{
			Nodes: []core.WorkflowNode{
				{ID: "seed", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ready":true}}`)},
				{ID: "on-exists", Kind: core.NodeTool, Ref: "on-exists", DependsOn: []string{"seed"}, Condition: `exists(steps.seed.ready)`},
				{ID: "on-missing", Kind: core.NodeTool, Ref: "on-missing", DependsOn: []string{"seed"}, Condition: `missing(steps.seed.missing_field)`},
				{ID: "never", Kind: core.NodeTool, Ref: "on-exists", DependsOn: []string{"seed"}, Condition: "never"},
			},
		}},
	}
	if err := NewWorkflowRunner(reg, runs, nil).Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["on-exists"]; !ok {
		t.Fatal("expected exists branch to run")
	}
	if _, ok := loaded.StepOutputs["on-missing"]; !ok {
		t.Fatal("expected missing branch to run")
	}
	if _, ok := loaded.StepOutputs["never"]; ok {
		t.Fatal("never condition should skip node")
	}
}

func TestWorkflowRunnerConditionNotEquals(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("status", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		return core.ToolResult{Tool: "status", Output: json.RawMessage(`{"state":"ready"}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterTool("branch", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "ne-condition",
		Orchestration: core.Orchestration{Workflow: &core.Workflow{
			Nodes: []core.WorkflowNode{
				{ID: "status", Kind: core.NodeTool, Ref: "status"},
				{ID: "branch", Kind: core.NodeTool, Ref: "branch", DependsOn: []string{"status"}, Condition: `ne(steps.status.output.state, "blocked")`},
			},
		}},
	}
	if err := NewWorkflowRunner(reg, runs, nil).Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["branch"]; !ok {
		t.Fatal("expected ne branch to run")
	}
}

func TestWorkflowRunnerToolPolicyDeniesInvocation(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("blocked", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		t.Fatal("tool should not execute when denied")
		return core.ToolResult{}, nil
	})); err != nil {
		t.Fatal(err)
	}
	runs := runstateinmem.NewRepository()
	if err := runs.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	scenario := core.Scenario{
		Name: "governed",
		Tools: map[string]core.Tool{
			"blocked": {Name: "blocked", Type: "custom.blocked", SideEffect: core.SideEffectWrite},
		},
		Orchestration: core.Orchestration{Workflow: &core.Workflow{
			Nodes: []core.WorkflowNode{{ID: "call", Kind: core.NodeTool, Ref: "blocked"}},
		}},
	}
	runner := NewWorkflowRunner(reg, runs, nil, WithWorkflowToolPolicy(governance.NewMaxSideEffectPolicy(core.SideEffectRead)))
	err := runner.Run(context.Background(), scenario, "run-1")
	if err == nil || !errors.Is(err, governance.ErrDenied) {
		t.Fatalf("expected governance denial, got %v", err)
	}
}

func TestWorkflowRunnerTransformCopyMissingPath(t *testing.T) {
	reg := registry.New()
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "copy-missing",
		Orchestration: core.Orchestration{Workflow: &core.Workflow{
			Nodes: []core.WorkflowNode{{
				ID:    "bad-copy",
				Kind:  core.NodeTransform,
				Input: json.RawMessage(`{"copy":{"field":"steps.missing.output.value"}}`),
			}},
		}},
	}
	err := NewWorkflowRunner(reg, runs, nil).Run(context.Background(), scenario, "run-1")
	if err == nil {
		t.Fatal("expected missing path error")
	}
}
