package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
	// checkpointResumedVar is deprecated; reads accept it for backward compatibility.
	checkpointResumedVar        = "checkpoint_resumed"
	checkpointToolCallsVar      = "checkpoint_tool_calls"
	checkpointMessagesVar       = "checkpoint_messages"
	checkpointToolCountsVar     = "checkpoint_tool_counts"
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
		snapshot.Variables[resumeTriggerKindVar] = json.RawMessage(fmt.Sprintf("%q", triggerKind))
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
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
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
		toolorch.RememberAllow(e.approvalStore, runID, approved.Name, approved.Input)
		if e.denyBreaker != nil {
			e.denyBreaker.RecordAllow(runID)
		}
		contextResult, _ := e.materializeToolResultForContext(approved.Name, result, profile)
		raw, err := json.Marshal(contextResult)
		if err != nil {
			return "", err
		}
		messages = append(messages, llm.Message{
			Role:       llm.RoleTool,
			Content:    string(raw),
			Name:       approved.Name,
			ToolCallID: approved.ID,
		})
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

func (e *Engine) completeRun(ctx context.Context, runID, output string) (RunResult, error) {
	finalRaw, err := json.Marshal(map[string]string{"text": output})
	if err != nil {
		return RunResult{}, fmt.Errorf("runtime: marshal final output: %w", err)
	}
	if _, err := e.persistRunCompleted(ctx, runID, finalRaw); err != nil {
		var conflict completionConflictError
		if errors.As(err, &conflict) {
			return nonRunningCompletionResult(runID, conflict.status)
		}
		// The run produced its answer but the terminal state could not be
		// persisted; mark it Failed so it does not linger in Running forever.
		e.markRunFailedOrCancelled(ctx, runID, err)
		return RunResult{}, err
	}
	return RunResult{RunID: runID, Status: runstate.RunStatusCompleted, Output: output}, nil
}

func applyHumanAmendment(vars map[string]json.RawMessage, prompt string) string {
	if vars == nil {
		return prompt
	}
	raw, ok := vars[humanAmendmentVar]
	if !ok || len(raw) == 0 {
		return prompt
	}
	amendment := decodeAmendmentText(raw)
	if amendment == "" {
		return prompt
	}
	if strings.TrimSpace(prompt) == "" {
		return amendment
	}
	return prompt + "\n\nHuman feedback: " + amendment
}

func humanAmendmentText(vars map[string]json.RawMessage) string {
	if vars == nil {
		return ""
	}
	raw, ok := vars[humanAmendmentVar]
	if !ok || len(raw) == 0 {
		return ""
	}
	return decodeAmendmentText(raw)
}

func decodeAmendmentText(raw json.RawMessage) string {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return strings.TrimSpace(value)
}

func clearHumanAmendment(snapshot *runstate.RunSnapshot) {
	if snapshot == nil || snapshot.Variables == nil {
		return
	}
	delete(snapshot.Variables, humanAmendmentVar)
}

// clearCheckpointOnCompletionConflict drops leftover checkpoint variables
// when the completion save lost the race to a concurrent writer that moved
// the run out of Running (completionConflictError). Without the cleanup the
// unconsumed checkpoint would survive into the winner's chosen terminal
// state and look resumable. A Paused winner keeps its metadata: the new
// pause owns the checkpoint vars and clearing them would break its resume.
func (e *Engine) clearCheckpointOnCompletionConflict(ctx context.Context, snapshot *runstate.RunSnapshot, err error) {
	var conflict completionConflictError
	if !errors.As(err, &conflict) || conflict.status == runstate.RunStatusPaused {
		return
	}
	if clearErr := e.saveSnapshotWithRetry(ctx, snapshot.RunID, func(loaded *runstate.RunSnapshot) error {
		if loaded.Variables == nil {
			return nil
		}
		clearHumanAmendment(loaded)
		clearCheckpointVariables(loaded.Variables)
		return nil
	}); clearErr != nil {
		e.logWarn(ctx, "runtime: failed to clear checkpoint variables after completion conflict", "run_id", snapshot.RunID, "error", clearErr)
	}
}

func checkpointStepsConsumed(vars map[string]json.RawMessage) int {
	return checkpointIntVar(vars, checkpointStepsConsumedVar)
}

func checkpointIntVar(vars map[string]json.RawMessage, key string) int {
	if vars == nil {
		return 0
	}
	raw, ok := vars[key]
	if !ok || len(raw) == 0 {
		return 0
	}
	var value int
	if err := json.Unmarshal(raw, &value); err != nil {
		return 0
	}
	return value
}

func variableString(vars map[string]json.RawMessage, key string) string {
	if vars == nil {
		return ""
	}
	raw, ok := vars[key]
	if !ok {
		return ""
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return strings.TrimSpace(string(raw))
	}
	return value
}

// externalizeCheckpointVar returns the payload to persist for a potentially
// large checkpoint variable: the raw JSON itself when it fits within the
// step-output threshold, otherwise a StepOutputRef pointing at a blob.
func (e *Engine) externalizeCheckpointVar(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	threshold := e.scenario.Runtime.StepOutputThreshold
	if threshold <= 0 || int64(len(raw)) <= threshold || e.blobs == nil {
		return raw, nil
	}
	ref, err := e.blobs.Put(ctx, raw)
	if err != nil {
		return nil, err
	}
	return json.Marshal(runstate.StepOutputRef{Blob: &ref})
}

// resolveCheckpointVar resolves a checkpoint variable persisted by
// externalizeCheckpointVar back to its raw payload. Inline payloads (legacy
// JSON arrays or values below the threshold) pass through unchanged.
func (e *Engine) resolveCheckpointVar(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return raw, nil
	}
	var ref runstate.StepOutputRef
	if err := json.Unmarshal(trimmed, &ref); err != nil || ref.Blob == nil {
		return raw, nil
	}
	if e.blobs == nil {
		return nil, fmt.Errorf("runtime: blob store is required to resolve externalized checkpoint state")
	}
	return e.blobs.Get(ctx, *ref.Blob)
}

func (e *Engine) isBeforeFinalResumed(snapshot runstate.RunSnapshot) bool {
	if checkpointBoolVar(snapshot.Variables, beforeFinalResumedVar) {
		return true
	}
	// Accept the legacy global flag written by older releases.
	return checkpointBoolVar(snapshot.Variables, checkpointResumedVar)
}

func checkpointBoolVar(vars map[string]json.RawMessage, key string) bool {
	if vars == nil {
		return false
	}
	raw, ok := vars[key]
	if !ok || len(raw) == 0 {
		return false
	}
	var value bool
	if err := json.Unmarshal(raw, &value); err != nil {
		return false
	}
	return value
}

// ClearCheckpointState removes any pending checkpoint metadata (and a stored
// human amendment) for a run without resuming execution. It is used when a run
// is resumed/approved but the caller chose not to continue execution, so a
// later Run() must not re-enter or act on the now-consumed checkpoint.
func (e *Engine) ClearCheckpointState(ctx context.Context, runID string) error {
	return e.saveSnapshotWithRetry(ctx, runID, func(loaded *runstate.RunSnapshot) error {
		if loaded.Variables == nil {
			return nil
		}
		clearHumanAmendment(loaded)
		clearCheckpointVariables(loaded.Variables)
		return nil
	})
}

// ClearOrphanedCheckpointState removes tool_approval checkpoint metadata when
// the serialized conversation was already consumed and cannot be resumed.
func ClearOrphanedCheckpointState(snapshot *runstate.RunSnapshot) {
	if snapshot == nil || snapshot.Variables == nil {
		return
	}
	if variableString(snapshot.Variables, checkpointKindVar) != "tool_approval" {
		return
	}
	if len(snapshot.Variables[checkpointMessagesVar]) > 0 {
		return
	}
	clearCheckpointVariables(snapshot.Variables)
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
	return errors.Is(err, ErrLLMGatewayRequired)
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
		if e.approvalEvaluator != nil {
			pauseRequired, err = e.approvalEvaluator.PauseRequired(ctx, runID, tool, call)
		}
	} else {
		pauseRequired, err = toolinvoke.EvaluatePauseRequired(ctx, tool, e.approvalEvaluator, runID, call)
	}
	if err != nil {
		return nil, err
	}
	if !pauseRequired {
		return nil, nil
	}
	if e.orchestrator != nil {
		decision, orchErr := e.orchestrator.DecideApproval(ctx, toolorch.ApprovalRequest{
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
	if e.gate == nil {
		return nil, nil
	}
	if delegationDepthFromContext(ctx) > 0 {
		// A delegated sub-agent shares the parent's run snapshot; pausing
		// here would overwrite the parent tool loop's own checkpoint state
		// (see dispatchSubAgent). Fail the tool call instead so the
		// delegation returns a clear error and the parent loop continues.
		return nil, fmt.Errorf("runtime: tool %q requires human approval, which is not supported for delegated sub-agent calls", call.Name)
	}
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
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
	vars := map[string]json.RawMessage{
		checkpointKindVar:           json.RawMessage(fmt.Sprintf("%q", checkpointKindToolApproval)),
		checkpointAgentVar:          json.RawMessage(fmt.Sprintf("%q", agent.Name)),
		checkpointPromptVar:         json.RawMessage(fmt.Sprintf("%q", prompt)),
		checkpointToolCallsVar:      toolCallsRaw,
		checkpointMessagesVar:       messagesRaw,
		checkpointToolCountsVar:     countsRaw,
		checkpointStepsConsumedVar:  json.RawMessage(fmt.Sprintf("%d", stepsConsumed)),
		checkpointReplanAttemptsVar: json.RawMessage(fmt.Sprintf("%d", replanAttempts)),
	}
	if nodeID := core.WorkflowNodeFromContext(ctx); nodeID != "" {
		vars[checkpointWorkflowNodeVar] = json.RawMessage(fmt.Sprintf("%q", nodeID))
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
	if e.approvalEvaluator != nil {
		if named, ok := e.approvalEvaluator.(core.NamedToolApprovalEvaluator); ok {
			evaluatorName = named.Name()
		} else if approvalKind == "evaluator" {
			evaluatorName = fmt.Sprintf("%T", e.approvalEvaluator)
		}
	}
	return approvalKind, evaluatorName
}
