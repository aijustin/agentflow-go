package builder_test

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestWorkflowBuilderAllNodeKinds(t *testing.T) {
	input := json.RawMessage(`{"members":["a","b"]}`)
	wf := builder.NewWorkflow().
		NodeTool("tool", "echo").
		NodeAgent("agent", "assistant").
		NodeSkill("skill", "review").
		NodeTransform("transform", json.RawMessage(`{"set":{"x":1}}`)).
		NodeHumanGate("gate").
		NodeParallelGroup("parallel", input).
		Edge("tool", "agent").
		EdgeIf("agent", "transform", "eq(steps.agent.ok,true)").
		DependsOn("tool").
		WithInterrupt().
		Build()
	if len(wf.Nodes) != 6 {
		t.Fatalf("nodes=%d", len(wf.Nodes))
	}
	if wf.Nodes[len(wf.Nodes)-1].Interrupt != true {
		t.Fatal("expected interrupt on last node")
	}
	if len(wf.Edges) != 2 || wf.Edges[1].Condition == "" {
		t.Fatalf("edges=%+v", wf.Edges)
	}
}

func TestInterruptNodeHelper(t *testing.T) {
	node := builder.InterruptNode(core.WorkflowNode{ID: "pause", Kind: core.NodeTransform})
	if !node.Interrupt {
		t.Fatal("expected interrupt flag")
	}
}
