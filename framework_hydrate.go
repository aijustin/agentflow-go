package agentflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func (f *Framework) hydrateRunRequest(ctx context.Context, req RunRequest, snapshot runstate.RunSnapshot) (RunRequest, error) {
	if len(snapshot.StepOutputs) == 0 {
		return req, nil
	}
	raw, err := runstate.HydrateStepContext(ctx, f.blobs, snapshot.StepOutputs)
	if err != nil {
		return req, fmt.Errorf("agentflow: hydrate workflow context: %w", err)
	}
	if isEmptyOrNullJSON(req.Context) {
		req.Context = raw
		return req, nil
	}
	merged, err := mergeWorkflowContext(req.Context, raw)
	if err != nil {
		return req, fmt.Errorf("agentflow: merge workflow context: %w", err)
	}
	req.Context = merged
	return req, nil
}

// mergeWorkflowContext merges hydrated workflow step outputs (shaped as
// {"steps":{...}}) into a caller-supplied context so the autonomous phase sees
// both the original input and the preceding workflow outputs. When the caller
// context is a JSON object, the workflow steps are attached under "steps"
// (falling back to "workflow_steps" if the caller already set "steps"). When
// the caller context is not an object (array/scalar/string), it is nested under
// "input" alongside the workflow steps.
func (f *Framework) hydrateRunRequestForRunID(ctx context.Context, req RunRequest) (RunRequest, error) {
	snapshot, err := runstate.LoadAuthorized(ctx, f.runs, req.RunID)
	if err != nil {
		return req, err
	}
	return f.hydrateRunRequest(ctx, req, snapshot)
}

func mergeWorkflowContext(userContext, hydrated json.RawMessage) (json.RawMessage, error) {
	if len(hydrated) == 0 {
		return userContext, nil
	}
	var hydratedObj map[string]json.RawMessage
	if err := json.Unmarshal(hydrated, &hydratedObj); err != nil {
		return nil, err
	}
	stepsValue, ok := hydratedObj["steps"]
	if !ok {
		return userContext, nil
	}
	var userObj map[string]json.RawMessage
	if err := json.Unmarshal(userContext, &userObj); err != nil || userObj == nil {
		userObj = map[string]json.RawMessage{"input": userContext}
	}
	if _, exists := userObj["steps"]; exists {
		userObj["workflow_steps"] = stepsValue
	} else {
		userObj["steps"] = stepsValue
	}
	return json.Marshal(userObj)
}

func (f *Framework) workflowRunOutput(ctx context.Context, snapshot runstate.RunSnapshot) string {
	if ref, ok := snapshot.StepOutputs["final"]; ok && len(ref.Inline) > 0 {
		return string(ref.Inline)
	}
	if len(snapshot.StepOutputs) == 0 {
		return ""
	}
	raw, err := runstate.HydrateStepContext(ctx, f.blobs, snapshot.StepOutputs)
	if err != nil {
		return ""
	}
	return string(raw)
}

func completedHybridResult(ctx context.Context, f *Framework, snapshot runstate.RunSnapshot) (RunResult, bool) {
	if snapshot.Status != runstate.RunStatusCompleted {
		return RunResult{}, false
	}
	output := f.workflowRunOutput(ctx, snapshot)
	if ref, ok := snapshot.StepOutputs["final"]; ok && len(ref.Inline) > 0 {
		return RunResult{
			RunID:            snapshot.RunID,
			Status:           runstate.RunStatusCompleted,
			Output:           output,
			StructuredOutput: ref.Inline,
		}, true
	}
	return RunResult{RunID: snapshot.RunID, Status: runstate.RunStatusCompleted, Output: output}, true
}

func isEmptyOrNullJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
