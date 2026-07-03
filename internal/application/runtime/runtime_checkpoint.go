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
	payload := []byte(fmt.Sprintf(`{"prompt":%q,"agent":%q}`, req.Prompt, agent.Name))
	token, err := e.pauseWithRetry(ctx, req.RunID, func(version int64) core.CheckpointState {
		return core.CheckpointState{RunID: req.RunID, Version: version, NodeID: "before_final_answer", Payload: payload}
	})
	if err != nil {
		return RunResult{}, err
	}
	if err := e.ensureRunPaused(ctx, req.RunID); err != nil {
		e.logWarn(ctx, "runtime: failed to persist paused status", "run_id", req.RunID, "error", err)
	}
	e.emit(ctx, core.EventRunPaused, req.RunID, payload)
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
