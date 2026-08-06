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
	var opts []contextwindow.ManagerOption
	if e.dualVisibility {
		opts = append(opts, contextwindow.WithMarkInsteadOfDrop(true))
	}
	if normalized.SummaryMode != "llm" || e.llm == nil {
		return contextwindow.New(normalized, opts...)
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
	}, opts...)
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
	DroppedToolTurns    int
	DenialOccupiedSlots int
	ExcludedTurns       int
	CompactedDenials    int
}

func classifyToolResultMessage(msg llm.Message) contextwindow.ToolResultClass {
	return contextwindow.ClassifyToolResult(contextwindow.Message{
		Role:       contextwindow.RoleTool,
		Content:    msg.Content,
		Name:       msg.Name,
		ToolCallID: msg.ToolCallID,
		Metadata:   msg.Metadata,
	})
}

func staleClassExcluded(class contextwindow.ToolResultClass, exclude []contextwindow.ToolResultClass) bool {
	return slices.Contains(exclude, class)
}

func evictStaleToolMessages(messages []llm.Message, keepTurns int) []llm.Message {
	out, _ := evictStaleToolMessagesWithPolicy(messages, keepTurns, nil, nil)
	return out
}

type staleToolBatch struct {
	resultIndexes []int
	counted       bool
}

func denialContextSignature(msg llm.Message, callNames map[string]string) string {
	tool := strings.TrimSpace(msg.Name)
	if tool == "" {
		tool = strings.TrimSpace(callNames[msg.ToolCallID])
	}
	reason := strings.TrimSpace(msg.Content)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(reason), &parsed); err == nil {
		if value, ok := parsed["error"].(string); ok {
			reason = strings.TrimSpace(value)
		}
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(reason), " "))
	if index := strings.IndexByte(normalized, ':'); index >= 0 {
		code := strings.TrimSpace(normalized[:index])
		if strings.HasPrefix(code, "run_tool_") || code == "tool_denied" {
			normalized = code
		}
	}
	return tool + "|" + normalized
}

func toolNameExcludedFromStale(msg llm.Message, callNames map[string]string, excludeNames map[string]struct{}) bool {
	if len(excludeNames) == 0 {
		return false
	}
	tool := strings.TrimSpace(msg.Name)
	if tool == "" {
		tool = strings.TrimSpace(callNames[msg.ToolCallID])
	}
	if tool == "" {
		return false
	}
	_, ok := excludeNames[tool]
	return ok
}

func staleExcludeToolNameSet(names []string) map[string]struct{} {
	if len(names) == 0 {
		return nil
	}
	out := make(map[string]struct{}, len(names))
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name != "" {
			out[name] = struct{}{}
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func evictStaleToolMessagesWithPolicy(
	messages []llm.Message,
	keepTurns int,
	exclude []contextwindow.ToolResultClass,
	excludeToolNames []string,
) ([]llm.Message, staleEvictionStats) {
	var stats staleEvictionStats
	if keepTurns <= 0 || len(messages) == 0 {
		return messages, stats
	}
	if exclude == nil {
		exclude = contextwindow.Policy{}.ExcludeFromStaleWindowOrDefault()
	}
	excludeNames := staleExcludeToolNameSet(excludeToolNames)

	batches := make([]staleToolBatch, 0)
	callBatches := make(map[string]int)
	callNames := make(map[string]string)
	activeBatch := -1
	for index, msg := range messages {
		if len(msg.ToolCalls) > 0 {
			activeBatch = len(batches)
			batches = append(batches, staleToolBatch{})
			for _, call := range msg.ToolCalls {
				if call.ID != "" {
					callBatches[call.ID] = activeBatch
					callNames[call.ID] = call.Name
				}
			}
			continue
		}
		if msg.Role != llm.RoleTool {
			activeBatch = -1
			continue
		}

		batchIndex, ok := callBatches[msg.ToolCallID]
		if !ok && msg.ToolCallID == "" && activeBatch >= 0 {
			batchIndex = activeBatch
			ok = true
		}
		if !ok {
			batchIndex = len(batches)
			batches = append(batches, staleToolBatch{})
		}

		class := classifyToolResultMessage(msg)
		nameExcluded := toolNameExcludedFromStale(msg, callNames, excludeNames)
		counted := !staleClassExcluded(class, exclude) && !nameExcluded
		if !counted {
			stats.ExcludedTurns++
			if class == contextwindow.ToolResultClassDenied {
				stats.DenialOccupiedSlots++
			}
		}
		batches[batchIndex].resultIndexes = append(batches[batchIndex].resultIndexes, index)
		batches[batchIndex].counted = batches[batchIndex].counted || counted
	}

	countedBatches := 0
	for _, batch := range batches {
		if batch.counted {
			countedBatches++
		}
	}

	keepBatchFrom := 0
	if countedBatches > keepTurns {
		seenCounted := 0
		for index := len(batches) - 1; index >= 0; index-- {
			if !batches[index].counted {
				continue
			}
			seenCounted++
			if seenCounted == keepTurns {
				keepBatchFrom = index
				break
			}
		}
	}

	dropIndex := make(map[int]struct{})
	for batchIndex, batch := range batches {
		if countedBatches <= keepTurns || batchIndex >= keepBatchFrom {
			continue
		}
		droppedAny := false
		for _, resultIndex := range batch.resultIndexes {
			// Pinned tool names (e.g. request_user_interaction) are durable user
			// facts — never hard-drop them even when their batch is stale.
			if toolNameExcludedFromStale(messages[resultIndex], callNames, excludeNames) {
				continue
			}
			dropIndex[resultIndex] = struct{}{}
			droppedAny = true
		}
		if batch.counted && droppedAny {
			stats.DroppedToolTurns++
		}
	}

	// Governance denials are actionable once per tool/reason. Keep the newest
	// copy and remove older duplicates (including duplicates inside one
	// parallel tool batch) so repeated loop-guard messages cannot crowd the
	// successful tool evidence that the model needs for its final answer.
	seenDenials := make(map[string]struct{})
	for batchIndex := len(batches) - 1; batchIndex >= 0; batchIndex-- {
		batch := batches[batchIndex]
		for resultOffset := len(batch.resultIndexes) - 1; resultOffset >= 0; resultOffset-- {
			resultIndex := batch.resultIndexes[resultOffset]
			if _, dropped := dropIndex[resultIndex]; dropped {
				continue
			}
			msg := messages[resultIndex]
			if classifyToolResultMessage(msg) != contextwindow.ToolResultClassDenied {
				continue
			}
			signature := denialContextSignature(msg, callNames)
			if _, duplicate := seenDenials[signature]; duplicate {
				dropIndex[resultIndex] = struct{}{}
				stats.CompactedDenials++
				continue
			}
			seenDenials[signature] = struct{}{}
		}
	}

	dropped := make(map[string]struct{})
	droppedUnidentified := false
	for index := range dropIndex {
		msg := messages[index]
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
