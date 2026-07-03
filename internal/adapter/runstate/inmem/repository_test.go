package inmem

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
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

func TestRepositoryListDeleteAndFilters(t *testing.T) {
	repo := NewRepository()
	ctx := context.Background()
	for _, snap := range []runstate.RunSnapshot{
		{RunID: "run-a", ScenarioName: "demo", TenantID: "t1", Status: runstate.RunStatusRunning},
		{RunID: "run-b", ScenarioName: "demo", TenantID: "t2", Status: runstate.RunStatusCompleted},
		{RunID: "run-c", ScenarioName: "other", Status: runstate.RunStatusRunning},
	} {
		s := snap
		if err := repo.Save(ctx, &s, 0); err != nil {
			t.Fatal(err)
		}
	}
	running, err := repo.List(ctx, runstate.ListFilter{ScenarioName: "demo", Status: runstate.RunStatusRunning})
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 1 || running[0].RunID != "run-a" {
		t.Fatalf("unexpected running filter: %+v", running)
	}
	tenant, err := repo.List(ctx, runstate.ListFilter{TenantID: "t2"})
	if err != nil || len(tenant) != 1 || tenant[0].RunID != "run-b" {
		t.Fatalf("unexpected tenant filter: %+v err=%v", tenant, err)
	}
	if err := repo.Delete(ctx, "run-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Load(ctx, "run-a"); err == nil {
		t.Fatal("expected deleted run to be missing")
	}
}

func TestRepositoryListDeepClonesSnapshot(t *testing.T) {
	repo := NewRepository()
	ctx := context.Background()
	snap := runstate.RunSnapshot{
		RunID: "rich", ScenarioName: "demo", Status: runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"nested": json.RawMessage(`{"items":[1,2]}`),
		},
		StepOutputs: map[string]runstate.StepOutputRef{"step-1": {Inline: json.RawMessage(`"done"`)}},
		PendingGate: &core.CheckpointState{NodeID: "review", Version: 2},
	}
	if err := repo.Save(ctx, &snap, 0); err != nil {
		t.Fatal(err)
	}
	listed, err := repo.List(ctx, runstate.ListFilter{ScenarioName: "demo"})
	if err != nil || len(listed) != 1 {
		t.Fatalf("list=%+v err=%v", listed, err)
	}
	got := listed[0]
	got.Variables["nested"][0] = 'X'
	got.StepOutputs["step-1"].Inline[0] = 'X'
	got.PendingGate.Version = 99
	loaded, err := repo.Load(ctx, "rich")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Variables["nested"][0] != '{' || loaded.StepOutputs["step-1"].Inline[0] != '"' || loaded.PendingGate.Version != 2 {
		t.Fatalf("list should return deep clones, got %+v", loaded)
	}
}
