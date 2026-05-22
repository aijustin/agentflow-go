package orchestration

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestDownstreamNodeIDs(t *testing.T) {
	workflow := core.Workflow{
		Nodes: []core.WorkflowNode{
			{ID: "a"},
			{ID: "b"},
			{ID: "c"},
			{ID: "d"},
		},
		Edges: []core.WorkflowEdge{
			{From: "a", To: "b"},
			{From: "a", To: "c"},
			{From: "b", To: "d"},
			{From: "c", To: "d"},
		},
	}
	down := downstreamNodeIDs(workflow, "b")
	for _, id := range []string{"b", "d"} {
		if !down[id] {
			t.Fatalf("expected %q downstream of b, got %+v", id, down)
		}
	}
	if down["a"] || down["c"] {
		t.Fatalf("unexpected downstream nodes: %+v", down)
	}
}

func TestTruncateStepOutputsForRerun(t *testing.T) {
	workflow := core.Workflow{
		Nodes: []core.WorkflowNode{
			{ID: "prep"},
			{ID: "fanout"},
			{ID: "merge"},
		},
		Edges: []core.WorkflowEdge{
			{From: "prep", To: "fanout"},
			{From: "fanout", To: "merge"},
		},
	}
	outputs := map[string]runstate.StepOutputRef{
		"prep":                    {Inline: json.RawMessage(`{"ready":true}`)},
		"fanout":                  {Inline: json.RawMessage(`{"members":{}}`)},
		"fanout.agent.analyst.0":  {Inline: json.RawMessage(`{"text":"a"}`)},
		"fanout.agent.analyst.1":  {Inline: json.RawMessage(`{"text":"b"}`)},
		"merge":                   {Inline: json.RawMessage(`{"done":true}`)},
		"final":                   {Inline: json.RawMessage(`{"answer":"x"}`)},
	}
	truncateStepOutputsForRerun(outputs, workflow, "fanout")
	for _, id := range []string{"fanout", "fanout.agent.analyst.0", "fanout.agent.analyst.1", "merge"} {
		if _, ok := outputs[id]; ok {
			t.Fatalf("expected %q to be truncated, still present: %+v", id, outputs)
		}
	}
	if _, ok := outputs["final"]; !ok {
		t.Fatalf("final is removed separately by ResumeFromStep, should remain here: %+v", outputs)
	}
	if _, ok := outputs["prep"]; !ok {
		t.Fatalf("expected prep output to remain: %+v", outputs)
	}
}

func TestWorkflowRunnerResumeFromStep(t *testing.T) {
	runs := newWorkflowRun(t)
	runner := NewWorkflowRunner(nil, runs, nil)
	scenario := core.Scenario{
		Name: "resume-from-step",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "first", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"value":"v1"}}`)},
					{ID: "second", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"value":"v2"}}`)},
					{ID: "third", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"value":"v3"}}`)},
				},
				Edges: []core.WorkflowEdge{
					{From: "first", To: "second"},
					{From: "second", To: "third"},
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
	if len(snapshot.StepOutputs) != 3 {
		t.Fatalf("expected three outputs before rewind, got %+v", snapshot.StepOutputs)
	}
	if err := runs.Save(context.Background(), &runstate.RunSnapshot{
		RunID:        "run-1",
		ScenarioName: scenario.Name,
		Status:       runstate.RunStatusCompleted,
		StepOutputs:  snapshot.StepOutputs,
		Version:      snapshot.Version,
	}, snapshot.Version); err != nil {
		t.Fatal(err)
	}

	if err := runner.ResumeFromStep(context.Background(), scenario, "run-1", "second"); err != nil {
		t.Fatal(err)
	}
	loaded, err := runs.Load(context.Background(), "run-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := loaded.StepOutputs["first"]; !ok {
		t.Fatalf("expected first output preserved: %+v", loaded.StepOutputs)
	}
	if _, ok := loaded.StepOutputs["second"]; !ok {
		t.Fatalf("expected second output after rerun: %+v", loaded.StepOutputs)
	}
	if _, ok := loaded.StepOutputs["third"]; !ok {
		t.Fatalf("expected third output after rerun: %+v", loaded.StepOutputs)
	}
}
