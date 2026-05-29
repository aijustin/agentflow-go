package orchestration

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestWorkflowRunnerSubgraphExecutesNamedWorkflow(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "subgraph-demo",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflows: map[string]core.Workflow{
				"prep": {
					Nodes: []core.WorkflowNode{{
						ID:    "mark",
						Kind:  core.NodeTransform,
						Input: json.RawMessage(`{"set":{"ready":true}}`),
					}},
				},
			},
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{
				ID:   "run_prep",
				Kind: core.NodeSubgraph,
				Ref:  "prep",
			}}},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(snapshot.StepOutputs["run_prep"].Inline, &output); err != nil {
		t.Fatal(err)
	}
	if output["subgraph"] != "prep" {
		t.Fatalf("subgraph=%v want prep", output["subgraph"])
	}
	steps, ok := output["steps"].(map[string]any)
	if !ok {
		t.Fatalf("steps=%T want map", output["steps"])
	}
	mark, ok := steps["mark"].(map[string]any)
	if !ok || mark["ready"] != true {
		t.Fatalf("mark step=%v", steps["mark"])
	}
	if _, ok := snapshot.StepOutputs["run_prep::mark"]; !ok {
		t.Fatalf("expected namespaced step run_prep::mark in snapshot")
	}
}

// TestWorkflowRunnerSubgraphHydratesExternalizedChildOutput verifies that a
// subgraph child whose output was externalized to a blob is hydrated back into
// the aggregated parent output, instead of being replaced by an opaque
// {"external":true} marker that downstream nodes cannot consume.
func TestWorkflowRunnerSubgraphHydratesExternalizedChildOutput(t *testing.T) {
	ctx := context.Background()
	runs := newWorkflowRun(t)
	blobs := blobinmem.NewStore()
	runner := NewWorkflowRunner(nil, runs, nil, WithBlobStore(blobs))
	big := strings.Repeat("x", 256)
	scenario := core.Scenario{
		Name: "subgraph-blob",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflows: map[string]core.Workflow{
				"prep": {Nodes: []core.WorkflowNode{{
					ID:    "mark",
					Kind:  core.NodeTransform,
					Input: json.RawMessage(`{"set":{"payload":"` + big + `"}}`),
				}}},
			},
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{
				ID:   "run_prep",
				Kind: core.NodeSubgraph,
				Ref:  "prep",
			}}},
		},
		Runtime: core.RuntimePolicy{StepOutputThreshold: 16},
	}
	if err := runner.Run(ctx, scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	childRef, ok := snapshot.StepOutputs["run_prep::mark"]
	if !ok || childRef.Blob == nil {
		t.Fatalf("expected child output to be externalized to a blob, got %+v", childRef)
	}
	agg := snapshot.StepOutputs["run_prep"]
	raw := agg.Inline
	if agg.Blob != nil {
		raw, err = blobs.Get(ctx, *agg.Blob)
		if err != nil {
			t.Fatal(err)
		}
	}
	var output map[string]any
	if err := json.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	steps, ok := output["steps"].(map[string]any)
	if !ok {
		t.Fatalf("steps=%T want map", output["steps"])
	}
	mark, ok := steps["mark"].(map[string]any)
	if !ok {
		t.Fatalf("mark step missing or wrong type: %v", steps["mark"])
	}
	if mark["external"] == true {
		t.Fatalf("aggregate kept opaque marker instead of hydrated content: %v", mark)
	}
	if mark["payload"] != big {
		t.Fatalf("aggregate did not hydrate externalized child content")
	}
}
