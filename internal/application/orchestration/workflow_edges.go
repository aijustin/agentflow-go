package orchestration

import (
	"context"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func incomingEdges(workflow core.Workflow, nodeID string) []core.WorkflowEdge {
	incoming := make([]core.WorkflowEdge, 0)
	for _, edge := range workflow.Edges {
		if edge.To == nodeID {
			incoming = append(incoming, edge)
		}
	}
	return incoming
}

func (r *WorkflowRunner) nodeShouldSkip(ctx context.Context, runID string, workflow core.Workflow, nodeID string) (bool, error) {
	incoming := incomingEdges(workflow, nodeID)
	if len(incoming) == 0 {
		return false, nil
	}
	hasConditional := false
	anySatisfied := false
	for _, edge := range incoming {
		switch classifyEdgeCondition(edge.Condition) {
		case edgeCondUnconditional:
			anySatisfied = true
		case edgeCondAlwaysFalse:
			hasConditional = true
		case edgeCondDynamic:
			hasConditional = true
			ok, err := r.conditionEnabled(ctx, runID, edge.Condition)
			if err != nil {
				return false, err
			}
			if ok {
				anySatisfied = true
			}
		}
	}
	return hasConditional && !anySatisfied, nil
}

type edgeCondKind int

const (
	edgeCondUnconditional edgeCondKind = iota
	edgeCondAlwaysFalse
	edgeCondDynamic
)

func classifyEdgeCondition(condition string) edgeCondKind {
	switch strings.TrimSpace(condition) {
	case "", "true", "always":
		return edgeCondUnconditional
	case "false", "never":
		return edgeCondAlwaysFalse
	default:
		return edgeCondDynamic
	}
}
