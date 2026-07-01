package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

func (e *Engine) contextManager(ctx context.Context, runID string, policy contextwindow.Policy) *contextwindow.Manager {
	normalized := policy.Normalize()
	if normalized.SummaryMode != "llm" || e.llm == nil {
		return contextwindow.New(normalized)
	}
	return contextwindow.NewWithSummarizer(normalized, func(messages []contextwindow.Message, budget int) string {
		if len(messages) == 0 {
			return ""
		}
		// The summarizer call goes out to whatever profile happens to be
		// configured first, which may be a different (e.g. cheaper, third
		// party) model than the one governing this conversation. Redact
		// each message the same way step outputs and stored memory are
		// redacted before it ever leaves the process, so summarization
		// cannot become a side channel that bypasses output governance.
		var b strings.Builder
		for _, msg := range messages {
			b.WriteString(string(msg.Role))
			b.WriteString(": ")
			b.WriteString(e.redactSummaryContent(ctx, runID, msg))
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

func (e *Engine) redactSummaryContent(ctx context.Context, runID string, msg contextwindow.Message) string {
	if e.redactor == nil || msg.Content == "" {
		return msg.Content
	}
	raw, err := json.Marshal(struct {
		Content string `json:"content"`
	}{Content: msg.Content})
	if err != nil {
		return msg.Content
	}
	redacted, err := e.redactor.RedactOutput(ctx, governance.OutputRedaction{
		RunID:  runID,
		StepID: "context_summary",
		Kind:   "context_summary." + string(msg.Role),
		Data:   raw,
	})
	if err != nil {
		return msg.Content
	}
	var out struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal(redacted, &out); err != nil {
		return msg.Content
	}
	return out.Content
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

// enforceToolCallPairing repairs the tool_call/tool_result contract after
// context-window truncation. contextwindow.Manager trims purely by token
// budget with no notion of tool_call_id, so it can keep a tool result while
// dropping the assistant message that issued the call (or the reverse),
// producing a message list most LLM providers reject outright. This removes
// any orphaned tool result and strips any tool_call left unanswered by a
// dropped result, so truncated history is always self-consistent.
func enforceToolCallPairing(messages []llm.Message) []llm.Message {
	issued := make(map[string]struct{})
	answered := make(map[string]struct{})
	for _, msg := range messages {
		for _, call := range msg.ToolCalls {
			issued[call.ID] = struct{}{}
		}
		if msg.Role == llm.RoleTool && msg.ToolCallID != "" {
			answered[msg.ToolCallID] = struct{}{}
		}
	}
	out := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == llm.RoleTool && msg.ToolCallID != "" {
			if _, ok := issued[msg.ToolCallID]; !ok {
				continue
			}
		}
		if len(msg.ToolCalls) > 0 {
			kept := make([]llm.ToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				if _, ok := answered[call.ID]; ok {
					kept = append(kept, call)
				}
			}
			if len(kept) != len(msg.ToolCalls) {
				msg.ToolCalls = kept
				if len(kept) == 0 && strings.TrimSpace(msg.Content) == "" {
					continue
				}
			}
		}
		out = append(out, msg)
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
	// tool_call_id to be answered. A tool message with no ToolCallID can't be
	// correlated to a specific call by ID, so droppedUnidentified is set
	// whenever one is evicted and every unidentified (empty-ID) tool_call is
	// conservatively stripped too, rather than risk leaving an unanswerable
	// call in the trimmed history.
	dropped := make(map[string]struct{})
	droppedUnidentified := false
	for index, msg := range messages {
		if msg.Role != llm.RoleTool || index >= dropUntil {
			continue
		}
		if msg.ToolCallID != "" {
			dropped[msg.ToolCallID] = struct{}{}
		} else {
			droppedUnidentified = true
		}
	}
	out := make([]llm.Message, 0, len(messages))
	for index, msg := range messages {
		if msg.Role == llm.RoleTool && index < dropUntil {
			continue
		}
		if len(msg.ToolCalls) > 0 && (len(dropped) > 0 || droppedUnidentified) {
			kept := make([]llm.ToolCall, 0, len(msg.ToolCalls))
			for _, call := range msg.ToolCalls {
				if call.ID == "" {
					if droppedUnidentified {
						continue
					}
				} else if _, gone := dropped[call.ID]; gone {
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
