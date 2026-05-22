package graph

import (
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestNodeResumeMetaHumanGate(t *testing.T) {
	workflow := core.Workflow{Nodes: []core.WorkflowNode{{ID: "gate", Kind: core.NodeHumanGate}}}
	resumable, hint := nodeResumeMeta(workflow, workflow.Nodes[0])
	if resumable || hint == "" {
		t.Fatalf("human_gate resumable=%v hint=%q", resumable, hint)
	}
}

func TestNodeResumeMetaLoopBody(t *testing.T) {
	workflow := core.Workflow{Nodes: []core.WorkflowNode{
		{ID: "loop", Kind: core.NodeLoop, Input: json.RawMessage(`{"body":["body_step"]}`)},
		{ID: "body_step", Kind: core.NodeTransform},
	}}
	resumable, hint := nodeResumeMeta(workflow, workflow.Nodes[1])
	if resumable || hint == "" {
		t.Fatalf("loop body resumable=%v hint=%q", resumable, hint)
	}
}
