package orchestration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/internal/adapter/tool/builtin"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestWorkflowRunnerRoutesDynamicEdgeConditions(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("status", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		return core.ToolResult{Tool: "status", Output: json.RawMessage(`{"status":"ready"}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterTool("ready_branch", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterTool("blocked_branch", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{Workflow: &core.Workflow{
			Nodes: []core.WorkflowNode{
				{ID: "status", Kind: core.NodeTool, Ref: "status"},
				{ID: "ready_branch", Kind: core.NodeTool, Ref: "ready_branch"},
				{ID: "blocked_branch", Kind: core.NodeTool, Ref: "blocked_branch"},
			},
			Edges: []core.WorkflowEdge{
				{From: "status", To: "ready_branch", Condition: `eq(steps.status.output.status, "ready")`},
				{From: "status", To: "blocked_branch", Condition: `eq(steps.status.output.status, "blocked")`},
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
	if _, ok := loaded.StepOutputs["ready_branch"]; !ok {
		t.Fatalf("expected ready branch output: %+v", loaded.StepOutputs)
	}
	if _, ok := loaded.StepOutputs["blocked_branch"]; ok {
		t.Fatalf("blocked branch should be skipped: %+v", loaded.StepOutputs)
	}
}

func TestWorkflowRunnerResolveNodeInputCopyFrom(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("source", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		return core.ToolResult{Tool: "source", Output: json.RawMessage(`{"message":"from-source"}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterTool("echo", toolFunc(func(_ context.Context, call core.ToolCall) (core.ToolResult, error) {
		var input map[string]any
		if err := json.Unmarshal(call.Input, &input); err != nil {
			t.Fatal(err)
		}
		if input["message"] != "from-source" {
			t.Fatalf("expected copied message, got %+v", input)
		}
		return core.ToolResult{Tool: call.Tool, Output: json.RawMessage(`{"ok":true}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{Workflow: &core.Workflow{
			Nodes: []core.WorkflowNode{
				{ID: "source", Kind: core.NodeTool, Ref: "source"},
				{
					ID:        "echo",
					Kind:      core.NodeTool,
					Ref:       "echo",
					DependsOn: []string{"source"},
					Input:     json.RawMessage(`{"copy_from":{"message":"steps.source.output.message"}}`),
				},
			},
		}},
	}
	if err := NewWorkflowRunner(reg, runs, nil).Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
}

func TestWorkflowRunnerRedactsPersistedStepOutput(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("secret", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		return core.ToolResult{Tool: "secret", Output: json.RawMessage(`{"secret":"classified"}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(reg, runs, nil, WithOutputRedactor(governance.NewJSONFieldRedactor("secret")))
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{Workflow: &core.Workflow{
			Nodes: []core.WorkflowNode{{ID: "secret", Kind: core.NodeTool, Ref: "secret"}},
		}},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	body := string(loaded.StepOutputs["secret"].Inline)
	if !containsSubstring(body, "[REDACTED]") || containsSubstring(body, "classified") {
		t.Fatalf("output not redacted: %s", body)
	}
}

func indexSubstring(text, part string) int {
	for i := 0; i+len(part) <= len(text); i++ {
		if text[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}

func TestResolveNodeInputPromptFrom(t *testing.T) {
	runs := runstateinmem.NewRepository()
	if err := runs.Save(context.Background(), &runstate.RunSnapshot{
		RunID:       "run-1",
		Status:      runstate.RunStatusRunning,
		StepOutputs: map[string]runstate.StepOutputRef{
			"source": {Inline: json.RawMessage(`{"output":{"message":"hello"}}`)},
		},
	}, 0); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(nil, runs, nil)
	raw, err := runner.resolveNodeInput(context.Background(), "run-1", json.RawMessage(`{"prompt_from":"steps.source.output.message","extra":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out["prompt"] != "hello" || out["extra"] != float64(1) {
		t.Fatalf("unexpected resolved input: %+v", out)
	}
}

func containsSubstring(text, part string) bool {
	return indexSubstring(text, part) >= 0
}
