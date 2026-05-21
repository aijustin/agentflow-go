package agentflow

import (
	"context"
	"testing"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkPurgeOrphanBlobs(t *testing.T) {
	repo := runstateinmem.NewRepository()
	blobs := blobinmem.NewStore()
	ctx := context.Background()
	ref, err := blobs.Put(ctx, []byte("referenced"))
	if err != nil {
		t.Fatal(err)
	}
	orphan, err := blobs.Put(ctx, []byte("orphan"))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-1", ScenarioName: "blob-gc", Status: runstate.RunStatusCompleted,
		StepOutputs: map[string]runstate.StepOutputRef{
			"large": {Blob: &ref},
		},
	}, 0); err != nil {
		t.Fatal(err)
	}
	fw, err := New(core.Scenario{
		Name:   "blob-gc",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo), WithBlobStore(blobs))
	if err != nil {
		t.Fatal(err)
	}
	removed, err := fw.PurgeOrphanBlobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected one orphan blob removed, got %d", removed)
	}
	if _, err := blobs.Get(ctx, ref); err != nil {
		t.Fatalf("referenced blob should remain: %v", err)
	}
	if _, err := blobs.Get(ctx, orphan); err == nil {
		t.Fatal("orphan blob should be deleted")
	}
}
