package inmem

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRepositoryCAS(t *testing.T) {
	repo := NewRepository()
	ctx := context.Background()
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}

	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("expected version 1, got %d", snapshot.Version)
	}
	if err := repo.Save(ctx, &snapshot, 0); !errors.Is(err, runstate.ErrStaleSnapshot) {
		t.Fatalf("expected stale snapshot error, got %v", err)
	}
	snapshot.Status = runstate.RunStatusPaused
	if err := repo.Save(ctx, &snapshot, 1); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 2 {
		t.Fatalf("expected version 2, got %d", snapshot.Version)
	}
}

func TestRepositoryRejectsInvalidStatusTransition(t *testing.T) {
	repo := NewRepository()
	ctx := context.Background()
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	snapshot.Status = runstate.RunStatusCompleted
	if err := repo.Save(ctx, &snapshot, snapshot.Version); err != nil {
		t.Fatal(err)
	}
	snapshot.Status = runstate.RunStatusRunning
	if err := repo.Save(ctx, &snapshot, snapshot.Version); !errors.Is(err, runstate.ErrInvalidTransition) {
		t.Fatalf("expected invalid transition error, got %v", err)
	}
}

func TestRepositoryLoadsClone(t *testing.T) {
	repo := NewRepository()
	ctx := context.Background()
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	loaded.Status = runstate.RunStatusCompleted
	reloaded, err := repo.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Status != runstate.RunStatusRunning {
		t.Fatalf("stored snapshot was mutated: %s", reloaded.Status)
	}
}
