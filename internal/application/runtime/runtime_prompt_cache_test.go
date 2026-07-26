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

// pruningScenario builds a planning-enabled scenario whose profile prunes tool
// schemas, optionally also asking for a cacheable prefix.
func pruningScenario(promptCache bool) core.Scenario {
	scenario := baseScenario(false)
	scenario.Orchestration.Planning = core.PlanningPolicy{Enabled: true, Execute: true}
	scenario.LLMs = map[string]core.LLMProfileRef{
		"default": {
			Provider:    "mock",
			Model:       "test",
			Context:     contextwindow.Policy{ToolSchemaPruning: true},
			PromptCache: llm.PromptCacheConfig{Enabled: promptCache},
		},
	}
	scenario.Tools = map[string]core.Tool{
		"echo":   {Name: "echo", Type: "builtin.echo", Description: "Echo the input"},
		"repeat": {Name: "repeat", Type: "builtin.echo", Description: "Repeat the input"},
		"search": {Name: "search", Type: "builtin.echo", Description: "Search things"},
	}
	agent := scenario.Agents["assistant"]
	agent.Tools = []string{"echo", "repeat", "search"}
	scenario.Agents["assistant"] = agent
	return scenario
}

// saveRunWithPlannedTool persists a run whose next pending plan step names tool.
func saveRunWithPlannedTool(t *testing.T, repo *runstateinmem.Repository, runID, tool string) {
	t.Helper()
	planRaw, err := json.Marshal(planExecutionState{
		Steps: []planExecutionStep{{Goal: "call " + tool, Tool: tool, Status: "pending"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := repo.Load(context.Background(), runID)
	version := int64(0)
	if err == nil {
		version = snapshot.Version
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID:       runID,
		Status:      runstate.RunStatusRunning,
		StepOutputs: map[string]runstate.StepOutputRef{"plan": {Inline: planRaw}},
	}, version); err != nil {
		t.Fatal(err)
	}
}

func specNames(specs []llm.ToolSpec) []string {
	names := make([]string, 0, len(specs))
	for _, spec := range specs {
		names = append(names, spec.Name)
	}
	return names
}

// The tool catalog is the very front of the prompt. Narrowing it as the plan
// advances rewrites the prefix on every turn, so the cache behind it is never
// hit and the system prompt plus the whole conversation get re-billed at full
// price to save a couple of schemas.
func TestToolCatalogStaysStableAcrossTurnsWhenPromptCacheEnabled(t *testing.T) {
	scenario := pruningScenario(true)
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(scenario, Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	agent := scenario.Agents["assistant"]

	saveRunWithPlannedTool(t, repo, "run-cache-stable", "echo")
	turnOne := specNames(engine.toolSpecs(ctx, "run-cache-stable", agent))

	// The plan advances to a different tool, which is exactly when pruning
	// would have rewritten the catalog.
	saveRunWithPlannedTool(t, repo, "run-cache-stable", "search")
	turnTwo := specNames(engine.toolSpecs(ctx, "run-cache-stable", agent))

	if len(turnOne) != 3 {
		t.Fatalf("expected the full catalog on turn one, got %v", turnOne)
	}
	if len(turnTwo) != len(turnOne) {
		t.Fatalf("tool catalog churned between turns: %v then %v", turnOne, turnTwo)
	}
	for i := range turnOne {
		if turnOne[i] != turnTwo[i] {
			t.Fatalf("tool catalog churned between turns: %v then %v", turnOne, turnTwo)
		}
	}
}

// Without prompt caching the pruning behaviour is unchanged, so profiles that
// rely on it keep the smaller prompt.
func TestToolCatalogStillPrunesWhenPromptCacheDisabled(t *testing.T) {
	scenario := pruningScenario(false)
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(scenario, Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	agent := scenario.Agents["assistant"]

	saveRunWithPlannedTool(t, repo, "run-cache-off", "echo")
	names := specNames(engine.toolSpecs(ctx, "run-cache-off", agent))
	if len(names) != 1 || names[0] != "echo" {
		t.Fatalf("expected pruning to restrict the catalog to the planned tool, got %v", names)
	}
}

// Enabling the cache must not quietly drop tools the agent is allowed to use.
func TestPromptCacheKeepsEveryDeclaredTool(t *testing.T) {
	scenario := pruningScenario(true)
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(scenario, Dependencies{Runs: repo})
	if err != nil {
		t.Fatal(err)
	}
	saveRunWithPlannedTool(t, repo, "run-cache-full", "echo")
	names := specNames(engine.toolSpecs(context.Background(), "run-cache-full", scenario.Agents["assistant"]))
	for _, want := range []string{"echo", "repeat", "search"} {
		found := false
		for _, got := range names {
			if got == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("expected tool %q to stay exposed, got %v", want, names)
		}
	}
}
