package inmem

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

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

func TestRepositorySaveFenced(t *testing.T) {
	repo := NewRepository()
	ctx := context.Background()

	snapshot := runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.SaveFenced(ctx, &snapshot, 0, 5); err != nil {
		t.Fatal(err)
	}
	if snapshot.Version != 1 {
		t.Fatalf("expected version 1, got %d", snapshot.Version)
	}
	// Same token keeps writing; higher token takes over.
	snapshot.Status = runstate.RunStatusPaused
	if err := repo.SaveFenced(ctx, &snapshot, 1, 5); err != nil {
		t.Fatal(err)
	}
	snapshot.Status = runstate.RunStatusRunning
	if err := repo.SaveFenced(ctx, &snapshot, 2, 9); err != nil {
		t.Fatal(err)
	}
	// Superseded holder: regressed token is rejected with ErrStaleFence.
	zombie := runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "scenario", Status: runstate.RunStatusPaused}
	if err := repo.SaveFenced(ctx, &zombie, 3, 5); !errors.Is(err, runstate.ErrStaleFence) {
		t.Fatalf("expected ErrStaleFence for regressed token, got %v", err)
	}
	// Version is checked first: a writer behind on version with a FRESH
	// token still sees ErrStaleSnapshot, not ErrStaleFence.
	behind := runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.SaveFenced(ctx, &behind, 1, 10); !errors.Is(err, runstate.ErrStaleSnapshot) {
		t.Fatalf("expected ErrStaleSnapshot for stale version, got %v", err)
	}
	// Plain Save neither checks nor resets the fence.
	loaded, err := repo.Load(ctx, "run-fenced")
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(ctx, &loaded, loaded.Version); err != nil {
		t.Fatal(err)
	}
	zombie2 := runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "scenario", Status: runstate.RunStatusPaused}
	if err := repo.SaveFenced(ctx, &zombie2, loaded.Version, 5); !errors.Is(err, runstate.ErrStaleFence) {
		t.Fatalf("expected ErrStaleFence after plain Save, got %v", err)
	}
}

func TestRepositorySaveFencedDeleteResetsFence(t *testing.T) {
	repo := NewRepository()
	ctx := context.Background()
	snapshot := runstate.RunSnapshot{RunID: "run-reset", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.SaveFenced(ctx, &snapshot, 0, 9); err != nil {
		t.Fatal(err)
	}
	if err := repo.Delete(ctx, "run-reset"); err != nil {
		t.Fatal(err)
	}
	// Like a dropped PostgreSQL row, deleting clears the fence record.
	fresh := runstate.RunSnapshot{RunID: "run-reset", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.SaveFenced(ctx, &fresh, 0, 1); err != nil {
		t.Fatalf("expected low token to be accepted after delete, got %v", err)
	}
}

func TestRepositoryListStale(t *testing.T) {
	repo := NewRepository()
	ctx := context.Background()
	for _, runID := range []string{"run-a", "run-b"} {
		snap := runstate.RunSnapshot{RunID: runID, ScenarioName: "scenario", Status: runstate.RunStatusRunning}
		if err := repo.Save(ctx, &snap, 0); err != nil {
			t.Fatal(err)
		}
	}
	paused := runstate.RunSnapshot{RunID: "run-c", ScenarioName: "scenario", Status: runstate.RunStatusPaused}
	if err := repo.Save(ctx, &paused, 0); err != nil {
		t.Fatal(err)
	}

	// Everything saved so far is fresh: a large grace returns nothing.
	none, err := repo.ListStale(ctx, runstate.ListFilter{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(none) != 0 {
		t.Fatalf("expected no stale runs, got %d", len(none))
	}
	// Zero grace makes every stamped snapshot stale (UpdatedAt < now).
	stale, err := repo.ListStale(ctx, runstate.ListFilter{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 3 {
		t.Fatalf("expected 3 stale runs with zero grace, got %d", len(stale))
	}
	// Filters still apply on top of the staleness cutoff.
	running, err := repo.ListStale(ctx, runstate.ListFilter{Status: runstate.RunStatusRunning}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(running) != 2 {
		t.Fatalf("expected 2 running stale runs, got %d", len(running))
	}
}
