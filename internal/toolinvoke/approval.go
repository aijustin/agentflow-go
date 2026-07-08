package toolinvoke

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// EvaluatePauseRequired reports whether a tool call should pause for human
// approval when a human gate is configured. Static scenario policies are
// checked first; when they do not require a pause, an optional evaluator may
// still require one (for example dynamic MCP invoke targets).
func EvaluatePauseRequired(ctx context.Context, tool core.Tool, evaluator core.ToolApprovalEvaluator, runID string, call llm.ToolCall) (bool, error) {
	if core.ToolApprovalPauseRequired(tool) {
		return true, nil
	}
	if evaluator == nil {
		return false, nil
	}
	return evaluator.PauseRequired(ctx, runID, tool, call)
}

// DenialWithoutGateWithEvaluator extends DenialWithoutGate with dynamic pause
// evaluation for tools whose static policy is ApprovalNever.
func DenialWithoutGateWithEvaluator(ctx context.Context, tool core.Tool, gateConfigured, approved bool, evaluator core.ToolApprovalEvaluator, runID string, call llm.ToolCall) string {
	if approved {
		return ""
	}
	if core.ToolApprovalPauseRequired(tool) {
		if gateConfigured {
			return ""
		}
		if reason := core.ToolApprovalDenialReason(tool); reason != "" {
			return reason
		}
		return "tool requires human gate for pause approval"
	}
	pauseRequired, err := EvaluatePauseRequired(ctx, tool, evaluator, runID, call)
	if err != nil {
		return err.Error()
	}
	if pauseRequired {
		if gateConfigured {
			return ""
		}
		return "tool requires human gate for pause approval"
	}
	return core.ToolApprovalDenialReason(tool)
}
