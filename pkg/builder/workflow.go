package builder

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// WorkflowBuilder constructs a core.Workflow graph.
type WorkflowBuilder struct {
	wf core.Workflow
}

// NewWorkflow creates an empty workflow builder.
func NewWorkflow() *WorkflowBuilder {
	return &WorkflowBuilder{}
}

// NodeTool adds a tool node.
func (w *WorkflowBuilder) NodeTool(id, toolRef string) *WorkflowBuilder {
	w.wf.Nodes = append(w.wf.Nodes, core.WorkflowNode{
		ID:   id,
		Kind: core.NodeTool,
		Ref:  toolRef,
	})
	return w
}

// NodeAgent adds an agent node.
func (w *WorkflowBuilder) NodeAgent(id, agentRef string) *WorkflowBuilder {
	w.wf.Nodes = append(w.wf.Nodes, core.WorkflowNode{
		ID:   id,
		Kind: core.NodeAgent,
		Ref:  agentRef,
	})
	return w
}

// NodeSkill adds a skill node.
func (w *WorkflowBuilder) NodeSkill(id, skillRef string) *WorkflowBuilder {
	w.wf.Nodes = append(w.wf.Nodes, core.WorkflowNode{
		ID:   id,
		Kind: core.NodeSkill,
		Ref:  skillRef,
	})
	return w
}

// NodeTransform adds a transform node with optional JSON input payload.
func (w *WorkflowBuilder) NodeTransform(id string, input json.RawMessage) *WorkflowBuilder {
	w.wf.Nodes = append(w.wf.Nodes, core.WorkflowNode{
		ID:    id,
		Kind:  core.NodeTransform,
		Input: input,
	})
	return w
}

// NodeHumanGate adds a human gate node.
func (w *WorkflowBuilder) NodeHumanGate(id string) *WorkflowBuilder {
	w.wf.Nodes = append(w.wf.Nodes, core.WorkflowNode{
		ID:   id,
		Kind: core.NodeHumanGate,
	})
	return w
}

// NodeParallelGroup adds a parallel_group node.
func (w *WorkflowBuilder) NodeParallelGroup(id string, input json.RawMessage) *WorkflowBuilder {
	w.wf.Nodes = append(w.wf.Nodes, core.WorkflowNode{
		ID:    id,
		Kind:  core.NodeParallelGroup,
		Input: input,
	})
	return w
}

// NodeWithDepends adds a pre-built node and sets depends_on in one call.
func (w *WorkflowBuilder) NodeWithDepends(node core.WorkflowNode, deps ...string) *WorkflowBuilder {
	node.DependsOn = append([]string(nil), deps...)
	w.wf.Nodes = append(w.wf.Nodes, node)
	return w
}

// Edge adds a directed edge between nodes.
func (w *WorkflowBuilder) Edge(from, to string) *WorkflowBuilder {
	w.wf.Edges = append(w.wf.Edges, core.WorkflowEdge{From: from, To: to})
	return w
}

// EdgeIf adds a conditional edge.
func (w *WorkflowBuilder) EdgeIf(from, to, condition string) *WorkflowBuilder {
	w.wf.Edges = append(w.wf.Edges, core.WorkflowEdge{
		From:      from,
		To:        to,
		Condition: condition,
	})
	return w
}

// DependsOn sets depends_on for the most recently added node.
func (w *WorkflowBuilder) DependsOn(deps ...string) *WorkflowBuilder {
	if len(w.wf.Nodes) == 0 {
		return w
	}
	last := len(w.wf.Nodes) - 1
	w.wf.Nodes[last].DependsOn = append([]string(nil), deps...)
	return w
}

// WithInterrupt marks the most recently added node for post-step declarative pause.
func (w *WorkflowBuilder) WithInterrupt() *WorkflowBuilder {
	if len(w.wf.Nodes) == 0 {
		return w
	}
	w.wf.Nodes[len(w.wf.Nodes)-1].Interrupt = true
	return w
}

// InterruptNode returns node with Interrupt enabled.
func InterruptNode(node core.WorkflowNode) core.WorkflowNode {
	node.Interrupt = true
	return node
}

// Build returns the workflow graph.
func (w *WorkflowBuilder) Build() core.Workflow {
	return w.wf
}
