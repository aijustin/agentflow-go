package orchestration

import (
	"context"
	"encoding/json"
	"errors"
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
