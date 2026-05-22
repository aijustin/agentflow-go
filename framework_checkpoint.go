package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/aijustin/agentflow-go/internal/application/orchestration"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// RunStep describes one persisted workflow step output.
type RunStep struct {
	NodeID string                  `json:"node_id"`
	Output runstate.StepOutputRef  `json:"output"`
}

// ListRunStepsResult summarizes checkpointed workflow steps for a run.
type ListRunStepsResult struct {
	RunID         string             `json:"run_id"`
	Version       int64              `json:"version"`
	Status        runstate.RunStatus `json:"status"`
	CurrentNodeID string             `json:"current_node_id,omitempty"`
	Steps         []RunStep          `json:"steps"`
}

// ListRunSteps returns persisted step outputs and the current snapshot version.
func (f *Framework) ListRunSteps(ctx context.Context, runID string) (ListRunStepsResult, error) {
	if f.runs == nil {
		return ListRunStepsResult{}, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return ListRunStepsResult{}, err
	}
	steps := make([]RunStep, 0, len(snapshot.StepOutputs))
	for nodeID, output := range snapshot.StepOutputs {
		steps = append(steps, RunStep{NodeID: nodeID, Output: output})
	}
	slices.SortFunc(steps, func(a, b RunStep) int {
		if a.NodeID < b.NodeID {
			return -1
		}
		if a.NodeID > b.NodeID {
			return 1
		}
		return 0
	})
	return ListRunStepsResult{
		RunID:         snapshot.RunID,
		Version:       snapshot.Version,
		Status:        snapshot.Status,
		CurrentNodeID: snapshot.CurrentNodeID,
		Steps:         steps,
	}, nil
}

// ResumeFromStep rewinds a workflow run to the given node, truncating that node
// and all downstream step outputs, then reruns from that point forward.
func (f *Framework) ResumeFromStep(ctx context.Context, runID, nodeID string) (RunResult, error) {
	switch f.scenario.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow, core.OrchestrationHybrid:
	default:
		return RunResult{}, fmt.Errorf("agentflow: ResumeFromStep requires fixed_workflow or hybrid orchestration mode")
	}
	if f.scenario.Orchestration.Workflow == nil {
		return RunResult{}, fmt.Errorf("agentflow: ResumeFromStep requires a configured workflow")
	}

	runner := f.newWorkflowRunner()
	if err := runner.ResumeFromStep(ctx, f.scenario, runID, nodeID); err != nil {
		var paused orchestration.WorkflowPausedError
		if errors.As(err, &paused) {
			return RunResult{RunID: runID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		f.markWorkflowFailed(ctx, runID, err)
		return RunResult{}, err
	}

	loaded, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	if f.scenario.Orchestration.Mode == core.OrchestrationHybrid {
		if loaded.Variables == nil {
			loaded.Variables = make(map[string]json.RawMessage)
		}
		loaded.Variables[executionPhaseVar] = json.RawMessage(fmt.Sprintf("%q", executionPhaseWorkflow))
	}
	loaded.Status = runstate.RunStatusCompleted
	if err := f.runs.Save(ctx, &loaded, loaded.Version); err != nil {
		return RunResult{}, err
	}
	f.emit(ctx, core.EventRunCompleted, runID, nil)
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: "fixed workflow completed"}, nil
}
