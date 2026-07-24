package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	queueinmem "github.com/aijustin/agentflow-go/internal/adapter/queue/inmem"
	runstatefile "github.com/aijustin/agentflow-go/internal/adapter/runstate/file"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/async"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// saveLeaseManagedZombie persists a Running run stamped with a lease owner
// whose worker is gone (no lease held): the zombie the crash-recovery loop
// must self-heal.
func saveLeaseManagedZombie(t *testing.T, repo runstate.Repository, runID, scenarioName, owner string) {
	t.Helper()
	zombie := runstate.RunSnapshot{
		RunID:        runID,
		ScenarioName: scenarioName,
		Status:       runstate.RunStatusRunning,
		Variables:    map[string]json.RawMessage{runstate.VarRunLeaseOwner: json.RawMessage(`"` + owner + `"`)},
	}
	if err := repo.Save(context.Background(), &zombie, 0); err != nil {
		t.Fatal(err)
	}
}

// TestFrameworkHandleRunReapsZombieAndRetries is the end-to-end zombie
// self-heal: a redelivered run job finds the run still Running, the lease
// probe proves no live worker holds it, and the handler reaps the zombie and
// re-enters through RetryFailedRun instead of failing the job into the
// dead-letter queue.
func TestFrameworkHandleRunReapsZombieAndRetries(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(locker, "worker-async", time.Second),
		// Shrink the reaper grace window so the freshly-saved zombie is
		// reapable without a TTL-length sleep; the hourly interval keeps the
		// reaper loop itself out of this test.
		agentflow.WithRunReaper(time.Hour, 20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	saveLeaseManagedZombie(t, repo, "run-zombie-job", "wf-retry", "dead-worker")
	time.Sleep(50 * time.Millisecond)

	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	job := async.Job{ID: "job-zombie-1", Type: async.RunJobType, RunID: "run-zombie-job"}
	if err := handler.HandleJob(context.Background(), job); err != nil {
		t.Fatalf("zombie run job should self-heal, got %v", err)
	}
	got, err := repo.Load(context.Background(), "run-zombie-job")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected re-entered run to complete, got %s", got.Status)
	}
}

// TestFrameworkHandleRunKeepsLiveRunFailing is the negative counterpart: a
// run whose lease is still held by a live worker is genuinely in progress,
// so the redelivered job keeps the previous fail-and-redeliver semantics and
// the run is never reaped.
func TestFrameworkHandleRunKeepsLiveRunFailing(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(locker, "worker-async", time.Second),
		agentflow.WithRunReaper(time.Hour, 20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	saveLeaseManagedZombie(t, repo, "run-live-job", "wf-retry", "live-worker")
	// A live worker holds the run's lease well past the grace window.
	lease, ok, err := locker.Acquire(context.Background(), "run:run-live-job", "live-worker", time.Minute)
	if err != nil || !ok {
		t.Fatalf("failed to pre-acquire live lease: ok=%v err=%v", ok, err)
	}
	defer func() { _ = locker.Release(context.Background(), lease) }()
	time.Sleep(50 * time.Millisecond)

	handler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
	if err != nil {
		t.Fatal(err)
	}
	job := async.Job{ID: "job-live-1", Type: async.RunJobType, RunID: "run-live-job"}
	err = handler.HandleJob(context.Background(), job)
	if !errors.Is(err, agentflow.ErrRunInProgress) {
		t.Fatalf("expected ErrRunInProgress for a live run, got %v", err)
	}
	got, err := repo.Load(context.Background(), "run-live-job")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != runstate.RunStatusRunning {
		t.Fatalf("live run must stay running, got %s", got.Status)
	}
}

// TestFrameworkRunReaperReapsZombieAndStops covers the built-in reaper loop:
// it marks a zombie run Failed without any external trigger, and
// Framework.Close stops the loop promptly.
func TestFrameworkRunReaperReapsZombieAndStops(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(locker, "worker-reaper", time.Second),
		agentflow.WithRunReaper(20*time.Millisecond, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	saveLeaseManagedZombie(t, repo, "run-reaped-by-loop", "wf-retry", "dead-worker")

	deadline := time.Now().Add(3 * time.Second)
	for {
		got, err := repo.Load(context.Background(), "run-reaped-by-loop")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status == runstate.RunStatusFailed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reaper did not mark zombie run, status %s", got.Status)
		}
		time.Sleep(10 * time.Millisecond)
	}

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fw.Close(closeCtx); err != nil {
		t.Fatalf("close with reaper: %v", err)
	}
}

// TestFrameworkConcurrentReapersReapExactlyOnce runs two frameworks with
// reaper loops against the same repository and locker: racing sweeps must be
// safe and each zombie must be marked (and emit RunFailed) exactly once.
func TestFrameworkConcurrentReapersReapExactlyOnce(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	repo := runstateinmem.NewRepository()
	var mu sync.Mutex
	failedRuns := make(map[string]int)
	sink := core.EventSinkFunc(func(_ context.Context, event core.Event) error {
		if event.Type == core.EventRunFailed {
			mu.Lock()
			failedRuns[event.RunID]++
			mu.Unlock()
		}
		return nil
	})
	newReaper := func(owner string) *agentflow.Framework {
		fw, err := agentflow.New(
			retryWorkflowScenario(),
			agentflow.WithLLMGateway(fakeGateway{content: "x"}),
			agentflow.WithToolExecutor("stepA", noopTool{}),
			agentflow.WithToolExecutor("stepB", noopTool{}),
			agentflow.WithRunStateRepository(repo),
			agentflow.WithEventSink(sink),
			agentflow.WithRunLease(locker, owner, time.Second),
			agentflow.WithRunReaper(15*time.Millisecond, 10*time.Millisecond),
		)
		if err != nil {
			t.Fatal(err)
		}
		return fw
	}
	fw1 := newReaper("worker-1")
	fw2 := newReaper("worker-2")

	zombies := []string{"run-race-1", "run-race-2", "run-race-3"}
	for _, runID := range zombies {
		saveLeaseManagedZombie(t, repo, runID, "wf-retry", "dead-worker")
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		reaped := 0
		for _, runID := range zombies {
			got, err := repo.Load(context.Background(), runID)
			if err != nil {
				t.Fatal(err)
			}
			if got.Status == runstate.RunStatusFailed {
				reaped++
			}
		}
		if reaped == len(zombies) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("reapers did not mark all zombies: %v", failedRuns)
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Let any racing sweep settle, then verify exactly-once marking.
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	for runID, count := range failedRuns {
		if count != 1 {
			t.Fatalf("run %s marked %d times, expected exactly once", runID, count)
		}
	}
	if len(failedRuns) != len(zombies) {
		t.Fatalf("expected %d RunFailed events, got %v", len(zombies), failedRuns)
	}
	mu.Unlock()

	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := fw1.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	if err := fw2.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
}

func TestWithRunReaperRequiresRunLease(t *testing.T) {
	_, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunReaper(time.Minute, 0),
	)
	if err == nil || !strings.Contains(err.Error(), "WithRunLease") {
		t.Fatalf("expected WithRunReaper without WithRunLease to fail, got %v", err)
	}
	if _, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(agentflow.NewInMemoryLocker(), "worker", time.Second),
		agentflow.WithRunReaper(-time.Second, 0),
	); err == nil {
		t.Fatal("expected negative reaper interval to fail")
	}
}

// warnCaptureLogger records Warn messages for wiring-validation assertions.
type warnCaptureLogger struct {
	mu       sync.Mutex
	warnings []string
}

func (l *warnCaptureLogger) Warn(_ context.Context, msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warnings = append(l.warnings, msg)
}

func (l *warnCaptureLogger) Error(context.Context, string, ...any) {}

func (l *warnCaptureLogger) contains(substr string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for _, warning := range l.warnings {
		if strings.Contains(warning, substr) {
			return true
		}
	}
	return false
}

// TestValidateWiringWarnsQueueWithoutRunLease covers the wiring check: a job
// queue plus shared (non-in-memory) run-state without WithRunLease warns,
// while the same setup with in-memory run-state or with a lease stays quiet.
func TestValidateWiringWarnsQueueWithoutRunLease(t *testing.T) {
	scenario := retryWorkflowScenario()
	queue := queueinmem.NewQueue()
	sharedRepo, err := runstatefile.NewRepository(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	logger := &warnCaptureLogger{}
	if err := agentflow.ValidateWiring(scenario,
		agentflow.WithJobQueue(queue),
		agentflow.WithRunStateRepository(sharedRepo),
		agentflow.WithLogger(logger),
	); err != nil {
		t.Fatal(err)
	}
	if !logger.contains("WithRunLease") {
		t.Fatalf("expected wiring warning about missing run lease, got %v", logger.warnings)
	}

	quiet := &warnCaptureLogger{}
	if err := agentflow.ValidateWiring(scenario,
		agentflow.WithJobQueue(queue),
		agentflow.WithLogger(quiet),
	); err != nil {
		t.Fatal(err)
	}
	if quiet.contains("WithRunLease") {
		t.Fatalf("in-memory run-state must not trigger the lease warning, got %v", quiet.warnings)
	}

	leased := &warnCaptureLogger{}
	if err := agentflow.ValidateWiring(scenario,
		agentflow.WithJobQueue(queue),
		agentflow.WithRunStateRepository(sharedRepo),
		agentflow.WithRunLease(agentflow.NewInMemoryLocker(), "worker", time.Second),
		agentflow.WithLogger(leased),
	); err != nil {
		t.Fatal(err)
	}
	if leased.contains("WithRunLease") {
		t.Fatalf("configured run lease must not trigger the warning, got %v", leased.warnings)
	}
}
