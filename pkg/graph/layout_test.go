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
