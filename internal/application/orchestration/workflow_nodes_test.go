package orchestration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type stubAgent struct {
	name   string
	output string
}

func (a stubAgent) Run(_ context.Context, input core.AgentInput) (core.AgentOutput, error) {
	return core.AgentOutput{RunID: input.RunID, Text: a.output}, nil
}

type stubAgentRegistry struct {
	agents map[string]stubAgent
}

func (r stubAgentRegistry) Agent(name string) (core.AgentRunner, bool) {
	agent, ok := r.agents[name]
	return agent, ok
}

func TestWorkflowRunnerParallelGroup(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil, WithAgentRegistry(stubAgentRegistry{
		agents: map[string]stubAgent{
			"macro":    {name: "macro", output: "macro view"},
			"industry": {name: "industry", output: "industry view"},
		},
	}))
	scenario := core.Scenario{
		Name: "experts",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{
					ID:    "experts",
					Kind:  core.NodeParallelGroup,
					Input: json.RawMessage(`{"refs":["macro","industry"]}`),
				}},
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
	ref, ok := snapshot.StepOutputs["experts"]
	if !ok {
		t.Fatal("expected parallel group output")
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

func TestWorkflowRunnerLoopUntilCondition(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "loop",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "mark", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"approved":true}}`)},
					{
						ID:   "revise",
						Kind: core.NodeLoop,
						Input: json.RawMessage(`{
							"body":["mark"],
							"max_iterations":3,
							"until":"eq(steps.mark.approved,true)"
						}`),
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
	if _, ok := snapshot.StepOutputs["revise"]; !ok {
		t.Fatalf("expected loop output, got %+v", snapshot.StepOutputs)
	}
}
