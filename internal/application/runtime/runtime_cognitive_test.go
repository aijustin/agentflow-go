package runtime

import (
	"context"
	"testing"
	"time"

	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

func TestEngineTierMemoryRecallPrefersQueryMatch(t *testing.T) {
	ctx := context.Background()
	store := tierinmem.NewStore()
	manager := tier.NewManager(store, tier.DefaultPolicy(), tier.NoopMigrationObserver{})
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "semantic:assistant", Agent: "assistant"}
	now := time.Now().UTC()

	for _, spec := range []struct {
		id, content string
	}{
		{"a", "weather forecast sunny"},
		{"b", "billing invoice overdue"},
	} {
		record, err := messageToTierRecord(memoryMessage{Role: "user", Content: spec.content, Time: now}, ns)
		if err != nil {
			t.Fatal(err)
		}
		record.ID = spec.id
		record.CognitiveRecord.Metadata["searchable"] = spec.content
		if err := manager.Remember(ctx, ns, record); err != nil {
			t.Fatal(err)
		}
	}

	scenario := baseScenario(false)
	scenario.Memories = map[string]core.MemoryRef{
		"session": {
			Type:      "custom",
			Scope:     string(memory.ScopeSession),
			Namespace: "semantic",
			Tiers: &core.MemoryTierSettings{
				Enabled:     true,
				HotCapacity: 10,
				RecallBudget: core.MemoryTierRecallBudget{Total: 1, Hot: 1},
			},
		},
	}
	agent := scenario.Agents["assistant"]
	agent.Memory = "session"
	scenario.Agents["assistant"] = agent
	engine, err := NewEngine(scenario, Dependencies{
		Runs:       runstateinmem.NewRepository(),
		LLM:        &capturingGateway{response: "ok"},
		TierMemory: map[string]tier.Manager{"session": manager},
	})
	if err != nil {
		t.Fatal(err)
	}
	messages, err := engine.readMemory(ctx, "run-semantic", agent, "billing invoice")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages) != 1 || messages[0].Content != "billing invoice overdue" {
		t.Fatalf("expected billing-related recall, got %+v", messages)
	}
}
