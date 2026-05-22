package tier

import (
	"context"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

func TestCognitiveAdapterRememberRecall(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	manager := NewManager(store, DefaultPolicy(), NoopMigrationObserver{})
	adapter := NewCognitiveAdapter(manager, RecallWeights{})
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "cog:assistant", Agent: "assistant"}

	if err := adapter.Remember(ctx, ns, memory.CognitiveRecord{
		ID: "fact-1", Content: "user prefers dark mode", Importance: 0.8, CreatedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	got, err := adapter.Recall(ctx, ns, "dark mode", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "fact-1" {
		t.Fatalf("unexpected recall: %+v", got)
	}
}

func TestDualWriteManagerIndexesSearchableContent(t *testing.T) {
	ctx := context.Background()
	inner := newTestStore()
	index := newTestStore()
	manager := NewDualWriteManager(NewManager(inner, DefaultPolicy(), NoopMigrationObserver{}), &cognitiveStoreAdapter{store: index})
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "dual:assistant", Agent: "assistant"}
	now := time.Now().UTC()

	record := Record{
		CognitiveRecord: memory.CognitiveRecord{
			ID: "msg-1", Content: `{"role":"user","content":"billing issue"}`, CreatedAt: now,
			Metadata: map[string]string{"searchable": "billing issue"},
		},
		Tier: LevelHot, LastAccessAt: now,
	}
	if err := manager.Remember(ctx, ns, record); err != nil {
		t.Fatal(err)
	}
	got, err := index.Get(ctx, ns, "msg-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "billing issue" {
		t.Fatalf("index content = %q", got.Content)
	}
}

// cognitiveStoreAdapter adapts tier Store to CognitiveMemory for tests.
type cognitiveStoreAdapter struct {
	store Store
}

func (a *cognitiveStoreAdapter) Remember(ctx context.Context, ns memory.Namespace, record memory.CognitiveRecord) error {
	return a.store.Put(ctx, ns, Record{CognitiveRecord: record, Tier: LevelHot, LastAccessAt: time.Now().UTC()})
}

func (a *cognitiveStoreAdapter) Recall(ctx context.Context, ns memory.Namespace, query string, limit int) ([]memory.CognitiveRecord, error) {
	records, err := a.store.List(ctx, ns, LevelHot, limit)
	if err != nil {
		return nil, err
	}
	out := make([]memory.CognitiveRecord, len(records))
	for i, record := range records {
		out[i] = record.CognitiveRecord
	}
	return out, nil
}
