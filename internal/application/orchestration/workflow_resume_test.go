package orchestration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type capturingAgent struct {
	lastPrompt string
}

func (a *capturingAgent) Run(_ context.Context, input core.AgentInput) (core.AgentOutput, error) {
	a.lastPrompt = input.Prompt
	return core.AgentOutput{RunID: input.RunID, Text: "ok"}, nil
}

func TestWorkflowRunnerAppliesAmendmentToAgentNode(t *testing.T) {
	runs := newWorkflowRun(t)
	agent := &capturingAgent{}
	runner := NewWorkflowRunner(nil, runs, nil, WithAgentRegistry(singleAgentRegistry{agent: agent}))
	snapshot := runstate.RunSnapshot{
		RunID:  "run-amend",
		Status: runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"workflow_amendment": json.RawMessage(`"please revise"`),
		},
		StepOutputs: make(map[string]runstate.StepOutputRef),
	}
	if err := runs.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	scenario := core.Scenario{
		Name: "scenario",
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{
				ID:    "review",
				Kind:  core.NodeAgent,
				Ref:   "assistant",
				Input: json.RawMessage(`{"prompt":"analyze"}`),
			}}},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-amend"); err != nil {
		t.Fatal(err)
	}
	want := "analyze\n\nHuman feedback: please revise"
	if agent.lastPrompt != want {
		t.Fatalf("expected amended prompt %q, got %q", want, agent.lastPrompt)
	}
}

type singleAgentRegistry struct {
	agent *capturingAgent
}

func (r singleAgentRegistry) Agent(string) (core.AgentRunner, bool) {
	return r.agent, true
}
