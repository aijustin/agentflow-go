package agentflow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkRunLeaseBlocksConcurrentWorker(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(locker, "worker-a", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	// Another live worker already holds the run's lease.
	if _, ok, err := locker.Acquire(context.Background(), "run:run-lease-held", "worker-b", time.Minute); err != nil || !ok {
		t.Fatalf("failed to pre-acquire lease: ok=%v err=%v", ok, err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-lease-held", Prompt: "go"})
	if !errors.Is(err, agentflow.ErrRunInProgress) {
		t.Fatalf("expected ErrRunInProgress for leased run, got %v", err)
	}
	// A normal run acquires and releases its lease, so the key is free after.
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-lease-free", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := locker.Acquire(context.Background(), "run:run-lease-free", "worker-b", time.Minute); err != nil || !ok {
		t.Fatalf("lease should be free after run completion: ok=%v err=%v", ok, err)
	}
}

func TestFrameworkMarkAbandonedRunsMarksZombie(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(locker, "reaper", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	// A Running run with no lease: its worker crashed without releasing.
	zombie := runstate.RunSnapshot{RunID: "run-zombie", ScenarioName: "wf-retry", Status: runstate.RunStatusRunning}
	if err := repo.Save(context.Background(), &zombie, 0); err != nil {
		t.Fatal(err)
	}
	// A Running run whose lease is held by a live worker must be skipped.
	alive := runstate.RunSnapshot{RunID: "run-alive", ScenarioName: "wf-retry", Status: runstate.RunStatusRunning}
	if err := repo.Save(context.Background(), &alive, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := locker.Acquire(context.Background(), "run:run-alive", "live-worker", time.Minute); err != nil || !ok {
		t.Fatalf("failed to pre-acquire live lease: ok=%v err=%v", ok, err)
	}

	marked, err := fw.MarkAbandonedRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 1 || marked[0] != "run-zombie" {
		t.Fatalf("expected only run-zombie to be marked, got %v", marked)
	}
	got, err := repo.Load(context.Background(), "run-zombie")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != runstate.RunStatusFailed {
		t.Fatalf("expected zombie run failed, got %s", got.Status)
	}
	if string(got.Variables["run_error_message"]) != `"worker lost"` {
		t.Fatalf("expected worker lost reason, got %s", got.Variables["run_error_message"])
	}
	stillAlive, err := repo.Load(context.Background(), "run-alive")
	if err != nil {
		t.Fatal(err)
	}
	if stillAlive.Status != runstate.RunStatusRunning {
		t.Fatalf("live run must stay running, got %s", stillAlive.Status)
	}
}

func TestFrameworkMarkAbandonedRunsRequiresLease(t *testing.T) {
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.MarkAbandonedRuns(context.Background()); err == nil {
		t.Fatal("expected error when run lease coordination is not configured")
	}
}
