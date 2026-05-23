//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	blobfile "github.com/aijustin/agentflow-go/internal/adapter/blob/file"
	runstatefile "github.com/aijustin/agentflow-go/internal/adapter/runstate/file"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestCrossProcessRetentionAndBlobGC(t *testing.T) {
	dir := t.TempDir()
	runDir := dir + "/runs"
	blobDir := dir + "/blobs"

	repo1, err := runstatefile.NewRepository(runDir)
	if err != nil {
		t.Fatal(err)
	}
	blobs, err := blobfile.NewStore(blobDir)
	if err != nil {
		t.Fatal(err)
	}

	scenario := core.Scenario{
		Name:   "retention-integration",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}
	fw1, err := agentflow.New(scenario,
		agentflow.WithRunStateRepository(repo1),
		agentflow.WithBlobStore(blobs),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	ref, err := blobs.Put(ctx, []byte("orphan-payload"))
	if err != nil {
		t.Fatal(err)
	}
	snap := &runstate.RunSnapshot{
		RunID:        "run-retention",
		ScenarioName: scenario.Name,
		Status:       runstate.RunStatusCompleted,
		StepOutputs: map[string]runstate.StepOutputRef{
			"prep": {Blob: &ref},
		},
	}
	if err := repo1.Save(ctx, snap, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	orphanRef, err := blobs.Put(ctx, []byte("should-be-gc"))
	if err != nil {
		t.Fatal(err)
	}

	repo2, err := runstatefile.NewRepository(runDir)
	if err != nil {
		t.Fatal(err)
	}
	blobs2, err := blobfile.NewStore(blobDir)
	if err != nil {
		t.Fatal(err)
	}
	fw2, err := agentflow.New(scenario,
		agentflow.WithRunStateRepository(repo2),
		agentflow.WithBlobStore(blobs2),
	)
	if err != nil {
		t.Fatal(err)
	}

	removed, err := fw2.PurgeWithPolicy(ctx, agentflow.RetentionPolicy{MaxAge: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected one run purged, got %d", removed)
	}
	gc, err := fw2.PurgeOrphanBlobs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if gc != 2 {
		t.Fatalf("expected two orphan blobs removed after run purge, got %d", gc)
	}
	if _, err := blobs2.Get(ctx, ref); err == nil {
		t.Fatal("referenced blob should be deleted after run purge")
	}
	if _, err := blobs2.Get(ctx, orphanRef); err == nil {
		t.Fatal("orphan blob should be deleted")
	}
	_ = fw1
}
