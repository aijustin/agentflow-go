package agentflow_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestFrameworkListRunSteps(t *testing.T) {
	scenario := core.Scenario{
		Name: "list-steps",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
					{ID: "b", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"y":2}}`)},
				},
				Edges: []core.WorkflowEdge{{From: "a", To: "b"}},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-steps"}); err != nil {
		t.Fatal(err)
	}
	result, err := fw.ListRunSteps(context.Background(), "run-steps")
	if err != nil {
		t.Fatal(err)
	}
	if result.Version <= 0 {
		t.Fatalf("expected positive version, got %+v", result)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected status: %+v", result)
	}
	if len(result.Steps) != 2 {
		t.Fatalf("expected two steps, got %+v", result.Steps)
	}
	if result.Steps[0].NodeID != "a" || result.Steps[1].NodeID != "b" {
		t.Fatalf("unexpected step order: %+v", result.Steps)
	}
}

func TestFrameworkResumeFromStep(t *testing.T) {
	scenario := core.Scenario{
		Name: "resume-from-step",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{
						ID:   "seed",
						Kind: core.NodeTransform,
						Input: json.RawMessage(`{
							"set": {"list": ["one", "two"]}
						}`),
					},
					{
						ID:   "fanout",
						Kind: core.NodeMap,
						Input: json.RawMessage(`{
							"items_path": "steps.seed.list",
							"branch": {"kind": "transform", "input": {"set": {"tag": "mapped"}}}
						}`),
					},
					{
						ID:   "tail",
						Kind: core.NodeTransform,
						Input: json.RawMessage(`{
							"set": {"done": true}
						}`),
					},
				},
				Edges: []core.WorkflowEdge{
					{From: "seed", To: "fanout"},
					{From: "fanout", To: "tail"},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-resume"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}
	before, err := fw.ListRunSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Steps) < 3 {
		t.Fatalf("expected seed, fanout, tail outputs, got %+v", before.Steps)
	}

	result, err := fw.ResumeFromStep(context.Background(), runID, "fanout")
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected result: %+v", result)
	}
	after, err := fw.ListRunSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	hasSeed := false
	hasFanout := false
	hasTail := false
	for _, step := range after.Steps {
		switch step.NodeID {
		case "seed":
			hasSeed = true
		case "fanout":
			hasFanout = true
		case "tail":
			hasTail = true
		}
	}
	if !hasSeed || !hasFanout || !hasTail {
		t.Fatalf("expected seed, fanout, tail after resume, got %+v", after.Steps)
	}
}

func TestFrameworkCheckpointHistory(t *testing.T) {
	scenario := core.Scenario{
		Name: "checkpoint-history",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
					{ID: "b", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"y":2}}`)},
				},
				Edges: []core.WorkflowEdge{{From: "a", To: "b"}},
			},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithCheckpointHistory(agentflow.NewInMemoryCheckpointHistory()))
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-checkpoints"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}

	list, err := fw.ListRunCheckpoints(context.Background(), runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Checkpoints) < 2 {
		t.Fatalf("expected at least two checkpoints, got %+v", list.Checkpoints)
	}

	first := list.Checkpoints[0]
	snapshot, err := fw.GetRunCheckpoint(context.Background(), runID, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RunID != runID {
		t.Fatalf("unexpected snapshot: %+v", snapshot)
	}

	result, err := fw.ResumeFromCheckpoint(context.Background(), runID, first.Version)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusCompleted {
		t.Fatalf("unexpected result: %+v", result)
	}
}

func TestFrameworkResumeFromStepInvalidNode(t *testing.T) {
	scenario := core.Scenario{
		Name: "resume-invalid-step",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-invalid-step"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}
	_, err = fw.ResumeFromStep(context.Background(), runID, "missing")
	if err == nil {
		t.Fatal("expected error for unknown node")
	}
}

func TestFrameworkResumeFromCheckpointUnknownVersion(t *testing.T) {
	scenario := core.Scenario{
		Name: "resume-unknown-version",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithCheckpointHistory(agentflow.NewInMemoryCheckpointHistory()))
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-unknown-version"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID}); err != nil {
		t.Fatal(err)
	}
	_, err = fw.ResumeFromCheckpoint(context.Background(), runID, 99999)
	if !errors.Is(err, runstate.ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestFrameworkListRunCheckpointsRequiresHistory(t *testing.T) {
	scenario := core.Scenario{
		Name: "no-history",
		Agents: map[string]core.Agent{
			"noop": {Name: "noop"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTransform, Input: json.RawMessage(`{"set":{"x":1}}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(scenario)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fw.ListRunCheckpoints(context.Background(), "run-x", 0); err == nil {
		t.Fatal("expected error when checkpoint history is not configured")
	}
	if _, err := fw.GetRunCheckpoint(context.Background(), "run-x", 1); err == nil {
		t.Fatal("expected error when checkpoint history is not configured")
	}
}

func TestFrameworkListRunStepsWithPendingHITL(t *testing.T) {
	scenario := core.Scenario{
		Name: "steps-hitl",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Agents: map[string]core.Agent{
			"assistant": {Name: "assistant", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "approve", Kind: core.NodeHumanGate},
					{ID: "done", Kind: core.NodeTransform, DependsOn: []string{"approve"}, Input: json.RawMessage(`{"set":{"ok":true}}`)},
				},
			},
			HumanInLoop: core.HumanInLoopPolicy{Enabled: true},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithHITLTokenSecret([]byte("secret"), nil))
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-steps-hitl"
	result, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID, Prompt: "review"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != runstate.RunStatusPaused {
		t.Fatalf("expected paused, got %+v", result)
	}
	steps, err := fw.ListRunSteps(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if steps.PendingHITL == nil || steps.PendingHITL.NodeID != "approve" {
		t.Fatalf("expected pending HITL on approve node, got %+v", steps.PendingHITL)
	}
}

func TestFrameworkHybridCheckpointPhaseStamp(t *testing.T) {
	scenario := core.Scenario{
		Name: "hybrid-checkpoint",
		LLMs: map[string]core.LLMProfileRef{"default": {Provider: "mock", Model: "test"}},
		Tools: map[string]core.Tool{
			"echo": {Name: "echo", Type: "builtin.echo", Approval: core.ApprovalNever},
		},
		Agents: map[string]core.Agent{
			"analyst": {Name: "analyst", LLM: "default"},
		},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationHybrid,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "prep", Kind: core.NodeTool, Ref: "echo", Input: json.RawMessage(`{"message":"data"}`)},
				},
			},
		},
	}
	fw, err := agentflow.New(
		scenario,
		agentflow.WithLLMGateway(fakeGateway{content: "hybrid done"}),
		agentflow.WithToolExecutor("echo", noopTool{}),
		agentflow.WithCheckpointHistory(agentflow.NewInMemoryCheckpointHistory()),
	)
	if err != nil {
		t.Fatal(err)
	}
	runID := "run-hybrid-checkpoint"
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: runID, Agent: "analyst", Prompt: "go"}); err != nil {
		t.Fatal(err)
	}
	list, err := fw.ListRunCheckpoints(context.Background(), runID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.Checkpoints) == 0 {
		t.Fatal("expected checkpoints")
	}
	first := list.Checkpoints[0]
	if _, err := fw.ResumeFromCheckpoint(context.Background(), runID, first.Version); err != nil {
		t.Fatal(err)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), runID)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(snapshot.Variables["execution_phase"]); got != `"workflow"` {
		t.Fatalf("expected hybrid workflow phase stamp, got %s", got)
	}
}
