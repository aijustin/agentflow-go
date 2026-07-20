package agentflow

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func (f *Framework) hydrateRunRequest(ctx context.Context, req RunRequest, snapshot runstate.RunSnapshot) (RunRequest, error) {
	if req.TrustMode == "" {
		req.TrustMode = TrustMode(variableJSONString(snapshot.Variables, resumeTrustModeVar))
	}
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

func (f *Framework) workflowRunOutput(ctx context.Context, snapshot runstate.RunSnapshot) (string, error) {
	if ref, ok := snapshot.StepOutputs["final"]; ok {
		raw, err := runstate.LoadStepOutput(ctx, f.blobs, ref)
		if err != nil {
			if f.logger != nil {
				f.logger.Warn(ctx, "agentflow: load final step output failed", "run_id", snapshot.RunID, "error", err)
			}
			return "", fmt.Errorf("agentflow: load final step output: %w", err)
		}
		return string(raw), nil
	}
	if len(snapshot.StepOutputs) == 0 {
		return "", nil
	}
	raw, err := runstate.HydrateStepContext(ctx, f.blobs, snapshot.StepOutputs)
	if err != nil {
		if f.logger != nil {
			f.logger.Warn(ctx, "agentflow: hydrate step outputs failed", "run_id", snapshot.RunID, "error", err)
		}
		return "", fmt.Errorf("agentflow: hydrate step outputs: %w", err)
	}
	return string(raw), nil
}

func completedHybridResult(ctx context.Context, f *Framework, snapshot runstate.RunSnapshot) (RunResult, bool, error) {
	if snapshot.Status != runstate.RunStatusCompleted {
		return RunResult{}, false, nil
	}
	output, err := f.workflowRunOutput(ctx, snapshot)
	if err != nil {
		return RunResult{}, false, err
	}
	result := RunResult{RunID: snapshot.RunID, Status: runstate.RunStatusCompleted, Output: output}
	if ref, ok := snapshot.StepOutputs["final"]; ok {
		raw, loadErr := runstate.LoadStepOutput(ctx, f.blobs, ref)
		if loadErr != nil {
			return RunResult{}, false, fmt.Errorf("agentflow: load final structured output: %w", loadErr)
		}
		result.StructuredOutput = raw
	}
	return result, true, nil
}

func isEmptyOrNullJSON(raw json.RawMessage) bool {
	if len(raw) == 0 {
		return true
	}
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// workflowContainsAgentNode reports whether the scenario's primary workflow
// (or any named subgraph) includes an agent node. Used to reject
// RunStructured/Stream on fixed_workflow graphs that would otherwise
// complete the workflow (running agents) and then re-execute the agent.
func workflowContainsAgentNode(scenario core.Scenario) bool {
	if containsAgentNode(scenario.Orchestration.Workflow) {
		return true
	}
	for _, wf := range scenario.Orchestration.Workflows {
		if containsAgentNode(&wf) {
			return true
		}
	}
	return false
}

func containsAgentNode(workflow *core.Workflow) bool {
	if workflow == nil {
		return false
	}
	for _, node := range workflow.Nodes {
		if node.Kind == core.NodeAgent || node.Kind == core.NodeParallelGroup || node.Kind == core.NodeSupervisor {
			return true
		}
	}
	return false
}
