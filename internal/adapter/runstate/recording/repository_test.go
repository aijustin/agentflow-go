package recording

import (
	"context"
	"fmt"
	"sync"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type failingHistory struct{}

func (failingHistory) Append(context.Context, runstate.RunSnapshot) error {
	return fmt.Errorf("history backend unavailable")
}

func (failingHistory) List(context.Context, string, int) ([]runstate.CheckpointSummary, error) {
	return nil, fmt.Errorf("history backend unavailable")
}

func (failingHistory) Load(context.Context, string, int64) (runstate.RunSnapshot, error) {
	return runstate.RunSnapshot{}, fmt.Errorf("history backend unavailable")
}

type capturingLogger struct {
	mu       sync.Mutex
	warnings int
}

func (l *capturingLogger) Warn(context.Context, string, ...any) {
	l.mu.Lock()
	l.warnings++
	l.mu.Unlock()
}

func (l *capturingLogger) Error(context.Context, string, ...any) {}

func (l *capturingLogger) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.warnings
}

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

func TestRepositorySaveSucceedsAndLogsWhenHistoryAppendFails(t *testing.T) {
	inner := runstateinmem.NewRepository()
	logger := &capturingLogger{}
	repo := &Repository{Inner: inner, History: failingHistory{}, Logger: logger}
	ctx := context.Background()

	snap := &runstate.RunSnapshot{RunID: "run-1", ScenarioName: "demo", Status: runstate.RunStatusRunning}
	if err := repo.Save(ctx, snap, 0); err != nil {
		t.Fatalf("expected Save to succeed despite history append failure, got %v", err)
	}
	loaded, err := inner.Load(ctx, "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.RunID != "run-1" {
		t.Fatalf("expected inner save to have persisted the snapshot, got %+v", loaded)
	}
	if logger.count() == 0 {
		t.Fatal("expected the history append failure to be logged")
	}
}
