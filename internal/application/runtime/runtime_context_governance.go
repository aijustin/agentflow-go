package runtime

import (
	"context"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

func (e *Engine) contextManager(ctx context.Context, policy contextwindow.Policy) *contextwindow.Manager {
	normalized := policy.Normalize()
	if normalized.SummaryMode != "llm" || e.llm == nil {
		return contextwindow.New(normalized)
	}
	return contextwindow.NewWithSummarizer(normalized, func(messages []contextwindow.Message, budget int) string {
		if len(messages) == 0 {
			return ""
		}
		var b strings.Builder
		for _, msg := range messages {
			b.WriteString(string(msg.Role))
			b.WriteString(": ")
			b.WriteString(msg.Content)
			b.WriteByte('\n')
		}
		profile := firstLLMProfile(e.scenario.LLMs)
		if profile == "" {
			return contextwindowSummaryFallback(messages, budget)
		}
		resp, err := e.llm.Chat(ctx, profile, llm.ChatRequest{
			Messages: []llm.Message{
				{Role: llm.RoleSystem, Content: fmt.Sprintf("Summarize the following conversation in at most %d tokens worth of text.", budget)},
				{Role: llm.RoleUser, Content: b.String()},
			},
		})
		if err != nil || strings.TrimSpace(resp.Message.Content) == "" {
			return contextwindowSummaryFallback(messages, budget)
		}
		return strings.TrimSpace(resp.Message.Content)
	})
}

func firstLLMProfile(profiles map[string]core.LLMProfileRef) string {
	for name := range profiles {
		return name
	}
	return ""
}

func contextwindowSummaryFallback(messages []contextwindow.Message, budget int) string {
	var b strings.Builder
	b.WriteString("Earlier context summary:\n")
	for _, msg := range messages {
		line := fmt.Sprintf("- %s: %s\n", msg.Role, msg.Content)
		if contextwindow.EstimateTokens(b.String()+line) > budget {
			break
		}
		b.WriteString(line)
	}
	return strings.TrimSpace(b.String())
}

func pruneToolSpecs(specs []llm.ToolSpec, allowed map[string]struct{}) []llm.ToolSpec {
	if len(allowed) == 0 {
		return specs
	}
	out := make([]llm.ToolSpec, 0, len(specs))
	for _, spec := range specs {
		if _, ok := allowed[spec.Name]; ok {
			out = append(out, spec)
		}
	}
	return out
}

func evictStaleToolMessages(messages []llm.Message, keepTurns int) []llm.Message {
	if keepTurns <= 0 || len(messages) == 0 {
		return messages
	}
	toolIndices := make([]int, 0)
	for index, msg := range messages {
		if msg.Role == llm.RoleTool {
			toolIndices = append(toolIndices, index)
		}
	}
	if len(toolIndices) <= keepTurns {
		return messages
	}
	dropUntil := toolIndices[len(toolIndices)-keepTurns]
	// Track the tool_call IDs whose result messages are being evicted so the
	// matching assistant tool_calls can be removed too. Leaving an assistant
	// tool_call without its tool response breaks providers that require every
	// tool_call_id to be answered.
	dropped := make(map[string]struct{})
	for index, msg := range messages {
		if msg.Role == llm.RoleTool && index < dropUntil && msg.ToolCallID != "" {
			dropped[msg.ToolCallID] = struct{}{}
		}
	}
	out := make([]llm.Message, 0, len(messages))
	for index, msg := range messages {
		if msg.Role == llm.RoleTool && index < dropUntil {
			continue
		}
		if len(msg.ToolCalls) > 0 && len(dropped) > 0 {
			kept := make([]llm.ToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				if _, gone := dropped[call.ID]; gone {
					continue
				}
				kept = append(kept, call)
			}
			if len(kept) == 0 && strings.TrimSpace(msg.Content) == "" {
				continue
			}
			msg.ToolCalls = kept
		}
		out = append(out, msg)
	}
	return out
}
