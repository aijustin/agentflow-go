package agentflow

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestSaveRunSnapshotWithRetryUpdatesSnapshot(t *testing.T) {
	runs := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{RunID: "run-1", Status: runstate.RunStatusRunning}
	if err := runs.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	fw := &Framework{runs: runs}
	loaded, err := fw.saveRunSnapshotWithRetry(context.Background(), "run-1", func(s *runstate.RunSnapshot) error {
		s.Variables = map[string]json.RawMessage{"flag": json.RawMessage(`true`)}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Variables == nil {
		t.Fatal("expected variables saved")
	}
}

func TestWithScenarioTimeout(t *testing.T) {
	ctx, cancel := withScenarioTimeout(context.Background(), 0)
	cancel()
	if ctx.Err() != nil {
		t.Fatal("zero timeout should not cancel context")
	}
	ctx, cancel = withScenarioTimeout(context.Background(), time.Millisecond)
	defer cancel()
	deadline, ok := ctx.Deadline()
	if !ok || !deadline.After(time.Now()) {
		t.Fatal("expected positive timeout to set deadline")
	}
}
