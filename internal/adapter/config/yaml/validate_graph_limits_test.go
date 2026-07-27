package yaml

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func baseScenario() core.Scenario {
	return core.Scenario{
		Name:   "graph-limits",
		Agents: map[string]core.Agent{"worker": {}},
	}
}

func mustJSON(t *testing.T, value any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal input: %v", err)
	}
	return raw
}

// A subgraph that references itself used to recurse until the goroutine stack
// overflowed, which kills the whole process rather than returning an error.
func TestValidateRejectsSelfReferentialSubgraph(t *testing.T) {
	s := baseScenario()
	s.Orchestration = core.Orchestration{
		Mode: core.OrchestrationFixedWorkflow,
		Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
			{ID: "entry", Kind: core.NodeSubgraph, Ref: "loopy"},
		}},
		Workflows: map[string]core.Workflow{
			"loopy": {Nodes: []core.WorkflowNode{{ID: "inner", Kind: core.NodeSubgraph, Ref: "loopy"}}},
		},
	}
	err := Validate(s)
	if err == nil {
		t.Fatal("expected recursive subgraph to be rejected")
	}
	if !strings.Contains(err.Error(), "recursive") {
		t.Fatalf("expected recursion error, got %v", err)
	}
}

// Mutual recursion between two subgraphs is the same hazard one hop out.
func TestValidateRejectsMutuallyRecursiveSubgraphs(t *testing.T) {
	s := baseScenario()
	s.Orchestration = core.Orchestration{
		Mode: core.OrchestrationFixedWorkflow,
		Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
			{ID: "entry", Kind: core.NodeSubgraph, Ref: "a"},
		}},
		Workflows: map[string]core.Workflow{
			"a": {Nodes: []core.WorkflowNode{{ID: "a1", Kind: core.NodeSubgraph, Ref: "b"}}},
			"b": {Nodes: []core.WorkflowNode{{ID: "b1", Kind: core.NodeSubgraph, Ref: "a"}}},
		},
	}
	err := Validate(s)
	if err == nil {
		t.Fatal("expected mutually recursive subgraphs to be rejected")
	}
	if !strings.Contains(err.Error(), "recursive") {
		t.Fatalf("expected recursion error, got %v", err)
	}
}

// A deep but acyclic chain must be bounded too, otherwise a long enough chain
// reproduces the stack overflow without any cycle.
func TestValidateRejectsSubgraphChainDeeperThanLimit(t *testing.T) {
	s := baseScenario()
	workflows := map[string]core.Workflow{}
	depth := MaxSubgraphDepth + 5
	for i := 0; i < depth; i++ {
		name := fmt.Sprintf("w%d", i)
		if i == depth-1 {
			workflows[name] = core.Workflow{Nodes: []core.WorkflowNode{{ID: "leaf", Kind: core.NodeAgent, Ref: "worker"}}}
			continue
		}
		workflows[name] = core.Workflow{Nodes: []core.WorkflowNode{
			{ID: "next", Kind: core.NodeSubgraph, Ref: fmt.Sprintf("w%d", i+1)},
		}}
	}
	s.Orchestration = core.Orchestration{
		Mode:      core.OrchestrationFixedWorkflow,
		Workflow:  &core.Workflow{Nodes: []core.WorkflowNode{{ID: "entry", Kind: core.NodeSubgraph, Ref: "w0"}}},
		Workflows: workflows,
	}
	err := Validate(s)
	if err == nil {
		t.Fatal("expected over-deep subgraph chain to be rejected")
	}
	if !strings.Contains(err.Error(), "max subgraph depth") {
		t.Fatalf("expected depth error, got %v", err)
	}
}

func TestValidateAcceptsSubgraphChainWithinLimit(t *testing.T) {
	s := baseScenario()
	workflows := map[string]core.Workflow{
		"leaf": {Nodes: []core.WorkflowNode{{ID: "leaf", Kind: core.NodeAgent, Ref: "worker"}}},
		"mid":  {Nodes: []core.WorkflowNode{{ID: "mid", Kind: core.NodeSubgraph, Ref: "leaf"}}},
	}
	s.Orchestration = core.Orchestration{
		Mode:      core.OrchestrationFixedWorkflow,
		Workflow:  &core.Workflow{Nodes: []core.WorkflowNode{{ID: "entry", Kind: core.NodeSubgraph, Ref: "mid"}}},
		Workflows: workflows,
	}
	if err := Validate(s); err != nil {
		t.Fatalf("expected nested subgraphs within the depth limit to validate, got %v", err)
	}
}

// The same subgraph reached twice through sibling branches is a diamond, not a
// cycle, and must stay valid.
func TestValidateAcceptsDiamondSubgraphReuse(t *testing.T) {
	s := baseScenario()
	s.Orchestration = core.Orchestration{
		Mode: core.OrchestrationFixedWorkflow,
		Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
			{ID: "left", Kind: core.NodeSubgraph, Ref: "shared"},
			{ID: "right", Kind: core.NodeSubgraph, Ref: "shared"},
		}},
		Workflows: map[string]core.Workflow{
			"shared": {Nodes: []core.WorkflowNode{{ID: "leaf", Kind: core.NodeAgent, Ref: "worker"}}},
		},
	}
	if err := Validate(s); err != nil {
		t.Fatalf("expected reused (non-recursive) subgraph to validate, got %v", err)
	}
}

// A loop node inside a skill workflow used to dereference the (absent) main
// workflow and panic with a nil pointer.
func TestValidateLoopNodeInSkillWorkflowWithoutMainWorkflow(t *testing.T) {
	s := baseScenario()
	s.Skills = map[string]core.Skill{
		"researcher": {Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
			{ID: "body", Kind: core.NodeAgent, Ref: "worker"},
			{ID: "loop", Kind: core.NodeLoop, Input: mustJSON(t, map[string]any{"body": []string{"body"}})},
		}}},
	}
	if err := Validate(s); err != nil {
		t.Fatalf("expected loop node resolved against its own workflow to validate, got %v", err)
	}
}

// Loop bodies must resolve against the enclosing workflow, so a body id that
// only exists in the main workflow is a dangling reference.
func TestValidateLoopNodeBodyIsScopedToEnclosingWorkflow(t *testing.T) {
	s := baseScenario()
	s.Skills = map[string]core.Skill{
		"researcher": {Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
			{ID: "loop", Kind: core.NodeLoop, Input: mustJSON(t, map[string]any{"body": []string{"main_only"}})},
		}}},
	}
	s.Orchestration = core.Orchestration{
		Mode: core.OrchestrationFixedWorkflow,
		Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
			{ID: "main_only", Kind: core.NodeAgent, Ref: "worker"},
		}},
	}
	err := Validate(s)
	if err == nil {
		t.Fatal("expected cross-workflow loop body reference to be rejected")
	}
	if !strings.Contains(err.Error(), "unknown body node") {
		t.Fatalf("expected dangling body node error, got %v", err)
	}
}

func TestValidateRejectsWorkflowExceedingNodeLimit(t *testing.T) {
	s := baseScenario()
	nodes := make([]core.WorkflowNode, 0, MaxWorkflowNodes+1)
	for i := 0; i <= MaxWorkflowNodes; i++ {
		nodes = append(nodes, core.WorkflowNode{ID: fmt.Sprintf("n%d", i), Kind: core.NodeAgent, Ref: "worker"})
	}
	s.Orchestration = core.Orchestration{
		Mode:     core.OrchestrationFixedWorkflow,
		Workflow: &core.Workflow{Nodes: nodes},
	}
	err := Validate(s)
	if err == nil {
		t.Fatal("expected oversized workflow to be rejected")
	}
	if !strings.Contains(err.Error(), "limit is") {
		t.Fatalf("expected node limit error, got %v", err)
	}
}
