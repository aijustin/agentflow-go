package catalog

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestSkillSpecWorkflowToCore(t *testing.T) {
	spec := skillSpec{
		Description: "research skill",
		Version:     "1.0",
		Workflow: &workflowSpec{
			Nodes: []workflowNodeSpec{{ID: "start", Kind: string(core.NodeTransform)}},
			Edges: []workflowEdgeSpec{{From: "start", To: "start"}},
		},
		PromptFragments: []promptFragmentSpec{{Name: "intro", Content: "hello"}},
		ToolPolicies: []skillToolPolicySpec{{
			Tool:       "echo",
			Approval:   string(core.ApprovalNever),
			SideEffect: string(core.SideEffectRead),
			RateCap:    3,
		}},
	}
	skill, err := spec.toCore("research")
	if err != nil {
		t.Fatal(err)
	}
	if skill.Workflow == nil || len(skill.Workflow.Nodes) != 1 {
		t.Fatalf("expected workflow nodes, got %+v", skill.Workflow)
	}
	if len(skill.PromptFragments) != 1 || len(skill.ToolPolicies) != 1 {
		t.Fatalf("unexpected skill payload: %+v", skill)
	}
}
