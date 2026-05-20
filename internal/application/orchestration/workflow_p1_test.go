package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	"github.com/aijustin/agentflow-go/pkg/core"
)

var errTestParallel = errors.New("parallel tool failed")

func TestWorkflowRunnerParallelGroupToolsCollectErrors(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("ok_tool", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		return core.ToolResult{Tool: "ok_tool", Output: json.RawMessage(`{"ok":true}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterTool("bad_tool", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		return core.ToolResult{}, errTestParallel
	})); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(reg, runs, nil)
	scenario := core.Scenario{
		Name: "parallel-tools",
		Tools: map[string]core.Tool{
			"ok_tool":  {Type: "test"},
			"bad_tool": {Type: "test"},
		},
		Orchestration: core.Orchestration{Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{
			ID:    "group",
			Kind:  core.NodeParallelGroup,
			Input: json.RawMessage(`{"tools":["ok_tool","bad_tool"],"on_error":"collect_errors"}`),
		}}}},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(snapshot.StepOutputs["group"].Inline, &output); err != nil {
		t.Fatal(err)
	}
	errorsField, ok := output["errors"].([]any)
	if !ok || len(errorsField) == 0 {
		t.Fatalf("expected collected errors, got %+v", output)
	}
}

func TestWorkflowRunnerLoopChecksUntilBeforeIteration(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "loop-until-first",
		Orchestration: core.Orchestration{Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
			{ID: "mark", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"done":true}}`)},
			{ID: "loop", Kind: core.NodeLoop, Input: json.RawMessage(`{"body":["mark"],"max_iterations":3,"until":"eq(steps.mark.done,true)"}`)},
		}}},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(snapshot.StepOutputs["loop"].Inline, &output); err != nil {
		t.Fatal(err)
	}
	if output["iterations"] != float64(1) {
		t.Fatalf("expected one iteration, got %+v", output)
	}
	if _, ok := snapshot.StepOutputs["mark"]; !ok {
		t.Fatal("expected body node output")
	}
}
