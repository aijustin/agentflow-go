package graph

import (
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// ImportWorkflow converts a Studio graph view into a core workflow DAG.
func ImportWorkflow(view GraphView) (core.Workflow, error) {
	if len(view.Nodes) == 0 {
		return core.Workflow{}, fmt.Errorf("graph: workflow requires at least one node")
	}
	seen := make(map[string]struct{}, len(view.Nodes))
	nodes := make([]core.WorkflowNode, 0, len(view.Nodes))
	for _, node := range view.Nodes {
		id := strings.TrimSpace(node.ID)
		if id == "" {
			return core.Workflow{}, fmt.Errorf("graph: workflow node id is required")
		}
		if _, ok := seen[id]; ok {
			return core.Workflow{}, fmt.Errorf("graph: duplicate workflow node %q", id)
		}
		seen[id] = struct{}{}
		kind := core.WorkflowNodeKind(strings.TrimSpace(node.Kind))
		if kind == "" {
			return core.Workflow{}, fmt.Errorf("graph: workflow node %q kind is required", id)
		}
		nodes = append(nodes, core.WorkflowNode{
			ID:        id,
			Kind:      kind,
			Ref:       strings.TrimSpace(node.Ref),
			Input:     cloneJSON(node.Input),
			Condition: strings.TrimSpace(node.Condition),
			DependsOn: append([]string(nil), node.DependsOn...),
		})
	}
	edges := make([]core.WorkflowEdge, 0, len(view.Edges))
	for _, edge := range view.Edges {
		from := strings.TrimSpace(edge.From)
		to := strings.TrimSpace(edge.To)
		if from == "" || to == "" {
			return core.Workflow{}, fmt.Errorf("graph: workflow edge requires from and to")
		}
		if _, ok := seen[from]; !ok {
			return core.Workflow{}, fmt.Errorf("graph: workflow edge references unknown node %q", from)
		}
		if _, ok := seen[to]; !ok {
			return core.Workflow{}, fmt.Errorf("graph: workflow edge references unknown node %q", to)
		}
		edges = append(edges, core.WorkflowEdge{
			From:      from,
			To:        to,
			Condition: strings.TrimSpace(edge.Condition),
		})
	}
	return core.Workflow{Nodes: nodes, Edges: edges}, nil
}

// ApplyGraph merges a Studio graph into a scenario, replacing the main workflow
// and any named subgraph workflows present in the graph payload.
func ApplyGraph(base core.Scenario, graph ScenarioGraph) (core.Scenario, error) {
	out := base
	if graph.Name != "" {
		out.Name = strings.TrimSpace(graph.Name)
	}
	if graph.Mode != "" {
		out.Orchestration.Mode = core.OrchestrationMode(strings.TrimSpace(graph.Mode))
	}
	if graph.Workflow != nil {
		wf, err := ImportWorkflow(*graph.Workflow)
		if err != nil {
			return core.Scenario{}, err
		}
		out.Orchestration.Workflow = &wf
	}
	if len(graph.Workflows) > 0 {
		if out.Orchestration.Workflows == nil {
			out.Orchestration.Workflows = make(map[string]core.Workflow, len(graph.Workflows))
		}
		for name, view := range graph.Workflows {
			wf, err := ImportWorkflow(view)
			if err != nil {
				return core.Scenario{}, fmt.Errorf("graph: subgraph %q: %w", name, err)
			}
			out.Orchestration.Workflows[name] = wf
		}
	}
	return out, nil
}
