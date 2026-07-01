package agentflow_test

import (
	"context"
	"errors"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
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
	// A Running run leased by this very worker (Acquire is reentrant for the
	// holding owner) must not be reaped either.
	own := runstate.RunSnapshot{RunID: "run-own", ScenarioName: "wf-retry", Status: runstate.RunStatusRunning}
	if err := repo.Save(context.Background(), &own, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := locker.Acquire(context.Background(), "run:run-own", "reaper", time.Minute); err != nil || !ok {
		t.Fatalf("failed to pre-acquire own lease: ok=%v err=%v", ok, err)
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
	stillOwn, err := repo.Load(context.Background(), "run-own")
	if err != nil {
		t.Fatal(err)
	}
	if stillOwn.Status != runstate.RunStatusRunning {
		t.Fatalf("this worker's own run must stay running, got %s", stillOwn.Status)
	}
}

func TestFrameworkStreamReleasesLeaseAfterCompletion(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	scenario := core.Scenario{
		Name: "stream-lease",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
	}
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapStream)
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "streamed"}})
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithRunLease(locker, "worker-a", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	ch, err := fw.Stream(context.Background(), agentflow.RunRequest{RunID: "run-stream-lease", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	// While the caller has not drained the stream, the run stays leased.
	if _, ok, err := locker.Acquire(context.Background(), "run:run-stream-lease", "worker-b", time.Minute); err != nil || ok {
		t.Fatalf("lease must be held during streaming: ok=%v err=%v", ok, err)
	}
	for range ch {
	}
	if _, ok, err := locker.Acquire(context.Background(), "run:run-stream-lease", "worker-b", time.Minute); err != nil || !ok {
		t.Fatalf("lease should be free after stream closes: ok=%v err=%v", ok, err)
	}
}

func TestFrameworkMarkAbandonedRunsHonorsTenantScope(t *testing.T) {
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
	for _, run := range []runstate.RunSnapshot{
		{RunID: "run-tenant-a", ScenarioName: "wf-retry", TenantID: "tenant-a", Status: runstate.RunStatusRunning},
		{RunID: "run-tenant-b", ScenarioName: "wf-retry", TenantID: "tenant-b", Status: runstate.RunStatusRunning},
	} {
		snapshot := run
		if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
			t.Fatal(err)
		}
	}
	ctx := identity.WithPrincipal(context.Background(), identity.Principal{
		ID:    "ops",
		Type:  identity.PrincipalUser,
		Scope: identity.Scope{TenantID: "tenant-a"},
	})
	marked, err := fw.MarkAbandonedRuns(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 1 || marked[0] != "run-tenant-a" {
		t.Fatalf("expected only tenant-a's zombie to be marked, got %v", marked)
	}
	otherTenant, err := repo.Load(context.Background(), "run-tenant-b")
	if err != nil {
		t.Fatal(err)
	}
	if otherTenant.Status != runstate.RunStatusRunning {
		t.Fatalf("tenant-b's run must not be touched, got %s", otherTenant.Status)
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
