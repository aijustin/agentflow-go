package runtime

import (
	"context"
	"encoding/json"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
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

	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(scenario, Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{RunID: "run-plan-specs", Status: runstate.RunStatusRunning}, 0); err != nil {
		t.Fatal(err)
	}

	specs := engine.toolSpecs(ctx, "run-plan-specs", agent)
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
		t.Fatalf("expected regular tool %q to remain present when no plan step names a tool, got specs %+v", "echo", specs)
	}
}

// TestToolSpecsPrunesToPlannedToolWhenPersistedPlanNamesOne verifies that
// schema pruning actually restricts the exposed tool schema to the next
// pending plan step's tool, instead of always returning every tool the
// agent has (which is equivalent to no pruning at all).
func TestToolSpecsPrunesToPlannedToolWhenPersistedPlanNamesOne(t *testing.T) {
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
		"echo":   {Name: "echo", Type: "builtin.echo", Description: "Echo the input"},
		"repeat": {Name: "repeat", Type: "builtin.echo", Description: "Repeat the input"},
	}
	agent := scenario.Agents["assistant"]
	agent.Tools = []string{"echo", "repeat"}
	scenario.Agents["assistant"] = agent

	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(scenario, Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	planState := planExecutionState{Steps: []planExecutionStep{{Goal: "call echo", Tool: "echo", Status: "pending"}}}
	planRaw, err := json.Marshal(planState)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID:  "run-plan-prune",
		Status: runstate.RunStatusRunning,
		StepOutputs: map[string]runstate.StepOutputRef{
			"plan": {Inline: planRaw},
		},
	}, 0); err != nil {
		t.Fatal(err)
	}

	specs := engine.toolSpecs(ctx, "run-plan-prune", agent)
	if len(specs) != 1 || specs[0].Name != "echo" {
		t.Fatalf("expected pruning to restrict to the planned tool %q only, got specs %+v", "echo", specs)
	}
}

func TestAppendPlanningHintDeduplicates(t *testing.T) {
	hint := "Next planned step prefers tool \"echo\": call echo"
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "hello"},
	}
	once := appendPlanningHint(messages, hint)
	if len(once) != 2 || once[0].Content != hint {
		t.Fatalf("expected hint prepended once, got %+v", once)
	}
	twice := appendPlanningHint(once, hint)
	if len(twice) != 2 {
		t.Fatalf("expected duplicate hint to be skipped, got %d messages", len(twice))
	}
	if twice[0].Content != hint {
		t.Fatalf("expected first message to remain the hint, got %q", twice[0].Content)
	}
}
