// Package toolinvoke holds shared tool-invocation preflight checks used by
// both the autonomous runtime tool loop and the workflow NodeTool path so
// approval, input validation, side-effect retry, and security resource shape
// cannot drift between the two stacks.
package toolinvoke

import (
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/security"
	"github.com/aijustin/agentflow-go/pkg/toolschema"
)

// AutoRetrySafe reports whether a tool may be automatically re-executed after
// a classified transient failure. write/external/dangerous tools are never
// auto-retried: a failed attempt may already have committed its side effect.
func AutoRetrySafe(tool core.Tool) bool {
	switch tool.SideEffect {
	case core.SideEffectWrite, core.SideEffectExternal, core.SideEffectDangerous:
		return false
	default:
		return true
	}
}

// ValidateInput enforces the tool's InputSchema when validation is enabled.
func ValidateInput(enabled bool, tool core.Tool, input json.RawMessage) error {
	if !enabled || len(tool.InputSchema) == 0 {
		return nil
	}
	if err := toolschema.Validate(tool.InputSchema, input); err != nil {
		return fmt.Errorf("invalid tool input: %w", err)
	}
	return nil
}

// DenialWithoutGate returns a non-empty reason when the tool must be denied
// (rather than paused). When gateConfigured is true and the policy is
// pause-required, this returns "" so the caller can pause for human approval.
// Callers that already obtained human approval should pass approved=true.
func DenialWithoutGate(tool core.Tool, gateConfigured, approved bool) string {
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
	return core.ToolApprovalDenialReason(tool)
}

// SecurityResource builds the security.Resource used for tool.invoke
// authorization. extra metadata (agent, node_id, ...) is copied in.
func SecurityResource(toolName string, tool core.Tool, extra map[string]string) security.Resource {
	meta := map[string]string{
		"tool_type":   tool.Type,
		"side_effect": string(tool.SideEffect),
	}
	for key, value := range extra {
		if value == "" {
			continue
		}
		meta[key] = value
	}
	return security.Resource{Type: "tool", ID: toolName, Metadata: meta}
}
