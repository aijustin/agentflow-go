package orchestration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestWorkflowRunnerQueryRouter(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "router",
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
		t.Fatal("expected router output")
	}
	var output struct {
		Route string `json:"route"`
	}
	if err := json.Unmarshal(ref.Inline, &output); err != nil {
		t.Fatal(err)
	}
	if output.Route != "rag" {
		t.Fatalf("expected rag route, got %q", output.Route)
	}
}

func TestWorkflowRunnerRAGGrade(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "grade",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{
					ID:   "grade",
					Kind: core.NodeRAGGrade,
					Input: json.RawMessage(`{
						"query":"refund policy",
						"results":{"results":[{"content":"refund policy details","score":0.8}]},
						"min_score":0.35
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
	ref := snapshot.StepOutputs["grade"]
	var output struct {
		Relevant bool    `json:"relevant"`
		Score    float64 `json:"score"`
	}
	if err := json.Unmarshal(ref.Inline, &output); err != nil {
		t.Fatal(err)
	}
	if !output.Relevant {
		t.Fatalf("expected relevant grade, got %+v", output)
	}
}

func TestClassifyQueryRoute(t *testing.T) {
	if got := classifyQueryRoute("What is in the knowledge base?"); got != "rag" {
		t.Fatalf("expected rag, got %q", got)
	}
	if got := classifyQueryRoute("hello there"); got != "direct" {
		t.Fatalf("expected direct, got %q", got)
	}
}
