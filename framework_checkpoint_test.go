package agentflow_test

import (
	"context"
	"encoding/json"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkListRunSteps(t *testing.T) {
	scenario := core.Scenario{
		Name: "list-steps",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
					{ID: "b", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"y":2}}`)},
				},
				Edges: []core.WorkflowEdge{{From: "a", To: "b"}},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-steps"}); err != nil {
		t.Fatal(err)
	}
	result, err := fw.ListRunSteps(context.Background(), "run-steps")
	if err != nil {
		t.Fatal(err)
	}
	if result.Version <= 0 {
		t.Fatalf("expected positive version, got %+v", result)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected status: %+v", result)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("expected two steps, got %+v", result.Steps)
	}
	if result.Steps[0].NodeID != "a" || result.Steps[1].NodeID != "b" {
		t.Fatalf("unexpected step order: %+v", result.Steps)
	}
}

func TestFrameworkResumeFromStep(t *testing.T) {
	scenario := core.Scenario{
		Name: "resume-from-step",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{
						ID:   "seed",
						Kind: core.NodeTransform,
						Input: json.RawMessage(`{
							"set": {"list": ["one", "two"]}
						}`),
					},
					{
						ID:   "fanout",
						Kind: core.NodeMap,
						Input: json.RawMessage(`{
							"items_path": "steps.seed.list",
							"branch": {"kind": "transform", "input": {"set": {"tag": "mapped"}}}
						}`),
					},
					{
						ID:   "tail",
						Kind: core.NodeTransform,
						Input: json.RawMessage(`{
							"set": {"done": true}
						}`),
					},
				},
				Edges: []core.WorkflowEdge{
					{From: "seed", To: "fanout"},
					{From: "fanout", To: "tail"},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-resume"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}
	before, err := fw.ListRunSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Steps) < 3 {
		t.Fatalf("expected seed, fanout, tail outputs, got %+v", before.Steps)
	}

	result, err := fw.ResumeFromStep(context.Background(), runID, "fanout")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected result: %+v", result)
	}
	after, err := fw.ListRunSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	hasSeed := false
	hasFanout := false
	hasTail := false
	for _, step := range after.Steps {
		switch step.NodeID {
		case "seed":
			hasSeed = true
		case "fanout":
			hasFanout = true
		case "tail":
			hasTail = true
		}
	}
	if !hasSeed || !hasFanout || !hasTail {
		t.Fatalf("expected seed, fanout, tail after resume, got %+v", after.Steps)
	}
}

func TestFrameworkCheckpointHistory(t *testing.T) {
	scenario := core.Scenario{
		Name: "checkpoint-history",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
					{ID: "b", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"y":2}}`)},
				},
				Edges: []core.WorkflowEdge{{From: "a", To: "b"}},
			},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithCheckpointHistory(agentflow.NewInMemoryCheckpointHistory()))
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-checkpoints"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}

	list, err := fw.ListRunCheckpoints(context.Background(), runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Checkpoints) < 2 {
		t.Fatalf("expected at least two checkpoints, got %+v", list.Checkpoints)
	}

	first := list.Checkpoints[0]
	snapshot, err := fw.GetRunCheckpoint(context.Background(), runID, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RunID != runID {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}

	result, err := fw.ResumeFromCheckpoint(context.Background(), runID, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected result: %+v", result)
	}
}
