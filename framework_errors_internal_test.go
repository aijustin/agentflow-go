package agentflow

import (
	"context"
	"errors"
	"strings"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func TestRunNotRunningErrorMessage(t *testing.T) {
	err := runNotRunningError{runID: "run-1", status: runstate.RunStatusCompleted}
	if got := err.Error(); !strings.Contains(got, "run-1") || !strings.Contains(got, "completed") {
		t.Fatalf("unexpected message: %q", got)
	}
}

func TestCompleteWorkflowRunRejectsNonRunningSnapshot(t *testing.T) {
	runs := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{RunID: "run-1", Status: runstate.RunStatusCompleted, Version: 1}
	if err := runs.Save(context.Background(), &snapshot, 0); err != nil {
		t.Fatal(err)
	}
	fw := &Framework{
		scenario: core.Scenario{
			Name:          "wf",
			Orchestration: core.Orchestration{Mode: core.OrchestrationFixedWorkflow},
		},
		runs: runs,
	}
	_, err := fw.completeWorkflowRun(context.Background(), "run-1", nil)
	if err == nil {
		t.Fatal("expected conflict completing non-running snapshot")
	}
	var conflict runNotRunningError
	if !errors.As(err, &conflict) {
		t.Fatalf("expected runNotRunningError, got %v", err)
	}
}
