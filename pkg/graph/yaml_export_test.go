package graph

import (
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestGenerateScenarioYAML(t *testing.T) {
	scenario := core.Scenario{
		Name: "yaml-export",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{ID: "a", Kind: core.NodeTransform}},
			},
		},
	}
	out, err := GenerateScenarioYAML(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "yaml-export") || !strings.Contains(out, "orchestration") {
		t.Fatalf("unexpected yaml: %s", out)
	}
}
