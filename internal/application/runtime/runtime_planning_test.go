package runtime

import (
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
)

// TestToolSpecsRetainsSubAgentDelegateToolsUnderSchemaPruning guards against
// the schema-pruning feature silently taking away an agent's ability to
// delegate to its sub-agents: delegate tools are synthesized (not part of
// agent.Tools), so planAllowedTools must add them to the allow-list itself.
func TestToolSpecsRetainsSubAgentDelegateToolsUnderSchemaPruning(t *testing.T) {
	scenario := baseScenario(false)
	scenario.Orchestration.Planning = core.PlanningPolicy{Enabled: true, Execute: true}
	scenario.LLMs = map[string]core.LLMProfileRef{
		"default": {
			Provider: "mock",
			Model:    "test",
			Context:  contextwindow.Policy{ToolSchemaPruning: true},
		},
	}
	scenario.Tools = map[string]core.Tool{
		"echo": {Name: "echo", Type: "builtin.echo", Description: "Echo the input"},
	}
	scenario.Agents["helper"] = core.Agent{Name: "helper", LLM: "default", Instructions: "help"}
	agent := scenario.Agents["assistant"]
	agent.Tools = []string{"echo"}
	agent.SubAgents = []string{"helper"}
	scenario.Agents["assistant"] = agent

	engine, err := NewEngine(scenario, Dependencies{Runs: runstateinmem.NewRepository()})
	if err != nil {
		t.Fatal(err)
	}

	specs := engine.toolSpecs(agent)
	wantDelegate := delegateToolName("helper")
	found := false
	for _, spec := range specs {
		if spec.Name == wantDelegate {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected delegate tool %q to survive schema pruning, got specs %+v", wantDelegate, specs)
	}
	foundEcho := false
	for _, spec := range specs {
		if spec.Name == "echo" {
			foundEcho = true
		}
	}
	if !foundEcho {
		t.Fatalf("expected regular tool %q to remain present, got specs %+v", "echo", specs)
	}
}
