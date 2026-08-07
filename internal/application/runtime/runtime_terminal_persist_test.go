package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type terminalPersistLogger struct {
	mu     sync.Mutex
	warns  []string
	errors []string
}

func (l *terminalPersistLogger) Warn(_ context.Context, msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, msg)
}

func (l *terminalPersistLogger) Error(_ context.Context, msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, msg)
}

func (l *terminalPersistLogger) hasErrorContaining(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, msg := range l.errors {
		if strings.Contains(msg, substr) {
			return true
		}
	}
	return false
}

func (l *terminalPersistLogger) errorCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.errors)
}

func terminalPersistFailedMarker(t *testing.T, repo runstate.Repository, runID string) string {
	t.Helper()
	loaded, err := repo.Load(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := loaded.Variables[runstate.VarTerminalPersistFailed]
	if !ok {
		return ""
	}
	var marker string
	if err := json.Unmarshal(raw, &marker); err != nil {
		t.Fatalf("terminal-persist-failed marker is not a JSON string: %s", raw)
	}
	return marker
}

// DEFECT_REPORT D2 (deepened): when the terminal-status save exhausts every
// jittered CAS retry, the run must not silently strand in Running — the
// engine stamps the terminal_persist_failed snapshot variable (best-effort,
// optimistic CAS, never a force-write) for the reaper/operator inspection,
// logs at error level, and emits the RunTerminalPersistFailed diagnostic
// event.
func TestEngineTerminalPersistExhaustionStampsMarker(t *testing.T) {
	cases := []struct {
		name   string
		mark   func(engine *Engine, ctx context.Context, runID string)
		target runstate.RunStatus
	}{
		{
			name: "failed",
			mark: func(engine *Engine, ctx context.Context, runID string) {
				engine.markRunFailed(ctx, runID, errors.New("boom"))
			},
			target: runstate.RunStatusFailed,
		},
		{
			name: "cancelled",
			mark: func(engine *Engine, ctx context.Context, runID string) {
				engine.markRunCancelled(ctx, runID)
			},
			target: runstate.RunStatusCancelled,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			inner := runstateinmem.NewRepository()
			// Exactly 5 conflicts exhaust saveSnapshotWithRetry; the next save
			// (the marker stamp) succeeds.
			repo := &conflictingRepository{Repository: inner, err: runstate.ErrStaleSnapshot}
			repo.failures.Store(5)
			logger := &terminalPersistLogger{}
			events := &captureEvents{}
			engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, Events: events, Logger: logger})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			if err := inner.Save(ctx, &runstate.RunSnapshot{
				RunID: "run-exhausted", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
			}, 0); err != nil {
				t.Fatal(err)
			}
			tc.mark(engine, ctx, "run-exhausted")
			engine.obs.emitter.Flush()

			loaded, err := inner.Load(ctx, "run-exhausted")
			if err != nil {
				t.Fatal(err)
			}
			if loaded.Status != runstate.RunStatusRunning {
				t.Fatalf("status=%s want Running (no force-write on retry exhaustion)", loaded.Status)
			}
			if marker := terminalPersistFailedMarker(t, inner, "run-exhausted"); marker != string(tc.target) {
				t.Fatalf("terminal-persist-failed marker=%q want %q", marker, tc.target)
			}
			if !logger.hasErrorContaining("retries exhausted") {
				t.Fatalf("expected an error-level log about exhausted retries, got errors=%v", logger.errors)
			}
			if !events.has(core.EventRunTerminalPersistFailed) {
				t.Fatalf("expected RunTerminalPersistFailed diagnostic event, got %v", events.types())
			}
			if saves := repo.saves.Load(); saves != 6 {
				t.Fatalf("expected 5 failed terminal saves + 1 marker save, got %d", saves)
			}
		})
	}
}

// DEFECT_REPORT D2 (deepened): a terminal save rejected by ErrStaleFence means
// a newer lease holder owns the run and will settle it — no marker, no error
// log, no diagnostic event, and no extra writes.
func TestEngineTerminalPersistExhaustionSkipsMarkerOnStaleFence(t *testing.T) {
	inner := runstateinmem.NewRepository()
	repo := &conflictingRepository{Repository: inner, err: runstate.ErrStaleFence}
	repo.failures.Store(100)
	logger := &terminalPersistLogger{}
	events := &captureEvents{}
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, Events: events, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := inner.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-fence-exhausted", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	engine.markRunFailed(ctx, "run-fence-exhausted", errors.New("boom"))
	engine.obs.emitter.Flush()

	if marker := terminalPersistFailedMarker(t, inner, "run-fence-exhausted"); marker != "" {
		t.Fatalf("stale-fence path must not stamp the marker, got %q", marker)
	}
	if saves := repo.saves.Load(); saves != 1 {
		t.Fatalf("expected exactly one save attempt on the stale-fence path, got %d", saves)
	}
	if logger.errorCount() != 0 {
		t.Fatalf("stale-fence path must not log at error level, got %v", logger.errors)
	}
	if events.has(core.EventRunTerminalPersistFailed) {
		t.Fatal("stale-fence path must not emit RunTerminalPersistFailed")
	}
}

// The normal terminal path (a transient conflict that a retry resolves) keeps
// its previous behavior: terminal status persisted, no marker, no diagnostic
// event.
func TestEngineTerminalPersistSuccessLeavesNoMarker(t *testing.T) {
	inner := runstateinmem.NewRepository()
	repo := &conflictingRepository{Repository: inner, err: runstate.ErrStaleSnapshot}
	repo.failures.Store(1)
	logger := &terminalPersistLogger{}
	events := &captureEvents{}
	engine, err := NewEngine(baseScenario(false), Dependencies{Runs: repo, Events: events, Logger: logger})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := inner.Save(ctx, &runstate.RunSnapshot{
		RunID: "run-ok", ScenarioName: "scenario", Status: runstate.RunStatusRunning,
	}, 0); err != nil {
		t.Fatal(err)
	}
	engine.markRunFailed(ctx, "run-ok", errors.New("boom"))
	engine.obs.emitter.Flush()

	loaded, err := inner.Load(ctx, "run-ok")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != runstate.RunStatusFailed {
		t.Fatalf("status=%s want Failed", loaded.Status)
	}
	if marker := terminalPersistFailedMarker(t, inner, "run-ok"); marker != "" {
		t.Fatalf("successful terminal persist must not stamp the marker, got %q", marker)
	}
	if logger.errorCount() != 0 {
		t.Fatalf("successful terminal persist must not log errors, got %v", logger.errors)
	}
	if events.has(core.EventRunTerminalPersistFailed) {
		t.Fatal("successful terminal persist must not emit RunTerminalPersistFailed")
	}
}
