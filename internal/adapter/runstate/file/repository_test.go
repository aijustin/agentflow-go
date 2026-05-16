package file

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRepositoryPersistsSnapshots(t *testing.T) {
	ctx := context.Background()
	repo, err := NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewRepository(repo.dir)
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := reopened.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Version != 1 || loaded.Status != runstate.RunStatusRunning {
		t.Fatalf("unexpected snapshot: %+v", loaded)
	}
	if err := reopened.Save(ctx, &loaded, 0); !errors.Is(err, runstate.ErrStaleSnapshot) {
		t.Fatalf("expected stale snapshot, got %v", err)
	}
}
