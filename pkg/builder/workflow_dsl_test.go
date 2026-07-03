package builder_test

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/builder"
)

func TestWorkflowDSLConditions(t *testing.T) {
	if got := builder.ConditionEq(builder.StepPath("status", "output", "message"), "ready"); got != `eq(steps.status.output.message, "ready")` {
		t.Fatalf("ConditionEq = %q", got)
	}
	if got := builder.ConditionNe(builder.StepPath("flag", "output", "ok"), true); got != "ne(steps.flag.output.ok, true)" {
		t.Fatalf("ConditionNe = %q", got)
	}
	if got := builder.ConditionExists(builder.StepPath("prep", "output")); got != "exists(steps.prep.output)" {
		t.Fatalf("ConditionExists = %q", got)
	}
}

func TestMapOver(t *testing.T) {
	wf := builder.NewWorkflow().
		NodeTransform("items", json.RawMessage(`{"set":{"list":["a","b"]}}`)).
		MapOver("fanout", builder.StepPath("items", "list"), builder.MapAgentBranch("analyst"), builder.MapOnError("collect_errors")).
		Edge("items", "fanout").
		Build()
	if len(wf.Nodes) != 2 || wf.Nodes[1].Kind != builder.NodeMap {
		t.Fatalf("nodes = %+v", wf.Nodes)
	}
	if len(wf.Edges) != 1 {
		t.Fatalf("edges = %+v", wf.Edges)
	}
}

func TestRouteIf(t *testing.T) {
	wf := builder.NewWorkflow().
		NodeTransform("status", json.RawMessage(`{"set":{"message":"ready"}}`)).
		NodeTransform("ready_branch", json.RawMessage(`{"set":{"ok":true}}`)).
		RouteIf("status", "ready_branch", builder.StepPath("status", "output", "message"), "ready").
		Build()
	if len(wf.Edges) != 1 || wf.Edges[0].Condition != `eq(steps.status.output.message, "ready")` {
		t.Fatalf("edges = %+v", wf.Edges)
	}
}

func TestParallelGroupSugar(t *testing.T) {
	wf := builder.NewWorkflow().
		ParallelGroup("experts", "macro", "sector").
		Build()
	if len(wf.Nodes) != 1 || wf.Nodes[0].Kind != builder.NodeParallelGroup {
		t.Fatalf("nodes = %+v", wf.Nodes)
	}
}

func TestRouteIfNe(t *testing.T) {
	wf := builder.NewWorkflow().
		RouteIfNe("a", "b", builder.StepPath("flag", "output", "ok"), true).
		Build()
	if len(wf.Edges) != 1 || wf.Edges[0].Condition != "ne(steps.flag.output.ok, true)" {
		t.Fatalf("edges = %+v", wf.Edges)
	}
}

func TestWorkflowDSLMissingAndMapBranches(t *testing.T) {
	if got := builder.ConditionMissing(builder.StepPath("prep", "output")); got != "missing(steps.prep.output)" {
		t.Fatalf("ConditionMissing = %q", got)
	}
	wf := builder.NewWorkflow().
		NodeTransform("items", json.RawMessage(`{"set":{"list":["a"]}}`)).
		MapOver("fanout", builder.StepPath("items", "list"), builder.MapToolBranch("echo"), builder.MapOnError("collect_errors")).
		RouteIfExists("items", "fanout", builder.StepPath("items", "output")).
		RouteIfMissing("fanout", "items", builder.StepPath("missing", "output")).
		Build()
	if len(wf.Nodes) != 2 || wf.Nodes[1].Kind != builder.NodeMap {
		t.Fatalf("nodes = %+v", wf.Nodes)
	}
	if len(wf.Edges) != 2 {
		t.Fatalf("edges = %+v", wf.Edges)
	}
}

func TestMapSubgraphBranchConfig(t *testing.T) {
	branch := builder.MapSubgraphBranch("prep")
	if branch.Kind != builder.NodeSubgraph || branch.Ref != "prep" {
		t.Fatalf("unexpected branch: %+v", branch)
	}
}
