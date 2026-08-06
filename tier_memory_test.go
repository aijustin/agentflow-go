package agentflow

import (
	"context"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/adapters"

	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

func TestTierMemoryWrappers(t *testing.T) {
	if adapters.NewInMemoryTierHotStore() == nil {
		t.Fatal("expected hot store")
	}
	manager := tier.NewManager(tierinmem.NewStore(), tier.DefaultPolicy(), tier.NoopMigrationObserver{})
	cog := adapters.NewCognitiveTierMemory(manager, tier.RecallWeights{})
	if cog == nil {
		t.Fatal("expected cognitive tier memory")
	}
	summarizer := adapters.NewLLMTierSummarizer(stubChatter{}, "chat")
	if summarizer == nil {
		t.Fatal("expected summarizer")
	}
}

type stubChatter struct{}

func (stubChatter) Chat(context.Context, string, llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{}, nil
}

func (stubChatter) ChatStream(context.Context, string, llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	return nil, nil
}

func (stubChatter) ChatWithTools(context.Context, string, llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	return llm.ToolCallResponse{}, nil
}

func (stubChatter) StructuredChat(context.Context, string, []byte, llm.ChatRequest) ([]byte, error) {
	return nil, nil
}

func TestCompositeTierStoreWarmColdSurvivesHotRestart(t *testing.T) {
	ctx := context.Background()
	warm := tierinmem.NewStore()
	cold, err := adapters.NewFileTierColdStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hotA := tierinmem.NewStore()
	compositeA := adapters.NewCompositeTierStore(adapters.CompositeTierStoreConfig{Hot: hotA, Warm: warm, Cold: cold})
	policy := tier.Policy{HotCapacity: 1, WarmCapacity: 10, ColdCapacity: 10, PromoteAccess: 99}
	managerA := tier.NewManager(compositeA, policy, tier.NoopMigrationObserver{})
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "persist:assistant", Agent: "assistant"}
	now := time.Now().UTC()

	for _, id := range []string{"m1", "m2"} {
		if err := managerA.Remember(ctx, ns, tier.Record{
			CognitiveRecord: memory.CognitiveRecord{ID: id, Content: id, CreatedAt: now},
			Tier:            tier.LevelHot,
			LastAccessAt:    now,
		}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := managerA.Reconcile(ctx, ns, now); err != nil {
		t.Fatal(err)
	}

	hotB := tierinmem.NewStore()
	compositeB := adapters.NewCompositeTierStore(adapters.CompositeTierStoreConfig{Hot: hotB, Warm: warm, Cold: cold})
	managerB := tier.NewManager(compositeB, policy, tier.NoopMigrationObserver{})
	got, err := managerB.Recall(ctx, ns, "", tier.RecallBudget{Total: 5, Hot: 2, Warm: 3}.Normalize())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected warm/cold records to survive hot restart")
	}
}
