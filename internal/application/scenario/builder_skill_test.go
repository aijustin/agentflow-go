package scenario

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestExpandWorkflowSkillNodes(t *testing.T) {
	scenario := core.Scenario{
		Name: "skill-node",
		Skills: map[string]core.Skill{
			"research": {Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{ID: "fetch", Kind: core.NodeTransform, Input: []byte(`{"set":{"ok":true}}`)}},
				Edges: []core.WorkflowEdge{},
			}},
		},
		Orchestration: core.Orchestration{Workflow: &core.Workflow{
			Nodes: []core.WorkflowNode{
				{ID: "skill_step", Kind: core.NodeSkill, Ref: "research"},
				{ID: "after", Kind: core.NodeTransform, DependsOn: []string{"skill_step"}, Input: []byte(`{"set":{"next":true}}`)},
			},
		}},
	}
	expanded, err := expandWorkflowSkillNodes(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if err := validateNoSkillNodes(expanded); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, node := range expanded.Orchestration.Workflow.Nodes {
		if node.ID == "skill_step.fetch" {
			found = true
		}
		if node.ID == "skill_step" {
			t.Fatal("skill node should be expanded away")
		}
	}
	if !found {
		t.Fatal("expected expanded skill workflow node")
	}
}
