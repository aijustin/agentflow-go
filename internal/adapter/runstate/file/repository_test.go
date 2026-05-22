package file

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRepositoryListFiltersThreadAndParent(t *testing.T) {
	ctx := context.Background()
	repo, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	parent := runstate.RunSnapshot{RunID: "run-parent", ScenarioName: "scenario", Status: runstate.RunStatusCompleted}
	child := runstate.RunSnapshot{
		RunID:       "run-child",
		ScenarioName: "scenario",
		Status:      runstate.RunStatusRunning,
		ParentRunID: "run-parent",
		ThreadID:    "thread-1",
	}
	other := runstate.RunSnapshot{RunID: "run-other", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	for _, snap := range []runstate.RunSnapshot{parent, child, other} {
		copy := snap
		if err := repo.Save(ctx, &copy, 0); err != nil {
			t.Fatal(err)
		}
	}
	threadRuns, err := repo.List(ctx, runstate.ListFilter{ThreadID: "thread-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(threadRuns) != 1 || threadRuns[0].RunID != "run-child" {
		t.Fatalf("unexpected thread filter result: %+v", threadRuns)
	}
	forks, err := repo.List(ctx, runstate.ListFilter{ParentRunID: "run-parent"})
	if err != nil {
		t.Fatal(err)
	}
	if len(forks) != 1 || forks[0].RunID != "run-child" {
		t.Fatalf("unexpected parent filter result: %+v", forks)
	}
}
