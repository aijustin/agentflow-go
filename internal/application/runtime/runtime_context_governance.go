package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// contextSummaryTimeout bounds the summarizer's LLM call. Summarization is a
// side operation inside context preparation, not the run's actual work, so a
// slow or hung summarizer model must not be able to consume the whole run
// budget; it should time out quickly and fall back to the non-LLM summary
// instead of blocking every turn.
const contextSummaryTimeout = 10 * time.Second

func (e *Engine) contextManager(ctx context.Context, runID string, agent core.Agent, policy contextwindow.Policy) *contextwindow.Manager {
	normalized := policy.Normalize()
	if normalized.SummaryMode != "llm" || e.llm == nil {
		return contextwindow.New(normalized)
	}
	summarizerProfile := agent.LLM
	if summarizerProfile == "" {
		summarizerProfile = sortedLLMProfileName(e.scenario.LLMs)
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
		profile := summarizerProfile
		if profile == "" {
			return contextwindowSummaryFallback(messages, budget)
		}
		summaryCtx, cancel := context.WithTimeout(ctx, contextSummaryTimeout)
		defer cancel()
		resp, err := e.llm.Chat(summaryCtx, profile, llm.ChatRequest{
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

func sortedLLMProfileName(profiles map[string]core.LLMProfileRef) string {
	if len(profiles) == 0 {
		return ""
	}
	names := make([]string, 0, len(profiles))
	for name := range profiles {
		names = append(names, name)
	}
	slices.Sort(names)
	return names[0]
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
// pairingDrops counts what enforceToolCallPairing removed so callers can
// surface an EventContextIncomplete when truncation broke tool_call pairing.
type pairingDrops struct {
	orphanToolResults   int
	unansweredToolCalls int
}

func (d pairingDrops) any() bool {
	return d.orphanToolResults > 0 || d.unansweredToolCalls > 0
}

func enforceToolCallPairing(messages []llm.Message) []llm.Message {
	out, _ := enforceToolCallPairingWithStats(messages)
	return out
}

func enforceToolCallPairingWithStats(messages []llm.Message) ([]llm.Message, pairingDrops) {
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
	var drops pairingDrops
	out := make([]llm.Message, 0, len(messages))
	for _, msg := range messages {
		if msg.Role == llm.RoleTool && msg.ToolCallID != "" {
			if _, ok := issued[msg.ToolCallID]; !ok {
				drops.orphanToolResults++
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
				drops.unansweredToolCalls += len(msg.ToolCalls) - len(kept)
				msg.ToolCalls = kept
				if len(kept) == 0 && strings.TrimSpace(msg.Content) == "" {
					continue
				}
			}
		}
		out = append(out, msg)
	}
	return out, drops
}

type staleEvictionStats struct {
	DroppedToolTurns   int
	DenialOccupiedSlots int
	ExcludedTurns      int
}

func classifyToolResultMessage(msg llm.Message) contextwindow.ToolResultClass {
	if msg.Metadata != nil {
		switch msg.Metadata["tool_result_class"] {
		case string(contextwindow.ToolResultClassDenied):
			return contextwindow.ToolResultClassDenied
		case string(contextwindow.ToolResultClassEmpty):
			return contextwindow.ToolResultClassEmpty
		case string(contextwindow.ToolResultClassSuccess):
			return contextwindow.ToolResultClassSuccess
		}
	}
	content := strings.TrimSpace(msg.Content)
	if content == "" || content == "{}" || content == "null" || content == `""` {
		return contextwindow.ToolResultClassEmpty
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(content), &parsed); err == nil {
		if errText, ok := parsed["error"].(string); ok && strings.TrimSpace(errText) != "" {
			return contextwindow.ToolResultClassDenied
		}
		if output, ok := parsed["output"]; ok {
			switch v := output.(type) {
			case nil:
				return contextwindow.ToolResultClassEmpty
			case string:
				if strings.TrimSpace(v) == "" {
					return contextwindow.ToolResultClassEmpty
				}
			case map[string]any:
				if len(v) == 0 {
					return contextwindow.ToolResultClassEmpty
				}
			}
		}
		// Structured ToolResult JSON without an error is treated as success.
		if _, hasTool := parsed["tool"]; hasTool {
			return contextwindow.ToolResultClassSuccess
		}
		if _, hasOutput := parsed["output"]; hasOutput {
			return contextwindow.ToolResultClassSuccess
		}
	}
	lower := strings.ToLower(content)
	if strings.Contains(lower, "run_tool_budget_exceeded") ||
		strings.Contains(lower, "tool_denied") ||
		strings.Contains(lower, "rate cap exceeded") {
		return contextwindow.ToolResultClassDenied
	}
	return contextwindow.ToolResultClassSuccess
}

func staleClassExcluded(class contextwindow.ToolResultClass, exclude []contextwindow.ToolResultClass) bool {
	return slices.Contains(exclude, class)
}

func evictStaleToolMessages(messages []llm.Message, keepTurns int) []llm.Message {
	out, _ := evictStaleToolMessagesWithPolicy(messages, keepTurns, nil)
	return out
}

func evictStaleToolMessagesWithPolicy(messages []llm.Message, keepTurns int, exclude []contextwindow.ToolResultClass) ([]llm.Message, staleEvictionStats) {
	var stats staleEvictionStats
	if keepTurns <= 0 || len(messages) == 0 {
		return messages, stats
	}
	if exclude == nil {
		exclude = contextwindow.Policy{}.ExcludeFromStaleWindowOrDefault()
	}
	type toolSlot struct {
		index   int
		class   contextwindow.ToolResultClass
		counted bool
	}
	slots := make([]toolSlot, 0)
	for index, msg := range messages {
		if msg.Role != llm.RoleTool {
			continue
		}
		class := classifyToolResultMessage(msg)
		counted := !staleClassExcluded(class, exclude)
		if !counted {
			stats.ExcludedTurns++
			if class == contextwindow.ToolResultClassDenied {
				stats.DenialOccupiedSlots++
			}
		}
		slots = append(slots, toolSlot{index: index, class: class, counted: counted})
	}
	counted := 0
	for _, slot := range slots {
		if slot.counted {
			counted++
		}
	}
	if counted <= keepTurns {
		return messages, stats
	}
	// Keep the newest keepTurns counted (success) tool results; older counted
	// results are dropped. Excluded (denied/empty) messages that sit between
	// kept successes remain so pairing stays intact for recent turns, but
	// excluded messages older than the oldest kept success are also dropped.
	keepCountedFrom := 0
	seenCounted := 0
	for i := len(slots) - 1; i >= 0; i-- {
		if !slots[i].counted {
			continue
		}
		seenCounted++
		if seenCounted == keepTurns {
			keepCountedFrom = slots[i].index
			break
		}
	}
	dropped := make(map[string]struct{})
	droppedUnidentified := false
	dropIndex := make(map[int]struct{})
	for _, slot := range slots {
		if slot.index >= keepCountedFrom {
			continue
		}
		// Drop older counted successes, and also older excluded messages so
		// they cannot linger ahead of the retained success window.
		dropIndex[slot.index] = struct{}{}
		if slot.counted {
			stats.DroppedToolTurns++
		}
		msg := messages[slot.index]
		if msg.ToolCallID != "" {
			dropped[msg.ToolCallID] = struct{}{}
		} else {
			droppedUnidentified = true
		}
	}
	out := make([]llm.Message, 0, len(messages))
	for index, msg := range messages {
		if _, drop := dropIndex[index]; drop {
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
	return out, stats
}
