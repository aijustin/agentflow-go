package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/coordination"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

// renewFailLocker acquires normally, then fails Renew so the Framework must
// cancel the in-flight run with ErrRunLeaseLost instead of continuing.
type renewFailLocker struct {
	inner     coordination.Locker
	renews    atomic.Int32
	failAfter int32
}

func (l *renewFailLocker) Acquire(ctx context.Context, key, owner string, ttl time.Duration) (coordination.Lease, bool, error) {
	return l.inner.Acquire(ctx, key, owner, ttl)
}

func (l *renewFailLocker) Renew(ctx context.Context, lease coordination.Lease, ttl time.Duration) (coordination.Lease, bool, error) {
	n := l.renews.Add(1)
	if n > l.failAfter {
		return coordination.Lease{}, false, nil
	}
	return l.inner.Renew(ctx, lease, ttl)
}

func (l *renewFailLocker) Release(ctx context.Context, lease coordination.Lease) error {
	return l.inner.Release(ctx, lease)
}

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
	// Lease TTL doubles as reaper grace; use the minimum clamp (1s) so the
	// freshly-saved zombie ages out of the grace window quickly.
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(locker, "reaper", time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	// A Running run with no lease: its worker crashed without releasing. The
	// lease-owner marker identifies it as lease-managed and thus reapable.
	leaseManaged := func(runID, scenarioName, owner string) runstate.RunSnapshot {
		return runstate.RunSnapshot{
			RunID:        runID,
			ScenarioName: scenarioName,
			Status:       runstate.RunStatusRunning,
			Variables:    map[string]json.RawMessage{runstate.VarRunLeaseOwner: json.RawMessage(`"` + owner + `"`)},
		}
	}
	zombie := leaseManaged("run-zombie", "wf-retry", "dead-worker")
	if err := repo.Save(context.Background(), &zombie, 0); err != nil {
		t.Fatal(err)
	}
	// A Running run whose lease is held by a live worker must be skipped.
	alive := leaseManaged("run-alive", "wf-retry", "live-worker")
	if err := repo.Save(context.Background(), &alive, 0); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := locker.Acquire(context.Background(), "run:run-alive", "live-worker", time.Minute); err != nil || !ok {
		t.Fatalf("failed to pre-acquire live lease: ok=%v err=%v", ok, err)
	}
	// A Running run leased by this very worker (Acquire is reentrant for the
	// holding owner) must not be reaped either.
	own := leaseManaged("run-own", "wf-retry", "reaper")
	if err := repo.Save(context.Background(), &own, 0); err != nil {
		t.Fatal(err)
	}
	// Hold own lease longer than the grace sleep so it is not free for reaping.
	if _, ok, err := locker.Acquire(context.Background(), "run:run-own", "reaper", time.Minute); err != nil || !ok {
		t.Fatalf("failed to pre-acquire own lease: ok=%v err=%v", ok, err)
	}
	// A Running run WITHOUT the lease-owner marker belongs to a worker that
	// does not use lease coordination; the reaper must not touch it even
	// though no lease protects it.
	unmanaged := runstate.RunSnapshot{RunID: "run-unmanaged", ScenarioName: "wf-retry", Status: runstate.RunStatusRunning}
	if err := repo.Save(context.Background(), &unmanaged, 0); err != nil {
		t.Fatal(err)
	}

	time.Sleep(1100 * time.Millisecond)
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
	stillUnmanaged, err := repo.Load(context.Background(), "run-unmanaged")
	if err != nil {
		t.Fatal(err)
	}
	if stillUnmanaged.Status != runstate.RunStatusRunning {
		t.Fatalf("unmanaged run must never be reaped, got %s", stillUnmanaged.Status)
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
		agentflow.WithRunLease(locker, "reaper", time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	repo := fw.RunStateRepository()
	for _, run := range []runstate.RunSnapshot{
		{RunID: "run-tenant-a", ScenarioName: "wf-retry", TenantID: "tenant-a", Status: runstate.RunStatusRunning,
			Variables: map[string]json.RawMessage{runstate.VarRunLeaseOwner: json.RawMessage(`"worker-a"`)}},
		{RunID: "run-tenant-b", ScenarioName: "wf-retry", TenantID: "tenant-b", Status: runstate.RunStatusRunning,
			Variables: map[string]json.RawMessage{runstate.VarRunLeaseOwner: json.RawMessage(`"worker-b"`)}},
	} {
		snapshot := run
		if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(1100 * time.Millisecond)
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

func TestFrameworkMarkAbandonedRunsSkipsRecentRunning(t *testing.T) {
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
	fresh := runstate.RunSnapshot{RunID: "run-fresh", ScenarioName: "wf-retry", Status: runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{runstate.VarRunLeaseOwner: json.RawMessage(`"worker-fresh"`)}}
	if err := repo.Save(context.Background(), &fresh, 0); err != nil {
		t.Fatal(err)
	}
	marked, err := fw.MarkAbandonedRuns(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(marked) != 0 {
		t.Fatalf("expected grace to skip recently updated Running run, got %v", marked)
	}
	got, err := repo.Load(context.Background(), "run-fresh")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != runstate.RunStatusRunning {
		t.Fatalf("expected still Running, got %s", got.Status)
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

func TestWithRunLeaseValidation(t *testing.T) {
	_, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(nil, "worker", time.Minute),
	)
	if err == nil {
		t.Fatal("expected error for nil locker")
	}

	locker := agentflow.NewInMemoryLocker()
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(locker, "", 0),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-default-lease", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
}

func TestFrameworkRunStructuredHoldsLease(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	scenario := core.Scenario{
		Name: "structured-lease",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {
				Name: "assistant",
				LLM:  "default",
				Policy: core.AgentPolicy{
					OutputSchema: json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string"}},"required":["answer"]}`),
				},
			},
		},
	}
	gateway := structuredFakeGateway{payload: json.RawMessage(`{"answer":"ok"}`)}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithRunLease(locker, "worker-a", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := locker.Acquire(context.Background(), "run:run-structured-lease", "worker-b", time.Minute); err != nil || !ok {
		t.Fatalf("failed to pre-acquire lease: ok=%v err=%v", ok, err)
	}
	_, err = fw.RunStructured(context.Background(), agentflow.RunRequest{
		RunID:  "run-structured-lease",
		Agent:  "assistant",
		Prompt: "json",
	})
	if !errors.Is(err, agentflow.ErrRunInProgress) {
		t.Fatalf("expected ErrRunInProgress, got %v", err)
	}
}

func TestFrameworkResumeAndContinueHoldsLease(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	scenario := core.Scenario{
		Name: "continue-lease",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Tools: map[string]core.Tool{
			"slow": {Name: "slow", Type: "builtin.slow", Approval: core.ApprovalNever},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "approve", Kind: core.NodeHumanGate},
					{ID: "wait", Kind: core.NodeTool, Ref: "slow", DependsOn: []string{"approve"}, Input: json.RawMessage(`{}`)},
				},
			},
			HumanInLoop: core.HumanInLoopPolicy{Enabled: true},
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithHITLTokenSecret([]byte("test-secret-012345"), nil),
		agentflow.WithToolExecutor("slow", slowTool{delay: 200 * time.Millisecond}),
		agentflow.WithRunLease(locker, "worker-a", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-resume-lease", Prompt: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused, got %+v", result)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := fw.ResumeAndContinue(context.Background(), result.Token, core.DecisionApprove, nil); err != nil {
			t.Errorf("continue failed: %v", err)
		}
	}()
	time.Sleep(50 * time.Millisecond)
	if _, ok, err := locker.Acquire(context.Background(), "run:run-resume-lease", "worker-b", time.Minute); err != nil || ok {
		t.Fatalf("lease must be held during continue: ok=%v err=%v", ok, err)
	}
	<-done
	if _, ok, err := locker.Acquire(context.Background(), "run:run-resume-lease", "worker-b", time.Minute); err != nil || !ok {
		t.Fatalf("lease should be free after continue: ok=%v err=%v", ok, err)
	}
}

type slowTool struct {
	delay time.Duration
}

func (s slowTool) Execute(ctx context.Context, _ core.ToolCall) (core.ToolResult, error) {
	timer := time.NewTimer(s.delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return core.ToolResult{}, ctx.Err()
	case <-timer.C:
		return core.ToolResult{Output: json.RawMessage(`{"ok":true}`)}, nil
	}
}

func TestFrameworkHoldRunLeaseRenewal(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	scenario := core.Scenario{
		Name: "lease-renewal",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Tools: map[string]core.Tool{
			"slow": {Name: "slow", Type: "builtin.slow", Approval: core.ApprovalNever},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "wait", Kind: core.NodeTool, Ref: "slow", Input: json.RawMessage(`{}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithToolExecutor("slow", slowTool{delay: 200 * time.Millisecond}),
		agentflow.WithRunLease(locker, "worker-a", 100*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-renew"}); err != nil {
			t.Errorf("run failed: %v", err)
		}
	}()
	time.Sleep(150 * time.Millisecond)
	if _, ok, err := locker.Acquire(context.Background(), "run:run-renew", "worker-b", time.Minute); err != nil || ok {
		t.Fatalf("renewal should keep lease held: ok=%v err=%v", ok, err)
	}
	<-done
	if _, ok, err := locker.Acquire(context.Background(), "run:run-renew", "worker-b", time.Minute); err != nil || !ok {
		t.Fatalf("lease should be free after run: ok=%v err=%v", ok, err)
	}
}

func TestFrameworkAbortsWhenLeaseRenewalFails(t *testing.T) {
	inner := agentflow.NewInMemoryLocker()
	locker := &renewFailLocker{inner: inner, failAfter: 0}
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", slowTool{delay: 250 * time.Millisecond}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunLease(locker, "worker-a", 60*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-lease-lost", Prompt: "go"})
	if err == nil || !errors.Is(err, agentflow.ErrRunLeaseLost) {
		t.Fatalf("expected ErrRunLeaseLost after renew failure, got %v", err)
	}
	// The lease-lost run must persist as Failed with the lease-lost reason,
	// never as Cancelled: it is a worker-ownership failure, not a caller
	// cancel.
	snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), "run-lease-lost")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected Failed after lease loss, got %s", snapshot.Status)
	}
	if msg := snapshot.Variables[runstate.VarRunErrorMessage]; !strings.Contains(string(msg), "lease lost") {
		t.Fatalf("expected lease-lost reason on snapshot, got %s", msg)
	}
}

// TestFrameworkAutonomousLeaseLostMarksFailed covers the same lease-lost
// classification on the autonomous engine path (markRunFailedOrCancelled),
// where a cancel caused by ErrRunLeaseLost previously persisted Cancelled
// because the cause was discarded.
func TestFrameworkAutonomousLeaseLostMarksFailed(t *testing.T) {
	inner := agentflow.NewInMemoryLocker()
	locker := &renewFailLocker{inner: inner, failAfter: 0}
	scenario := core.Scenario{
		Name: "lease-lost-auto",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default", Tools: []string{"slow"}},
		},
		Tools: map[string]core.Tool{
			"slow": {Name: "slow", Type: "builtin.slow", Approval: core.ApprovalNever},
		},
	}
	gateway := llmmock.NewGateway()
	gateway.SetCapabilities("default", llm.CapChat, llm.CapToolCall)
	gateway.QueueToolCall("default", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "c1", Name: "slow", Input: json.RawMessage(`{}`)}},
	})
	gateway.QueueChat("default", llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "done"}})
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(gateway),
		agentflow.WithToolExecutor("slow", slowTool{delay: 250 * time.Millisecond}),
		agentflow.WithRunLease(locker, "worker-a", 60*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-lease-lost-auto", Agent: "assistant", Prompt: "go"})
	if err == nil || !errors.Is(err, agentflow.ErrRunLeaseLost) {
		t.Fatalf("expected ErrRunLeaseLost after renew failure, got %v", err)
	}
	snapshot, err := runstate.LoadAuthorized(context.Background(), fw.RunStateRepository(), "run-lease-lost-auto")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected Failed after lease loss, got %s", snapshot.Status)
	}
	if msg := snapshot.Variables[runstate.VarRunErrorMessage]; !strings.Contains(string(msg), "lease lost") {
		t.Fatalf("expected lease-lost reason on snapshot, got %s", msg)
	}
}

// leaseMustBeHeld probes the run's lease with a foreign owner — the same
// probe MarkAbandonedRuns uses to detect zombie runs — and fails the test
// when the lease is free while the run is expected to be alive.
func leaseMustBeHeld(t *testing.T, locker coordination.Locker, runID string) {
	t.Helper()
	if _, ok, err := locker.Acquire(context.Background(), "run:"+runID, "worker-b", time.Minute); err != nil || ok {
		t.Fatalf("lease must stay held while the detached run executes: ok=%v err=%v", ok, err)
	}
}

// leaseMustBeFreeEventually waits for the run's lease to become acquirable,
// which is how a completed run releases its lease for other workers.
func leaseMustBeFreeEventually(t *testing.T, locker coordination.Locker, runID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, ok, err := locker.Acquire(context.Background(), "run:"+runID, "worker-b", time.Minute); err == nil && ok {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("lease should be free after the detached run completed")
}

// TestFrameworkStreamDetachedHoldsLeaseUntilRunEnds: with WithRunLease +
// StreamDetached, a client disconnect must NOT release the run lease while
// the detached run keeps executing in the background — otherwise
// MarkAbandonedRuns would reap the live run and another worker could take
// over the same run ID. The lease is released only when the run settles.
func TestFrameworkStreamDetachedHoldsLeaseUntilRunEnds(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	gateway := newSlowStreamGateway()
	fw, err := agentflow.New(
		streamScenarioForGateway(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithRunLease(locker, "worker-a", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	chunks, err := fw.Stream(agentflow.StreamDetached(ctx), agentflow.RunRequest{RunID: "run-detached-lease", Agent: "assistant", Prompt: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	<-gateway.started
	<-chunks // first chunk
	// Client disconnects mid-stream; the detached run keeps executing
	// (the gateway is still blocked on release).
	cancel()
	time.Sleep(100 * time.Millisecond)
	leaseMustBeHeld(t, locker, "run-detached-lease")
	close(gateway.release)
	for range chunks {
	}
	awaitRunStatus(t, fw, "run-detached-lease", runstate.RunStatusCompleted)
	leaseMustBeFreeEventually(t, locker, "run-detached-lease")
}

// TestFrameworkStreamRunDetachedHoldsLeaseUntilRunEnds covers the same
// invariant through the StreamRun facade with WithStreamDetached, the
// combination SSE handlers use.
func TestFrameworkStreamRunDetachedHoldsLeaseUntilRunEnds(t *testing.T) {
	locker := agentflow.NewInMemoryLocker()
	gateway := newSlowStreamGateway()
	fw, err := agentflow.New(
		streamScenarioForGateway(),
		agentflow.WithLLMGateway(gateway),
		agentflow.WithRunLease(locker, "worker-a", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	frames, err := fw.StreamRun(ctx, agentflow.RunRequest{RunID: "run-detached-lease-sr", Agent: "assistant", Prompt: "hi"}, agentflow.WithStreamDetached())
	if err != nil {
		t.Fatal(err)
	}
	<-gateway.started
	<-frames // first token frame
	cancel()
	time.Sleep(100 * time.Millisecond)
	leaseMustBeHeld(t, locker, "run-detached-lease-sr")
	close(gateway.release)
	for range frames {
	}
	awaitRunStatus(t, fw, "run-detached-lease-sr", runstate.RunStatusCompleted)
	leaseMustBeFreeEventually(t, locker, "run-detached-lease-sr")
}

// transientRenewLocker fails Renew with a transient error for the first
// failCount renewals, then delegates to the inner locker. Unlike
// renewFailLocker (which reports the lease as not-held, a definitive loss),
// the error path exercises the renewal grace window. The transient failure
// models an ambiguous-outcome error (e.g. a store timeout after the write
// applied): the inner lease is still renewed underneath, but the framework
// only sees the error and must tolerate it.
type transientRenewLocker struct {
	inner     coordination.Locker
	renews    atomic.Int32
	failCount int32
}

func (l *transientRenewLocker) Acquire(ctx context.Context, key, owner string, ttl time.Duration) (coordination.Lease, bool, error) {
	return l.inner.Acquire(ctx, key, owner, ttl)
}

func (l *transientRenewLocker) Renew(ctx context.Context, lease coordination.Lease, ttl time.Duration) (coordination.Lease, bool, error) {
	if l.renews.Add(1) <= l.failCount {
		_, _, _ = l.inner.Renew(ctx, lease, ttl)
		return coordination.Lease{}, false, errors.New("transient lease store outage")
	}
	return l.inner.Renew(ctx, lease, ttl)
}

func (l *transientRenewLocker) Release(ctx context.Context, lease coordination.Lease) error {
	return l.inner.Release(ctx, lease)
}

// TestFrameworkRunLeaseToleratesTransientRenewErrors: a couple of transient
// renewal errors inside one TTL must not abort the run; once renewals
// recover, the run completes normally.
func TestFrameworkRunLeaseToleratesTransientRenewErrors(t *testing.T) {
	locker := &transientRenewLocker{inner: agentflow.NewInMemoryLocker(), failCount: 2}
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", slowTool{delay: 200 * time.Millisecond}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		// TTL 90ms renews every 30ms and tolerates 3 consecutive transient
		// failures; 2 must be survived.
		agentflow.WithRunLease(locker, "worker-a", 90*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-transient-renew", Prompt: "go"}); err != nil {
		t.Fatalf("transient renew errors within the TTL grace must not abort the run: %v", err)
	}
	if got := locker.renews.Load(); got < 3 {
		t.Fatalf("expected renewals to recover after transient errors, got %d attempts", got)
	}
}

// TestFrameworkAbortsWhenRenewErrorsExceedTTL: transient renewal errors that
// persist for a full TTL mean the lease has genuinely expired, so the run is
// aborted with ErrRunLeaseLost exactly at the grace limit.
func TestFrameworkAbortsWhenRenewErrorsExceedTTL(t *testing.T) {
	locker := &transientRenewLocker{inner: agentflow.NewInMemoryLocker(), failCount: 100}
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", slowTool{delay: 300 * time.Millisecond}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		// TTL 60ms renews every 20ms, so the grace window is 3 consecutive
		// transient failures.
		agentflow.WithRunLease(locker, "worker-a", 60*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-renew-grace-exceeded", Prompt: "go"})
	if !errors.Is(err, agentflow.ErrRunLeaseLost) {
		t.Fatalf("expected ErrRunLeaseLost after the grace window, got %v", err)
	}
	if got := locker.renews.Load(); got != 3 {
		t.Fatalf("expected abort exactly at the 3rd consecutive failure, got %d attempts", got)
	}
}

func TestRedisLockerFacadeFencingToken(t *testing.T) {
	server := miniredis.RunT(t)
	locker, err := agentflow.NewRedisLocker(agentflow.RedisLockerConfig{Addr: server.Addr()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = locker.Close() })
	ctx := context.Background()
	lease, ok, err := locker.Acquire(ctx, "run:facade", "worker:1", 50*time.Millisecond)
	if err != nil || !ok {
		t.Fatalf("acquire failed: ok=%v err=%v", ok, err)
	}
	if lease.Token == 0 {
		t.Fatal("facade locker must mint a fencing token")
	}
	server.FastForward(time.Second)
	second, ok, err := locker.Acquire(ctx, "run:facade", "worker:2", time.Minute)
	if err != nil || !ok {
		t.Fatalf("takeover acquire failed: ok=%v err=%v", ok, err)
	}
	if second.Token <= lease.Token {
		t.Fatalf("token must increase: first=%d second=%d", lease.Token, second.Token)
	}
	if _, ok, err := locker.Renew(ctx, lease, time.Minute); err != nil || ok {
		t.Fatalf("stale renew must fail: ok=%v err=%v", ok, err)
	}

	shared, err := agentflow.NewRedisLockerFromClient(redis.NewClient(&redis.Options{Addr: server.Addr()}), "shared:")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := shared.Acquire(ctx, "run:shared", "worker:1", time.Minute); err != nil || !ok {
		t.Fatalf("shared-client acquire failed: ok=%v err=%v", ok, err)
	}
}
