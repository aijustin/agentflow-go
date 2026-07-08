package orchestration

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const (
	workflowCheckpointKindVar      = "checkpoint_kind"
	workflowCheckpointNodeVar      = "checkpoint_workflow_node"
	workflowCheckpointToolInputVar = "checkpoint_tool_input"
	workflowCheckpointToolRefVar   = "checkpoint_tool_ref"
	workflowToolApprovalKind       = "workflow_tool_approval"
	workflowToolCallCountsVar      = "workflow_tool_call_counts"
)

func (r *WorkflowRunner) workflowToolApprovalInput(ctx context.Context, runID, nodeID string) (json.RawMessage, bool, error) {
	if r.runs == nil {
		return nil, false, nil
	}
	snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
	if err != nil {
		return nil, false, err
	}
	if snapshot.Variables == nil {
		return nil, false, nil
	}
	if variableString(snapshot.Variables, workflowCheckpointKindVar) != workflowToolApprovalKind {
		return nil, false, nil
	}
	storedID := storageNodeID(ctx, nodeID)
	if variableString(snapshot.Variables, workflowCheckpointNodeVar) != storedID {
		return nil, false, nil
	}
	raw := snapshot.Variables[workflowCheckpointToolInputVar]
	if len(raw) == 0 {
		return nil, false, fmt.Errorf("orchestration: workflow tool approval for node %q is missing input", nodeID)
	}
	return raw, true, nil
}

func (r *WorkflowRunner) clearWorkflowToolApprovalCheckpoint(ctx context.Context, runID string) error {
	return r.saveSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Variables == nil {
			return nil
		}
		delete(snapshot.Variables, workflowCheckpointKindVar)
		delete(snapshot.Variables, workflowCheckpointNodeVar)
		delete(snapshot.Variables, workflowCheckpointToolInputVar)
		delete(snapshot.Variables, workflowCheckpointToolRefVar)
		return nil
	})
}

func (r *WorkflowRunner) pauseForWorkflowToolApproval(ctx context.Context, scenario core.Scenario, node core.WorkflowNode, runID string, tool core.Tool, input json.RawMessage) error {
	if r.gate == nil {
		return fmt.Errorf("orchestration: human gate is required for tool %q pause approval", tool.Name)
	}
	if r.runs == nil {
		return fmt.Errorf("orchestration: run-state repository is required for tool approval pause")
	}
	storedID := storageNodeID(ctx, node.ID)
	var payload json.RawMessage
	prepare := func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		snapshot.Variables[workflowCheckpointKindVar] = json.RawMessage(fmt.Sprintf("%q", workflowToolApprovalKind))
		snapshot.Variables[workflowCheckpointNodeVar] = json.RawMessage(fmt.Sprintf("%q", storedID))
		snapshot.Variables[workflowCheckpointToolInputVar] = input
		snapshot.Variables[workflowCheckpointToolRefVar] = json.RawMessage(fmt.Sprintf("%q", tool.Name))
		snapshot.CurrentNodeID = storedID
		payloadMap := map[string]any{
			"node_id": node.ID,
			"tool":    tool.Name,
			"kind":    workflowToolApprovalKind,
		}
		raw, err := json.Marshal(payloadMap)
		if err != nil {
			return err
		}
		payload = raw
		return nil
	}
	token, err := r.pauseWithRetry(ctx, runID, prepare, func(version int64) core.CheckpointState {
		return core.CheckpointState{RunID: runID, Version: version, NodeID: node.ID, Payload: payload}
	})
	if err != nil {
		return err
	}
	r.emitJSON(ctx, core.EventRunPaused, scenario.Name, runID, map[string]any{
		"node_id": node.ID,
		"tool":    tool.Name,
		"kind":    workflowToolApprovalKind,
	})
	return WorkflowPausedError{RunID: runID, NodeID: node.ID, Token: token}
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
		return string(raw)
	}
	return value
}

// toolCallCount returns how many successful executions of toolRef have been
// recorded for this run (used for RateCap). Counts persist across pauses so
// resume cannot reset a per-run budget.
func (r *WorkflowRunner) toolCallCount(ctx context.Context, runID, toolRef string) (int, error) {
	if r.runs == nil {
		return 0, nil
	}
	snapshot, err := runstate.LoadAuthorized(ctx, r.runs, runID)
	if err != nil {
		return 0, err
	}
	counts, err := decodeToolCallCounts(snapshot.Variables)
	if err != nil {
		return 0, err
	}
	return counts[toolRef], nil
}

func (r *WorkflowRunner) incrementToolCallCount(ctx context.Context, runID, toolRef string) error {
	if r.runs == nil {
		return nil
	}
	return r.saveSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		counts, err := decodeToolCallCounts(snapshot.Variables)
		if err != nil {
			return err
		}
		if counts == nil {
			counts = map[string]int{}
		}
		counts[toolRef]++
		raw, err := json.Marshal(counts)
		if err != nil {
			return err
		}
		snapshot.Variables[workflowToolCallCountsVar] = raw
		return nil
	})
}

func decodeToolCallCounts(vars map[string]json.RawMessage) (map[string]int, error) {
	if vars == nil {
		return nil, nil
	}
	raw, ok := vars[workflowToolCallCountsVar]
	if !ok || len(raw) == 0 {
		return nil, nil
	}
	var counts map[string]int
	if err := json.Unmarshal(raw, &counts); err != nil {
		return nil, fmt.Errorf("orchestration: decode tool call counts: %w", err)
	}
	return counts, nil
}
