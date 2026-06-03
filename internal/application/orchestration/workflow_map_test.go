package orchestration

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestWorkflowRunnerMapNodeWithAgents(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil, WithAgentRegistry(stubAgentRegistry{
		agents: map[string]stubAgent{
			"analyst": {name: "analyst", output: "done"},
		},
	}))
	scenario := core.Scenario{
		Name: "map-agents",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{
						ID:   "items",
						Kind: core.NodeTransform,
						Input: json.RawMessage(`{
							"set": {"list": ["alpha", "beta"]}
						}`),
					},
					{
						ID:   "fanout",
						Kind: core.NodeMap,
						Input: json.RawMessage(`{
							"items_path": "steps.items.list",
							"branch": {"kind": "agent", "ref": "analyst"}
						}`),
						DependsOn: []string{"items"},
					},
				},
			},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := snapshot.StepOutputs["fanout"]
	if !ok {
		t.Fatal("expected map node output")
	}
	var output map[string]any
	if err := json.Unmarshal(ref.Inline, &output); err != nil {
		t.Fatal(err)
	}
	members, ok := output["members"].(map[string]any)
	if !ok || len(members) != 2 {
		t.Fatalf("expected two member outputs, got %+v", output)
	}
}

func TestWorkflowRunnerMapNodeWithTransformBranch(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "map-transform",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{
						ID:   "items",
						Kind: core.NodeTransform,
						Input: json.RawMessage(`{
							"set": {"list": ["x", "y", "z"]}
						}`),
					},
					{
						ID:   "fanout",
						Kind: core.NodeMap,
						Input: json.RawMessage(`{
							"items_path": "steps.items.list",
							"branch": {"kind": "transform", "input": {"set": {"tag": "mapped"}}}
						}`),
						DependsOn: []string{"items"},
					},
				},
			},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := snapshot.StepOutputs["fanout"]
	if !ok {
		t.Fatal("expected map node output")
	}
	var output map[string]any
	if err := json.Unmarshal(ref.Inline, &output); err != nil {
		t.Fatal(err)
	}
	members, ok := output["members"].(map[string]any)
	if !ok || len(members) != 3 {
		t.Fatalf("expected three member outputs, got %+v", output)
	}
	first, ok := members["0"].(map[string]any)
	if !ok || first["item"] != "x" || first["tag"] != "mapped" {
		t.Fatalf("unexpected first member output: %+v", members["0"])
	}
}

func TestWorkflowRunnerMapNodeEmptyItems(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "map-empty",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{
						ID:   "items",
						Kind: core.NodeTransform,
						Input: json.RawMessage(`{
							"set": {"list": []}
						}`),
					},
					{
						ID:   "fanout",
						Kind: core.NodeMap,
						Input: json.RawMessage(`{
							"items_path": "steps.items.list",
							"branch": {"kind": "transform"}
						}`),
						DependsOn: []string{"items"},
					},
				},
			},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := snapshot.StepOutputs["fanout"]
	if !ok {
		t.Fatal("expected map node output")
	}
	var output map[string]any
	if err := json.Unmarshal(ref.Inline, &output); err != nil {
		t.Fatal(err)
	}
	members, ok := output["members"].(map[string]any)
	if !ok || len(members) != 0 {
		t.Fatalf("expected empty members, got %+v", output)
	}
}

// trackingAgent records the peak number of concurrently executing Run calls so
// a test can assert the map node honors its concurrency bound.
type trackingAgent struct {
	name   string
	active int32
	peak   int32
	pmu    sync.Mutex
}

func (a *trackingAgent) Run(_ context.Context, input core.AgentInput) (core.AgentOutput, error) {
	now := atomic.AddInt32(&a.active, 1)
	a.pmu.Lock()
	if now > a.peak {
		a.peak = now
	}
	a.pmu.Unlock()
	time.Sleep(5 * time.Millisecond)
	atomic.AddInt32(&a.active, -1)
	return core.AgentOutput{RunID: input.RunID, Text: a.name}, nil
}

type trackingRegistry struct {
	agent core.AgentRunner
}

func (r trackingRegistry) Agent(string) (core.AgentRunner, bool) {
	return r.agent, true
}

func TestWorkflowRunnerMapNodeBoundsConcurrency(t *testing.T) {
	runs := newWorkflowRun(t)
	tracker := &trackingAgent{name: "worker"}
	runner := NewWorkflowRunner(nil, runs, nil, WithAgentRegistry(trackingRegistry{agent: tracker}))
	scenario := core.Scenario{
		Name: "map-bounded",
		Orchestration: core.Orchestration{
			MaxParallel: 3,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{
						ID:    "items",
						Kind:  core.NodeTransform,
						Input: json.RawMessage(`{"set": {"list": [1,2,3,4,5,6,7,8,9,10,11,12,13,14,15,16,17,18,19,20]}}`),
					},
					{
						ID:   "fanout",
						Kind: core.NodeMap,
						Input: json.RawMessage(`{
							"items_path": "steps.items.list",
							"branch": {"kind": "agent", "ref": "worker"}
						}`),
						DependsOn: []string{"items"},
					},
				},
			},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	if tracker.peak > 3 {
		t.Fatalf("expected peak concurrency <= 3, got %d", tracker.peak)
	}
	if tracker.peak < 2 {
		t.Fatalf("expected branches to run concurrently (peak >= 2), got %d", tracker.peak)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	ref, ok := snapshot.StepOutputs["fanout"]
	if !ok {
		t.Fatal("expected map node output")
	}
	var output map[string]any
	if err := json.Unmarshal(ref.Inline, &output); err != nil {
		t.Fatal(err)
	}
	members, ok := output["members"].(map[string]any)
	if !ok || len(members) != 20 {
		t.Fatalf("expected 20 member outputs, got %d", len(members))
	}
}
