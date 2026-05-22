package orchestration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestWorkflowRunnerMapNodeWithAgents(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil, WithAgentRegistry(stubAgentRegistry{
		agents: map[string]stubAgent{
			"analyst": {name: "analyst", output: "done"},
		},
	}))
	scenario := core.Scenario{
		Name: "map-agents",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{
						ID:   "items",
						Kind: core.NodeTransform,
						Input: json.RawMessage(`{
							"set": {"list": ["alpha", "beta"]}
						}`),
					},
					{
						ID:   "fanout",
						Kind: core.NodeMap,
						Input: json.RawMessage(`{
							"items_path": "steps.items.list",
							"branch": {"kind": "agent", "ref": "analyst"}
						}`),
						DependsOn: []string{"items"},
					},
				},
			},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := snapshot.StepOutputs["fanout"]
	if !ok {
		t.Fatal("expected map node output")
	}
	var output map[string]any
	if err := json.Unmarshal(ref.Inline, &output); err != nil {
		t.Fatal(err)
	}
	members, ok := output["members"].(map[string]any)
	if !ok || len(members) != 2 {
		t.Fatalf("expected two member outputs, got %+v", output)
	}
}

func TestWorkflowRunnerMapNodeWithTransformBranch(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "map-transform",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{
						ID:   "items",
						Kind: core.NodeTransform,
						Input: json.RawMessage(`{
							"set": {"list": ["x", "y", "z"]}
						}`),
					},
					{
						ID:   "fanout",
						Kind: core.NodeMap,
						Input: json.RawMessage(`{
							"items_path": "steps.items.list",
							"branch": {"kind": "transform", "input": {"set": {"tag": "mapped"}}}
						}`),
						DependsOn: []string{"items"},
					},
				},
			},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := snapshot.StepOutputs["fanout"]
	if !ok {
		t.Fatal("expected map node output")
	}
	var output map[string]any
	if err := json.Unmarshal(ref.Inline, &output); err != nil {
		t.Fatal(err)
	}
	members, ok := output["members"].(map[string]any)
	if !ok || len(members) != 3 {
		t.Fatalf("expected three member outputs, got %+v", output)
	}
	first, ok := members["0"].(map[string]any)
	if !ok || first["item"] != "x" || first["tag"] != "mapped" {
		t.Fatalf("unexpected first member output: %+v", members["0"])
	}
}

func TestWorkflowRunnerMapNodeEmptyItems(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "map-empty",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{
						ID:   "items",
						Kind: core.NodeTransform,
						Input: json.RawMessage(`{
							"set": {"list": []}
						}`),
					},
					{
						ID:   "fanout",
						Kind: core.NodeMap,
						Input: json.RawMessage(`{
							"items_path": "steps.items.list",
							"branch": {"kind": "transform"}
						}`),
						DependsOn: []string{"items"},
					},
				},
			},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := snapshot.StepOutputs["fanout"]
	if !ok {
		t.Fatal("expected map node output")
	}
	var output map[string]any
	if err := json.Unmarshal(ref.Inline, &output); err != nil {
		t.Fatal(err)
	}
	members, ok := output["members"].(map[string]any)
	if !ok || len(members) != 0 {
		t.Fatalf("expected empty members, got %+v", output)
	}
}
