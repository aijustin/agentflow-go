package yaml

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestValidateAutonomousWorkflowIsStillChecked(t *testing.T) {
	// Autonomous scenarios don't normally set orchestration.workflow, but
	// skill expansion (appscenario.Build) can synthesize one before
	// Validate runs; a malformed synthesized workflow (e.g. duplicate node
	// ids) must still be caught even though the mode is autonomous.
	err := Validate(core.Scenario{
		Name: "autonomous",
		Agents: map[string]core.Agent{
			"assistant": {LLM: "mock"},
		},
		LLMs: map[string]core.LLMProfileRef{
			"mock": {Provider: "mock", Model: "test"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationAutonomous,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "dup", Kind: core.NodeTransform},
					{ID: "dup", Kind: core.NodeTransform},
				},
			},
		},
	})
	if err == nil {
		t.Fatal("expected autonomous workflow validation error for duplicate node id")
	}
}

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
