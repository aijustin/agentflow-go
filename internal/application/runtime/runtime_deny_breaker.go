package runtime

import (
	"context"
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/toolorch"
)

func (e *Engine) noteApprovalDeny(ctx context.Context, runID, tool string) error {
	if e == nil || e.denyBreaker == nil {
		return nil
	}
	tripped, count := e.denyBreaker.RecordDeny(runID)
	if !tripped {
		return nil
	}
	e.emitJSON(ctx, core.EventHITLDenyBreakerTripped, runID, map[string]any{
		"tool":  tool,
		"count": count,
		"limit": e.denyBreaker.Limit(),
	})
	return toolorch.TripError(e.denyBreaker.Limit(), count)
}

// RememberHITLReject records a human rejection against the approval cache and
// deny breaker. Used when ResumeAndContinue receives DecisionReject.
func (e *Engine) RememberHITLReject(ctx context.Context, runID string) {
	if e == nil || runID == "" {
		return
	}
	snapshot, err := e.runs.Load(ctx, runID)
	if err == nil {
		var pending []struct {
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		}
		// ToolCall JSON uses Name/Input fields matching llm.ToolCall.
		if raw := snapshot.Variables[checkpointToolCallsVar]; len(raw) > 0 {
			_ = json.Unmarshal(raw, &pending)
			if len(pending) > 0 {
				toolorch.RememberDeny(e.approvalStore, runID, pending[0].Name, pending[0].Input)
				_ = e.noteApprovalDeny(ctx, runID, pending[0].Name)
				return
			}
		}
	}
	_ = e.noteApprovalDeny(ctx, runID, "")
}
