package agentflow

import (
	"context"
	"testing"
	"time"

	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

func TestCompositeTierStoreWarmColdSurvivesHotRestart(t *testing.T) {
	ctx := context.Background()
	warm := tierinmem.NewStore()
	cold, err := NewFileTierColdStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	hotA := tierinmem.NewStore()
	compositeA := NewCompositeTierStore(CompositeTierStoreConfig{Hot: hotA, Warm: warm, Cold: cold})
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
	compositeB := NewCompositeTierStore(CompositeTierStoreConfig{Hot: hotB, Warm: warm, Cold: cold})
	managerB := tier.NewManager(compositeB, policy, tier.NoopMigrationObserver{})
	got, err := managerB.Recall(ctx, ns, "", tier.RecallBudget{Total: 5, Hot: 2, Warm: 3}.Normalize())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("expected warm/cold records to survive hot restart")
	}
}
