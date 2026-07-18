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
	resumePromptVar                 = "resume_prompt"
	resumeAgentVar                  = "resume_agent"
	resumeTrustModeVar              = "resume_trust_mode"
	resumeEpisodeIDVar              = "resume_episode_id"
	resumeTriggerKindVar            = "resume_trigger_kind"
	resumeSessionIDVar              = "resume_session_id"
	executionPhaseVar               = "execution_phase"
	executionPhaseWorkflow          = "workflow"
	executionPhaseAutonomous        = "autonomous"
	checkpointKindVar               = "checkpoint_kind"
	checkpointKindBeforeFinalAnswer = "before_final_answer"
	checkpointKindToolApproval      = "tool_approval"
	checkpointWorkflowNodeVar       = "checkpoint_workflow_node"
)

// ResumeAndContinue resumes a paused run and continues execution until the next
// pause point or completion.
func (f *Framework) ResumeAndContinue(ctx context.Context, token string, decision core.Decision, amendment json.RawMessage) (RunResult, error) {
	if f.gate == nil {
		return RunResult{}, fmt.Errorf("agentflow: human gate is not configured")
	}
	runID, err := f.runIDFromToken(ctx, token)
	if err != nil {
		return RunResult{}, err
	}
	if err := f.gate.Resume(ctx, token, decision, amendment); err != nil {
		return RunResult{}, err
	}
	if decision == core.DecisionReject {
		f.engine.RememberHITLReject(ctx, runID)
		return RunResult{RunID: runID, Status: runstate.RunStatusCancelled}, nil
	}
	if f.runLocker != nil {
		var release func()
		ctx, release, err = f.holdRunLease(ctx, runID)
		if err != nil {
			return RunResult{}, err
		}
		defer release()
	}
	if snapshot, loadErr := runstate.LoadAuthorized(ctx, f.runs, runID); loadErr == nil {
		f.engine.EmitRunResumedForSnapshot(ctx, snapshot)
		ctx = appexec.ContextWithRunResumedEmitted(ctx)
	}
	result, err := f.continueRun(ctx, runID)
	return result, mapLeaseLostError(ctx, err)
}

// ResumeRunByID resumes a paused run by signing a HITL token from the current snapshot.
// When continueExecution is true, execution continues until completion or the next pause.
func (f *Framework) ResumeRunByID(ctx context.Context, runID string, decision core.Decision, amendment json.RawMessage, continueExecution bool) (RunResult, error) {
	if f.tokenSigner == nil {
		return RunResult{}, fmt.Errorf("agentflow: HITL token signer is not configured")
	}
	snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
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
		return f.ResumeAndContinue(ctx, token, decision, amendment)
	}
	if err := f.gate.Resume(ctx, token, decision, amendment); err != nil {
		return RunResult{}, err
	}
	// The gate approved the run (status is now Running) but the caller does
	// not want execution to continue here. Clear the checkpoint metadata so a
	// later Run() on the same ID does not re-enter or overwrite the consumed
	// checkpoint. A rejected run is Cancelled and needs no cleanup.
	if decision == core.DecisionApprove || decision == core.DecisionAmend {
		if err := f.engine.ClearCheckpointState(ctx, runID); err != nil {
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

func (f *Framework) continueRun(ctx context.Context, runID string) (RunResult, error) {
	snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	switch f.scenario.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow:
		return f.continueFixedWorkflowRun(ctx, runID, snapshot)
	case core.OrchestrationHybrid:
		return f.continueHybridRun(ctx, runID, snapshot)
	default:
		return f.engine.ContinueAfterCheckpoint(ctx, runID)
	}
}

func (f *Framework) continueFixedWorkflowRun(ctx context.Context, runID string, snapshot runstate.RunSnapshot) (RunResult, error) {
	switch variableJSONString(snapshot.Variables, checkpointKindVar) {
	case checkpointKindBeforeFinalAnswer:
		return f.engine.ContinueAfterCheckpoint(ctx, runID)
	case checkpointKindToolApproval:
		nodeID := variableJSONString(snapshot.Variables, checkpointWorkflowNodeVar)
		if nodeID == "" {
			return f.engine.ContinueAfterCheckpoint(ctx, runID)
		}
		return f.continueWorkflowAgentCheckpoint(ctx, runID, nodeID)
	default:
		return f.continueWorkflowRun(ctx, runID)
	}
}

func (f *Framework) continueWorkflowAgentCheckpoint(ctx context.Context, runID, nodeID string) (RunResult, error) {
	result, err := f.engine.ContinueAfterCheckpointPhase(ctx, runID)
	if err != nil {
		return RunResult{}, err
	}
	if result.Status == runstate.RunStatusPaused {
		return result, nil
	}
	runner := f.newWorkflowRunner()
	if err := runner.SaveStepOutput(ctx, f.scenario, runID, nodeID, core.AgentOutput{RunID: runID, Text: result.Output}); err != nil {
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
	snapshot.Variables["workflow_amendment"] = json.RawMessage(fmt.Sprintf("%q", amendment))
	prior := variableJSONString(snapshot.Variables, resumePromptVar)
	if prior == "" {
		snapshot.Variables[resumePromptVar] = json.RawMessage(fmt.Sprintf("%q", amendment))
	} else {
		snapshot.Variables[resumePromptVar] = json.RawMessage(fmt.Sprintf("%q", prior+"\n\nHuman feedback: "+amendment))
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
	if err := runner.Resume(ctx, f.scenario, runID); err != nil {
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
	output := f.workflowRunOutput(ctx, loaded)
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: output}, nil
}

// RetryFailedRun moves a Failed workflow or hybrid run back to Running and
// re-executes it from its persisted progress: workflow nodes that already
// produced step outputs are skipped, and a hybrid run whose workflow phase
// already finished re-enters the autonomous phase directly (workflow step
// outputs are re-hydrated into the request context).
func (f *Framework) RetryFailedRun(ctx context.Context, runID string) (RunResult, error) {
	switch f.scenario.Orchestration.Mode {
	case core.OrchestrationFixedWorkflow, core.OrchestrationHybrid:
	default:
		return RunResult{}, fmt.Errorf("agentflow: RetryFailedRun requires fixed_workflow or hybrid orchestration mode")
	}
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
	if f.scenario.Orchestration.Mode == core.OrchestrationFixedWorkflow {
		return f.finishWorkflowRun(ctx, runID, true)
	}
	snapshot, err = runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	return f.continueHybridRun(ctx, runID, snapshot)
}

func (f *Framework) continueHybridRun(ctx context.Context, runID string, snapshot runstate.RunSnapshot) (RunResult, error) {
	if result, ok := completedHybridResult(ctx, f, snapshot); ok {
		return result, nil
	}
	if snapshot.Status == runstate.RunStatusPaused {
		return RunResult{RunID: runID, Status: runstate.RunStatusPaused}, nil
	}
	if variableJSONString(snapshot.Variables, executionPhaseVar) == executionPhaseAutonomous {
		switch variableJSONString(snapshot.Variables, checkpointKindVar) {
		case checkpointKindBeforeFinalAnswer, checkpointKindToolApproval:
			return f.engine.ContinueAfterCheckpoint(ctx, runID)
		case "":
			// No pending checkpoint: this is a recovery continuation (e.g.
			// RetryFailedRun) of a run whose workflow phase already
			// finished. Re-hydrate the workflow step outputs and re-enter
			// the autonomous phase.
			req, err := f.hydrateRunRequest(ctx, hybridRunRequest(snapshot), snapshot)
			if err != nil {
				return RunResult{}, err
			}
			return f.engine.RunHybrid(ctx, req)
		default:
			return RunResult{}, fmt.Errorf("agentflow: unknown autonomous checkpoint kind %q for run %q", variableJSONString(snapshot.Variables, checkpointKindVar), runID)
		}
	}
	var err error
	if variableJSONString(snapshot.Variables, executionPhaseVar) != executionPhaseAutonomous {
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
		snapshot.Variables[executionPhaseVar] = json.RawMessage(fmt.Sprintf("%q", executionPhaseAutonomous))
		snapshot.Status = runstate.RunStatusRunning
		if err := f.runs.Save(ctx, &snapshot, snapshot.Version); err != nil {
			return RunResult{}, err
		}
	}
	req := hybridRunRequest(snapshot)
	snapshot, err = runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	req, err = f.hydrateRunRequest(ctx, req, snapshot)
	if err != nil {
		return RunResult{}, err
	}
	return f.engine.RunHybrid(ctx, req)
}

func hybridRunRequest(snapshot runstate.RunSnapshot) RunRequest {
	return RunRequest{
		RunID:       snapshot.RunID,
		Agent:       variableJSONString(snapshot.Variables, resumeAgentVar),
		Prompt:      variableJSONString(snapshot.Variables, resumePromptVar),
		Context:     snapshot.Variables["input"],
		TrustMode:   TrustMode(variableJSONString(snapshot.Variables, resumeTrustModeVar)),
		EpisodeID:   variableJSONString(snapshot.Variables, resumeEpisodeIDVar),
		TriggerKind: variableJSONString(snapshot.Variables, resumeTriggerKindVar),
		SessionID:   variableJSONString(snapshot.Variables, resumeSessionIDVar),
	}
}

func (f *Framework) newWorkflowRunner() *orchestration.WorkflowRunner {
	return orchestration.NewWorkflowRunner(
		f.tools,
		f.runs,
		f.events,
		orchestration.WithAgentRegistry(workflowAgentRegistry{agents: f.scenario.Agents, engine: f.engine}),
		orchestration.WithHumanGate(f.gate),
		orchestration.WithToolApprovalEvaluator(f.approvalEvaluator),
		orchestration.WithBlobStore(f.blobs),
		orchestration.WithSecurityPolicy(f.policy),
		orchestration.WithAuditSink(f.audit),
		orchestration.WithWorkflowToolPolicy(f.toolGov),
		orchestration.WithOutputRedactor(f.redactor),
		orchestration.WithMemoryRewinder(f.engine),
	)
}

func saveRunResumeMetadata(snapshot *runstate.RunSnapshot, req RunRequest, resolvedAgent string) {
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	if req.Prompt != "" {
		snapshot.Variables[resumePromptVar] = json.RawMessage(fmt.Sprintf("%q", req.Prompt))
	}
	agentName := req.Agent
	if agentName == "" {
		agentName = resolvedAgent
	}
	if agentName != "" {
		snapshot.Variables[resumeAgentVar] = json.RawMessage(fmt.Sprintf("%q", agentName))
	}
	if req.TrustMode != "" {
		snapshot.Variables[resumeTrustModeVar] = json.RawMessage(fmt.Sprintf("%q", req.TrustMode))
	}
	if req.EpisodeID != "" {
		snapshot.Variables[resumeEpisodeIDVar] = json.RawMessage(fmt.Sprintf("%q", req.EpisodeID))
	}
	if req.TriggerKind != "" {
		snapshot.Variables[resumeTriggerKindVar] = json.RawMessage(fmt.Sprintf("%q", req.TriggerKind))
	}
	if req.SessionID != "" {
		snapshot.Variables[resumeSessionIDVar] = json.RawMessage(fmt.Sprintf("%q", req.SessionID))
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
