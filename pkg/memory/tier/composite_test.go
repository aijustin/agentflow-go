package tier

import (
	"context"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/memory"
)

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
