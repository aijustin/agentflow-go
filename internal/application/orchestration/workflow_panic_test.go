package orchestration

import (
	"context"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/internal/adapter/registry"
	"github.com/aijustin/agentflow-go/internal/adapter/tool/builtin"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// panickingSink simulates a buggy user EventSink that panics whenever the
// emitted event payload contains match.
type panickingSink struct {
	match string
}

func (s panickingSink) Emit(_ context.Context, event core.Event) error {
	if strings.Contains(string(event.Payload), s.match) {
		panic("sink exploded")
	}
	return nil
}

// panickingSaveRepo simulates a buggy user Repository that panics when a
// node step output is persisted from a concurrent node goroutine.
type panickingSaveRepo struct {
	runstate.Repository
}

func (r panickingSaveRepo) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	for key := range snapshot.StepOutputs {
		if key == "a" || key == "b" {
			panic("repository exploded")
		}
	}
	return r.Repository.Save(ctx, snapshot, expectedVersion)
}

func newEchoRegistry(t *testing.T) *registry.Registry {
	t.Helper()
	reg := registry.New()
	if err := reg.RegisterTool("echo", builtin.NewEchoTool()); err != nil {
		t.Fatal(err)
	}
	return reg
}

// TestWorkflowRunnerRecoversPanickingEventSink: a panicking user EventSink
// inside a concurrent batch node goroutine must become a node error (so the
// run settles as Failed through the normal error path), not crash the
// process.
func TestWorkflowRunnerRecoversPanickingEventSink(t *testing.T) {
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			MaxParallel: 2,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTool, Ref: "echo", Input: []byte(`{"message":"a"}`)},
					{ID: "b", Kind: core.NodeTool, Ref: "echo", Input: []byte(`{"message":"b"}`)},
				},
			},
		},
	}
	runner := NewWorkflowRunner(newEchoRegistry(t), newWorkflowRun(t), panickingSink{match: `"node_id":"a"`})
	err := runner.Run(context.Background(), scenario, "run-1")
	if err == nil || !strings.Contains(err.Error(), "panic recovered") {
		t.Fatalf("expected panic recovery error, got %v", err)
	}
}

// TestWorkflowRunnerParallelGroupRecoversPanickingEventSink: the same
// invariant for parallel_group member goroutines.
func TestWorkflowRunnerParallelGroupRecoversPanickingEventSink(t *testing.T) {
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "group", Kind: core.NodeParallelGroup, Input: []byte(`{"tools":["echo"]}`)},
				},
			},
		},
	}
	runner := NewWorkflowRunner(newEchoRegistry(t), newWorkflowRun(t), panickingSink{match: "parallel_member"})
	err := runner.Run(context.Background(), scenario, "run-1")
	if err == nil || !strings.Contains(err.Error(), "panic recovered") {
		t.Fatalf("expected panic recovery error, got %v", err)
	}
}

// TestWorkflowRunnerRecoversPanickingRepository: a panicking user Repository
// inside a concurrent batch node goroutine must become a node error, not
// crash the process.
func TestWorkflowRunnerRecoversPanickingRepository(t *testing.T) {
	scenario := core.Scenario{
		Name: "scenario",
		Orchestration: core.Orchestration{
			MaxParallel: 2,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{
					{ID: "a", Kind: core.NodeTool, Ref: "echo", Input: []byte(`{"message":"a"}`)},
				},
			},
		},
	}
	runner := NewWorkflowRunner(newEchoRegistry(t), panickingSaveRepo{Repository: newWorkflowRun(t)}, nil)
	err := runner.Run(context.Background(), scenario, "run-1")
	if err == nil || !strings.Contains(err.Error(), "panic recovered") {
		t.Fatalf("expected panic recovery error, got %v", err)
	}
}
