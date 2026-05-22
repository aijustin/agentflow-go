package graph

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestExportScenarioNestedWorkflows(t *testing.T) {
	scenario := core.Scenario{
		Name: "demo",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflows: map[string]core.Workflow{
				"prep": {Nodes: []core.WorkflowNode{{ID: "mark", Kind: core.NodeTransform}}},
			},
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{
				ID:   "run_prep",
				Kind: core.NodeSubgraph,
				Ref:  "prep",
			}}},
		},
	}
	out := ExportScenario(scenario)
	if out.Workflow == nil || len(out.Workflow.Nodes) != 1 {
		t.Fatalf("workflow nodes=%d", len(out.Workflow.Nodes))
	}
	if out.Workflows["prep"].Nodes[0].ID != "mark" {
		t.Fatalf("prep node=%v", out.Workflows["prep"].Nodes[0])
	}
}
