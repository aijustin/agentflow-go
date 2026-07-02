package graph

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestMergeLayoutPreservesEditedPositions(t *testing.T) {
	edited := ScenarioGraph{
		Workflow: &GraphView{
			Nodes: []GraphNode{{ID: "a", Kind: "transform"}},
			Layout: map[string]GraphPosition{
				"a": {X: 10, Y: 20},
			},
		},
	}
	exported := ExportScenario(coreScenario())
	got := MergeLayout(edited, exported)
	if got.Workflow == nil || got.Workflow.Layout["a"].X != 10 || got.Workflow.Layout["a"].Y != 20 {
		t.Fatalf("expected merged layout, got %+v", got.Workflow)
	}
}

func TestMergeLayoutAddsMissingNodes(t *testing.T) {
	edited := ScenarioGraph{}
	exported := ExportScenario(coreScenario())
	got := MergeLayout(edited, exported)
	if got.Workflow == nil || len(got.Workflow.Nodes) != 1 {
		t.Fatalf("expected exported nodes, got %+v", got.Workflow)
	}
}

func TestMergeLayoutMergesNamedWorkflows(t *testing.T) {
	edited := ScenarioGraph{
		Workflows: map[string]GraphView{
			"nested": {
				Layout: map[string]GraphPosition{
					"b": {X: 30, Y: 40},
				},
			},
		},
	}
	exported := ScenarioGraph{
		Workflows: map[string]GraphView{
			"nested": {
				Nodes: []GraphNode{{ID: "b", Kind: "transform"}},
			},
		},
	}
	got := MergeLayout(edited, exported)
	pos := got.Workflows["nested"].Layout["b"]
	if pos.X != 30 || pos.Y != 40 {
		t.Fatalf("expected merged workflow layout, got %+v", pos)
	}
}

func coreScenario() core.Scenario {
	return core.Scenario{
		Name: "layout-test",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{ID: "a", Kind: core.NodeTransform}},
			},
		},
	}
}
