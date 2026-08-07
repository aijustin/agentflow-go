package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/aijustin/agentflow-go/internal/toolinvoke"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

const (
	checkpointKindVar          = "checkpoint_kind"
	checkpointKindToolApproval = "tool_approval"
	checkpointPromptVar        = "checkpoint_prompt"
	checkpointAgentVar         = "checkpoint_agent"
	checkpointContextVar       = "checkpoint_context"
	beforeFinalResumedVar      = "before_final_resumed"
	// checkpointPendingPauseVar marks a checkpoint whose approval has not been
	// confirmed yet: the variables are persisted but the run never completed a
	// gate.Pause + resume cycle, so nobody could have approved anything. The
	// marker survives the pause itself (clearing it between gate.Pause and
	// gate.Resume would bump the snapshot version and invalidate the pause
	// token) and is removed by the resume paths once the gate confirms the
	// approval. A run still carrying it in Running state crashed between the
	// checkpoint write and the gate call, and its checkpoint must never be
	// executed.
	checkpointPendingPauseVar = runstate.VarCheckpointPendingPause
	// checkpointResumedVar is deprecated; reads accept it for backward compatibility.
	checkpointResumedVar    = "checkpoint_resumed"
	checkpointToolCallsVar  = "checkpoint_tool_calls"
	checkpointMessagesVar   = "checkpoint_messages"
	checkpointToolCountsVar = "checkpoint_tool_counts"
	// checkpointUsageVar persists the run-level usage tracker next to
	// checkpoint_tool_counts so a pause/resume cycle keeps both the
	// accumulated token totals and the context-length recovery budget.
	// Snapshots written before this variable existed decode to a zero
	// tracker.
	checkpointUsageVar = "checkpoint_usage"
	// checkpointApprovalsVar / checkpointDenyCountVar persist the run-scoped
	// approval cache decisions and the HITL deny-breaker count next to
	// checkpoint_tool_counts, so a run that migrates to another node (or is
	// recovered by the reaper) keeps its remembered approvals and consecutive
	// deny budget across the pause. Snapshots written before these variables
	// existed simply restore nothing (process-local behavior, as before).
	checkpointApprovalsVar      = "checkpoint_approvals"
	checkpointDenyCountVar      = "checkpoint_deny_count"
	checkpointOutputModeVar     = "checkpoint_output_mode"
	checkpointStepsConsumedVar  = "checkpoint_steps_consumed"
	checkpointReplanAttemptsVar = "checkpoint_replan_attempts"
	checkpointWorkflowNodeVar   = "checkpoint_workflow_node"
	humanAmendmentVar           = "human_amendment"
)

// RunPausedError indicates the run paused and requires human approval before continuing.
type RunPausedError struct {
	RunID string
	Token string
	Kind  string
}

func (e RunPausedError) Error() string {
	return fmt.Sprintf("runtime: run %q paused (%s)", e.RunID, e.Kind)
}

// ContinueAfterCheckpoint resumes execution for a run that was approved at a checkpoint.
func (e *Engine) ContinueAfterCheckpoint(ctx context.Context, runID string) (RunResult, error) {
	return e.continueAfterCheckpoint(ctx, runID, true)
}

type runResumedEmittedKey struct{}

// ContextWithRunResumedEmitted marks that RunResumed was already emitted for
// this resume call so continueAfterCheckpoint does not double-emit.
func ContextWithRunResumedEmitted(ctx context.Context) context.Context {
	return context.WithValue(ctx, runResumedEmittedKey{}, true)
}

func runResumedAlreadyEmitted(ctx context.Context) bool {
	v, _ := ctx.Value(runResumedEmittedKey{}).(bool)
	return v
}

func (e *Engine) emitRunResumed(ctx context.Context, snapshot runstate.RunSnapshot, checkpointKind string) {
	payload := map[string]any{
		"trigger_kind":    core.TriggerKindHITLResume,
		"checkpoint_kind": checkpointKind,
	}
	if agent := variableString(snapshot.Variables, resumeAgentVar); agent != "" {
		payload["agent"] = agent
	} else if agent := variableString(snapshot.Variables, checkpointAgentVar); agent != "" {
		payload["agent"] = agent
	}
	if trust := variableString(snapshot.Variables, resumeTrustModeVar); trust != "" {
		payload["trust_mode"] = trust
	}
	if checkpointKind == checkpointKindToolApproval {
		if tool := firstPendingCheckpointTool(snapshot.Variables[checkpointToolCallsVar]); tool != "" {
			payload["tool"] = tool
		}
	}
	for key, value := range core.FrameworkBuildFields() {
		payload[key] = value
	}
	e.emitJSON(ctx, core.EventRunResumed, snapshot.RunID, payload)
	_ = e.persistResumeTriggerKind(ctx, snapshot.RunID, core.TriggerKindHITLResume)
}

// EmitRunResumedForSnapshot exposes RunResumed emission for Framework resume
// entry points that may continue via workflow paths (AF-REQ-02/06).
func (e *Engine) EmitRunResumedForSnapshot(ctx context.Context, snapshot runstate.RunSnapshot) {
	kind := variableString(snapshot.Variables, checkpointKindVar)
	corr := episodeCorrelationFromSnapshot(snapshot)
	corr.TriggerKind = core.TriggerKindHITLResume
	ctx = core.ContextWithEpisodeCorrelation(ctx, corr)
	e.emitRunResumed(ctx, snapshot, kind)
}

func firstPendingCheckpointTool(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var calls []llm.ToolCall
	if err := json.Unmarshal(raw, &calls); err != nil || len(calls) == 0 {
		return ""
	}
	return calls[0].Name
}

func (e *Engine) persistResumeTriggerKind(ctx context.Context, runID, triggerKind string) error {
	return e.saveSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		snapshot.Variables[resumeTriggerKindVar] = jsonStringValue(triggerKind)
		return nil
	})
}

// ContinueAfterCheckpointPhase resumes a checkpointed agent phase without
// marking the run Completed. Callers embedding the runtime inside a workflow
// must persist the returned output as a workflow step and finish the run
// themselves.
func (e *Engine) ContinueAfterCheckpointPhase(ctx context.Context, runID string) (RunResult, error) {
	return e.continueAfterCheckpoint(ctx, runID, false)
}

func (e *Engine) continueAfterCheckpoint(ctx context.Context, runID string, completeRun bool) (RunResult, error) {
	snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
	if err != nil {
		return RunResult{}, err
	}
	if snapshot.Status != runstate.RunStatusRunning {
		return RunResult{}, fmt.Errorf("runtime: continue requires running snapshot, got %s", snapshot.Status)
	}
	if mode := TrustMode(variableString(snapshot.Variables, resumeTrustModeVar)); mode != "" {
		ctx = ContextWithTrustMode(ctx, mode)
	}
	corr := episodeCorrelationFromSnapshot(snapshot)
	corr.TriggerKind = core.TriggerKindHITLResume
	ctx = core.ContextWithEpisodeCorrelation(ctx, corr)
	kind := variableString(snapshot.Variables, checkpointKindVar)
	if checkpointBoolVar(snapshot.Variables, checkpointPendingPauseVar) {
		// The checkpoint variables were persisted but gate.Pause never
		// confirmed the pause (the process crashed in between, or the pause
		// marker cleanup failed): no human approved anything, so executing
		// the pending state would bypass the approval gate. Fail closed; an
		// operator can discard the checkpoint via ClearCheckpointState.
		return RunResult{}, fmt.Errorf("runtime: run %q carries an unconfirmed pause checkpoint (pending_pause); refusing to resume an unapproved checkpoint", runID)
	}
	if !runResumedAlreadyEmitted(ctx) {
		e.emitRunResumed(ctx, snapshot, kind)
	}
	switch kind {
	case "before_final_answer":
		return e.continueBeforeFinalAnswer(ctx, snapshot, completeRun)
	case "tool_approval":
		return e.continueToolApproval(ctx, snapshot, completeRun)
	default:
		return RunResult{}, fmt.Errorf("runtime: unknown checkpoint kind %q", kind)
	}
}

func (e *Engine) continueBeforeFinalAnswer(ctx context.Context, snapshot runstate.RunSnapshot, completeRun bool) (RunResult, error) {
	prompt := applyHumanAmendment(snapshot.Variables, variableString(snapshot.Variables, checkpointPromptVar))
	req := RunRequest{
		RunID:   snapshot.RunID,
		Agent:   variableString(snapshot.Variables, checkpointAgentVar),
		Prompt:  prompt,
		Context: snapshot.Variables[checkpointContextVar],
	}
	if variableString(snapshot.Variables, checkpointOutputModeVar) == "structured" {
		raw, err := e.structuredAnswer(ctx, req)
		if err != nil {
			var paused RunPausedError
			if errorsAsRunPaused(err, &paused) {
				return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
			}
			if isPermanentContinueError(err) {
				return RunResult{}, e.failContinuePermanent(ctx, req.RunID, err)
			}
			// Checkpoint vars stay intact; keep the run Running so the caller
			// can retry ContinueAfterCheckpoint after a transient error.
			return RunResult{}, err
		}
		if completeRun {
			result, err := e.completeStructuredRun(ctx, req.RunID, raw)
			if err != nil {
				e.clearCheckpointOnCompletionConflict(ctx, &snapshot, err)
				return RunResult{}, err
			}
			if err := e.clearCheckpointState(ctx, &snapshot, "before_final_answer"); err != nil {
				return RunResult{}, err
			}
			return result, nil
		}
		if err := e.clearCheckpointState(ctx, &snapshot, "before_final_answer"); err != nil {
			return RunResult{}, err
		}
		return RunResult{RunID: req.RunID, Status: runstate.RunStatusRunning, Output: string(raw), StructuredOutput: raw}, nil
	}
	output, err := e.answer(ctx, req)
	if err != nil {
		var paused RunPausedError
		if errorsAsRunPaused(err, &paused) {
			return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		if isPermanentContinueError(err) {
			return RunResult{}, e.failContinuePermanent(ctx, req.RunID, err)
		}
		// Checkpoint vars stay intact; keep the run Running so the caller
		// can retry ContinueAfterCheckpoint after a transient error.
		return RunResult{}, err
	}
	if completeRun {
		result, err := e.completeRun(ctx, req.RunID, output)
		if err != nil {
			e.clearCheckpointOnCompletionConflict(ctx, &snapshot, err)
			return RunResult{}, err
		}
		if err := e.clearCheckpointState(ctx, &snapshot, "before_final_answer"); err != nil {
			return RunResult{}, err
		}
		return result, nil
	}
	if err := e.clearCheckpointState(ctx, &snapshot, "before_final_answer"); err != nil {
		return RunResult{}, err
	}
	return RunResult{RunID: req.RunID, Status: runstate.RunStatusRunning, Output: output}, nil
}

func (e *Engine) continueToolApproval(ctx context.Context, snapshot runstate.RunSnapshot, completeRun bool) (RunResult, error) {
	agentName := variableString(snapshot.Variables, checkpointAgentVar)
	agent, err := e.resolveAgent(agentName)
	if err != nil {
		return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, err)
	}
	var pending []llm.ToolCall
	if raw := snapshot.Variables[checkpointToolCallsVar]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &pending); err != nil {
			return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, fmt.Errorf("runtime: decode checkpoint tool calls: %w", err))
		}
	}
	var messages []llm.Message
	if raw := snapshot.Variables[checkpointMessagesVar]; len(raw) > 0 {
		resolved, err := e.resolveCheckpointVar(ctx, raw)
		if err != nil {
			return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, fmt.Errorf("runtime: resolve checkpoint messages: %w", err))
		}
		if err := json.Unmarshal(resolved, &messages); err != nil {
			return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, fmt.Errorf("runtime: decode checkpoint messages: %w", err))
		}
	}
	stepsConsumed := checkpointStepsConsumed(snapshot.Variables)
	messages, pending = normalizeCheckpointToolCallIDs(snapshot.RunID, stepsConsumed, messages, pending)
	tracker := newToolCallTracker()
	if raw := snapshot.Variables[checkpointToolCountsVar]; len(raw) > 0 {
		decoded, err := decodeToolCallTracker(raw)
		if err != nil {
			return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, fmt.Errorf("runtime: decode checkpoint tool counts: %w", err))
		}
		tracker = decoded
	}
	// Restore the usage tracker so the run's token accounting and its
	// context-length recovery budget continue across the pause; a snapshot
	// written before checkpoint_usage existed decodes to a zero tracker.
	usage, err := decodeUsageTracker(snapshot.Variables[checkpointUsageVar])
	if err != nil {
		return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, fmt.Errorf("runtime: decode checkpoint usage: %w", err))
	}
	e.restoreUsageTracker(snapshot.RunID, usage)
	if err := e.restoreApprovalCheckpointState(snapshot.RunID, snapshot.Variables); err != nil {
		return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, err)
	}
	if len(messages) == 0 {
		// A tool_approval checkpoint always persists the conversation up to
		// and including the paused assistant turn. Missing messages mean the
		// checkpoint state was already consumed by a prior resume (it is
		// cleared by clearCheckpointState); continuing with an empty
		// conversation would silently re-run the tool loop from nothing.
		return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, fmt.Errorf("runtime: checkpoint messages for run %q are missing; the checkpoint may already have been consumed by a prior resume", snapshot.RunID))
	}
	prompt := applyHumanAmendment(snapshot.Variables, variableString(snapshot.Variables, checkpointPromptVar))
	// Capture the amendment text before clearCheckpointState clears it; it
	// is injected as a user message only after every pending tool call of the
	// paused assistant turn has produced its tool result, because providers
	// reject a user message wedged between an assistant tool_calls message
	// and its tool responses.
	amendment := humanAmendmentText(snapshot.Variables)
	profile, err := e.llmProfile(agent.LLM)
	if err != nil {
		return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, err)
	}
	caller, ok := e.llm.(llm.ToolCaller)
	if !ok || !e.llm.Supports(agent.LLM, llm.CapToolCall) {
		return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, fmt.Errorf("runtime: llm profile %q does not support tool calling", agent.LLM))
	}
	// Cumulative across the whole run (including replans before this pause)
	// so pausing and resuming cannot reset the replan budget.
	replanAttempts := checkpointIntVar(snapshot.Variables, checkpointReplanAttemptsVar)
	output, err := e.continueToolLoopFrom(ctx, snapshot.RunID, agent, profile, messages, pending, tracker, caller, prompt, amendment, stepsConsumed, replanAttempts)
	if err != nil {
		var paused RunPausedError
		if errorsAsRunPaused(err, &paused) {
			return RunResult{RunID: snapshot.RunID, Status: runstate.RunStatusPaused, Token: paused.Token}, nil
		}
		if isPermanentContinueError(err) {
			return RunResult{}, e.failContinuePermanent(ctx, snapshot.RunID, err)
		}
		// Checkpoint vars are still intact; keep the run Running so the
		// caller can retry ContinueAfterCheckpoint after a transient error.
		return RunResult{}, err
	}
	if completeRun {
		result, err := e.completeRun(ctx, snapshot.RunID, output)
		if err != nil {
			return RunResult{}, err
		}
		if err := e.clearCheckpointState(ctx, &snapshot, "tool_approval"); err != nil {
			return RunResult{}, err
		}
		return result, nil
	}
	if err := e.clearCheckpointState(ctx, &snapshot, "tool_approval"); err != nil {
		return RunResult{}, err
	}
	return RunResult{RunID: snapshot.RunID, Status: runstate.RunStatusRunning, Output: output}, nil
}

func normalizeCheckpointToolCallIDs(runID string, logicalStep int, messages []llm.Message, pending []llm.ToolCall) ([]llm.Message, []llm.ToolCall) {
	assistantIndex := lastAssistantWithToolCallsIndex(messages)
	if assistantIndex < 0 {
		return messages, ensureToolCallIDs(runID, logicalStep, pending)
	}
	allCalls := ensureToolCallIDs(runID, logicalStep, messages[assistantIndex].ToolCalls)
	messages[assistantIndex].ToolCalls = allCalls
	out := append([]llm.ToolCall(nil), pending...)
	start := len(allCalls) - len(out)
	if start < 0 {
		start = 0
	}
	for index := range out {
		if strings.TrimSpace(out[index].ID) != "" {
			continue
		}
		position := start + index
		if position < len(allCalls) &&
			allCalls[position].Name == out[index].Name &&
			canonicalJSON(allCalls[position].Input) == canonicalJSON(out[index].Input) {
			out[index].ID = allCalls[position].ID
			continue
		}
		out[index].ID = stableToolCallID(runID, logicalStep, position, out[index])
	}
	return messages, out
}

func (e *Engine) continueToolLoopFrom(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, messages []llm.Message, pending []llm.ToolCall, tracker *toolCallTracker, caller llm.ToolCaller, prompt string, amendment string, stepsConsumed int, replanAttempts int) (string, error) {
	if len(pending) > 0 {
		turnStart := lastAssistantWithToolCallsIndex(messages)
		// pending[0] is the tool call the human just approved; execute it
		// directly. The remaining calls from the same assistant turn still go
		// through the normal approval/dispatch path and may pause again.
		approved := pending[0]
		result, err := e.dispatchApprovedTool(ctx, runID, agent, approved, tracker)
		if err != nil {
			return "", err
		}
		toolorch.RememberAllow(e.tooling.approvalStore, runID, approved.Name, approved.Input)
		if e.tooling.denyBreaker != nil {
			e.tooling.denyBreaker.RecordAllow(runID)
		}
		// Materialize exactly like the normal batch path
		// (materializeToolBatchItem): compaction, ToolOutputMaxBytes
		// truncation, and the tool_result_class/truncate_strategy metadata
		// must not diverge just because the call went through approval.
		approvedItem := toolBatchItem{call: approved, result: result}
		if err := e.materializeToolBatchItem(&approvedItem, profile); err != nil {
			return "", err
		}
		messages = append(messages, approvedItem.message)
		if e.scenario.Orchestration.Planning.Enabled && e.scenario.Orchestration.Planning.Execute && result.Error == "" {
			// See the identical comment in dispatchToolCalls: this is plan
			// bookkeeping, not the tool result itself, so a failure here
			// must not discard the already-successful approved tool call.
			if err := e.markPlanStepDone(ctx, runID, approved.Name); err != nil {
				e.logWarn(ctx, "runtime: failed to update plan progress after successful tool call", "run_id", runID, "tool", approved.Name, "error", err)
			}
		}
		messages, _, err = e.dispatchToolCalls(ctx, runID, agent, profile, llm.Message{}, pending[1:], messages, tracker, prompt, false, stepsConsumed, replanAttempts, false, nil)
		if err != nil {
			return "", err
		}
		if turnStart >= 0 {
			if err := e.persistToolTurnFromStepOutputs(ctx, runID, agent, messages[turnStart]); err != nil {
				return "", err
			}
		}
	}
	// All tool_call IDs of the paused turn now have their tool responses, so
	// the human feedback can be appended without breaking the provider's
	// assistant(tool_calls) -> tool message ordering contract.
	if amendment != "" {
		messages = append(messages, llm.Message{Role: llm.RoleUser, Content: "Human feedback: " + amendment})
	}
	maxSteps := firstPositive(agent.Policy.MaxSteps, e.scenario.Runtime.MaxSteps, 8)
	toolSpecs := e.toolSpecs(ctx, runID, agent)
	baseReq := llm.ChatRequest{
		Messages:        messages,
		Temperature:     profile.Temperature,
		TopP:            profile.TopP,
		MaxTokens:       profile.MaxOutputTokens,
		Thinking:        profile.Thinking,
		ReasoningEffort: profile.ReasoningEffort,
		ExtraBody:       profile.ExtraBody,
	}
	remainingSteps := maxSteps - stepsConsumed
	if remainingSteps <= 0 {
		// The step budget was already exhausted before control returned
		// here (e.g. the pause happened on the very last allowed step).
		// Try to replan instead of always failing hard, matching the
		// budget-exhaustion behavior of the non-paused tool loop.
		return e.replanOrFail(ctx, runID, agent, profile, baseReq, caller, toolSpecs, messages, tracker, maxSteps, prompt, replanAttempts, stepsConsumed, true, 0, nil)
	}
	return e.answerWithToolsFrom(ctx, runID, agent, profile, baseReq, caller, toolSpecs, messages, tracker, remainingSteps, prompt, replanAttempts, stepsConsumed, true, 0, nil)
}

func errorsAsRunPaused(err error, target *RunPausedError) bool {
	if err == nil {
		return false
	}
	return errors.As(err, target)
}

// isPermanentContinueError reports whether a continue failure can never
// succeed on a blind retry (as opposed to transient, retryable failures such
// as provider 429/5xx or network timeouts). Only explicitly classified
// errors are permanent; unknown errors stay transient, preserving the
// Running + checkpoint state for a later ContinueRun.
func isPermanentContinueError(err error) bool {
	// The token budget is scenario configuration: a blind retry of the same
	// checkpoint burns the same accumulated usage and fails again, so the run
	// settles as Failed (checkpoint kept) until an operator raises the budget.
	return errors.Is(err, ErrLLMGatewayRequired) || errors.Is(err, ErrTokenBudgetExceeded)
}

// failContinuePermanent persists the run as Failed with the error as its
// reason and returns the error. The checkpoint metadata is intentionally
// kept (markRunFailedPermanent forces past the checkpoint preservation
// without clearing the variables) so RetryFailedRun / ContinueRun can
// re-enter once the underlying configuration is fixed.
func (e *Engine) failContinuePermanent(ctx context.Context, runID string, err error) error {
	e.markRunFailedPermanent(ctx, runID, err)
	return err
}

func (e *Engine) persistUserPromptIfNeeded(ctx context.Context, runID string, agent core.Agent, prompt string) error {
	if strings.TrimSpace(prompt) == "" {
		return nil
	}
	return e.writeMemory(ctx, runID, agent, []memoryMessage{runTurnMemoryMessage(string(llm.RoleUser), prompt)})
}

func (e *Engine) maybePauseToolCall(ctx context.Context, runID string, agent core.Agent, pending []llm.ToolCall, messages []llm.Message, tracker *toolCallTracker, prompt string, stepsConsumed int, replanAttempts int) (*RunPausedError, error) {
	if len(pending) == 0 {
		return nil, nil
	}
	call := pending[0]
	tool, ok := e.scenario.Tools[call.Name]
	if !ok {
		return nil, nil
	}
	// full_trust skips static ApprovalPause / ApprovalAlways, but still honors
	// a dynamic ToolApprovalEvaluator (MCP auth, mandatory user-input tools).
	var pauseRequired bool
	var err error
	if TrustModeFromContext(ctx) == TrustModeFullTrust {
		if e.gov.approvalEvaluator != nil {
			pauseRequired, err = e.gov.approvalEvaluator.PauseRequired(ctx, runID, tool, call)
		}
	} else {
		pauseRequired, err = toolinvoke.EvaluatePauseRequired(ctx, tool, e.gov.approvalEvaluator, runID, call)
	}
	if err != nil {
		return nil, err
	}
	if !pauseRequired {
		return nil, nil
	}
	if e.tooling.orchestrator != nil {
		decision, orchErr := e.tooling.orchestrator.DecideApproval(ctx, toolorch.ApprovalRequest{
			RunID:         runID,
			Tool:          call.Name,
			Input:         call.Input,
			PauseRequired: true,
		})
		if orchErr != nil {
			return nil, orchErr
		}
		switch decision {
		case toolorch.DecisionAllow:
			return nil, nil
		case toolorch.DecisionDeny:
			// Soft-deny on the execute path; do not open the human gate again.
			return nil, nil
		}
	}
	if e.coord.gate == nil {
		return nil, nil
	}
	if delegationDepthFromContext(ctx) > 0 {
		// A delegated sub-agent shares the parent's run snapshot; pausing
		// here would overwrite the parent tool loop's own checkpoint state
		// (see dispatchSubAgent). Fail the tool call instead so the
		// delegation returns a clear error and the parent loop continues.
		return nil, fmt.Errorf("runtime: tool %q requires human approval, which is not supported for delegated sub-agent calls", call.Name)
	}
	snapshot, err := runstate.LoadAuthorized(ctx, e.persist.runs, runID)
	if err != nil {
		return nil, err
	}
	if snapshot.Status != runstate.RunStatusRunning {
		// Another writer already moved this run to a terminal or paused
		// state (e.g. cancellation, or a concurrent completion); do not
		// pause a run that is no longer actively Running.
		return nil, fmt.Errorf("runtime: cannot pause run %q in status %s", runID, snapshot.Status)
	}
	// Persist every still-pending call from this assistant turn (the one
	// awaiting approval plus any that follow it) so resume executes all of
	// them and never leaves orphaned tool_call IDs without a tool response.
	// Normalize tool inputs first: truncated/malformed json.RawMessage values
	// fail MarshalJSON and would abort the pause before HITL can recover.
	toolCallsRaw, err := json.Marshal(llm.NormalizeToolCallInputs(pending))
	if err != nil {
		return nil, err
	}
	messagesRaw, err := json.Marshal(llm.NormalizeMessageToolInputs(messages))
	if err != nil {
		return nil, err
	}
	// The serialized conversation can be arbitrarily large (it includes every
	// tool output of the run so far); externalize it to the blob store above
	// the step-output threshold so a pause never has to fit the whole
	// conversation into one snapshot row/value.
	messagesRaw, err = e.externalizeCheckpointVar(ctx, messagesRaw)
	if err != nil {
		return nil, err
	}
	countsRaw, err := json.Marshal(tracker.ensure())
	if err != nil {
		return nil, err
	}
	usageRaw, err := json.Marshal(e.usageTrackerFor(runID))
	if err != nil {
		return nil, err
	}
	vars := map[string]json.RawMessage{
		checkpointKindVar:           jsonStringValue(checkpointKindToolApproval),
		checkpointAgentVar:          jsonStringValue(agent.Name),
		checkpointPromptVar:         jsonStringValue(prompt),
		checkpointToolCallsVar:      toolCallsRaw,
		checkpointMessagesVar:       messagesRaw,
		checkpointToolCountsVar:     countsRaw,
		checkpointUsageVar:          usageRaw,
		checkpointStepsConsumedVar:  json.RawMessage(strconv.Itoa(stepsConsumed)),
		checkpointReplanAttemptsVar: json.RawMessage(strconv.Itoa(replanAttempts)),
		// Set until a confirmed approval resumes the run; a survivor in Running
		// state means the process crashed between the checkpoint write and
		// gate.Pause and the checkpoint was never approved (see
		// checkpointPendingPauseVar).
		checkpointPendingPauseVar: json.RawMessage(`true`),
	}
	// Externalize the run-scoped approval cache and deny-breaker state into
	// the checkpoint so a resume on another node restores them (see
	// checkpointApprovalsVar). Stores without RunStateExporter support simply
	// contribute nothing.
	if exporter, ok := e.tooling.approvalStore.(toolorch.RunStateExporter); ok {
		if approvalsRaw, ok := exporter.ExportRun(runID); ok {
			vars[checkpointApprovalsVar] = approvalsRaw
		}
	}
	if e.tooling.denyBreaker != nil {
		if count := e.tooling.denyBreaker.ExportRun(runID); count > 0 {
			vars[checkpointDenyCountVar] = json.RawMessage(strconv.Itoa(count))
		}
	}
	if nodeID := core.WorkflowNodeFromContext(ctx); nodeID != "" {
		vars[checkpointWorkflowNodeVar] = jsonStringValue(nodeID)
	}
	if err := e.saveCheckpointVariables(ctx, &snapshot, vars); err != nil {
		return nil, err
	}
	// Persist the user prompt when pausing so HITL inspection can see what
	// was asked, without writing it at tool-loop start where a later
	// failure/cancel would leave orphaned history.
	if err := e.persistUserPromptIfNeeded(ctx, runID, agent, prompt); err != nil {
		return nil, err
	}
	approvalKind, evaluatorName := e.toolPauseAttribution(ctx, tool)
	pausePayload := map[string]any{
		"tool":           call.Name,
		"tool_call":      call.ID,
		"agent":          agent.Name,
		"side_effect":    tool.SideEffect,
		"pause_required": true,
		"pause_reason":   "tool_approval",
		"approval_kind":  approvalKind,
	}
	if trust := string(TrustModeFromContext(ctx)); trust != "" {
		pausePayload["trust_mode"] = trust
	}
	if evaluatorName != "" {
		pausePayload["evaluator"] = evaluatorName
	}
	payload, err := json.Marshal(pausePayload)
	if err != nil {
		return nil, err
	}
	token, err := e.pauseWithRetry(ctx, runID, func(version int64) core.CheckpointState {
		return core.CheckpointState{RunID: runID, Version: version, NodeID: "tool_approval", Payload: payload}
	})
	if err != nil {
		// The gate never moved the run to Paused, so the tool-approval
		// checkpoint we just wrote would otherwise leave the run Running with
		// a consumable checkpoint (pending tool calls + messages). Roll it
		// back so no resume path can execute the pending tool without an
		// actual human approval.
		if clearErr := e.clearCheckpointState(ctx, &snapshot, ""); clearErr != nil {
			e.logWarn(ctx, "runtime: failed to roll back checkpoint variables after pause failure", "run_id", runID, "error", clearErr)
		}
		return nil, err
	}
	if err := e.ensureRunPaused(ctx, runID); err != nil {
		// The gate already persisted the Paused status and issued the token,
		// so the pause itself is valid; surfacing the error (instead of only
		// warning) lets the caller distinguish "paused but status
		// normalization failed" from a clean pause. The run stays Paused with
		// its checkpoint intact, so no rollback here.
		return nil, fmt.Errorf("runtime: persist paused status for run %q: %w", runID, err)
	}
	e.emitJSON(ctx, core.EventRunPaused, runID, pausePayload)
	paused := RunPausedError{RunID: runID, Token: token, Kind: "tool_approval"}
	return &paused, nil
}

// toolPauseAttribution reports whether the pause came from static policy or a
// dynamic evaluator (AF-REQ-04).
func (e *Engine) toolPauseAttribution(ctx context.Context, tool core.Tool) (approvalKind, evaluatorName string) {
	if TrustModeFromContext(ctx) == TrustModeFullTrust {
		approvalKind = "evaluator"
	} else if core.ToolApprovalPauseRequired(tool) {
		approvalKind = "static"
	} else {
		approvalKind = "evaluator"
	}
	if e.gov.approvalEvaluator != nil {
		if named, ok := e.gov.approvalEvaluator.(core.NamedToolApprovalEvaluator); ok {
			evaluatorName = named.Name()
		} else if approvalKind == "evaluator" {
			evaluatorName = fmt.Sprintf("%T", e.gov.approvalEvaluator)
		}
	}
	return approvalKind, evaluatorName
}
