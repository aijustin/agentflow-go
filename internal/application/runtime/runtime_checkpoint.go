package runtime

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type checkpointPauseOptions struct {
	outputMode string
}

func (e *Engine) pauseBeforeFinalAnswer(ctx context.Context, req RunRequest, agent core.Agent, snapshot *runstate.RunSnapshot, opts checkpointPauseOptions) (RunResult, error) {
	if e.gate == nil {
		return RunResult{}, fmt.Errorf("runtime: human gate required for configured checkpoint")
	}
	if delegationDepthFromContext(ctx) > 0 {
		return RunResult{}, fmt.Errorf("runtime: before_final_answer checkpoint is not supported inside delegated sub-agent calls")
	}
	checkpointVars := map[string]json.RawMessage{
		checkpointPromptVar:  json.RawMessage(fmt.Sprintf("%q", req.Prompt)),
		checkpointAgentVar:   json.RawMessage(fmt.Sprintf("%q", agent.Name)),
		checkpointContextVar: req.Context,
		checkpointKindVar:    json.RawMessage(`"before_final_answer"`),
	}
	if opts.outputMode != "" {
		checkpointVars[checkpointOutputModeVar] = json.RawMessage(fmt.Sprintf("%q", opts.outputMode))
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
		checkpointOutputModeVar,
		checkpointStepsConsumedVar,
		checkpointReplanAttemptsVar,
		checkpointWorkflowNodeVar,
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
