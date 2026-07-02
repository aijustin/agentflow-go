package openai

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func normalizeContentToolCalls(resp llm.ToolCallResponse, tools []llm.ToolSpec) llm.ToolCallResponse {
	if len(tools) == 0 || len(resp.ToolCalls) > 0 {
		return resp
	}
	content := strings.TrimSpace(resp.Message.Content)
	if content == "" {
		return resp
	}
	allowed := toolNameSet(tools)
	parsed, ok := parseContentToolCalls(content, allowed)
	if !ok {
		return resp
	}
	resp.Message.ToolCalls = parsed
	resp.Message.Content = ""
	resp.ToolCalls = append([]llm.ToolCall(nil), parsed...)
	if resp.FinishReason == "" || resp.FinishReason == "stop" {
		resp.FinishReason = "tool_calls"
	}
	return resp
}

func toolNameSet(tools []llm.ToolSpec) map[string]struct{} {
	out := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			out[name] = struct{}{}
		}
	}
	return out
}

func parseContentToolCalls(content string, allowed map[string]struct{}) ([]llm.ToolCall, bool) {
	content = stripCodeFence(content)
	if content == "" {
		return nil, false
	}
	var array []contentToolCall
	if err := json.Unmarshal([]byte(content), &array); err == nil && len(array) > 0 {
		return contentToolCallsToLLM(array, allowed)
	}
	var single contentToolCall
	if err := json.Unmarshal([]byte(content), &single); err == nil && strings.TrimSpace(single.Name) != "" {
		return contentToolCallsToLLM([]contentToolCall{single}, allowed)
	}
	return nil, false
}

type contentToolCall struct {
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

func contentToolCallsToLLM(calls []contentToolCall, allowed map[string]struct{}) ([]llm.ToolCall, bool) {
	out := make([]llm.ToolCall, 0, len(calls))
	for index, call := range calls {
		name := strings.TrimSpace(call.Name)
		if name == "" {
			continue
		}
		if _, ok := allowed[name]; !ok {
			return nil, false
		}
		out = append(out, llm.ToolCall{
			ID:    fmt.Sprintf("content-tool-call-%d", index),
			Name:  name,
			Input: normalizeToolArguments(call.Arguments),
		})
	}
	if len(out) == 0 {
		return nil, false
	}
	return out, true
}

func stripCodeFence(content string) string {
	content = strings.TrimSpace(content)
	if !strings.HasPrefix(content, "```") {
		return content
	}
	content = strings.TrimPrefix(content, "```")
	if idx := strings.Index(content, "\n"); idx >= 0 {
		content = content[idx+1:]
	}
	if idx := strings.LastIndex(content, "```"); idx >= 0 {
		content = content[:idx]
	}
	return strings.TrimSpace(content)
}
