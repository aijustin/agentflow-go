package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func saveWorkflowSnapshot(t *testing.T, runs runstate.Repository, snapshot runstate.RunSnapshot) {
	t.Helper()
	if snapshot.StepOutputs == nil {
		snapshot.StepOutputs = make(map[string]runstate.StepOutputRef)
	}
	if err := runs.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
}

type promptRecorder struct {
	mu      sync.Mutex
	prompts map[string]string
}

func (r *promptRecorder) record(name, prompt string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.prompts == nil {
		r.prompts = make(map[string]string)
	}
	r.prompts[name] = prompt
}

func (r *promptRecorder) countWithFeedback() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, prompt := range r.prompts {
		if strings.Contains(prompt, "please revise") {
			count++
		}
	}
	return count
}

func (r *promptRecorder) countNonEmpty() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, prompt := range r.prompts {
		if strings.TrimSpace(prompt) != "" {
			count++
		}
	}
	return count
}

type namedCapturingAgent struct {
	name string
	rec  *promptRecorder
}

func (a *namedCapturingAgent) Run(_ context.Context, input core.AgentInput) (core.AgentOutput, error) {
	a.rec.record(a.name, input.Prompt)
	return core.AgentOutput{RunID: input.RunID, Text: "ok"}, nil
}

type namedAgentRegistry struct {
	agents map[string]core.AgentRunner
}

func (r namedAgentRegistry) Agent(name string) (core.AgentRunner, bool) {
	agent, ok := r.agents[name]
	return agent, ok
}

// Concurrent parallel_group siblings must atomically claim workflow_amendment
// so human feedback reaches only one member, not every agent in the batch.
func TestWorkflowRunnerParallelGroupAmendmentClaimedOnce(t *testing.T) {
	runs := newWorkflowRun(t)
	rec := &promptRecorder{}
	runner := NewWorkflowRunner(nil, runs, nil, WithAgentRegistry(namedAgentRegistry{
		agents: map[string]core.AgentRunner{
			"alpha": &namedCapturingAgent{name: "alpha", rec: rec},
			"beta":  &namedCapturingAgent{name: "beta", rec: rec},
		},
	}))
	saveWorkflowSnapshot(t, runs, runstate.RunSnapshot{
		RunID:  "run-parallel-amend",
		Status: runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"workflow_amendment": json.RawMessage(`"please revise"`),
		},
	})
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{
				ID:   "experts",
				Kind: core.NodeParallelGroup,
				Input: json.RawMessage(`{
					"refs":["alpha","beta"]
				}`),
			}}},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-parallel-amend"); err != nil {
		t.Fatal(err)
	}
	if got := rec.countWithFeedback(); got != 1 {
		t.Fatalf("expected exactly one parallel member to receive amendment, got %d: %+v", got, rec.prompts)
	}
	if got := rec.countNonEmpty(); got != 1 {
		t.Fatalf("expected exactly one member with non-empty prompt, got %d: %+v", got, rec.prompts)
	}
	loaded, err := runs.Load(context.Background(), "run-parallel-amend")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Variables["workflow_amendment"]; ok {
		t.Fatalf("expected amendment cleared after parallel group, still present: %+v", loaded.Variables)
	}
}

func TestWorkflowValuesEqualNormalizesNumericTypes(t *testing.T) {
	tests := []struct {
		left  any
		right any
		want  bool
	}{
		{left: float64(1), right: int64(1), want: true},
		{left: float64(1), right: int(1), want: true},
		{left: float64(1.5), right: int64(1), want: false},
		{left: json.Number("1"), right: int64(1), want: true},
	}
	for _, tt := range tests {
		if got := workflowValuesEqual(tt.left, tt.right); got != tt.want {
			t.Fatalf("workflowValuesEqual(%#v, %#v) = %v, want %v", tt.left, tt.right, got, tt.want)
		}
	}
}

func TestWorkflowRunnerConditionEqTreatsOneAndOnePointZeroAsEqual(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	saveWorkflowSnapshot(t, runs, runstate.RunSnapshot{
		RunID:  "run-eq",
		Status: runstate.RunStatusRunning,
	})
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "set", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"count":1}}`)},
				{
					ID:        "check",
					Kind:      core.NodeTransform,
					DependsOn: []string{"set"},
					Condition: `eq(steps.set.count,1.0)`,
					Input:     json.RawMessage(`{"set":{"matched":true}}`),
				},
			}},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-eq"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(context.Background(), "run-eq")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["check"]; !ok {
		t.Fatalf("expected eq(1,1.0) condition to pass, outputs: %+v", snapshot.StepOutputs)
	}
}

// stepOutputRaw must keep working after a sibling cancels groupCtx; parallel
// decode therefore uses the outer ctx, not groupCtx.
func TestStepOutputRawFailsWhenContextCancelled(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	ctx := context.Background()
	if err := runner.saveStepOutput(ctx, core.Scenario{Name: "scenario"}, "run-1", "member", map[string]string{"ok": "true"}); err != nil {
		t.Fatal(err)
	}
	groupCtx, cancel := context.WithCancel(ctx)
	cancel()
	if _, _, err := runner.stepOutputRaw(groupCtx, "run-1", "member"); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancelled context to fail stepOutputRaw, got %v", err)
	}
	if _, ok, err := runner.stepOutputRaw(ctx, "run-1", "member"); err != nil || !ok {
		t.Fatalf("expected outer context to read saved output, ok=%v err=%v", ok, err)
	}
}

type blockingPauserAgent struct {
	release <-chan struct{}
}

func (a blockingPauserAgent) Run(ctx context.Context, input core.AgentInput) (core.AgentOutput, error) {
	select {
	case <-a.release:
	case <-ctx.Done():
		return core.AgentOutput{}, ctx.Err()
	}
	return core.AgentOutput{}, WorkflowPausedError{RunID: input.RunID, NodeID: "pauser", Token: "tok"}
}

// A fast parallel member that finishes before a sibling pauses must still
// persist its step output and remain readable after groupCtx is cancelled.
func TestWorkflowRunnerParallelGroupPreservesCompletedMemberOnPause(t *testing.T) {
	runs := newWorkflowRun(t)
	releasePauser := make(chan struct{})
	runner := NewWorkflowRunner(nil, runs, nil, WithAgentRegistry(namedAgentRegistry{
		agents: map[string]core.AgentRunner{
			"fast":   &namedCapturingAgent{name: "fast", rec: &promptRecorder{}},
			"pauser": blockingPauserAgent{release: releasePauser},
		},
	}))
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{
				ID:    "group",
				Kind:  core.NodeParallelGroup,
				Input: json.RawMessage(`{"refs":["fast","pauser"]}`),
			}}},
		},
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- runner.Run(context.Background(), scenario, "run-1")
	}()
	deadline := time.Now().Add(2 * time.Second)
	for {
		snapshot, err := runs.Load(context.Background(), "run-1")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := snapshot.StepOutputs["group.agent.fast.0"]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for fast parallel member output")
		}
		time.Sleep(5 * time.Millisecond)
	}
	close(releasePauser)
	err := <-errCh
	var paused WorkflowPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected WorkflowPausedError, got %v", err)
	}
	if _, ok, err := runner.stepOutputRaw(context.Background(), "run-1", "group.agent.fast.0"); err != nil || !ok {
		t.Fatalf("expected completed member output to remain readable after pause, ok=%v err=%v", ok, err)
	}
}
