package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aijustin/agentflow-go/internal/application/orchestration"
	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const (
	resumePromptVar                 = runstate.VarResumePrompt
	resumeAgentVar                  = runstate.VarResumeAgent
	resumeTrustModeVar              = runstate.VarResumeTrustMode
	resumeEpisodeIDVar              = runstate.VarResumeEpisodeID
	resumeTriggerKindVar            = runstate.VarResumeTriggerKind
	resumeSessionIDVar              = runstate.VarResumeSessionID
	executionPhaseVar               = runstate.VarExecutionPhase
	executionPhaseWorkflow          = runstate.ExecutionPhaseWorkflow
	executionPhaseAutonomous        = runstate.ExecutionPhaseAutonomous
	checkpointKindVar               = runstate.VarCheckpointKind
	checkpointKindBeforeFinalAnswer = runstate.CheckpointKindBeforeFinalAnswer
	checkpointKindToolApproval      = runstate.CheckpointKindToolApproval
	checkpointWorkflowNodeVar       = "checkpoint_workflow_node"
)

// ResumeAndContinue resumes a paused run and continues execution until the next
// pause point or completion.
//
// The call is idempotent: resuming a run that already reached Completed returns
// the persisted RunResult instead of a token error. A concurrent resume of the
// same run (with or without run leases) fails fast with ErrResumeInProgress
// rather than racing the pause token into an ambiguous ErrTokenSuperseded.
//
// When run leases are enabled, the lease is acquired before gate.Resume so the
// run is never left Running without a holder. Callers that only approve via
// Resume / ResumeRunByID(..., false) can carry the run to a terminal state
// later with ContinueRun.
func (f *Framework) ResumeAndContinue(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) (RunResult, error) {
	if f.gate == nil {
		return RunResult{}, fmt.Errorf("agentflow: human gate is not configured")
	}
	runID, err := f.runIDFromToken(ctx, token)
	if err != nil {
		return RunResult{}, err
	}
	if decision != core.DecisionReject {
		// Idempotent resume: a run that already completed (e.g. a duplicate
		// approval delivery, or a concurrent resume that finished first)
		// returns its persisted result instead of failing on the stale token.
		if snapshot, loadErr := runstate.LoadAuthorized(ctx, f.runs, runID); loadErr == nil &&
			snapshot.Status == runstate.RunStatusCompleted {
			return f.completedRunResult(ctx, snapshot)
		}
	}
	if decision == core.DecisionReject {
		if err := f.gate.Resume(ctx, token, decision, amendment); err != nil {
			return RunResult{}, err
		}
		f.currentEngine().RememberHITLReject(ctx, runID)
		return RunResult{RunID: runID, Status: runstate.RunStatusCancelled}, nil
	}
	releaseSlot, err := f.tryEnterResume(runID)
	if err != nil {
		return RunResult{}, err
	}
	defer releaseSlot()
	// Re-check under the slot: a concurrent resume that won the slot may have
	// completed the run between our first check and the slot acquisition.
	if snapshot, loadErr := runstate.LoadAuthorized(ctx, f.runs, runID); loadErr == nil &&
		snapshot.Status == runstate.RunStatusCompleted {
		return f.completedRunResult(ctx, snapshot)
	}
	return f.resumeAndContinueLocked(ctx, token, runID, decision, amendment)
}

// resumeAndContinueLocked performs the gate resume and continue with the
// run's in-process resume slot already held by the caller. A reject never
// takes the lease and never enters continueRun: the gate marks the run
// Cancelled and the call returns that terminal result, mirroring the
// reject semantics ResumeAndContinue has always had (ResumeRunByID reaches
// this path directly when continueExecution is true).
func (f *Framework) resumeAndContinueLocked(ctx context.Context, token, runID string, decision core.Decision, amendment json.RawMessage) (RunResult, error) {
	if decision == core.DecisionReject {
		if err := f.gate.Resume(ctx, token, decision, amendment); err != nil {
			return RunResult{}, err
		}
		f.currentEngine().RememberHITLReject(ctx, runID)
		return RunResult{RunID: runID, Status: runstate.RunStatusCancelled}, nil
	}
	var err error
	if f.runLocker != nil {
		var release func()
		ctx, release, err = f.holdRunLease(ctx, runID)
		if err != nil {
			return RunResult{}, err
		}
		defer release()
	}
	if err := f.gate.Resume(ctx, token, decision, amendment); err != nil {
		return RunResult{}, err
	}
	if snapshot, loadErr := runstate.LoadAuthorized(ctx, f.runs, runID); loadErr == nil {
		// load failure skips RunResumed; continue still runs from persisted state
		f.currentEngine().EmitRunResumedForSnapshot(ctx, snapshot)
		ctx = appexec.ContextWithRunResumedEmitted(ctx)
	}
	result, err := f.continueRun(ctx, runID)
	return result, mapLeaseLostError(ctx, err)
}

// tryEnterResume claims the in-process resume slot for runID. The second of
// two concurrent resume/continue calls on the same run loses deterministically
// with ErrResumeInProgress, which keeps a token race from surfacing as the
// ambiguous ErrTokenSuperseded. The returned function releases the slot.
func (f *Framework) tryEnterResume(runID string) (func(), error) {
	f.resumeMu.Lock()
	defer f.resumeMu.Unlock()
	if f.resumeInFlight == nil {
		f.resumeInFlight = make(map[string]struct{})
	}
	if _, ok := f.resumeInFlight[runID]; ok {
		return nil, fmt.Errorf("agentflow: run %q: %w", runID, runstate.ErrResumeInProgress)
	}
	f.resumeInFlight[runID] = struct{}{}
	return func() {
		f.resumeMu.Lock()
		defer f.resumeMu.Unlock()
		delete(f.resumeInFlight, runID)
	}, nil
}

// completedRunResult rebuilds the RunResult of an already-Completed run from
// its persisted snapshot so idempotent resume/continue entry points can return
// the same outcome the original execution produced.
func (f *Framework) completedRunResult(ctx context.Context, snapshot runstate.RunSnapshot) (RunResult, error) {
	result := RunResult{RunID: snapshot.RunID, Status: runstate.RunStatusCompleted}
	if final, ok := snapshot.StepOutputs["final"]; ok {
		raw, err := runstate.LoadStepOutput(ctx, f.blobs, final)
		if err != nil {
			return RunResult{}, fmt.Errorf("agentflow: load final step output: %w", err)
		}
		result.Output = string(raw)
		result.StructuredOutput = append(json.RawMessage(nil), raw...)
	}
	return result, nil
}

// ResumeRunByID resumes a paused run by signing a HITL token from the current snapshot.
// When continueExecution is true, execution continues until completion or the next pause.
//
// The call is idempotent for a run that already reached Completed: it returns
// the persisted RunResult instead of a "not paused" error.
//
// SECURITY: knowing the run ID alone is sufficient to mint a fresh resume
// token here — this is an indefinite resume capability that is NOT bounded
// by the HITL token TTL (the TTL only constrains tokens issued at pause
// time). Any HTTP surface exposing this method must authorize callers, e.g.
// via WithResumeAuthorizationHook; the built-in observability/studio and
// checkpoint write endpoints already default-deny without a configured
// policy.
func (f *Framework) ResumeRunByID(ctx context.Context, runID string, decision core.Decision, amendment json.RawMessage, continueExecution bool) (RunResult, error) {
	if f.tokenSigner == nil {
		return RunResult{}, fmt.Errorf("agentflow: HITL token signer is not configured")
	}
	if f.resumeAuthHook != nil {
		if err := f.resumeAuthHook(ctx, runID); err != nil {
			return RunResult{}, err
		}
	}
	snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	if snapshot.Status == runstate.RunStatusCompleted && decision != core.DecisionReject {
		return f.completedRunResult(ctx, snapshot)
	}
	// The slot is taken before the paused check so the second of two
	// concurrent ResumeRunByID calls loses deterministically with
	// ErrResumeInProgress even when the winner has already flipped the run
	// back to Running (without it, the loser would read a stale Paused
	// snapshot and race the winner's token).
	releaseSlot, err := f.tryEnterResume(runID)
	if err != nil {
		return RunResult{}, err
	}
	defer releaseSlot()
	// Re-load under the slot: the winner may have changed the status between
	// the first load and the slot acquisition.
	snapshot, err = runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	if snapshot.Status == runstate.RunStatusCompleted && decision != core.DecisionReject {
		return f.completedRunResult(ctx, snapshot)
	}
	if snapshot.Status != runstate.RunStatusPaused {
		return RunResult{}, fmt.Errorf("agentflow: run %q is not paused", runID)
	}
	payload := runstate.TokenPayload{RunID: snapshot.RunID, Version: snapshot.Version}
	if f.tokenTTL > 0 {
		payload.ExpiresAt = time.Now().UTC().Add(f.tokenTTL)
	}
	token, err := f.tokenSigner.Sign(payload)
	if err != nil {
		return RunResult{}, err
	}
	if continueExecution {
		return f.resumeAndContinueLocked(ctx, token, runID, decision, amendment)
	}
	if err := f.gate.Resume(ctx, token, decision, amendment); err != nil {
		return RunResult{}, err
	}
	// The gate approved the run (status is now Running) but the caller does
	// not want execution to continue here. Clear the checkpoint metadata so a
	// later Run() on the same ID does not re-enter or overwrite the consumed
	// checkpoint. A rejected run is Cancelled and needs no cleanup.
	if decision == core.DecisionApprove || decision == core.DecisionAmend {
		if err := f.currentEngine().ClearCheckpointState(ctx, runID); err != nil {
			return RunResult{}, err
		}
	}
	loaded, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{RunID: runID, Status: loaded.Status}, nil
}

func (f *Framework) runIDFromToken(ctx context.Context, token string) (string, error) {
	if f.tokenSigner != nil {
		payload, err := f.tokenSigner.Verify(token)
		if err != nil {
			return "", err
		}
		return payload.RunID, nil
	}
	if decoder, ok := f.gate.(core.PauseTokenDecoder); ok {
		return decoder.RunIDFromPauseToken(token)
	}
	// Custom gates that return the run ID itself as the opaque token can
	// resume without an HMAC signer as long as the snapshot is paused.
	if f.runs != nil {
		snapshot, err := runstate.LoadAuthorized(ctx, f.runs, token)
		if err == nil && snapshot.Status == runstate.RunStatusPaused {
			return token, nil
		}
	}
	return "", fmt.Errorf("agentflow: cannot resolve run ID from pause token; configure TokenSigner or implement core.PauseTokenDecoder")
}

// ContinueRun retries the continue of a run that is stuck in Running with
// unconsumed checkpoint metadata. That state arises when the human gate was
// approved (gate.Resume flipped the run back to Running) but the subsequent
// continue failed, or the worker crashed between the two; without this entry
// point the run would sit in Running forever with no public way forward.
//
// The call is idempotent and safe to retry:
//   - a run that already reached Completed returns its persisted RunResult;
//   - a Running run with pending checkpoint metadata re-enters the internal
//     continue-after-checkpoint path (workflow/hybrid runs resume from their
//     persisted step outputs);
//   - anything else (Paused, Failed, Cancelled, or Running with no checkpoint
//     metadata) returns a classified error naming the actual state, since the
//     right entry point for those is ResumeAndContinue, RetryFailedRun, or
//     none at all.
//
// A concurrent ContinueRun/ResumeAndContinue on the same run fails fast with
// ErrResumeInProgress.
func (f *Framework) ContinueRun(ctx context.Context, runID string) (*RunResult, error) {
	if f.runs == nil {
		return nil, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return nil, err
	}
	switch snapshot.Status {
	case runstate.RunStatusCompleted:
		result, err := f.completedRunResult(ctx, snapshot)
		if err != nil {
			return nil, err
		}
		return &result, nil
	case runstate.RunStatusRunning:
	default:
		return nil, fmt.Errorf("agentflow: ContinueRun requires a Running run with pending checkpoint metadata, run %q is %s: %w",
			runID, snapshot.Status, runstate.ErrInvalidTransition)
	}
	mode := f.currentScenario().Orchestration.Mode
	if mode != core.OrchestrationFixedWorkflow && mode != core.OrchestrationHybrid &&
		!hasPendingCheckpointMetadata(snapshot) {
		return nil, fmt.Errorf("agentflow: run %q is Running but has no pending checkpoint metadata; nothing to continue", runID)
	}
	releaseSlot, err := f.tryEnterResume(runID)
	if err != nil {
		return nil, err
	}
	defer releaseSlot()
	if f.runLocker != nil {
		var release func()
		ctx, release, err = f.holdRunLease(ctx, runID)
		if err != nil {
			return nil, err
		}
		defer release()
	}
	result, err := f.continueRun(ctx, runID)
	if err != nil {
		return nil, mapLeaseLostError(ctx, err)
	}
	return &result, nil
}

// hasPendingCheckpointMetadata reports whether the snapshot carries unconsumed
// checkpoint variables (a tool-approval or before-final-answer checkpoint) or
// an open human-gate pause record.
func hasPendingCheckpointMetadata(snapshot runstate.RunSnapshot) bool {
	if variableJSONString(snapshot.Variables, checkpointKindVar) != "" {
		return true
	}
	return snapshot.PendingGate != nil
}

func (f *Framework) continueRun(ctx context.Context, runID string) (RunResult, error) {
	snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	switch f.currentScenario().Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		return f.continueFixedWorkflowRun(ctx, runID, snapshot)
	case core.OrchestrationHybrid:
		return f.continueHybridRun(ctx, runID, snapshot)
	default:
		return f.currentEngine().ContinueAfterCheckpoint(ctx, runID)
	}
}

func (f *Framework) continueFixedWorkflowRun(ctx context.Context, runID string, snapshot runstate.RunSnapshot) (RunResult, error) {
	switch variableJSONString(snapshot.Variables, checkpointKindVar) {
	case checkpointKindBeforeFinalAnswer:
		return f.currentEngine().ContinueAfterCheckpoint(ctx, runID)
	case checkpointKindToolApproval:
		nodeID := variableJSONString(snapshot.Variables, checkpointWorkflowNodeVar)
		if nodeID == "" {
			return f.currentEngine().ContinueAfterCheckpoint(ctx, runID)
		}
		return f.continueWorkflowAgentCheckpoint(ctx, runID, nodeID)
	default:
		return f.continueWorkflowRun(ctx, runID)
	}
}

func (f *Framework) continueWorkflowAgentCheckpoint(ctx context.Context, runID, nodeID string) (RunResult, error) {
	result, err := f.currentEngine().ContinueAfterCheckpointPhase(ctx, runID)
	if err != nil {
		return RunResult{}, err
	}
	if result.Status == runstate.RunStatusPaused {
		return result, nil
	}
	runner := f.newWorkflowRunner()
	if err := runner.SaveStepOutput(ctx, f.currentScenario(), runID, nodeID, core.AgentOutput{RunID: runID, Text: result.Output}); err != nil {
		f.markWorkflowFailed(ctx, runID, err)
		return RunResult{}, err
	}
	return f.finishWorkflowRun(ctx, runID, true)
}

func (f *Framework) continueWorkflowRun(ctx context.Context, runID string) (RunResult, error) {
	if err := f.applyWorkflowAmendment(ctx, runID); err != nil {
		return RunResult{}, err
	}
	return f.finishWorkflowRun(ctx, runID, true)
}

func (f *Framework) applyWorkflowAmendment(ctx context.Context, runID string) error {
	snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return err
	}
	raw, ok := snapshot.Variables["human_amendment"]
	if !ok || len(raw) == 0 {
		return nil
	}
	amendment := variableJSONString(snapshot.Variables, "human_amendment")
	if amendment == "" {
		return nil
	}
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	snapshot.Variables["workflow_amendment"] = quoteJSONString(amendment)
	prior := variableJSONString(snapshot.Variables, resumePromptVar)
	if prior == "" {
		snapshot.Variables[resumePromptVar] = quoteJSONString(amendment)
	} else {
		snapshot.Variables[resumePromptVar] = quoteJSONString(prior + "\n\nHuman feedback: " + amendment)
	}
	delete(snapshot.Variables, "human_amendment")
	return f.runs.Save(ctx, &snapshot, snapshot.Version)
}

func (f *Framework) finishWorkflowRun(ctx context.Context, runID string, markCompleted bool) (RunResult, error) {
	if snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID); err == nil {
		if mode := TrustMode(variableJSONString(snapshot.Variables, resumeTrustModeVar)); mode != "" {
			ctx = core.ContextWithTrustMode(ctx, string(mode))
		}
	}
	runner := f.newWorkflowRunner()
	if err := runner.Resume(ctx, f.currentScenario(), runID); err != nil {
		var paused orchestration.WorkflowPausedError
		if errors.As(err, &paused) {
			return RunResult{RunID: runID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		f.markWorkflowFailed(ctx, runID, err)
		return RunResult{}, err
	}
	if !markCompleted {
		loaded, err := runstate.LoadAuthorized(ctx, f.runs, runID)
		if err != nil {
			return RunResult{}, err
		}
		return RunResult{RunID: runID, Status: loaded.Status}, nil
	}
	loaded, err := f.completeWorkflowRun(ctx, runID, nil)
	if err != nil {
		return RunResult{}, err
	}
	output, err := f.workflowRunOutput(ctx, loaded)
	if err != nil {
		return RunResult{}, err
	}
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: output}, nil
}

// RetryFailedRun moves a Failed run back to Running and re-executes it from
// its persisted progress: workflow nodes that already produced step outputs
// are skipped, a hybrid run whose workflow phase already finished re-enters
// the autonomous phase directly (workflow step outputs are re-hydrated into
// the request context), and an autonomous run with unconsumed checkpoint
// metadata (e.g. a lease-lost failure that left a tool-approval checkpoint
// behind) continues from that checkpoint. An autonomous run without pending
// checkpoint metadata has no resumable progress and returns an explicit
// error instead of silently re-running from scratch.
func (f *Framework) RetryFailedRun(ctx context.Context, runID string) (RunResult, error) {
	mode := f.currentScenario().Orchestration.Mode
	if f.runs == nil {
		return RunResult{}, fmt.Errorf("agentflow: run-state repository is not configured")
	}
	snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	if snapshot.Status != runstate.RunStatusFailed {
		return RunResult{}, fmt.Errorf("agentflow: run %q is not failed (status=%s)", runID, snapshot.Status)
	}
	autonomous := mode != core.OrchestrationFixedWorkflow && mode != core.OrchestrationHybrid
	if autonomous && !hasPendingCheckpointMetadata(snapshot) {
		return RunResult{}, fmt.Errorf("agentflow: RetryFailedRun for autonomous run %q requires pending checkpoint metadata; the failure left no resumable checkpoint (status variables carry no checkpoint_kind)", runID)
	}
	if f.runLocker != nil {
		var release func()
		ctx, release, err = f.holdRunLease(ctx, runID)
		if err != nil {
			return RunResult{}, err
		}
		defer release()
	}
	// Failed is terminal for normal transitions; this retry entry point is
	// the one deliberate exception, so it uses the explicit transition
	// override instead of weakening the state machine itself.
	snapshot.Status = runstate.RunStatusRunning
	if snapshot.Variables != nil {
		delete(snapshot.Variables, "run_error_message")
	}
	appexec.ClearOrphanedCheckpointState(&snapshot)
	saveCtx := runstate.ContextWithStatusTransitionOverride(ctx)
	if err := f.runs.Save(saveCtx, &snapshot, snapshot.Version); err != nil {
		return RunResult{}, err
	}
	corr := core.EpisodeCorrelation{
		EpisodeID:   variableJSONString(snapshot.Variables, resumeEpisodeIDVar),
		SessionID:   variableJSONString(snapshot.Variables, resumeSessionIDVar),
		TriggerKind: variableJSONString(snapshot.Variables, resumeTriggerKindVar),
	}
	ctx = core.ContextWithEpisodeCorrelation(ctx, corr)
	retryPayload := map[string]any{"checkpoint_kind": "retry_failed"}
	if agent := variableJSONString(snapshot.Variables, resumeAgentVar); agent != "" {
		retryPayload["agent"] = agent
	}
	for key, value := range core.FrameworkBuildFields() {
		retryPayload[key] = value
	}
	f.emitJSON(ctx, core.EventRunResumed, runID, retryPayload)
	switch mode {
	case core.OrchestrationFixedWorkflow:
		return f.finishWorkflowRun(ctx, runID, true)
	case core.OrchestrationHybrid:
		snapshot, err = runstate.LoadAuthorized(ctx, f.runs, runID)
		if err != nil {
			return RunResult{}, err
		}
		return f.continueHybridRun(ctx, runID, snapshot)
	default:
		// Autonomous runs retry from the checkpoint the failure left behind
		// (gated above), re-entering the same continue path a HITL resume
		// would take.
		return f.currentEngine().ContinueAfterCheckpoint(ctx, runID)
	}
}

func (f *Framework) continueHybridRun(ctx context.Context, runID string, snapshot runstate.RunSnapshot) (RunResult, error) {
	if result, ok, err := completedHybridResult(ctx, f, snapshot); err != nil {
		return RunResult{}, err
	} else if ok {
		return result, nil
	}
	if snapshot.Status == runstate.RunStatusPaused {
		return RunResult{RunID: runID, Status: runstate.RunStatusPaused}, nil
	}
	vars := decodeResumeVars(snapshot.Variables)
	if vars.ExecutionPhase == executionPhaseAutonomous {
		switch vars.CheckpointKind {
		case checkpointKindBeforeFinalAnswer, checkpointKindToolApproval:
			return f.currentEngine().ContinueAfterCheckpoint(ctx, runID)
		case "":
			// No pending checkpoint: this is a recovery continuation (e.g.
			// RetryFailedRun) of a run whose workflow phase already
			// finished. Re-hydrate the workflow step outputs and re-enter
			// the autonomous phase.
			req, err := f.hydrateRunRequest(ctx, hybridRunRequestFromVars(snapshot.RunID, snapshot.Variables, vars), snapshot)
			if err != nil {
				return RunResult{}, err
			}
			return f.currentEngine().RunHybrid(ctx, req)
		default:
			return RunResult{}, fmt.Errorf("agentflow: unknown autonomous checkpoint kind %q for run %q", vars.CheckpointKind, runID)
		}
	}
	var err error
	if vars.ExecutionPhase != executionPhaseAutonomous {
		if snapshot.PendingGate != nil {
			return RunResult{RunID: runID, Status: runstate.RunStatusPaused}, nil
		}
		if err := f.applyWorkflowAmendment(ctx, runID); err != nil {
			return RunResult{}, err
		}
		result, err := f.finishWorkflowRun(ctx, runID, false)
		if err != nil || result.Status == runstate.RunStatusPaused {
			return result, err
		}
		snapshot, err = runstate.LoadAuthorized(ctx, f.runs, runID)
		if err != nil {
			return RunResult{}, err
		}
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		snapshot.Variables[executionPhaseVar] = quoteJSONString(executionPhaseAutonomous)
		snapshot.Status = runstate.RunStatusRunning
		if err := f.runs.Save(ctx, &snapshot, snapshot.Version); err != nil {
			return RunResult{}, err
		}
		vars = decodeResumeVars(snapshot.Variables)
	}
	req := hybridRunRequestFromVars(snapshot.RunID, snapshot.Variables, vars)
	snapshot, err = runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	req, err = f.hydrateRunRequest(ctx, req, snapshot)
	if err != nil {
		return RunResult{}, err
	}
	return f.currentEngine().RunHybrid(ctx, req)
}

type resumeVars struct {
	Prompt         string
	Agent          string
	TrustMode      string
	EpisodeID      string
	TriggerKind    string
	SessionID      string
	ExecutionPhase string
	CheckpointKind string
}

func decodeResumeVars(vars map[string]json.RawMessage) resumeVars {
	return resumeVars{
		Prompt:         variableJSONString(vars, resumePromptVar),
		Agent:          variableJSONString(vars, resumeAgentVar),
		TrustMode:      variableJSONString(vars, resumeTrustModeVar),
		EpisodeID:      variableJSONString(vars, resumeEpisodeIDVar),
		TriggerKind:    variableJSONString(vars, resumeTriggerKindVar),
		SessionID:      variableJSONString(vars, resumeSessionIDVar),
		ExecutionPhase: variableJSONString(vars, executionPhaseVar),
		CheckpointKind: variableJSONString(vars, checkpointKindVar),
	}
}

func hybridRunRequest(snapshot runstate.RunSnapshot) RunRequest {
	return hybridRunRequestFromVars(snapshot.RunID, snapshot.Variables, decodeResumeVars(snapshot.Variables))
}

func hybridRunRequestFromVars(runID string, variables map[string]json.RawMessage, vars resumeVars) RunRequest {
	var input json.RawMessage
	if variables != nil {
		input = variables["input"]
	}
	return RunRequest{
		RunID:       runID,
		Agent:       vars.Agent,
		Prompt:      vars.Prompt,
		Context:     input,
		TrustMode:   TrustMode(vars.TrustMode),
		EpisodeID:   vars.EpisodeID,
		TriggerKind: vars.TriggerKind,
		SessionID:   vars.SessionID,
	}
}

func (f *Framework) newWorkflowRunner() *orchestration.WorkflowRunner {
	f.mu.RLock()
	cached := f.workflowRunner
	engine := f.engine
	scenario := f.scenario
	f.mu.RUnlock()
	if cached != nil {
		return cached
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.workflowRunner != nil {
		return f.workflowRunner
	}
	runner := orchestration.NewWorkflowRunner(
		f.tools,
		f.runs,
		// The runner emits straight into the sink, so wrap it with the
		// StreamRun tee fanout; engine and facade emissions consult the tee
		// in their own emit helpers instead.
		teeEventSink{inner: f.events},
		orchestration.WithAgentRegistry(workflowAgentRegistry{agents: scenario.Agents, engine: engine}),
		orchestration.WithHumanGate(f.gate),
		orchestration.WithToolApprovalEvaluator(f.approvalEvaluator),
		orchestration.WithBlobStore(f.blobs),
		orchestration.WithSecurityPolicy(f.policy),
		orchestration.WithAuditSink(f.audit),
		orchestration.WithWorkflowToolPolicy(f.toolGov),
		orchestration.WithOutputRedactor(f.redactor),
		orchestration.WithMemoryRewinder(engine),
	)
	f.workflowRunner = runner
	return runner
}

func saveRunResumeMetadata(snapshot *runstate.RunSnapshot, req RunRequest, resolvedAgent string) {
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	if req.Prompt != "" {
		snapshot.Variables[resumePromptVar] = quoteJSONString(req.Prompt)
	}
	agentName := req.Agent
	if agentName == "" {
		agentName = resolvedAgent
	}
	if agentName != "" {
		snapshot.Variables[resumeAgentVar] = quoteJSONString(agentName)
	}
	if req.TrustMode != "" {
		snapshot.Variables[resumeTrustModeVar] = quoteJSONString(string(req.TrustMode))
	}
	if req.EpisodeID != "" {
		snapshot.Variables[resumeEpisodeIDVar] = quoteJSONString(req.EpisodeID)
	}
	if req.TriggerKind != "" {
		snapshot.Variables[resumeTriggerKindVar] = quoteJSONString(req.TriggerKind)
	}
	if req.SessionID != "" {
		snapshot.Variables[resumeSessionIDVar] = quoteJSONString(req.SessionID)
	}
}

func variableJSONString(vars map[string]json.RawMessage, key string) string {
	if vars == nil {
		return ""
	}
	raw, ok := vars[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return string(raw)
	}
	return value
}
