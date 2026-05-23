package graph

import (
	"encoding/json"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// ScenarioGraph is a Studio-friendly view of orchestration topology.
type ScenarioGraph struct {
	Name      string               `json:"name"`
	Mode      string               `json:"mode"`
	Workflow  *GraphView           `json:"workflow,omitempty"`
	Workflows map[string]GraphView `json:"workflows,omitempty"`
}

// GraphView describes one workflow DAG.
type GraphView struct {
	ID     string                    `json:"id,omitempty"`
	Nodes  []GraphNode               `json:"nodes"`
	Edges  []GraphEdge               `json:"edges"`
	Layout map[string]GraphPosition  `json:"layout,omitempty"`
}

// GraphPosition stores a Studio canvas coordinate for a node.
type GraphPosition struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// GraphNode is a workflow node for visualization.
type GraphNode struct {
	ID         string          `json:"id"`
	Kind       string          `json:"kind"`
	Ref        string          `json:"ref,omitempty"`
	Input      json.RawMessage `json:"input,omitempty"`
	Condition  string          `json:"condition,omitempty"`
	DependsOn  []string        `json:"depends_on,omitempty"`
	Resumable  bool            `json:"resumable"`
	ResumeHint string          `json:"resume_hint,omitempty"`
}

// GraphEdge connects two workflow nodes.
type GraphEdge struct {
	From      string `json:"from"`
	To        string `json:"to"`
	Condition string `json:"condition,omitempty"`
}

// ExportScenario builds a nested graph from a core.Scenario.
func ExportScenario(scenario core.Scenario) ScenarioGraph {
	out := ScenarioGraph{
		Name: scenario.Name,
		Mode: string(scenario.Orchestration.Mode),
	}
	if scenario.Orchestration.Workflow != nil {
		view := exportWorkflow("", *scenario.Orchestration.Workflow)
		out.Workflow = &view
	}
	if len(scenario.Orchestration.Workflows) > 0 {
		out.Workflows = make(map[string]GraphView, len(scenario.Orchestration.Workflows))
		for name, wf := range scenario.Orchestration.Workflows {
			out.Workflows[name] = exportWorkflow(name, wf)
		}
	}
	return out
}

func exportWorkflow(id string, workflow core.Workflow) GraphView {
	view := GraphView{
		ID:    id,
		Nodes: make([]GraphNode, 0, len(workflow.Nodes)),
		Edges: make([]GraphEdge, 0, len(workflow.Edges)),
	}
	for _, node := range workflow.Nodes {
		resumable, hint := nodeResumeMeta(workflow, node)
		view.Nodes = append(view.Nodes, GraphNode{
			ID:         node.ID,
			Kind:       string(node.Kind),
			Ref:        node.Ref,
			Input:      cloneJSON(node.Input),
			Condition:  strings.TrimSpace(node.Condition),
			DependsOn:  append([]string(nil), node.DependsOn...),
			Resumable:  resumable,
			ResumeHint: hint,
		})
	}
	for _, edge := range workflow.Edges {
		view.Edges = append(view.Edges, GraphEdge{
			From:      edge.From,
			To:        edge.To,
			Condition: strings.TrimSpace(edge.Condition),
		})
	}
	return view
}

func cloneJSON(raw json.RawMessage) json.RawMessage {
	if raw == nil {
		return nil
	}
	out := make([]byte, len(raw))
	copy(out, raw)
	return out
}
