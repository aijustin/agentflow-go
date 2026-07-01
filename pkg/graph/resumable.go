package graph

import (
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type loopSpec struct {
	Body []string `json:"body"`
}

func loopBodyNodeIDs(workflow core.Workflow) (map[string]bool, error) {
	ids := make(map[string]bool)
	for _, node := range workflow.Nodes {
		if node.Kind != core.NodeLoop || len(node.Input) == 0 {
			continue
		}
		var spec loopSpec
		if err := json.Unmarshal(node.Input, &spec); err != nil {
			return nil, fmt.Errorf("graph: loop node %q decode input: %w", node.ID, err)
		}
		for _, bodyID := range spec.Body {
			ids[bodyID] = true
		}
	}
	return ids, nil
}

func nodeResumeMeta(workflow core.Workflow, node core.WorkflowNode) (bool, string) {
	switch node.Kind {
	case core.NodeHumanGate:
		return false, "human_gate nodes require POST /v1/hitl/resume"
	}
	if node.Interrupt {
		return false, "interrupt nodes require POST /v1/hitl/resume after review"
	}
	bodyOnly, err := loopBodyNodeIDs(workflow)
	if err != nil || bodyOnly[node.ID] {
		if err != nil {
			return false, err.Error()
		}
		return false, "loop body nodes cannot be resumed; resume from the loop node instead"
	}
	return true, ""
}
