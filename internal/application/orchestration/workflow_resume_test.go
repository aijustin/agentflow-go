package orchestration

import (
	"context"
	"encoding/json"
	"strings"
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

func TestApplyWorkflowAmendmentToAgentInput(t *testing.T) {
	snapshot := runstate.RunSnapshot{
		Variables: map[string]json.RawMessage{
			"workflow_amendment": json.RawMessage(`"revise output"`),
		},
	}
	input, applied := applyWorkflowAmendmentToAgentInput(snapshot, "run-1", json.RawMessage(`{"prompt":"draft"}`))
	if !applied || input.RunID != "run-1" || !strings.Contains(input.Prompt, "revise output") {
		t.Fatalf("input=%+v applied=%v", input, applied)
	}
}

func TestApplyWorkflowAmendmentHelpers(t *testing.T) {
	snapshot := runstate.RunSnapshot{
		Variables: map[string]json.RawMessage{
			"workflow_amendment": json.RawMessage(`"revise"`),
		},
	}
	input, applied := applyWorkflowAmendment(snapshot, core.AgentInput{Prompt: "analyze"})
	if !applied || input.Prompt != "analyze\n\nHuman feedback: revise" {
		t.Fatalf("unexpected amended input: %+v applied=%v", input, applied)
	}
	input, applied = applyWorkflowAmendment(runstate.RunSnapshot{}, core.AgentInput{})
	if applied {
		t.Fatal("expected no amendment without variable")
	}
	if got := snapshotVariableString(map[string]json.RawMessage{"resume_prompt": json.RawMessage(`"hi"`)}, "resume_prompt"); got != "hi" {
		t.Fatalf("got %q", got)
	}
}

func TestWorkflowRunnerClearWorkflowAmendment(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	snapshot := runstate.RunSnapshot{
		RunID:  "run-1",
		Status: runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"workflow_amendment": json.RawMessage(`"revise"`),
		},
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Version = loaded.Version
	if err := runs.Save(context.Background(), &snapshot, loaded.Version); err != nil {
		t.Fatal(err)
	}
	if err := runner.clearWorkflowAmendment(context.Background(), "run-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err = runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Variables["workflow_amendment"]; ok {
		t.Fatal("expected amendment cleared")
	}
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

// The amendment must reach only the node that resume runs into, not every
// later agent node in the rest of the run.
func TestWorkflowRunnerAmendmentAppliesOnceThenClears(t *testing.T) {
	runs := newWorkflowRun(t)
	first := &capturingAgent{}
	second := &capturingAgent{}
	runner := NewWorkflowRunner(nil, runs, nil, WithAgentRegistry(multiAgentRegistry{agents: map[string]core.AgentRunner{
		"assistant": first,
		"follow_up": second,
	}}))
	snapshot := runstate.RunSnapshot{
		RunID:  "run-amend-once",
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
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "review", Kind: core.NodeAgent, Ref: "assistant", Input: json.RawMessage(`{"prompt":"analyze"}`)},
				{ID: "next", Kind: core.NodeAgent, Ref: "follow_up", DependsOn: []string{"review"}, Input: json.RawMessage(`{"prompt":"summarize"}`)},
			}},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-amend-once"); err != nil {
		t.Fatal(err)
	}
	if want := "analyze\n\nHuman feedback: please revise"; first.lastPrompt != want {
		t.Fatalf("expected first node to receive amendment, got %q", first.lastPrompt)
	}
	if want := "summarize"; second.lastPrompt != want {
		t.Fatalf("expected later node to NOT receive stale amendment, got %q", second.lastPrompt)
	}
	loaded, err := runs.Load(context.Background(), "run-amend-once")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.Variables["workflow_amendment"]; ok {
		t.Fatalf("expected amendment to be cleared after use, still present: %+v", loaded.Variables)
	}
}

type multiAgentRegistry struct {
	agents map[string]core.AgentRunner
}

func (r multiAgentRegistry) Agent(name string) (core.AgentRunner, bool) {
	agent, ok := r.agents[name]
	return agent, ok
}

type singleAgentRegistry struct {
	agent *capturingAgent
}

func (r singleAgentRegistry) Agent(string) (core.AgentRunner, bool) {
	return r.agent, true
}
