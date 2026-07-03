package catalog

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestToolSpecToCore(t *testing.T) {
	spec := toolSpec{
		Type:         "builtin.echo",
		Description:  "echo tool",
		InputSchema:  map[string]any{"type": "object"},
		OutputSchema: map[string]any{"type": "object"},
		SideEffect:   string(core.SideEffectRead),
		Approval:     string(core.ApprovalNever),
		RateCap:      2,
		Metadata:     map[string]string{"team": "platform"},
	}
	tool, err := spec.toCore("echo")
	if err != nil {
		t.Fatal(err)
	}
	if tool.Name != "echo" || tool.RateCap != 2 || tool.SideEffect != core.SideEffectRead {
		t.Fatalf("unexpected tool: %+v", tool)
	}
}

func TestAgentPolicySpecToCore(t *testing.T) {
	spec := skillSpec{
		AgentPolicy: agentPolicySpec{
			MaxSteps:         5,
			RetryLimit:       2,
			OutputSchema:     map[string]any{"type": "object"},
			HumanCheckpoints: []string{"before_final_answer"},
		},
	}
	skill, err := spec.toCore("review")
	if err != nil {
		t.Fatal(err)
	}
	if skill.AgentPolicy.MaxSteps != 5 || len(skill.AgentPolicy.HumanCheckpoints) != 1 {
		t.Fatalf("unexpected agent policy: %+v", skill.AgentPolicy)
	}
}
