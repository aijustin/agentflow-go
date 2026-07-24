package agentflow_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	agentflow "github.com/aijustin/agentflow-go"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// fenceCountingRepo wraps the inmem FencedRepository and records how many
// saves went through SaveFenced vs plain Save, so tests can assert that a
// leased run fences every snapshot write.
type fenceCountingRepo struct {
	runstate.Repository
	fenced atomic.Int32
	plain  atomic.Int32
}

func newFenceCountingRepo() *fenceCountingRepo {
	return &fenceCountingRepo{Repository: runstateinmem.NewRepository()}
}

func (r *fenceCountingRepo) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	r.plain.Add(1)
	return r.Repository.Save(ctx, snapshot, expectedVersion)
}

func (r *fenceCountingRepo) SaveFenced(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64, fenceToken uint64) error {
	r.fenced.Add(1)
	return r.Repository.(runstate.FencedRepository).SaveFenced(ctx, snapshot, expectedVersion, fenceToken)
}

func (r *fenceCountingRepo) assertAllFenced(t *testing.T) {
	t.Helper()
	if r.fenced.Load() == 0 {
		t.Fatal("expected at least one fenced save during a leased run")
	}
	if r.plain.Load() != 0 {
		t.Fatalf("expected no plain Save during a leased run, got %d", r.plain.Load())
	}
}

// unfencedRunstateRepo hides SaveFenced so the framework must fall back to
// plain Save (and warn once) even though a lease is held.
type unfencedRunstateRepo struct {
	runstate.Repository
}

type warnCountingLogger struct {
	warns  atomic.Int32
	errors atomic.Int32
}

func (l *warnCountingLogger) Warn(context.Context, string, ...any)  { l.warns.Add(1) }
func (l *warnCountingLogger) Error(context.Context, string, ...any) { l.errors.Add(1) }

func TestFrameworkRunFencesSnapshotSaves(t *testing.T) {
	repo := newFenceCountingRepo()
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunStateRepository(repo),
		agentflow.WithRunLease(agentflow.NewInMemoryLocker(), "worker-a", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-fenced", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run, got %+v", result)
	}
	repo.assertAllFenced(t)
}

func TestFrameworkResumeAndContinueFencesSnapshotSaves(t *testing.T) {
	repo := newFenceCountingRepo()
	scenario := core.Scenario{
		Name: "continue-fence",
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
		agentflow.WithToolExecutor("slow", noopTool{}),
		agentflow.WithRunStateRepository(repo),
		agentflow.WithRunLease(agentflow.NewInMemoryLocker(), "worker-a", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-resume-fenced", Prompt: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused run, got %+v", result)
	}
	continued, err := fw.ResumeAndContinue(context.Background(), result.Token, core.DecisionApprove, nil)
	if err != nil {
		t.Fatal(err)
	}
	if continued.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run after resume, got %+v", continued)
	}
	repo.assertAllFenced(t)
}

func TestFrameworkRetryFailedRunFencesSnapshotSaves(t *testing.T) {
	repo := newFenceCountingRepo()
	var mu sync.Mutex
	bShouldFail := true
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", retryToolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
			mu.Lock()
			defer mu.Unlock()
			if bShouldFail {
				return core.ToolResult{}, fmt.Errorf("downstream unavailable")
			}
			return core.ToolResult{Tool: "stepB", Output: json.RawMessage(`{"ok":true}`)}, nil
		})),
		agentflow.WithRunStateRepository(repo),
		agentflow.WithRunLease(agentflow.NewInMemoryLocker(), "worker-a", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-retry-fenced", Prompt: "go"}); err == nil {
		t.Fatal("expected workflow failure")
	}
	snapshot, err := repo.Load(context.Background(), "run-retry-fenced")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusFailed {
		t.Fatalf("expected failed run, got %s", snapshot.Status)
	}
	mu.Lock()
	bShouldFail = false
	mu.Unlock()
	result, err := fw.RetryFailedRun(context.Background(), "run-retry-fenced")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run after retry, got %+v", result)
	}
	repo.assertAllFenced(t)
}

// TestFrameworkRunLeaseWithoutFencedRepositoryWarnsOnce covers repositories
// that cannot fence (file, redis runstate): the leased run falls back to
// plain Save, logs the multi-node-unsafe warning exactly once, and still
// completes normally.
func TestFrameworkRunLeaseWithoutFencedRepositoryWarnsOnce(t *testing.T) {
	logger := &warnCountingLogger{}
	repo := unfencedRunstateRepo{Repository: runstateinmem.NewRepository()}
	fw, err := agentflow.New(
		retryWorkflowScenario(),
		agentflow.WithLLMGateway(fakeGateway{content: "x"}),
		agentflow.WithToolExecutor("stepA", noopTool{}),
		agentflow.WithToolExecutor("stepB", noopTool{}),
		agentflow.WithRunStateRepository(repo),
		agentflow.WithLogger(logger),
		agentflow.WithRunLease(agentflow.NewInMemoryLocker(), "worker-a", time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-unfenced", Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("expected completed run without fencing support, got %+v", result)
	}
	if logger.errors.Load() != 0 {
		t.Fatalf("expected no error logs, got %d", logger.errors.Load())
	}
	// The framework facade and the runtime engine each warn at most once;
	// the workflow path here saves through the facade, so exactly one
	// warning must have been logged across the whole run.
	if got := logger.warns.Load(); got != 1 {
		t.Fatalf("expected exactly one fencing-unavailable warning, got %d", got)
	}
}
