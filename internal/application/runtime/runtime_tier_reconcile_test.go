package runtime

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestEngineReconcileTierMemory(t *testing.T) {
	store := tierinmem.NewStore()
	manager := tier.NewManager(store, tier.Policy{HotCapacity: 1, WarmCapacity: 5}, tier.NoopMigrationObserver{})
	scenario := baseScenario(false)
	scenario.Memories = map[string]core.MemoryRef{
		"session": {
			Type:  "custom",
			Scope: string(memory.ScopeSession),
			Tiers: &core.MemoryTierSettings{Enabled: true, HotCapacity: 1, WarmCapacity: 5},
		},
	}
	agent := scenario.Agents["assistant"]
	agent.Memory = "session"
	scenario.Agents["assistant"] = agent
	engine, err := NewEngine(scenario, Dependencies{
		Runs: runstateinmem.NewRepository(),
		TierMemory: map[string]tier.Manager{
			"session": manager,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runID := "tier-reconcile-run"
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "session", Agent: "assistant"}
	now := time.Now().UTC()
	for _, id := range []string{"m1", "m2"} {
		if err := manager.Remember(ctx, ns, tier.Record{
			CognitiveRecord: memory.CognitiveRecord{ID: id, Content: id, CreatedAt: now},
			Tier:            tier.LevelHot,
			LastAccessAt:    now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := engine.ReconcileTierMemory(ctx, runID, "session", "assistant"); err != nil {
		t.Fatal(err)
	}
}

func TestEngineMaybeReplanInjectsPlanWhenIncomplete(t *testing.T) {
	scenario := baseScenario(false)
	scenario.Orchestration.Planning = core.PlanningPolicy{
		Enabled:         true,
		Execute:         true,
		ReplanOnFailure: true,
		Agent:           "assistant",
		MaxSteps:        3,
	}
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(scenario, Dependencies{
		Runs: repo,
		LLM:  &planningGateway{plan: json.RawMessage(`{"steps":[{"goal":"call echo","tool":"echo","status":"pending"}]}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	runID := "replan-run"
	state := planExecutionState{Steps: []planExecutionStep{{Tool: "echo", Status: "pending"}}}
	inline, _ := json.Marshal(state)
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID:  runID,
		Status: runstate.RunStatusRunning,
		StepOutputs: map[string]runstate.StepOutputRef{
			"plan": {Inline: inline},
		},
	}, 0); err != nil {
		t.Fatal(err)
	}
	agent := scenario.Agents["assistant"]
	profile := scenario.LLMs["default"]
	messages, err := engine.maybeReplan(ctx, runID, agent, profile, RunRequest{RunID: runID, Agent: agent.Name, Prompt: "go"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) == 0 {
		t.Fatal("expected replanned messages")
	}
}

func TestEngineRunStructuredCompletesRun(t *testing.T) {
	scenario := baseScenario(false)
	agent := scenario.Agents["assistant"]
	agent.Policy.OutputSchema = json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}}}`)
	scenario.Agents["assistant"] = agent
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(scenario, Dependencies{
		Runs: repo,
		LLM:  &structuredGateway{payload: json.RawMessage(`{"answer":"done"}`)},
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := engine.RunStructured(context.Background(), RunRequest{
		RunID: "structured-complete", Agent: "assistant", Prompt: "go",
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || string(result.StructuredOutput) != `{"answer":"done"}` {
		t.Fatalf("unexpected result: %+v", result)
	}
}

type structuredGateway struct {
	payload json.RawMessage
}

func (g *structuredGateway) Supports(string, llm.Capability) bool { return true }
func (g *structuredGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}
func (g *structuredGateway) StructuredChat(context.Context, string, json.RawMessage, llm.ChatRequest) (json.RawMessage, error) {
	return g.payload, nil
}

type planningGateway struct {
	plan json.RawMessage
}

func (g *planningGateway) Supports(string, llm.Capability) bool { return true }
func (g *planningGateway) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Message: llm.Message{Content: string(g.plan)}}, nil
}
func (g *planningGateway) StructuredChat(context.Context, string, json.RawMessage, llm.ChatRequest) (json.RawMessage, error) {
	return g.plan, nil
}

func TestEngineRunHybridCompletesExistingRun(t *testing.T) {
	repo := runstateinmem.NewRepository()
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs: repo,
		LLM:  &capturingGateway{response: "hybrid done"},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID:        "run-hybrid-complete",
		ScenarioName: "scenario",
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"input":            json.RawMessage(`{"steps":{"prep":{"ok":true}}}`),
			"execution_phase": json.RawMessage(`"autonomous"`),
		},
		StepOutputs: map[string]runstate.StepOutputRef{
			"prep": {Inline: json.RawMessage(`{"ok":true}`)},
		},
	}, 0); err != nil {
		t.Fatal(err)
	}
	result, err := engine.RunHybrid(ctx, RunRequest{RunID: "run-hybrid-complete", Agent: "assistant", Prompt: "continue"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted || result.Output != "hybrid done" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestCompletionConflictErrorMessage(t *testing.T) {
	err := completionConflictError{status: runstate.RunStatusFailed}
	if err.Error() == "" {
		t.Fatal("expected error message")
	}
}
