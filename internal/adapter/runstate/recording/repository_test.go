package recording

import (
	"context"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRepositoryRecordsCheckpointHistory(t *testing.T) {
	inner := runstateinmem.NewRepository()
	history := runstateinmem.NewCheckpointHistory()
	repo := &Repository{Inner: inner, History: history}
	ctx := context.Background()

	snap := &runstate.RunSnapshot{RunID: "run-1", ScenarioName: "demo", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, snap, 0); err != nil {
		t.Fatal(err)
	}
	snap.StepOutputs = map[string]runstate.StepOutputRef{"a": {Inline: []byte(`{"ok":true}`)}}
	if err := repo.Save(ctx, snap, 1); err != nil {
		t.Fatal(err)
	}

	list, err := history.List(ctx, "run-1", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[1].StepCount != 1 {
		t.Fatalf("checkpoints=%+v", list)
	}
}
