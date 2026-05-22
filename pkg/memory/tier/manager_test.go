package tier

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

func TestManagerRecallRespectsBudget(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	policy := Policy{HotCapacity: 100, WarmCapacity: 100, ColdCapacity: 100, PromoteAccess: 3}
	manager := NewManager(store, policy, NoopMigrationObserver{})
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "budget:assistant", Agent: "assistant"}
	now := time.Now().UTC()

	for i := 0; i < 5; i++ {
		if err := manager.Remember(ctx, ns, Record{
			CognitiveRecord: memory.CognitiveRecord{
				ID:        fmt.Sprintf("msg-%d", i),
				Content:   "message",
				CreatedAt: now.Add(time.Duration(i) * time.Minute),
			},
			Tier:         LevelHot,
			LastAccessAt: now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	got, err := manager.Recall(ctx, ns, "", RecallBudget{Total: 2, Hot: 2}.Normalize())
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 records, got %d", len(got))
	}
}

func TestManagerPromoteOnAccess(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	policy := Policy{HotCapacity: 10, WarmCapacity: 10, ColdCapacity: 10, PromoteAccess: 2}
	manager := NewManager(store, policy, NoopMigrationObserver{})
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "promote:assistant", Agent: "assistant"}
	now := time.Now().UTC()

	record := Record{
		CognitiveRecord: memory.CognitiveRecord{
			ID:        "warm-1",
			Content:   "important",
			CreatedAt: now,
		},
		Tier:         LevelWarm,
		AccessCount:  1,
		LastAccessAt: now,
	}
	if err := store.Put(ctx, ns, record); err != nil {
		t.Fatal(err)
	}

	if _, err := manager.Recall(ctx, ns, "important", RecallBudget{Total: 1, Warm: 1}.Normalize()); err != nil {
		t.Fatal(err)
	}
	updated, err := store.Get(ctx, ns, "warm-1")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Tier != LevelHot {
		t.Fatalf("expected hot after promotion, got %q", updated.Tier)
	}
}

func TestManagerReconcileDemotesHotCapacity(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	policy := Policy{HotCapacity: 1, WarmCapacity: 10, ColdCapacity: 10, PromoteAccess: 99}
	manager := NewManager(store, policy, NoopMigrationObserver{})
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "demote:assistant", Agent: "assistant"}
	now := time.Now().UTC()

	for _, id := range []string{"hot-a", "hot-b"} {
		if err := store.Put(ctx, ns, Record{
			CognitiveRecord: memory.CognitiveRecord{ID: id, Content: id, CreatedAt: now},
			Tier:            LevelHot,
			LastAccessAt:    now,
		}); err != nil {
			t.Fatal(err)
		}
	}

	report, err := manager.Reconcile(ctx, ns, now)
	if err != nil {
		t.Fatal(err)
	}
	if report.Demoted == 0 {
		t.Fatal("expected at least one demotion when hot capacity exceeded")
	}
}
