package contextwindow

import (
	"encoding/json"
	"strings"
)

const metadataToolResultClass = "tool_result_class"

// ClassifyToolResult classifies a tool-role message for stale-window and
// observation-mask accounting. Metadata tool_result_class wins when set.
func ClassifyToolResult(msg Message) ToolResultClass {
	if msg.Metadata != nil {
		switch msg.Metadata[metadataToolResultClass] {
		case string(ToolResultClassDenied):
			return ToolResultClassDenied
		case string(ToolResultClassEmpty):
			return ToolResultClassEmpty
		case string(ToolResultClassSuccess):
			return ToolResultClassSuccess
		}
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" || content == "{}" || content == "null" || content == `""` {
		return ToolResultClassEmpty
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err == nil {
		if errText, ok := parsed["error"].(string); ok && strings.TrimSpace(errText) != "" {
			return ToolResultClassDenied
		}
		if output, ok := parsed["output"]; ok {
			switch v := output.(type) {
			case nil:
				return ToolResultClassEmpty
			case string:
				if strings.TrimSpace(v) == "" {
					return ToolResultClassEmpty
				}
			case map[string]any:
				if len(v) == 0 {
					return ToolResultClassEmpty
				}
			}
		}
		if _, hasTool := parsed["tool"]; hasTool {
			return ToolResultClassSuccess
		}
		if _, hasOutput := parsed["output"]; hasOutput {
			return ToolResultClassSuccess
		}
	}
	lower := strings.ToLower(content)
	if strings.Contains(lower, "run_tool_budget_exceeded") ||
		strings.Contains(lower, "run_tool_loop_guard") ||
		strings.Contains(lower, "tool invocation blocked by governance") ||
		strings.Contains(lower, "tool_denied") ||
		strings.Contains(lower, "rate cap exceeded") {
		return ToolResultClassDenied
	}
	return ToolResultClassSuccess
}
