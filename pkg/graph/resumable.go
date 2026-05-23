package graph

import (
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type loopSpec struct {
	Body []string `json:"body"`
}

func loopBodyNodeIDs(workflow core.Workflow) map[string]bool {
	ids := make(map[string]bool)
	for _, node := range workflow.Nodes {
		if node.Kind != core.NodeLoop || len(node.Input) == 0 {
			continue
		}
		var spec loopSpec
		if err := json.Unmarshal(node.Input, &spec); err != nil {
			continue
		}
		for _, bodyID := range spec.Body {
			ids[bodyID] = true
		}
	}
	return ids
}

func nodeResumeMeta(workflow core.Workflow, node core.WorkflowNode) (bool, string) {
	switch node.Kind {
	case core.NodeHumanGate:
		return false, "human_gate nodes require POST /v1/hitl/resume"
	}
	if node.Interrupt {
		return false, "interrupt nodes require POST /v1/hitl/resume after review"
	}
	if loopBodyNodeIDs(workflow)[node.ID] {
		return false, "loop body nodes cannot be resumed; resume from the loop node instead"
	}
	return true, ""
}
