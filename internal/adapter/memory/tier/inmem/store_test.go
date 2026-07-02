package inmem_test

import (
	"context"
	"testing"
	"time"

	tierinmem "github.com/aijustin/agentflow-go/internal/adapter/memory/tier/inmem"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

func TestStorePutGetListDelete(t *testing.T) {
	ctx := context.Background()
	store := tierinmem.NewStore()
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "s1", RunID: "r1", Agent: "assistant"}
	now := time.Now().UTC()
	record := tier.Record{
		CognitiveRecord: memory.CognitiveRecord{ID: "mem-1", Content: "hello", CreatedAt: now},
		Tier:            tier.LevelHot,
		LastAccessAt:    now,
	}
	if err := store.Put(ctx, ns, record); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, ns, "mem-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "hello" {
		t.Fatalf("unexpected content: %q", got.Content)
	}
	listed, err := store.List(ctx, ns, tier.LevelHot, 10)
	if err != nil || len(listed) != 1 {
		t.Fatalf("unexpected list: %+v err=%v", listed, err)
	}
	count, err := store.Count(ctx, ns, tier.LevelHot)
	if err != nil || count != 1 {
		t.Fatalf("unexpected count: %d err=%v", count, err)
	}
	if err := store.Delete(ctx, ns, "mem-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, ns, "mem-1"); err != memory.ErrNotFound {
		t.Fatalf("expected not found after delete, got %v", err)
	}
}

func TestStorePutRejectsEmptyID(t *testing.T) {
	store := tierinmem.NewStore()
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "s1"}
	err := store.Put(context.Background(), ns, tier.Record{Tier: tier.LevelHot})
	if err != memory.ErrNotFound {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}
