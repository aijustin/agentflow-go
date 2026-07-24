package orchestration

import (
	"context"
	"sync"
	"testing"

	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	"github.com/aijustin/agentflow-go/pkg/core"
)

// keyRecordingTool captures the idempotency key of every execution and fails
// its first failFirst calls with a retryable error.
type keyRecordingTool struct {
	mu        sync.Mutex
	keys      []string
	failFirst int
}

func (t *keyRecordingTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.keys = append(t.keys, core.IdempotencyKeyFromContext(ctx))
	if len(t.keys) <= t.failFirst {
		return core.ToolResult{}, transientWorkflowError{message: "flaky"}
	}
	return core.ToolResult{Tool: call.Tool}, nil
}

func (t *keyRecordingTool) captured() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.keys...)
}

func registerKeyRecorder(t *testing.T, name string, tool *keyRecordingTool) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.RegisterTool(name, tool); err != nil {
		t.Fatal(err)
	}
	return reg
}

// Workflow tool nodes get {run_id}:{node_id}:{attempt}: node-level retries
// are distinct logical executions and must observe distinct keys.
func TestWorkflowToolNodeIdempotencyKeyPerAttempt(t *testing.T) {
	tool := &keyRecordingTool{failFirst: 1}
	reg := registerKeyRecorder(t, "flaky", tool)
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{
				{ID: "flaky", Kind: core.NodeTool, Ref: "flaky", Retry: core.RetryPolicy{MaxAttempts: 2}},
			}},
		},
	}
	if err := NewWorkflowRunner(reg, runs, nil).Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	keys := tool.captured()
	if len(keys) != 2 {
		t.Fatalf("expected two attempts, got %v", keys)
	}
	if keys[0] != "run-1:flaky:1" || keys[1] != "run-1:flaky:2" {
		t.Fatalf("expected attempt-scoped keys run-1:flaky:1/2, got %v", keys)
	}
}

// ResumeFromStep replays the same logical node execution (the retry loop
// restarts at attempt 1), so the idempotency key must be identical before and
// after the resume — this is what lets side-effecting tools dedupe a replay.
func TestWorkflowResumeFromStepKeepsIdempotencyKey(t *testing.T) {
	tool := &keyRecordingTool{}
	reg := registerKeyRecorder(t, "writer", tool)
	runs := newWorkflowRun(t)
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "first", Kind: core.NodeTransform, Input: []byte(`{"set":{"value":"v1"}}`)},
					{ID: "second", Kind: core.NodeTool, Ref: "writer", DependsOn: []string{"first"}},
				},
			},
		},
	}
	runner := NewWorkflowRunner(reg, runs, nil)
	if err := runner.Run(context.Background(), scenario, "run-1"); err != nil {
		t.Fatal(err)
	}
	if err := runner.ResumeFromStep(context.Background(), scenario, "run-1", "second"); err != nil {
		t.Fatal(err)
	}
	keys := tool.captured()
	if len(keys) != 2 {
		t.Fatalf("expected the resumed node to execute twice in total, got %v", keys)
	}
	if keys[0] != "run-1:second:1" || keys[1] != keys[0] {
		t.Fatalf("resume replay must reuse the same idempotency key, got %v", keys)
	}
}
