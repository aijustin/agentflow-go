package orchestration

import (
	"context"
	"encoding/json"
	"testing"

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
}
