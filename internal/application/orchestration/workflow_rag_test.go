package orchestration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestClassifyQueryRoute(t *testing.T) {
	if got := classifyQueryRoute("search docs"); got != "rag" {
		t.Fatalf("got %q", got)
	}
	if got := classifyQueryRoute("please review"); got != "hitl" {
		t.Fatalf("got %q", got)
	}
	if got := classifyQueryRoute("hello"); got != "direct" {
		t.Fatalf("got %q", got)
	}
}

func TestWorkflowRunnerQueryRouterNode(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "query-router",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{
					ID:    "route",
					Kind:  core.NodeQueryRouter,
					Input: json.RawMessage(`{"query":"search knowledge base"}`),
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
	ref, ok := snapshot.StepOutputs["route"]
	if !ok {
		t.Fatal("expected route output")
	}
	var out queryRouterOutput
	if err := json.Unmarshal(ref.Inline, &out); err != nil {
		t.Fatal(err)
	}
	if out.Route != "rag" {
		t.Fatalf("unexpected route: %+v", out)
	}
}

func TestGradeRetrievalResultsScoresKeywordOverlap(t *testing.T) {
	raw := json.RawMessage(`{"results":[{"content":"billing policy details","score":0.5}]}`)
	if got := gradeRetrievalResults("billing policy", raw); got <= 0 {
		t.Fatalf("score=%v", got)
	}
	if got := gradeRetrievalResults("", json.RawMessage(`{}`)); got != 0 {
		t.Fatalf("empty=%v", got)
	}
}

func TestWorkflowRunnerSupervisorNode(t *testing.T) {
	runs := newWorkflowRun(t)
	agent := &capturingAgent{}
	runner := NewWorkflowRunner(nil, runs, nil, WithAgentRegistry(singleAgentRegistry{agent: agent}))
	scenario := core.Scenario{
		Name: "supervisor",
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{
					ID:    "delegate",
					Kind:  core.NodeSupervisor,
					Ref:   "assistant",
					Input: json.RawMessage(`{"prompt":"coordinate"}`),
				}},
			},
		},
	}
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	if agent.lastPrompt != "coordinate" {
		t.Fatalf("prompt=%q", agent.lastPrompt)
	}
	snapshot, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.StepOutputs["delegate"]; !ok {
		t.Fatalf("expected supervisor output: %+v", snapshot.StepOutputs)
	}
}

func TestWorkflowRunnerRAGGradeNode(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "rag-grade",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{
					ID:   "grade",
					Kind: core.NodeRAGGrade,
					Input: json.RawMessage(`{
						"query":"billing",
						"results":[{"content":"billing policy details","score":0.2}],
						"min_score":0.3
					}`),
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
	ref, ok := snapshot.StepOutputs["grade"]
	if !ok {
		t.Fatal("expected grade output")
	}
	var out ragGradeOutput
	if err := json.Unmarshal(ref.Inline, &out); err != nil {
		t.Fatal(err)
	}
	if out.Relevant || out.RewriteQuery == "" {
		t.Fatalf("expected rewrite for low score: %+v", out)
	}
}
