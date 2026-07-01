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
	NodeID string                 `json:"node_id"`
	Output runstate.StepOutputRef `json:"output"`
}

// ListRunStepsResult summarizes checkpointed workflow steps for a run.
type ListRunStepsResult struct {
	RunID         string             `json:"run_id"`
	Version       int64              `json:"version"`
	Status        runstate.RunStatus `json:"status"`
	CurrentNodeID string             `json:"current_node_id,omitempty"`
	PendingHITL   *PendingHITLInfo   `json:"pending_hitl,omitempty"`
	Steps         []RunStep          `json:"steps"`
}

// PendingHITLInfo describes a paused run awaiting HITL approval.
type PendingHITLInfo struct {
	NodeID    string `json:"node_id,omitempty"`
	Interrupt bool   `json:"interrupt,omitempty"`
}

// ListRunCheckpointsResult summarizes append-only snapshot revisions for a run.
type ListRunCheckpointsResult struct {
	RunID       string                       `json:"run_id"`
	Checkpoints []runstate.CheckpointSummary `json:"checkpoints"`
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
		PendingHITL:   pendingHITLFromSnapshot(snapshot),
		Steps:         steps,
	}, nil
}

func pendingHITLFromSnapshot(snapshot runstate.RunSnapshot) *PendingHITLInfo {
	if snapshot.Status != runstate.RunStatusPaused || snapshot.PendingGate == nil {
		return nil
	}
	info := &PendingHITLInfo{NodeID: snapshot.CurrentNodeID}
	if len(snapshot.PendingGate.Payload) > 0 {
		var payload struct {
			Interrupt bool `json:"interrupt"`
		}
		if err := json.Unmarshal(snapshot.PendingGate.Payload, &payload); err == nil {
			info.Interrupt = payload.Interrupt
		}
	}
	return info
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
	if f.runLocker != nil {
		release, err := f.holdRunLease(ctx, runID)
		if err != nil {
			return RunResult{}, err
		}
		defer release()
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

	loaded, err := f.completeWorkflowRun(ctx, runID, f.stampHybridWorkflowPhase)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: f.workflowRunOutput(ctx, loaded)}, nil
}

// stampHybridWorkflowPhase marks the snapshot as being in the workflow phase
// for hybrid scenarios; a no-op for other orchestration modes.
func (f *Framework) stampHybridWorkflowPhase(snapshot *runstate.RunSnapshot) {
	if f.scenario.Orchestration.Mode != core.OrchestrationHybrid {
		return
	}
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	snapshot.Variables[executionPhaseVar] = json.RawMessage(fmt.Sprintf("%q", executionPhaseWorkflow))
}

// ListRunCheckpoints returns append-only snapshot revisions recorded for a run.
func (f *Framework) ListRunCheckpoints(ctx context.Context, runID string, limit int) (ListRunCheckpointsResult, error) {
	if f.checkpointHistory == nil {
		return ListRunCheckpointsResult{}, fmt.Errorf("agentflow: checkpoint history is not configured")
	}
	if f.runs == nil {
		return ListRunCheckpointsResult{}, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	if _, err := runstate.LoadAuthorized(ctx, f.runs, runID); err != nil {
		return ListRunCheckpointsResult{}, err
	}
	checkpoints, err := f.checkpointHistory.List(ctx, runID, limit)
	if err != nil {
		return ListRunCheckpointsResult{}, err
	}
	return ListRunCheckpointsResult{RunID: runID, Checkpoints: checkpoints}, nil
}

// GetRunCheckpoint loads one historical snapshot revision for a run.
func (f *Framework) GetRunCheckpoint(ctx context.Context, runID string, version int64) (runstate.RunSnapshot, error) {
	if f.checkpointHistory == nil {
		return runstate.RunSnapshot{}, fmt.Errorf("agentflow: checkpoint history is not configured")
	}
	if f.runs == nil {
		return runstate.RunSnapshot{}, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	if _, err := runstate.LoadAuthorized(ctx, f.runs, runID); err != nil {
		return runstate.RunSnapshot{}, err
	}
	return f.checkpointHistory.Load(ctx, runID, version)
}

// ResumeFromCheckpoint restores a historical snapshot revision and reruns the
// workflow forward from that restored state.
func (f *Framework) ResumeFromCheckpoint(ctx context.Context, runID string, version int64) (RunResult, error) {
	switch f.scenario.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow, core.OrchestrationHybrid:
	default:
		return RunResult{}, fmt.Errorf("agentflow: ResumeFromCheckpoint requires fixed_workflow or hybrid orchestration mode")
	}
	if f.scenario.Orchestration.Workflow == nil {
		return RunResult{}, fmt.Errorf("agentflow: ResumeFromCheckpoint requires a configured workflow")
	}
	if f.checkpointHistory == nil {
		return RunResult{}, fmt.Errorf("agentflow: checkpoint history is not configured")
	}
	if f.runLocker != nil {
		release, err := f.holdRunLease(ctx, runID)
		if err != nil {
			return RunResult{}, err
		}
		defer release()
	}

	snapshot, err := f.checkpointHistory.Load(ctx, runID, version)
	if err != nil {
		return RunResult{}, err
	}

	runner := f.newWorkflowRunner()
	if err := runner.RestoreSnapshotAndRun(ctx, f.scenario, runID, snapshot); err != nil {
		var paused orchestration.WorkflowPausedError
		if errors.As(err, &paused) {
			return RunResult{RunID: runID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		f.markWorkflowFailed(ctx, runID, err)
		return RunResult{}, err
	}

	loaded, err := f.completeWorkflowRun(ctx, runID, f.stampHybridWorkflowPhase)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: f.workflowRunOutput(ctx, loaded)}, nil
}
