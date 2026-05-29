package tier

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

// failingPutStore wraps testStore and fails every Put, to verify that
// CompositeStore.Put never deletes an existing copy before the destination
// write succeeds.
type failingPutStore struct {
	*testStore
}

func (s failingPutStore) Put(context.Context, memory.Namespace, Record) error {
	return errors.New("put boom")
}

func TestCompositeStoreRoutesByTier(t *testing.T) {
	ctx := context.Background()
	hot := newTestStore()
	warm := newTestStore()
	cold := newTestStore()
	composite := &CompositeStore{Hot: hot, Warm: warm, Cold: cold}
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "composite:assistant", Agent: "assistant"}
	now := time.Now().UTC()

	record := Record{
		CognitiveRecord: memory.CognitiveRecord{ID: "rec-1", Content: "hello", CreatedAt: now},
		Tier:            LevelHot,
		LastAccessAt:    now,
	}
	if err := composite.Put(ctx, ns, record); err != nil {
		t.Fatal(err)
	}
	if _, err := hot.Get(ctx, ns, "rec-1"); err != nil {
		t.Fatalf("expected record in hot: %v", err)
	}

	record.Tier = LevelWarm
	if err := composite.Put(ctx, ns, record); err != nil {
		t.Fatal(err)
	}
	if _, err := warm.Get(ctx, ns, "rec-1"); err != nil {
		t.Fatalf("expected record in warm: %v", err)
	}
	if _, err := hot.Get(ctx, ns, "rec-1"); err == nil {
		t.Fatal("expected hot copy removed after migration")
	}

	got, err := composite.Get(ctx, ns, "rec-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Tier != LevelWarm {
		t.Fatalf("got tier %q, want warm", got.Tier)
	}
}

func TestCompositeStorePutFailureKeepsExistingRecord(t *testing.T) {
	ctx := context.Background()
	hot := newTestStore()
	warm := failingPutStore{testStore: newTestStore()}
	cold := newTestStore()
	composite := &CompositeStore{Hot: hot, Warm: warm, Cold: cold}
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "composite:assistant", Agent: "assistant"}
	now := time.Now().UTC()

	record := Record{
		CognitiveRecord: memory.CognitiveRecord{ID: "rec-1", Content: "hello", CreatedAt: now},
		Tier:            LevelHot,
		LastAccessAt:    now,
	}
	if err := composite.Put(ctx, ns, record); err != nil {
		t.Fatalf("seed put: %v", err)
	}

	// Migrating to warm fails on the destination write. The existing hot copy
	// must be preserved so the record is never lost.
	record.Tier = LevelWarm
	if err := composite.Put(ctx, ns, record); err == nil {
		t.Fatal("expected error when destination tier Put fails")
	}
	got, err := composite.Get(ctx, ns, "rec-1")
	if err != nil {
		t.Fatalf("record lost after failed migration: %v", err)
	}
	if got.Tier != LevelHot {
		t.Fatalf("got tier %q, want hot (original copy preserved)", got.Tier)
	}
}
