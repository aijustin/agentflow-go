package tier

import (
	"context"
	"errors"
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

// TestDualWriteManagerRebuildIndexConverges proves that a transient mirror
// failure (tier write succeeds, index write fails) can be repaired by
// RebuildIndex re-mirroring the durable tier records into the index.
func TestDualWriteManagerRebuildIndexConverges(t *testing.T) {
	ctx := context.Background()
	inner := newTestStore()
	index := newTestStore()
	flaky := &flakyCognitive{inner: &cognitiveStoreAdapter{store: index}, failNext: true}
	manager := NewDualWriteManager(NewManager(inner, DefaultPolicy(), NoopMigrationObserver{}), flaky)
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "rebuild:assistant", Agent: "assistant"}
	now := time.Now().UTC()

	record := Record{
		CognitiveRecord: memory.CognitiveRecord{
			ID: "msg-1", Content: `{"role":"user","content":"billing issue"}`, CreatedAt: now,
			Metadata: map[string]string{"searchable": "billing issue"},
		},
		Tier: LevelHot, LastAccessAt: now,
	}
	if err := manager.Remember(ctx, ns, record); err == nil {
		t.Fatal("expected wrapped mirror failure when the index write fails")
	}
	if _, err := index.Get(ctx, ns, "msg-1"); err == nil {
		t.Fatal("expected index miss after the mirror failed")
	}

	rebuilder, ok := manager.(IndexRebuilder)
	if !ok {
		t.Fatal("dual-write manager must implement IndexRebuilder")
	}
	n, err := rebuilder.RebuildIndex(ctx, ns)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("rebuilt %d records, want 1", n)
	}
	got, err := index.Get(ctx, ns, "msg-1")
	if err != nil {
		t.Fatalf("index still missing after rebuild: %v", err)
	}
	if got.Content != "billing issue" {
		t.Fatalf("index content = %q after rebuild", got.Content)
	}
}

// flakyCognitive fails the first Remember to simulate a transient mirror outage.
type flakyCognitive struct {
	inner    memory.CognitiveMemory
	failNext bool
}

func (f *flakyCognitive) Remember(ctx context.Context, ns memory.Namespace, record memory.CognitiveRecord) error {
	if f.failNext {
		f.failNext = false
		return errors.New("mirror boom")
	}
	return f.inner.Remember(ctx, ns, record)
}

func (f *flakyCognitive) Recall(ctx context.Context, ns memory.Namespace, query string, limit int) ([]memory.CognitiveRecord, error) {
	return f.inner.Recall(ctx, ns, query, limit)
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
