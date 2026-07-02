package builder_test

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/builder"
	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestMapNodeInputOptions(t *testing.T) {
	raw := builder.MapNodeInput("items", builder.MapBranch{
		Kind: core.NodeTransform,
		Ref:  "child",
	}, builder.MapOnError("collect_errors"), builder.MapItemField("element"))
	var spec struct {
		ItemsPath string `json:"items_path"`
		OnError   string `json:"on_error"`
		ItemField string `json:"item_field"`
	}
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	if spec.ItemsPath != "items" || spec.OnError != "collect_errors" || spec.ItemField != "element" {
		t.Fatalf("unexpected map spec: %+v", spec)
	}
}

func TestWorkflowBuilderAdvancedNodes(t *testing.T) {
	mapInput := builder.MapNodeInput("items", builder.MapBranch{Kind: core.NodeTransform})
	loopInput := json.RawMessage(`{"max_iterations":3}`)
	wf := builder.NewWorkflow().
		NodeSubgraph("sub", "nested").
		NodeMap("map", mapInput).
		NodeLoop("loop", loopInput).
		Build()
	if len(wf.Nodes) != 3 {
		t.Fatalf("nodes=%d", len(wf.Nodes))
	}
	if wf.Nodes[0].Kind != core.NodeSubgraph || wf.Nodes[0].Ref != "nested" {
		t.Fatalf("subgraph node=%+v", wf.Nodes[0])
	}
	if wf.Nodes[1].Kind != core.NodeMap || wf.Nodes[2].Kind != core.NodeLoop {
		t.Fatalf("map/loop kinds=%+v %v", wf.Nodes[1].Kind, wf.Nodes[2].Kind)
	}
}
