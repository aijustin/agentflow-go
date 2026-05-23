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
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`), Condition: "steps.seed.output.ok", DependsOn: []string{"seed"}},
					{ID: "b", Kind: core.NodeAgent, Ref: "reviewer"},
				},
				Edges: []core.WorkflowEdge{{From: "a", To: "b", Condition: "steps.a.output.ready"}},
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
	if wf.Nodes[0].Condition != "steps.seed.output.ok" || len(wf.Nodes[0].DependsOn) != 1 {
		t.Fatalf("node metadata not preserved: %+v", wf.Nodes[0])
	}
	if wf.Edges[0].Condition != "steps.a.output.ready" {
		t.Fatalf("edge condition not preserved: %+v", wf.Edges[0])
	}
}

func TestGraphLayoutRoundTrip(t *testing.T) {
	view := GraphView{
		Nodes: []GraphNode{{ID: "a", Kind: "transform"}},
		Layout: map[string]GraphPosition{
			"a": {X: 120, Y: 48},
		},
	}
	raw, err := json.Marshal(view)
	if err != nil {
		t.Fatal(err)
	}
	var decoded GraphView
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Layout["a"].X != 120 || decoded.Layout["a"].Y != 48 {
		t.Fatalf("layout not preserved: %+v", decoded.Layout)
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
