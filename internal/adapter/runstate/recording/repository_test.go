package recording

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

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

func (failingHistory) Delete(context.Context, string) error {
	return fmt.Errorf("history backend unavailable")
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

func TestRepositorySaveReturnsAndLogsHistoryAppendFailure(t *testing.T) {
	inner := runstateinmem.NewRepository()
	logger := &capturingLogger{}
	repo := &Repository{Inner: inner, History: failingHistory{}, Logger: logger}
	ctx := context.Background()

	snap := &runstate.RunSnapshot{RunID: "run-1", ScenarioName: "demo", Status: runstate.RunStatusRunning}
	err := repo.Save(ctx, snap, 0)
	if err == nil {
		t.Fatal("expected Save to surface the history append failure")
	}
	loaded, loadErr := inner.Load(ctx, "run-1")
	if loadErr != nil {
		t.Fatal(loadErr)
	}
	if loaded.RunID != "run-1" {
		t.Fatalf("expected inner save to have persisted the snapshot, got %+v", loaded)
	}
	if logger.count() == 0 {
		t.Fatal("expected the history append failure to be logged")
	}
}

func TestRepositoryDelegatesLoadDeleteAndList(t *testing.T) {
	inner := runstateinmem.NewRepository()
	repo := &Repository{Inner: inner}
	ctx := context.Background()
	snap := &runstate.RunSnapshot{RunID: "run-2", ScenarioName: "demo", Status: runstate.RunStatusCompleted}
	if err := repo.Save(ctx, snap, 0); err != nil {
		t.Fatal(err)
	}
	loaded, err := repo.Load(ctx, "run-2")
	if err != nil || loaded.RunID != "run-2" {
		t.Fatalf("load: %+v err=%v", loaded, err)
	}
	list, err := repo.List(ctx, runstate.ListFilter{Limit: 10})
	if err != nil || len(list) != 1 {
		t.Fatalf("list: %+v err=%v", list, err)
	}
	if err := repo.Delete(ctx, "run-2"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Load(ctx, "run-2"); err == nil {
		t.Fatal("expected missing run after delete")
	}
}

// bareRepo implements only runstate.Repository, so the wrapper must fall back
// to local-clock behavior for ListStale and refuse SaveFenced.
type bareRepo struct {
	snapshots []runstate.RunSnapshot
}

func (b *bareRepo) Save(context.Context, *runstate.RunSnapshot, int64) error { return nil }
func (b *bareRepo) Load(context.Context, string) (runstate.RunSnapshot, error) {
	return runstate.RunSnapshot{}, runstate.ErrNotFound
}
func (b *bareRepo) Delete(context.Context, string) error { return nil }
func (b *bareRepo) List(context.Context, runstate.ListFilter) ([]runstate.RunSnapshot, error) {
	return b.snapshots, nil
}

func TestRepositorySaveFencedDelegatesAndRecords(t *testing.T) {
	inner := runstateinmem.NewRepository()
	history := runstateinmem.NewCheckpointHistory()
	repo := &Repository{Inner: inner, History: history}
	ctx := context.Background()

	snap := &runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "demo", Status: runstate.RunStatusRunning}
	if err := repo.SaveFenced(ctx, snap, 0, 7); err != nil {
		t.Fatal(err)
	}
	list, err := history.List(ctx, "run-fenced", 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected fenced save recorded in history, got %d entries", len(list))
	}
	// Fence enforcement passes through the wrapper.
	zombie := &runstate.RunSnapshot{RunID: "run-fenced", ScenarioName: "demo", Status: runstate.RunStatusPaused}
	if err := repo.SaveFenced(ctx, zombie, 1, 3); !errors.Is(err, runstate.ErrStaleFence) {
		t.Fatalf("expected ErrStaleFence, got %v", err)
	}
}

func TestRepositorySaveFencedUnsupportedInner(t *testing.T) {
	repo := &Repository{Inner: &bareRepo{}}
	snap := &runstate.RunSnapshot{RunID: "run-x", ScenarioName: "demo", Status: runstate.RunStatusRunning}
	err := repo.SaveFenced(context.Background(), snap, 0, 1)
	if err == nil {
		t.Fatal("expected unsupported-inner error instead of a silent unfenced save")
	}
}

func TestRepositoryListStaleFallbackUsesLocalClock(t *testing.T) {
	old := runstate.RunSnapshot{RunID: "run-old", Status: runstate.RunStatusRunning, UpdatedAt: time.Now().UTC().Add(-2 * time.Hour)}
	fresh := runstate.RunSnapshot{RunID: "run-fresh", Status: runstate.RunStatusRunning, UpdatedAt: time.Now().UTC()}
	repo := &Repository{Inner: &bareRepo{snapshots: []runstate.RunSnapshot{old, fresh}}}
	stale, err := repo.ListStale(context.Background(), runstate.ListFilter{}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].RunID != "run-old" {
		t.Fatalf("expected only the old run to be stale, got %+v", stale)
	}
}

func TestRepositoryListStaleDelegates(t *testing.T) {
	inner := runstateinmem.NewRepository()
	repo := &Repository{Inner: inner}
	ctx := context.Background()
	snap := &runstate.RunSnapshot{RunID: "run-1", ScenarioName: "demo", Status: runstate.RunStatusRunning}
	if err := inner.Save(ctx, snap, 0); err != nil {
		t.Fatal(err)
	}
	stale, err := repo.ListStale(ctx, runstate.ListFilter{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 {
		t.Fatalf("expected delegated ListStale to return the run, got %d", len(stale))
	}
}
