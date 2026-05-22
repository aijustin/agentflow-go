package agentflow

import (
	"github.com/aijustin/agentflow-go/pkg/graph"
)

// ScenarioGraph is a Studio-friendly orchestration topology view.
type ScenarioGraph = graph.ScenarioGraph

// GraphView describes one workflow DAG for visualization.
type GraphView = graph.GraphView

// GraphNode is a workflow node in a GraphView.
type GraphNode = graph.GraphNode

// GraphEdge connects two GraphNodes.
type GraphEdge = graph.GraphEdge

// ExportScenarioGraph exports the framework scenario as a nested graph.
func (f *Framework) ExportScenarioGraph() ScenarioGraph {
	return graph.ExportScenario(f.scenario)
}
