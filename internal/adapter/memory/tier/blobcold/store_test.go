package blobcold

import (
	"context"
	"testing"
	"time"

	blobfile "github.com/aijustin/agentflow-go/internal/adapter/blob/file"
	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/memory/tier"
)

func TestStorePutGetListDelete(t *testing.T) {
	ctx := context.Background()
	blobs, err := blobfile.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewStore(Config{Blobs: blobs, IndexDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "cold:assistant", Agent: "assistant"}
	now := time.Now().UTC()
	record := tier.Record{
		CognitiveRecord: memory.CognitiveRecord{ID: "cold-1", Content: "archive", CreatedAt: now},
		Tier:            tier.LevelCold,
		LastAccessAt:    now,
	}
	if err := store.Put(ctx, ns, record); err != nil {
		t.Fatal(err)
	}
	got, err := store.Get(ctx, ns, "cold-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "archive" {
		t.Fatalf("unexpected content: %q", got.Content)
	}
	list, err := store.List(ctx, ns, tier.LevelCold, 10)
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v err=%v", list, err)
	}
	if err := store.Delete(ctx, ns, "cold-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Get(ctx, ns, "cold-1"); err != memory.ErrNotFound {
		t.Fatalf("expected not found after delete, got %v", err)
	}
	if _, err := NewStore(Config{Blobs: blobs, IndexDir: ""}); err == nil {
		t.Fatal("expected empty index dir error")
	}
}
