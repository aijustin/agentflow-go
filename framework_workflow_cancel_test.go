package agentflow_test

import (
	"context"
	"errors"
	"testing"

	agentflow "github.com/aijustin/agentflow-go"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type cancellationProbeTool struct{ started chan struct{} }

func (tool cancellationProbeTool) Execute(ctx context.Context, _ core.ToolCall) (core.ToolResult, error) {
	close(tool.started)
	<-ctx.Done()
	return core.ToolResult{}, ctx.Err()
}

func TestWorkflowCallerCancellationMarksRunCancelled(t *testing.T) {
	started := make(chan struct{})
	scenario := core.Scenario{
		Name:   "workflow-cancel",
		Agents: map[string]core.Agent{"noop": {Name: "noop"}},
		Tools:  map[string]core.Tool{"wait": {Name: "wait", Type: "builtin.wait"}},
		Orchestration: core.Orchestration{
			Mode: core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{
				Nodes: []core.WorkflowNode{{ID: "wait", Kind: core.NodeTool, Ref: "wait"}},
			},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithToolExecutor("wait", cancellationProbeTool{started: started}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := fw.Run(ctx, agentflow.RunRequest{RunID: "run-workflow-cancel"})
		done <- runErr
	}()
	<-started
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("run error = %v, want context.Canceled", err)
	}
	snapshot, err := fw.RunStateRepository().Load(context.Background(), "run-workflow-cancel")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Status != runstate.RunStatusCancelled {
		t.Fatalf("caller cancellation persisted %s, want Cancelled", snapshot.Status)
	}
	if _, failed := snapshot.Variables[runstate.VarRunErrorMessage]; failed {
		t.Fatalf("cancelled workflow must not retain a failure reason: %+v", snapshot.Variables)
	}
}

func TestFrameworkRejectsSecondInProcessDriverForSameRun(t *testing.T) {
	started := make(chan struct{})
	scenario := core.Scenario{
		Name:   "single-driver",
		Agents: map[string]core.Agent{"noop": {Name: "noop"}},
		Tools:  map[string]core.Tool{"wait": {Name: "wait", Type: "builtin.wait"}},
		Orchestration: core.Orchestration{
			Mode:     core.OrchestrationFixedWorkflow,
			Workflow: &core.Workflow{Nodes: []core.WorkflowNode{{ID: "wait", Kind: core.NodeTool, Ref: "wait"}}},
		},
	}
	fw, err := agentflow.New(scenario, agentflow.WithToolExecutor("wait", cancellationProbeTool{started: started}))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, runErr := fw.Run(ctx, agentflow.RunRequest{RunID: "run-single-driver"})
		done <- runErr
	}()
	<-started
	if _, err := fw.Run(context.Background(), agentflow.RunRequest{RunID: "run-single-driver"}); !errors.Is(err, agentflow.ErrRunInProgress) {
		t.Fatalf("second driver error = %v, want ErrRunInProgress", err)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatalf("first driver error = %v, want context.Canceled", err)
	}
}
