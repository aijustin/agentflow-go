package graph

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestApplyGraphMergesWorkflowAndSubgraphs(t *testing.T) {
	base := core.Scenario{
		Name: "base",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationAutonomous,
		},
	}
	graph := ScenarioGraph{
		Name: "edited",
		Mode: string(core.OrchestrationFixedWorkflow),
		Workflow: &GraphView{
			Nodes: []GraphNode{{ID: "main", Kind: string(core.NodeTransform), Input: json.RawMessage(`{"set":{"x":1}}`)}},
		},
		Workflows: map[string]GraphView{
			"sub": {Nodes: []GraphNode{{ID: "sub-node", Kind: string(core.NodeTransform)}}},
		},
	}
	out, err := ApplyGraph(base, graph)
	if err != nil {
		t.Fatal(err)
	}
	if out.Name != "edited" || out.Orchestration.Mode != core.OrchestrationFixedWorkflow {
		t.Fatalf("unexpected scenario: %+v", out)
	}
	if out.Orchestration.Workflow == nil || len(out.Orchestration.Workflows) != 1 {
		t.Fatalf("unexpected workflows: %+v", out.Orchestration)
	}
}

func TestApplyGraphRejectsInvalidSubgraph(t *testing.T) {
	base := core.Scenario{Name: "base"}
	graph := ScenarioGraph{
		Workflows: map[string]GraphView{
			"bad": {Nodes: []GraphNode{{ID: "", Kind: string(core.NodeTransform)}}},
		},
	}
	if _, err := ApplyGraph(base, graph); err == nil {
		t.Fatal("expected subgraph import error")
	}
}

func TestGenerateBuilderCodeSanitizesSubgraphName(t *testing.T) {
	scenario := core.Scenario{
		Name: "codegen-sub",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{ID: "main", Kind: core.NodeTransform}},
			},
			Workflows: map[string]core.Workflow{
				"123-bad/name": {
					Nodes: []core.WorkflowNode{{ID: "inner", Kind: core.NodeTransform}},
				},
			},
		},
	}
	code, err := GenerateBuilderCode(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if !containsAll(code, "wf_123_bad_name", "NamedWorkflow") {
		t.Fatalf("expected sanitized subgraph ident, got:\n%s", code)
	}
}
