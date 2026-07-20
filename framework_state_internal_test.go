package agentflow

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/graph"
)

func TestSaveStudioGraphRebuildsEngineScenario(t *testing.T) {
	scenario := core.Scenario{
		Name: "studio-rebuild",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "scenario.yaml")
	edited := fw.ExportScenarioGraph()
	edited.Workflow.Nodes = append(edited.Workflow.Nodes, graph.GraphNode{
		ID: "b", Kind: string(core.NodeTransform), Input: json.RawMessage(`{"set":{"y":2}}`),
	})
	if _, err := fw.SaveStudioGraph(context.Background(), edited, path); err != nil {
		t.Fatal(err)
	}
	if got := len(fw.currentScenario().Orchestration.Workflow.Nodes); got != 2 {
		t.Fatalf("framework scenario nodes=%d, want 2", got)
	}
	if got := len(fw.currentEngine().Scenario().Orchestration.Workflow.Nodes); got != 2 {
		t.Fatalf("engine scenario nodes=%d, want 2 (engine not rebuilt)", got)
	}
}
