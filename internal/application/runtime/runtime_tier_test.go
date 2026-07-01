package runtime

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

type capturingLogger struct {
	mu       sync.Mutex
	warnings []string
}

func (l *capturingLogger) Warn(_ context.Context, msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, msg)
}

func (l *capturingLogger) Error(context.Context, string, ...any) {}

func (l *capturingLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.warnings)
}

// TestEngineTierReconcileEnqueueFailureIsLogged guards against a failed tier
// reconcile enqueue being silently discarded: the memory write itself must
// still succeed, but the failure must be surfaced through the configured
// logger so a stuck queue or broken enqueuer doesn't go unnoticed.
func TestEngineTierReconcileEnqueueFailureIsLogged(t *testing.T) {
	ctx := context.Background()
	store := tierinmem.NewStore()
	manager := tier.NewManager(store, tier.Policy{HotCapacity: 100, WarmCapacity: 100, ColdCapacity: 100}, tier.NoopMigrationObserver{})

	scenario := baseScenario(false)
	scenario.LLMs = map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}}
	scenario.Memories = map[string]core.MemoryRef{
		"session": {
			Type:  "custom",
			Scope: string(memory.ScopeSession),
			Tiers: &core.MemoryTierSettings{Enabled: true, HotCapacity: 100},
		},
	}
	agent := scenario.Agents["assistant"]
	agent.Memory = "session"
	scenario.Agents["assistant"] = agent

	logger := &capturingLogger{}
	engine, err := NewEngine(scenario, Dependencies{
		Runs:       runstateinmem.NewRepository(),
		LLM:        &capturingGateway{response: "answer"},
		TierMemory: map[string]tier.Manager{"session": manager},
		Logger:     logger,
		EnqueueMemoryReconcile: func(context.Context, async.Job) error {
			return errors.New("queue unavailable")
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := engine.Run(ctx, RunRequest{RunID: "run-tier-reconcile", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	if logger.count() == 0 {
		t.Fatal("expected enqueue failure to be logged as a warning")
	}
}

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
