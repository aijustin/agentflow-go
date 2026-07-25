package agentflow

import (
	"context"
	"errors"
	"testing"
	"time"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkPurgeRuns(t *testing.T) {
	repo := runstateinmem.NewRepository()
	fw, err := New(core.Scenario{
		Name:   "purge-test",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.Save(context.Background(), &runstate.RunSnapshot{
		RunID: "run-1", ScenarioName: "purge-test", Status: runstate.RunStatusCompleted,
	}, 0); err != nil {
		t.Fatal(err)
	}
	removed, err := fw.PurgeRuns(context.Background(), runstate.ListFilter{ScenarioName: "purge-test"})
	if err != nil || removed != 1 {
		t.Fatalf("expected one purge, got %d err=%v", removed, err)
	}
}

func TestPurgeExpiredRespectsUpdatedAt(t *testing.T) {
	repo := runstateinmem.NewRepository()
	fw, err := New(core.Scenario{
		Name:   "retention",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := repo.Save(ctx, &runstate.RunSnapshot{RunID: "old", ScenarioName: "retention", Status: runstate.RunStatusCompleted}, 0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1100 * time.Millisecond)
	if err := repo.Save(ctx, &runstate.RunSnapshot{RunID: "new", ScenarioName: "retention", Status: runstate.RunStatusCompleted}, 0); err != nil {
		t.Fatal(err)
	}
	removed, err := fw.PurgeExpired(ctx, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected one expired run removed, got %d", removed)
	}
	if _, err := repo.Load(ctx, "new"); err != nil {
		t.Fatalf("recent run should remain: %v", err)
	}
	if _, err := repo.Load(ctx, "old"); err != runstate.ErrNotFound {
		t.Fatalf("old run should be deleted: %v", err)
	}
}

func TestPurgeExpiredSkipsRunning(t *testing.T) {
	repo := runstateinmem.NewRepository()
	fw, err := New(core.Scenario{
		Name:   "retention-running",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	old := time.Now().UTC().Add(-2 * time.Hour)
	snap := &runstate.RunSnapshot{
		RunID: "active", ScenarioName: "retention-running", Status: runstate.RunStatusRunning, UpdatedAt: old,
	}
	if err := repo.Save(ctx, snap, 0); err != nil {
		t.Fatal(err)
	}
	// Ensure running snapshot keeps stale UpdatedAt even if repository re-stamps on save.
	if loaded, err := repo.Load(ctx, "active"); err == nil {
		loaded.UpdatedAt = old
		if err := repo.Save(ctx, &loaded, loaded.Version); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := fw.PurgeWithPolicy(ctx, RetentionPolicy{MaxAge: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 0 {
		t.Fatalf("expected running run to be kept, removed=%d", removed)
	}
}

func TestPurgeWithPolicyMaxAgeAndLimit(t *testing.T) {
	repo := runstateinmem.NewRepository()
	fw, err := New(core.Scenario{
		Name:   "retention-limit",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, id := range []string{"old-a", "old-b"} {
		if err := repo.Save(ctx, &runstate.RunSnapshot{
			RunID: id, ScenarioName: "retention-limit", Status: runstate.RunStatusCompleted,
		}, 0); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(1100 * time.Millisecond)
	removed, err := fw.PurgeWithPolicy(ctx, RetentionPolicy{MaxAge: time.Second, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected limit=1 purge, removed=%d", removed)
	}
}

func TestPurgeWithPolicyByStatusFilter(t *testing.T) {
	repo := runstateinmem.NewRepository()
	fw, err := New(core.Scenario{
		Name:   "retention-status",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, spec := range []struct {
		id     string
		status runstate.RunStatus
	}{
		{"done", runstate.RunStatusCompleted},
		{"active", runstate.RunStatusRunning},
	} {
		if err := repo.Save(ctx, &runstate.RunSnapshot{
			RunID: spec.id, ScenarioName: "retention-status", Status: spec.status,
		}, 0); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := fw.PurgeWithPolicy(ctx, RetentionPolicy{Status: runstate.RunStatusCompleted})
	if err != nil || removed != 1 {
		t.Fatalf("removed=%d err=%v", removed, err)
	}
	if _, err := repo.Load(ctx, "active"); err != nil {
		t.Fatalf("running run should remain: %v", err)
	}
}

func TestPurgeWithPolicyScopesAuthenticatedTenant(t *testing.T) {
	repo := runstateinmem.NewRepository()
	fw, err := New(core.Scenario{
		Name:   "retention-tenant",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo))
	if err != nil {
		t.Fatal(err)
	}
	for _, snapshot := range []runstate.RunSnapshot{
		{RunID: "tenant-a-run", ScenarioName: "retention-tenant", TenantID: "tenant-a", Status: runstate.RunStatusCompleted},
		{RunID: "tenant-b-run", ScenarioName: "retention-tenant", TenantID: "tenant-b", Status: runstate.RunStatusCompleted},
	} {
		snapshot := snapshot
		if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
			t.Fatal(err)
		}
	}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "admin-a", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	removed, err := fw.PurgeWithPolicy(ctx, RetentionPolicy{Status: runstate.RunStatusCompleted})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("removed=%d, want 1", removed)
	}
	if _, err := repo.Load(context.Background(), "tenant-a-run"); !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("tenant-a run should be deleted, got %v", err)
	}
	if _, err := repo.Load(context.Background(), "tenant-b-run"); err != nil {
		t.Fatalf("tenant-b run must remain: %v", err)
	}
}

func TestPurgeRunsRejectsCrossTenantFilter(t *testing.T) {
	repo := runstateinmem.NewRepository()
	fw, err := New(core.Scenario{
		Name:   "retention-tenant-filter",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo))
	if err != nil {
		t.Fatal(err)
	}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID: "admin-a", Type: identity.PrincipalUser, Scope: identity.Scope{TenantID: "tenant-a"},
	})
	_, err = fw.PurgeRuns(ctx, runstate.ListFilter{TenantID: "tenant-b"})
	if !errors.Is(err, runstate.ErrTenantMismatch) {
		t.Fatalf("expected tenant mismatch, got %v", err)
	}
}

// TestPurgeRunsSkipsNonTerminalByDefault: a retention sweep must never
// delete a run that is still executing or awaiting a human, unless the
// caller explicitly forces it.
func TestPurgeRunsSkipsNonTerminalByDefault(t *testing.T) {
	repo := runstateinmem.NewRepository()
	fw, err := New(core.Scenario{
		Name:   "purge-guard",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, snapshot := range []runstate.RunSnapshot{
		{RunID: "run-running", ScenarioName: "purge-guard", Status: runstate.RunStatusRunning},
		{RunID: "run-paused", ScenarioName: "purge-guard", Status: runstate.RunStatusPaused},
		{RunID: "run-done", ScenarioName: "purge-guard", Status: runstate.RunStatusCompleted},
	} {
		snapshot := snapshot
		if err := repo.Save(ctx, &snapshot, 0); err != nil {
			t.Fatal(err)
		}
	}
	removed, err := fw.PurgeRuns(ctx, runstate.ListFilter{ScenarioName: "purge-guard"})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected only the terminal run purged, got %d", removed)
	}
	if _, err := repo.Load(ctx, "run-running"); err != nil {
		t.Fatalf("running run must survive: %v", err)
	}
	if _, err := repo.Load(ctx, "run-paused"); err != nil {
		t.Fatalf("paused run must survive: %v", err)
	}
	removed, err = fw.PurgeRuns(ctx, runstate.ListFilter{ScenarioName: "purge-guard"}, WithPurgeForce())
	if err != nil {
		t.Fatal(err)
	}
	if removed != 2 {
		t.Fatalf("expected force purge of the remaining runs, got %d", removed)
	}
}

// TestPurgeRunsDeletesCheckpointHistory: purging a run also drops its
// recorded checkpoint revisions so time-travel data does not outlive the run.
func TestPurgeRunsDeletesCheckpointHistory(t *testing.T) {
	repo := runstateinmem.NewRepository()
	history := runstateinmem.NewCheckpointHistory()
	fw, err := New(core.Scenario{
		Name:   "purge-history",
		Agents: map[string]core.Agent{"assistant": {Name: "assistant", LLM: "mock"}},
		LLMs:   map[string]core.LLMProfileRef{"mock": {Provider: "mock", Model: "test"}},
	}, WithRunStateRepository(repo), WithCheckpointHistory(history))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	snapshot := &runstate.RunSnapshot{RunID: "run-hist", ScenarioName: "purge-history", Status: runstate.RunStatusCompleted}
	if err := repo.Save(ctx, snapshot, 0); err != nil {
		t.Fatal(err)
	}
	if err := history.Append(ctx, *snapshot); err != nil {
		t.Fatal(err)
	}
	removed, err := fw.PurgeRuns(ctx, runstate.ListFilter{ScenarioName: "purge-history"})
	if err != nil {
		t.Fatal(err)
	}
	if removed != 1 {
		t.Fatalf("expected one purge, got %d", removed)
	}
	checkpoints, err := history.List(ctx, "run-hist", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(checkpoints) != 0 {
		t.Fatalf("expected checkpoint history deleted with the run, got %d entries", len(checkpoints))
	}
}
