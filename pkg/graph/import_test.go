package graph

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestImportWorkflowRoundTrip(t *testing.T) {
	scenario := core.Scenario{
		Name: "roundtrip",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
					{ID: "b", Kind: core.NodeAgent, Ref: "reviewer"},
				},
				Edges: []core.WorkflowEdge{{From: "a", To: "b"}},
			},
		},
	}
	exported := ExportScenario(scenario)
	wf, err := ImportWorkflow(*exported.Workflow)
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Nodes) != 2 || len(wf.Edges) != 1 {
		t.Fatalf("unexpected workflow: %+v", wf)
	}
	if string(wf.Nodes[0].Input) != `{"set":{"x":1}}` {
		t.Fatalf("input not preserved: %s", wf.Nodes[0].Input)
	}
}

func TestGenerateBuilderCode(t *testing.T) {
	scenario := core.Scenario{
		Name: "codegen",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform},
					{ID: "b", Kind: core.NodeAgent, Ref: "reviewer"},
				},
				Edges: []core.WorkflowEdge{{From: "a", To: "b"}},
			},
		},
	}
	code, err := GenerateBuilderCode(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if code == "" || !containsAll(code, "builder.NewWorkflow", "NodeAgent", "FixedWorkflow", "BuildScenario") {
		t.Fatalf("unexpected codegen: %s", code)
	}
}

func containsAll(value string, parts ...string) bool {
	for _, part := range parts {
		if !contains(value, part) {
			return false
		}
	}
	return true
}

func contains(value, part string) bool {
	return len(part) == 0 || (len(value) >= len(part) && indexOf(value, part) >= 0)
}

func indexOf(value, part string) int {
	for i := 0; i+len(part) <= len(value); i++ {
		if value[i:i+len(part)] == part {
			return i
		}
	}
	return -1
}
