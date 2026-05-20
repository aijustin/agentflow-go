package yaml

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestValidateHybridWorkflow(t *testing.T) {
	err := Validate(core.Scenario{
		Name: "hybrid",
		Agents: map[string]core.Agent{
			"assistant": {LLM: "mock"},
		},
		LLMs: map[string]core.LLMProfileRef{
			"mock": {Provider: "mock", Model: "test"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{ID: "missing", Kind: core.NodeAgent, Ref: "unknown"}},
			},
		},
	})
	if err == nil {
		t.Fatal("expected hybrid workflow validation error")
	}
}
