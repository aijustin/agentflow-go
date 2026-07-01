package orchestration

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// countingGate records how many times Pause is invoked so tests can prove a
// nested pause is not retried.
type countingGate struct {
	*workflowGate
	pauses int
}

func (g *countingGate) Pause(ctx context.Context, state core.CheckpointState) (string, error) {
	g.pauses++
	return g.workflowGate.Pause(ctx, state)
}

// A human gate nested inside a subgraph must pause exactly once and surface a
// WorkflowPausedError, even when the parent node is configured to retry.
func TestWorkflowRunnerSubgraphHumanGateNotRetried(t *testing.T) {
	runs := newWorkflowRun(t)
	gate := &countingGate{workflowGate: &workflowGate{repo: runs}}
	runner := NewWorkflowRunner(registry.New(), runs, nil, WithHumanGate(gate))
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflows: map[string]core.Workflow{
				"prep": {Nodes: []core.WorkflowNode{
					{ID: "approval", Kind: core.NodeHumanGate, Input: json.RawMessage(`{"question":"continue?"}`)},
				}},
			},
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{
				ID:    "run_prep",
				Kind:  core.NodeSubgraph,
				Ref:   "prep",
				Retry: core.RetryPolicy{MaxAttempts: 3},
			}}},
		},
	}
	err := runner.Run(context.Background(), scenario, "run-1")
	var paused WorkflowPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected WorkflowPausedError, got %v", err)
	}
	if gate.pauses != 1 {
		t.Fatalf("expected exactly one pause, got %d (nested pause was retried)", gate.pauses)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused snapshot, got %q", snapshot.Status)
	}
}

// A map node fanning out into subgraph branches that pause must surface the
// pause instead of swallowing it as a collected error under collect_errors.
func TestWorkflowRunnerMapCollectErrorsPropagatesPause(t *testing.T) {
	runs := newWorkflowRun(t)
	gate := &workflowGate{repo: runs}
	runner := NewWorkflowRunner(registry.New(), runs, nil, WithHumanGate(gate))
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflows: map[string]core.Workflow{
				"branch": {Nodes: []core.WorkflowNode{
					{ID: "approval", Kind: core.NodeHumanGate, Input: json.RawMessage(`{"question":"continue?"}`)},
				}},
			},
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "items", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"list":["a"]}}`)},
				{
					ID:        "fanout",
					Kind:      core.NodeMap,
					DependsOn: []string{"items"},
					Input: json.RawMessage(`{
						"items_path": "steps.items.list",
						"on_error": "collect_errors",
						"branch": {"kind": "subgraph", "ref": "branch"}
					}`),
				},
			}},
		},
	}
	err := runner.Run(context.Background(), scenario, "run-1")
	var paused WorkflowPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected WorkflowPausedError under collect_errors, got %v", err)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused snapshot, got %q", snapshot.Status)
	}
	if _, ok := snapshot.StepOutputs["fanout"]; ok {
		t.Fatalf("map node must not complete while a branch is paused: %+v", snapshot.StepOutputs)
	}
}

func TestWorkflowRunnerParallelGroupPrefersPauseOverError(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil, WithAgentRegistry(mixedAgentRegistry{}))
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{
				ID:    "group",
				Kind:  core.NodeParallelGroup,
				Input: json.RawMessage(`{"refs":["pauser","failing"]}`),
			}}},
		},
	}
	err := runner.Run(context.Background(), scenario, "run-1")
	var paused WorkflowPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected WorkflowPausedError, got %v", err)
	}
}

// A loop with a human_gate inside its body must not re-run an already
// completed iteration's side effects when resumed, and must not re-run a
// later iteration's body more than once either.
func TestWorkflowRunnerLoopResumeDoesNotRerunCompletedIterations(t *testing.T) {
	runs := newWorkflowRun(t)
	gate := &workflowGate{repo: runs}
	var mu sync.Mutex
	incrementCalls := 0
	reg := registry.New()
	if err := reg.RegisterTool("increment", toolFunc(func(context.Context, core.ToolCall) (core.ToolResult, error) {
		mu.Lock()
		incrementCalls++
		n := incrementCalls
		mu.Unlock()
		raw, _ := json.Marshal(map[string]int{"n": n})
		return core.ToolResult{Output: raw}, nil
	})); err != nil {
		t.Fatal(err)
	}
	runner := NewWorkflowRunner(reg, runs, nil, WithHumanGate(gate))
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "increment", Kind: core.NodeTool, Ref: "increment"},
				{ID: "gate", Kind: core.NodeHumanGate, Input: json.RawMessage(`{"question":"continue?"}`)},
				{ID: "loop", Kind: core.NodeLoop, Input: json.RawMessage(`{"body":["increment","gate"],"max_iterations":2}`)},
			}},
		},
	}

	err := runner.Run(context.Background(), scenario, "run-1")
	var paused WorkflowPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected WorkflowPausedError on iteration 1, got %v", err)
	}
	if incrementCalls != 1 {
		t.Fatalf("expected increment to run once before first pause, got %d", incrementCalls)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}

	// Resuming must complete iteration 1 (skipping the already-run
	// "increment" tool and the already-approved gate) and then pause again
	// on iteration 2's fresh gate - it must not re-run "increment" a second
	// time for iteration 1.
	err = runner.Resume(context.Background(), scenario, "run-1")
	if !errors.As(err, &paused) {
		t.Fatalf("expected WorkflowPausedError on iteration 2, got %v", err)
	}
	if incrementCalls != 2 {
		t.Fatalf("expected increment to run exactly twice (once per iteration) after first resume, got %d", incrementCalls)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}

	if err := runner.Resume(context.Background(), scenario, "run-1"); err != nil {
		t.Fatalf("expected loop to finish after second resume, got %v", err)
	}
	if incrementCalls != 2 {
		t.Fatalf("expected increment to have run exactly twice total, got %d", incrementCalls)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	var output map[string]any
	if err := json.Unmarshal(snapshot.StepOutputs["loop"].Inline, &output); err != nil {
		t.Fatal(err)
	}
	if output["iterations"] != float64(2) || output["completed"] != true {
		t.Fatalf("expected loop to report 2 completed iterations, got %+v", output)
	}
}

// A human_gate nested inside a subgraph must be recognized as already
// resolved when the run is resumed, instead of re-executing the subgraph
// from its first node and pausing on the same gate a second time.
func TestWorkflowRunnerSubgraphHumanGateResumeCompletes(t *testing.T) {
	runs := newWorkflowRun(t)
	gate := &countingGate{workflowGate: &workflowGate{repo: runs}}
	runner := NewWorkflowRunner(registry.New(), runs, nil, WithHumanGate(gate))
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflows: map[string]core.Workflow{
				"prep": {Nodes: []core.WorkflowNode{
					{ID: "before", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"ran":true}}`)},
					{ID: "approval", Kind: core.NodeHumanGate, DependsOn: []string{"before"}, Input: json.RawMessage(`{"question":"continue?"}`)},
				}},
			},
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{
				ID:   "run_prep",
				Kind: core.NodeSubgraph,
				Ref:  "prep",
			}}},
		},
	}
	err := runner.Run(context.Background(), scenario, "run-1")
	var paused WorkflowPausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected WorkflowPausedError, got %v", err)
	}
	if err := gate.Resume(context.Background(), paused.Token, core.DecisionApprove, nil); err != nil {
		t.Fatal(err)
	}

	if err := runner.Resume(context.Background(), scenario, "run-1"); err != nil {
		t.Fatalf("expected subgraph to complete after resume, got %v", err)
	}
	if gate.pauses != 1 {
		t.Fatalf("expected the gate to pause exactly once, got %d (resume re-triggered it)", gate.pauses)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["run_prep"]; !ok {
		t.Fatalf("expected subgraph node to have completed, got %+v", snapshot.StepOutputs)
	}
}

type mixedAgentRegistry struct{}

func (mixedAgentRegistry) Agent(name string) (core.AgentRunner, bool) {
	if name == "pauser" {
		return pausingAgent{}, true
	}
	if name == "failing" {
		return failingAgent{}, true
	}
	return nil, false
}

type pausingAgent struct{}

func (pausingAgent) Run(_ context.Context, input core.AgentInput) (core.AgentOutput, error) {
	return core.AgentOutput{}, WorkflowPausedError{RunID: input.RunID, NodeID: "pauser", Token: "tok"}
}

type failingAgent struct{}

func (failingAgent) Run(context.Context, core.AgentInput) (core.AgentOutput, error) {
	return core.AgentOutput{}, errors.New("agent failed")
}
