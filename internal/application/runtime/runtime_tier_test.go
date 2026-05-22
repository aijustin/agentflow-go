package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

func TestEngineTierMemoryRecallBudget(t *testing.T) {
	ctx := context.Background()
	store := tierinmem.NewStore()
	policy := tier.Policy{HotCapacity: 100, WarmCapacity: 100, ColdCapacity: 100, PromoteAccess: 3}
	manager := tier.NewManager(store, policy, tier.NoopMigrationObserver{})
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "tier-session:assistant", Agent: "assistant"}
	now := time.Now().UTC()

	for i := 0; i < 4; i++ {
		raw, _ := messageToTierRecord(memoryMessage{
			Role:    "user",
			Content: "old-" + fmt.Sprintf("%d", i),
			Time:    now.Add(time.Duration(i) * time.Minute),
		}, ns)
		if err := manager.Remember(ctx, ns, raw); err != nil {
			t.Fatal(err)
		}
	}

	gateway := &capturingGateway{response: "answer"}
	scenario := baseScenario(false)
	scenario.LLMs = map[string]core.LLMProfileRef{
		"default": {
			Provider: "mock",
			Model:    "test",
			Context: contextwindow.Policy{
				MemoryRecallLimit: 2,
			},
		},
	}
	scenario.Memories = map[string]core.MemoryRef{
		"session": {
			Type:  "custom",
			Scope: string(memory.ScopeSession),
			Tiers: &core.MemoryTierSettings{
				Enabled:     true,
				HotCapacity: 100,
				RecallBudget: core.MemoryTierRecallBudget{
					Total: 10,
					Hot:   10,
				},
			},
		},
	}
	agent := scenario.Agents["assistant"]
	agent.Memory = "session"
	scenario.Agents["assistant"] = agent
	engine, err := NewEngine(scenario, Dependencies{
		Runs:       runstateinmem.NewRepository(),
		LLM:        gateway,
		TierMemory: map[string]tier.Manager{"session": manager},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(ctx, RunRequest{RunID: "run-tier", Agent: "assistant", Prompt: "latest"}); err != nil {
		t.Fatal(err)
	}
	recalled := 0
	for _, msg := range gateway.req.Messages {
		if msg.Role == llm.RoleUser && strings.HasPrefix(msg.Content, "old-") {
			recalled++
		}
	}
	if recalled > 2 {
		t.Fatalf("expected at most 2 tier recalled messages, got %d: %+v", recalled, gateway.req.Messages)
	}
}
