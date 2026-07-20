package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aijustin/agentflow-go/internal/application/orchestration"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func (f *Framework) runWorkflow(ctx context.Context, req RunRequest) (RunResult, error) {
	return f.runWorkflowScenario(ctx, f.currentScenario(), req)
}

func (f *Framework) runWorkflowScenario(ctx context.Context, scenario core.Scenario, req RunRequest) (RunResult, error) {
	ctx, cancel := withScenarioTimeout(ctx, scenario.Runtime.Timeout)
	defer cancel()
	ctx = core.ContextWithTrustMode(ctx, string(req.TrustMode))
	ctx = core.ContextWithEpisodeCorrelation(ctx, core.EpisodeCorrelation{
		EpisodeID:   req.EpisodeID,
		TriggerKind: req.TriggerKind,
		SessionID:   req.SessionID,
	})
	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	snapshot := runstate.RunSnapshot{
		RunID:        req.RunID,
		ScenarioName: scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"input": req.Context,
		},
		StepOutputs: make(map[string]runstate.StepOutputRef),
	}
	resolvedAgent, _ := f.currentEngine().ResolveAgentName(req.Agent)
	saveRunResumeMetadata(&snapshot, req, resolvedAgent)
	runstate.StampTenant(ctx, &snapshot)
	if err := f.runs.Save(ctx, &snapshot, 0); err != nil {
		if errors.Is(err, runstate.ErrStaleSnapshot) {
			return RunResult{}, f.classifyExistingRun(ctx, req.RunID)
		}
		return RunResult{}, err
	}
	f.emitJSON(ctx, core.EventRunStarted, req.RunID, runStartedPayload(req))
	runner := f.newWorkflowRunner()
	if err := runner.Run(ctx, scenario, req.RunID); err != nil {
		var paused orchestration.WorkflowPausedError
		if errors.As(err, &paused) {
			return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		f.markWorkflowFailed(ctx, req.RunID, err)
		return RunResult{}, err
	}
	loaded, err := f.completeWorkflowRun(ctx, req.RunID, nil)
	if err != nil {
		return RunResult{}, err
	}
	output, err := f.workflowRunOutput(ctx, loaded)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{RunID: req.RunID, Status: runstate.RunStatusCompleted, Output: output}, nil
}

// runHybrid executes a hybrid scenario: the optional fixed workflow DAG runs
// first, then an autonomous agent executes with the workflow step outputs
// injected as context.  If no workflow is defined, execution falls back to
// pure autonomous mode.
func (f *Framework) runHybrid(ctx context.Context, req RunRequest) (RunResult, error) {
	if f.currentScenario().Orchestration.Workflow == nil {
		return f.currentEngine().Run(ctx, req)
	}
	ctx, cancel := withScenarioTimeout(ctx, f.currentScenario().Runtime.Timeout)
	defer cancel()
	req, paused, err := f.prepareHybridAutonomousRunScenario(ctx, f.currentScenario(), req)
	if err != nil || paused.Status != "" {
		return paused, err
	}
	return f.currentEngine().RunHybrid(ctx, req)
}

func (f *Framework) prepareHybridAutonomousRunScenario(ctx context.Context, scenario core.Scenario, req RunRequest) (RunRequest, RunResult, error) {
	if scenario.Orchestration.Workflow == nil {
		return req, RunResult{}, fmt.Errorf("agentflow: hybrid scenario has no workflow configured")
	}
	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	snapshot := runstate.RunSnapshot{
		RunID:        req.RunID,
		ScenarioName: scenario.Name,
		Status:       runstate.RunStatusRunning,
		Variables: map[string]json.RawMessage{
			"input":           req.Context,
			executionPhaseVar: quoteJSONString(executionPhaseWorkflow),
		},
		StepOutputs: make(map[string]runstate.StepOutputRef),
	}
	resolvedAgent, _ := f.currentEngine().ResolveAgentName(req.Agent)
	saveRunResumeMetadata(&snapshot, req, resolvedAgent)
	runstate.StampTenant(ctx, &snapshot)
	if err := f.runs.Save(ctx, &snapshot, 0); err != nil {
		if errors.Is(err, runstate.ErrStaleSnapshot) {
			return req, RunResult{}, f.classifyExistingRun(ctx, req.RunID)
		}
		return req, RunResult{}, err
	}
	f.emitJSON(ctx, core.EventRunStarted, req.RunID, runStartedPayload(req))
	runner := f.newWorkflowRunner()
	workflowCtx := core.ContextWithTrustMode(ctx, string(req.TrustMode))
	if err := runner.Run(workflowCtx, scenario, req.RunID); err != nil {
		var paused orchestration.WorkflowPausedError
		if errors.As(err, &paused) {
			return req, RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		f.markWorkflowFailed(ctx, req.RunID, err)
		return req, RunResult{}, err
	}
	loaded, err := f.saveRunSnapshotWithRetry(ctx, req.RunID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		snapshot.Variables[executionPhaseVar] = quoteJSONString(executionPhaseAutonomous)
		return nil
	})
	if err != nil {
		// The workflow phase finished but the phase transition could not be
		// persisted; without it a resume would re-run the whole workflow.
		f.markWorkflowFailed(ctx, req.RunID, err)
		return req, RunResult{}, err
	}
	req, err = f.hydrateRunRequest(ctx, req, loaded)
	if err != nil {
		return req, RunResult{}, fmt.Errorf("agentflow: hydrate workflow context for autonomous phase: %w", err)
	}
	return req, RunResult{}, nil
}

// classifyExistingRun converts a create-conflict on an already-existing run
// ID into the same classified sentinel errors the autonomous beginRun path
// reports, instead of surfacing a bare optimistic-concurrency error that
// hides which state the existing run is actually in.
func (f *Framework) classifyExistingRun(ctx context.Context, runID string) error {
	existing, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return fmt.Errorf("agentflow: run %q already exists: %w", runID, err)
	}
	switch existing.Status {
	case runstate.RunStatusCompleted:
		return ErrRunAlreadyCompleted
	case runstate.RunStatusCancelled:
		return ErrRunCancelled
	case runstate.RunStatusPaused:
		return ErrRunPaused
	case runstate.RunStatusFailed:
		return ErrRunFailed
	default:
		return ErrRunInProgress
	}
}

// runNotRunningError reports that a completion save observed the run in a
// non-Running status: a concurrent writer (cancellation, failure, pause)
// got there first and its state must not be clobbered with Completed.
type runNotRunningError struct {
	runID  string
	status runstate.RunStatus
}

func (e runNotRunningError) Error() string {
	return fmt.Sprintf("agentflow: cannot complete run %q in status %s", e.runID, e.status)
}

// saveRunSnapshotWithRetry mirrors the engine's saveSnapshotWithRetry:
// reload-mutate-save with retries on optimistic-concurrency conflicts, so a
// single ErrStaleSnapshot from a concurrent writer does not abort a
// bookkeeping save (e.g. the final Completed transition).
func (f *Framework) saveRunSnapshotWithRetry(ctx context.Context, runID string, mutate func(*runstate.RunSnapshot) error) (runstate.RunSnapshot, error) {
	for attempt := 0; attempt < 5; attempt++ {
		snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
		if err != nil {
			return runstate.RunSnapshot{}, err
		}
		if err := mutate(&snapshot); err != nil {
			return runstate.RunSnapshot{}, err
		}
		if err := f.runs.Save(ctx, &snapshot, snapshot.Version); err != nil {
			if errors.Is(err, runstate.ErrStaleSnapshot) {
				continue
			}
			return runstate.RunSnapshot{}, err
		}
		return snapshot, nil
	}
	return runstate.RunSnapshot{}, fmt.Errorf("agentflow: failed to save snapshot %q after stale retries", runID)
}

// completeWorkflowRun persists the terminal Completed transition for a
// workflow (or hybrid workflow-phase) run. On a status conflict the error is
// returned without touching the run; on a persistent save failure the run is
// marked Failed so it does not linger in Running forever.
func (f *Framework) completeWorkflowRun(ctx context.Context, runID string, mutate func(*runstate.RunSnapshot)) (runstate.RunSnapshot, error) {
	loaded, err := f.saveRunSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Status != runstate.RunStatusRunning {
			return runNotRunningError{runID: runID, status: snapshot.Status}
		}
		if mutate != nil {
			mutate(snapshot)
		}
		snapshot.Status = runstate.RunStatusCompleted
		return nil
	})
	if err != nil {
		var conflict runNotRunningError
		if !errors.As(err, &conflict) {
			f.markWorkflowFailed(ctx, runID, err)
		}
		return runstate.RunSnapshot{}, err
	}
	f.emit(ctx, core.EventRunCompleted, runID, nil)
	return loaded, nil
}

func (f *Framework) markWorkflowFailed(ctx context.Context, runID string, cause error) {
	// Persist the failure with a context stripped of the caller's own
	// cancellation/deadline: cause is frequently the caller's context being
	// cancelled or timing out, and using that same context here would make
	// this bookkeeping save fail too, leaving the run stuck as Running.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if snapshot, err := runstate.LoadAuthorized(persistCtx, f.runs, runID); err == nil {
		snapshot.Status = runstate.RunStatusFailed
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		// Mirrors engine.markRunFailed's run_error_message variable so a
		// failed run's reason survives on the snapshot itself, not just in
		// the (possibly rotated-out) event stream.
		snapshot.Variables[runstate.VarRunErrorMessage] = quoteJSONString(cause.Error())
		if saveErr := f.runs.Save(persistCtx, &snapshot, snapshot.Version); saveErr != nil {
			if f.logger != nil {
				f.logger.Warn(persistCtx, "agentflow: failed to persist workflow failure status", "run_id", runID, "save_error", saveErr)
			}
			f.emit(persistCtx, core.EventRunFailed, runID, []byte(fmt.Sprintf(`{"error":%q,"save_error":%q}`, cause.Error(), saveErr.Error())))
			return
		}
	}
	f.emit(persistCtx, core.EventRunFailed, runID, []byte(fmt.Sprintf(`{"error":%q}`, cause.Error())))
}

