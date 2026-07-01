package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"

	blobinmem "github.com/aijustin/agentflow-go/internal/adapter/blob/inmem"
	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/internal/adapter/tool/builtin"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestWorkflowRunnerExecutesToolNode(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("echo", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(reg, runs, nil)
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{ID: "echo", Kind: core.NodeTool, Ref: "echo", Input: []byte(`{"message":"hi"}`)}},
			},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["echo"]; !ok {
		t.Fatal("expected tool output in run snapshot")
	}
}

func TestWorkflowRunnerUsesDependenciesAndRetries(t *testing.T) {
	reg := registry.New()
	state := &workflowTestState{seen: make(map[string]bool), failures: make(map[string]int)}
	if err := reg.RegisterTool("first", state.tool("first", "", 0)); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterTool("second", state.tool("second", "first", 1)); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(reg, runs, nil)
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			MaxParallel: 2,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "b", Kind: core.NodeTool, Ref: "second", Retry: core.RetryPolicy{MaxAttempts: 2}},
					{ID: "a", Kind: core.NodeTool, Ref: "first"},
				},
				Edges: []core.WorkflowEdge{{From: "a", To: "b"}},
			},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	if state.failures["second"] != 1 {
		t.Fatalf("expected one retryable failure, got %+v", state.failures)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["a"]; !ok {
		t.Fatalf("missing output a: %+v", loaded.StepOutputs)
	}
	if _, ok := loaded.StepOutputs["b"]; !ok {
		t.Fatalf("missing output b: %+v", loaded.StepOutputs)
	}
}

func TestWorkflowRunnerDoesNotRetryPermanentError(t *testing.T) {
	reg := registry.New()
	calls := 0
	if err := reg.RegisterTool("flaky", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		calls++
		return core.ToolResult{}, fmt.Errorf("permanent failure")
	})); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "flaky", Kind: core.NodeTool, Ref: "flaky", Retry: core.RetryPolicy{MaxAttempts: 3}},
			}},
		},
	}
	err := NewWorkflowRunner(reg, runs, nil).Run(context.Background(), scenario, "run-1")
	if err == nil || calls != 1 {
		t.Fatalf("permanent error must fail after one attempt, got err=%v calls=%d", err, calls)
	}
}

func TestWorkflowRunnerDoesNotAutoRetrySideEffectToolNode(t *testing.T) {
	reg := registry.New()
	calls := 0
	if err := reg.RegisterTool("writer", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		calls++
		return core.ToolResult{}, transientWorkflowError{message: "flaky write"}
	})); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name:    "scenario",
		Runtime: core.RuntimePolicy{MaxRetries: 3},
		Tools: map[string]core.Tool{
			"writer": {Name: "writer", SideEffect: core.SideEffectWrite},
		},
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "writer", Kind: core.NodeTool, Ref: "writer"},
			}},
		},
	}
	err := NewWorkflowRunner(reg, runs, nil).Run(context.Background(), scenario, "run-1")
	if err == nil || calls != 1 {
		t.Fatalf("write side-effect node must not auto-retry, got err=%v calls=%d", err, calls)
	}
	// An explicit per-node retry policy remains an opt-in override.
	calls = 0
	scenario.Orchestration.Workflow.Nodes[0].Retry = core.RetryPolicy{MaxAttempts: 2}
	err = NewWorkflowRunner(reg, newWorkflowRun(t), nil).Run(context.Background(), scenario, "run-2")
	if err == nil || calls != 2 {
		t.Fatalf("explicit retry policy must be honored, got err=%v calls=%d", err, calls)
	}
}

func TestWorkflowRunnerSkipsFalseConditionAndRunsParallelOutputs(t *testing.T) {
	reg := registry.New()
	state := &workflowTestState{seen: make(map[string]bool)}
	for _, name := range []string{"one", "two", "skip"} {
		if err := reg.RegisterTool(name, state.tool(name, "", 0)); err != nil {
			t.Fatal(err)
		}
	}
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			MaxParallel: 2,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "one", Kind: core.NodeTool, Ref: "one"},
				{ID: "two", Kind: core.NodeTool, Ref: "two"},
				{ID: "skip", Kind: core.NodeTool, Ref: "skip", Condition: "false"},
			}},
		},
	}
	if err := NewWorkflowRunner(reg, runs, nil).Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["one"]; !ok {
		t.Fatalf("missing output one: %+v", loaded.StepOutputs)
	}
	if _, ok := loaded.StepOutputs["two"]; !ok {
		t.Fatalf("missing output two: %+v", loaded.StepOutputs)
	}
	if _, ok := loaded.StepOutputs["skip"]; ok {
		t.Fatalf("skipped node should not have output: %+v", loaded.StepOutputs)
	}
}

func TestWorkflowRunnerEvaluatesConditionFromStepOutputs(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("status", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		return core.ToolResult{Tool: "status", Output: json.RawMessage(`{"status":"ready"}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterTool("skip", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	if err := reg.RegisterTool("run", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{Workflow: &core.Workflow{
			Nodes: []core.WorkflowNode{
				{ID: "status", Kind: core.NodeTool, Ref: "status"},
				{ID: "skip", Kind: core.NodeTool, Ref: "skip", DependsOn: []string{"status"}, Condition: `eq(steps.status.output.status, "blocked")`},
				{ID: "run", Kind: core.NodeTool, Ref: "run", DependsOn: []string{"status"}, Condition: `eq(steps.status.output.status, "ready")`},
			},
		}},
	}

	if err := NewWorkflowRunner(reg, runs, nil).Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["skip"]; ok {
		t.Fatalf("skip node should not have output: %+v", loaded.StepOutputs)
	}
	if _, ok := loaded.StepOutputs["run"]; !ok {
		t.Fatalf("run node should have output: %+v", loaded.StepOutputs)
	}
}

func TestWorkflowRunnerTransformCopiesPreviousStepOutput(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("first", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		return core.ToolResult{Tool: "first", Output: json.RawMessage(`{"message":"hello","nested":{"id":"42"}}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{Workflow: &core.Workflow{
			Nodes: []core.WorkflowNode{
				{ID: "first", Kind: core.NodeTool, Ref: "first"},
				{ID: "shape", Kind: core.NodeTransform, DependsOn: []string{"first"}, Input: json.RawMessage(`{"set":{"kind":"summary"},"copy":{"message":"steps.first.output.message","id":"steps.first.output.nested.id"}}`)},
			},
		}},
	}

	if err := NewWorkflowRunner(reg, runs, nil).Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(loaded.StepOutputs["shape"].Inline, &output); err != nil {
		t.Fatal(err)
	}
	if output["kind"] != "summary" || output["message"] != "hello" || output["id"] != "42" {
		t.Fatalf("unexpected transform output: %+v", output)
	}
}

func TestWorkflowRunnerPausesAfterDeclarativeInterrupt(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("after", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	gate := &workflowGate{repo: runs}
	runner := NewWorkflowRunner(reg, runs, nil, WithHumanGate(gate))
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "work", Kind: core.NodeTransform, Interrupt: true, Input: json.RawMessage(`{"set":{"done":true}}`)},
					{ID: "after", Kind: core.NodeTool, Ref: "after"},
				},
				Edges: []core.WorkflowEdge{{From: "work", To: "after"}},
			},
		},
	}
	err := runner.Run(context.Background(), scenario, "run-1")
	var paused WorkflowPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected WorkflowPausedError, got %v", err)
	}
	if paused.NodeID != "work" {
		t.Fatalf("unexpected pause node: %+v", paused)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusPaused || snapshot.CurrentNodeID != "work" {
		t.Fatalf("expected paused at work, got %+v", snapshot)
	}
	if _, ok := snapshot.StepOutputs["work"]; !ok {
		t.Fatalf("expected work output before pause: %+v", snapshot.StepOutputs)
	}
	if _, ok := snapshot.StepOutputs["after"]; ok {
		t.Fatalf("downstream should not run before resume: %+v", snapshot.StepOutputs)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	if err := runner.Resume(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["after"]; !ok {
		t.Fatalf("expected downstream output after resume: %+v", loaded.StepOutputs)
	}
}

func TestWorkflowRunnerPausesAndResumesHumanGate(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("after", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	gate := &workflowGate{repo: runs}
	runner := NewWorkflowRunner(reg, runs, nil, WithHumanGate(gate))
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "approval", Kind: core.NodeHumanGate, Input: json.RawMessage(`{"question":"continue?"}`)},
					{ID: "after", Kind: core.NodeTool, Ref: "after"},
				},
				Edges: []core.WorkflowEdge{{From: "approval", To: "after"}},
			},
		},
	}
	err := runner.Run(context.Background(), scenario, "run-1")
	var paused WorkflowPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected WorkflowPausedError, got %v", err)
	}
	if paused.Token == "" || paused.NodeID != "approval" {
		t.Fatalf("unexpected pause error: %+v", paused)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusPaused || snapshot.CurrentNodeID != "approval" || snapshot.PendingGate == nil {
		t.Fatalf("expected paused snapshot at approval, got %+v", snapshot)
	}
	if _, ok := snapshot.StepOutputs["after"]; ok {
		t.Fatalf("downstream node should not run before resume: %+v", snapshot.StepOutputs)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}
	if err := runner.Resume(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["after"]; !ok {
		t.Fatalf("expected downstream output after resume: %+v", loaded.StepOutputs)
	}
}

func TestWorkflowRunnerExternalizesLargeStepOutput(t *testing.T) {
	reg := registry.New()
	if err := reg.RegisterTool("large", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		return core.ToolResult{Tool: "large", Output: json.RawMessage(`{"text":"this output is intentionally large"}`)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	runs := newWorkflowRun(t)
	blobs := blobinmem.NewStore()
	runner := NewWorkflowRunner(reg, runs, nil, WithBlobStore(blobs))
	scenario := core.Scenario{
		Name:    "scenario",
		Runtime: core.RuntimePolicy{StepOutputThreshold: 8},
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{ID: "large", Kind: core.NodeTool, Ref: "large"}}},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.StepOutputs["large"].Blob == nil {
		t.Fatalf("expected blob ref, got %+v", loaded.StepOutputs["large"])
	}
}

func newWorkflowRun(t *testing.T) runstate.Repository {
	t.Helper()
	runs := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{RunID: "run-1", ScenarioName: "scenario", Status: runstate.RunStatusRunning}
	if err := runs.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	return runs
}

type workflowTestState struct {
	mu       sync.Mutex
	seen     map[string]bool
	failures map[string]int
}

func (s *workflowTestState) tool(name, requires string, failBeforeSuccess int) core.ToolExecutor {
	return toolFunc(func(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
		if err := ctx.Err(); err != nil {
			return core.ToolResult{}, err
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if requires != "" && !s.seen[requires] {
			return core.ToolResult{}, fmt.Errorf("%s ran before %s", name, requires)
		}
		if s.failures == nil {
			s.failures = make(map[string]int)
		}
		if s.failures[name] < failBeforeSuccess {
			s.failures[name]++
			return core.ToolResult{}, transientWorkflowError{message: "temporary failure"}
		}
		s.seen[name] = true
		raw, _ := json.Marshal(map[string]string{"tool": name})
		return core.ToolResult{Tool: call.Tool, Output: raw}, nil
	})
}

// transientWorkflowError classifies itself as retryable, matching the shared
// pkg/retry semantics used by runNodeWithRetry.
type transientWorkflowError struct{ message string }

func (e transientWorkflowError) Error() string { return e.message }

func (e transientWorkflowError) Retryable() bool { return true }

type toolFunc func(context.Context, core.ToolCall) (core.ToolResult, error)

func (f toolFunc) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	return f(ctx, call)
}

type workflowGate struct {
	repo   runstate.Repository
	mu     sync.Mutex
	tokens map[string]string
}

func (g *workflowGate) Pause(ctx context.Context, state core.CheckpointState) (string, error) {
	snapshot, err := g.repo.Load(ctx, state.RunID)
	if err != nil {
		return "", err
	}
	if snapshot.Version != state.Version {
		return "", runstate.ErrStaleSnapshot
	}
	snapshot.Status = runstate.RunStatusPaused
	snapshot.PendingGate = &state
	if err := g.repo.Save(ctx, &snapshot, state.Version); err != nil {
		return "", err
	}
	token := "token-" + state.NodeID
	g.mu.Lock()
	if g.tokens == nil {
		g.tokens = make(map[string]string)
	}
	g.tokens[token] = state.RunID
	g.mu.Unlock()
	return token, nil
}

func (g *workflowGate) Resume(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) error {
	g.mu.Lock()
	runID := g.tokens[token]
	g.mu.Unlock()
	if runID == "" {
		return fmt.Errorf("unknown token")
	}
	snapshot, err := g.repo.Load(ctx, runID)
	if err != nil {
		return err
	}
	if decision == core.DecisionReject {
		snapshot.Status = runstate.RunStatusCancelled
	} else {
		snapshot.Status = runstate.RunStatusRunning
	}
	snapshot.PendingGate = nil
	return g.repo.Save(ctx, &snapshot, snapshot.Version)
}
