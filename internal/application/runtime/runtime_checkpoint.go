package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

type checkpointPauseOptions struct {
	outputMode string
}

func (e *Engine) pauseBeforeFinalAnswer(ctx context.Context, req RunRequest, agent core.Agent, snapshot *runstate.RunSnapshot, opts checkpointPauseOptions) (RunResult, error) {
	if e.coord.gate == nil {
		return RunResult{}, fmt.Errorf("runtime: human gate required for configured checkpoint")
	}
	if delegationDepthFromContext(ctx) > 0 {
		return RunResult{}, fmt.Errorf("runtime: before_final_answer checkpoint is not supported inside delegated sub-agent calls")
	}
	checkpointVars := map[string]json.RawMessage{
		checkpointPromptVar:  jsonStringValue(req.Prompt),
		checkpointAgentVar:   jsonStringValue(agent.Name),
		checkpointContextVar: req.Context,
		checkpointKindVar:    json.RawMessage(`"before_final_answer"`),
		// Same contract as the tool-approval pause: set until a confirmed
		// approval resumes the run (see checkpointPendingPauseVar).
		checkpointPendingPauseVar: json.RawMessage(`true`),
	}
	if opts.outputMode != "" {
		checkpointVars[checkpointOutputModeVar] = jsonStringValue(opts.outputMode)
	}
	if err := e.saveCheckpointVariables(ctx, snapshot, checkpointVars); err != nil {
		return RunResult{}, err
	}
	pausePayload := map[string]any{
		"prompt":         req.Prompt,
		"agent":          agent.Name,
		"pause_required": true,
		"pause_reason":   "before_final_answer",
		"approval_kind":  "checkpoint",
	}
	if req.TrustMode != "" {
		pausePayload["trust_mode"] = string(req.TrustMode)
	} else if trust := string(TrustModeFromContext(ctx)); trust != "" {
		pausePayload["trust_mode"] = trust
	}
	payload, err := json.Marshal(pausePayload)
	if err != nil {
		return RunResult{}, err
	}
	token, err := e.pauseWithRetry(ctx, req.RunID, func(version int64) core.CheckpointState {
		return core.CheckpointState{RunID: req.RunID, Version: version, NodeID: "before_final_answer", Payload: payload}
	})
	if err != nil {
		// The gate never moved the run to Paused, so the checkpoint metadata
		// we just wrote would otherwise leave the run Running with a
		// consumable checkpoint. Roll it back so no resume path can act on a
		// pause that did not happen.
		if clearErr := e.clearCheckpointState(ctx, snapshot, ""); clearErr != nil {
			e.logWarn(ctx, "runtime: failed to roll back checkpoint variables after pause failure", "run_id", req.RunID, "error", clearErr)
		}
		return RunResult{}, err
	}
	if err := e.ensureRunPaused(ctx, req.RunID); err != nil {
		return RunResult{}, fmt.Errorf("runtime: persist paused status for run %q: %w", req.RunID, err)
	}
	e.emitJSON(ctx, core.EventRunPaused, req.RunID, pausePayload)
	return RunResult{RunID: req.RunID, Status: runstate.RunStatusPaused, Token: token}, nil
}

func (e *Engine) saveCheckpointVariables(ctx context.Context, snapshot *runstate.RunSnapshot, values map[string]json.RawMessage) error {
	if snapshot.Variables == nil {
		snapshot.Variables = make(map[string]json.RawMessage)
	}
	for key, value := range values {
		snapshot.Variables[key] = value
	}
	return e.saveSnapshotWithRetry(ctx, snapshot.RunID, func(loaded *runstate.RunSnapshot) error {
		if loaded.Variables == nil {
			loaded.Variables = make(map[string]json.RawMessage)
		}
		for key, value := range values {
			loaded.Variables[key] = value
		}
		return nil
	})
}

func checkpointVariableKeys() []string {
	return []string{
		checkpointKindVar,
		checkpointPromptVar,
		checkpointAgentVar,
		checkpointContextVar,
		checkpointToolCallsVar,
		checkpointMessagesVar,
		checkpointToolCountsVar,
		checkpointUsageVar,
		checkpointApprovalsVar,
		checkpointDenyCountVar,
		checkpointOutputModeVar,
		checkpointStepsConsumedVar,
		checkpointReplanAttemptsVar,
		checkpointWorkflowNodeVar,
		checkpointPendingPauseVar,
	}
}

func clearCheckpointVariables(vars map[string]json.RawMessage) {
	if vars == nil {
		return
	}
	for _, key := range checkpointVariableKeys() {
		delete(vars, key)
	}
	delete(vars, checkpointResumedVar)
}

func (e *Engine) clearCheckpointState(ctx context.Context, snapshot *runstate.RunSnapshot, kind string) error {
	return e.saveSnapshotWithRetry(ctx, snapshot.RunID, func(loaded *runstate.RunSnapshot) error {
		if loaded.Variables == nil {
			loaded.Variables = make(map[string]json.RawMessage)
		}
		clearHumanAmendment(loaded)
		clearCheckpointVariables(loaded.Variables)
		if kind == "before_final_answer" {
			loaded.Variables[beforeFinalResumedVar] = json.RawMessage(`true`)
		}
		return nil
	})
}

// clearPendingPauseMarker removes the pending-pause marker once the gate has
// confirmed the pause (or, on the framework resume paths, once the gate has
// confirmed the approval). It deliberately keeps every other checkpoint
// variable intact.
func (e *Engine) clearPendingPauseMarker(ctx context.Context, runID string) error {
	return e.saveSnapshotWithRetry(ctx, runID, func(loaded *runstate.RunSnapshot) error {
		if loaded.Variables == nil {
			return nil
		}
		delete(loaded.Variables, checkpointPendingPauseVar)
		return nil
	})
}

// ClearPendingPauseMarker exposes the marker cleanup to the framework facade:
// a gate.Resume approval is definitive proof the pause happened, so a marker
// left behind by a failed post-pause cleanup must not block the approved
// continue (the engine refuses unconfirmed checkpoints fail-closed).
func (e *Engine) ClearPendingPauseMarker(ctx context.Context, runID string) error {
	return e.clearPendingPauseMarker(ctx, runID)
}

// ClearUnconfirmedCheckpoint drops checkpoint metadata when the snapshot still
// carries the pending-pause marker: the pause was never confirmed by the gate,
// so no human approved anything and the checkpoint must not be resumable.
// It reports whether it cleared the checkpoint. Used by the abandoned-run
// reaper so a crashed-between-checkpoint-and-pause run cannot be "retried"
// into executing unapproved tool calls.
func ClearUnconfirmedCheckpoint(snapshot *runstate.RunSnapshot) bool {
	if snapshot == nil || snapshot.Variables == nil {
		return false
	}
	if !checkpointBoolVar(snapshot.Variables, checkpointPendingPauseVar) {
		return false
	}
	clearHumanAmendment(snapshot)
	clearCheckpointVariables(snapshot.Variables)
	return true
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

// restoreApprovalCheckpointState imports the checkpointed approval-cache
// decisions and deny-breaker count so a resume on a fresh node keeps
// remembered allow/deny decisions (no repeated HITL prompts) and the
// consecutive-deny budget. This is regenerable, cache-like state: an import
// failure degrades to a warn log plus an empty cache (fail-open), matching
// the "store without RunStateExporter" degradation, instead of failing the
// run permanently. The worst case of an empty cache is a repeated HITL
// prompt, never a lost approval guarantee.
func (e *Engine) restoreApprovalCheckpointState(ctx context.Context, runID string, vars map[string]json.RawMessage) {
	if raw := vars[checkpointApprovalsVar]; len(raw) > 0 {
		if exporter, ok := e.tooling.approvalStore.(toolorch.RunStateExporter); ok {
			if err := exporter.ImportRun(runID, raw); err != nil {
				e.logWarn(ctx, "runtime: failed to import checkpoint approvals; resuming with an empty approval cache", "run_id", runID, "error", err)
			}
		}
	}
	if e.tooling.denyBreaker != nil {
		if count := checkpointIntVar(vars, checkpointDenyCountVar); count > 0 {
			e.tooling.denyBreaker.ImportRun(runID, count)
		}
	}
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
	if threshold <= 0 || int64(len(raw)) <= threshold || e.persist.blobs == nil {
		return raw, nil
	}
	ref, err := e.persist.blobs.Put(ctx, raw)
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
	if e.persist.blobs == nil {
		return nil, fmt.Errorf("runtime: blob store is required to resolve externalized checkpoint state")
	}
	return e.persist.blobs.Get(ctx, *ref.Blob)
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
