package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

func (e *Engine) compactToolResultForContext(result core.ToolResult, maxTokens int) (core.ToolResult, contextwindow.TransformMeta) {
	meta := contextwindow.TransformMeta{Strategy: contextwindow.TransformStrategyNone, OriginalBytes: len(result.Output)}
	if maxTokens <= 0 && (e == nil || len(e.toolTransforms) == 0) {
		meta.TruncatedBytes = len(result.Output)
		return result, meta
	}
	content := result.Output
	if len(content) == 0 && result.Error != "" {
		content = json.RawMessage(strconv.Quote(result.Error))
	}
	toolName := result.Tool
	var transforms map[string]contextwindow.ToolOutputTransform
	if e != nil {
		transforms = e.toolTransforms
	}
	// Prefer transforming the tool Output payload (JSON body) so knowledge_retrieve
	// and MCP tools keep a parseable structure. Fall back to wrapping the full
	// ToolResult only when the output alone still exceeds the budget after transform.
	out, meta := contextwindow.ApplyToolOutputTransform(toolName, content, maxTokens, transforms)
	if maxTokens > 0 {
		raw, err := json.Marshal(core.ToolResult{Tool: result.Tool, Output: out, Error: result.Error})
		if err == nil && contextwindow.EstimateTokens(string(raw)) <= maxTokens {
			return core.ToolResult{Tool: result.Tool, Output: out, Error: result.Error}, meta
		}
	} else {
		return core.ToolResult{Tool: result.Tool, Output: out, Error: result.Error}, meta
	}
	// Full ToolResult serialization still over budget: wrap with truncated marker.
	originalTokens := contextwindow.EstimateTokens(string(mustMarshal(result)))
	budget := maxTokens
	for attempt := 0; attempt < 8; attempt++ {
		payload := out
		if budget > 0 && contextwindow.EstimateTokens(string(payload)) > budget {
			payload, meta = contextwindow.ApplyToolOutputTransform(toolName, content, budget, transforms)
		}
		compact := map[string]any{
			"truncated":       true,
			"original_tokens": originalTokens,
			"max_tokens":      maxTokens,
			"strategy":        meta.Strategy,
			"content":         json.RawMessage(payload),
		}
		// If payload is not valid JSON, store as string.
		if !json.Valid(payload) {
			compact["content"] = string(payload)
		}
		encoded, err := json.Marshal(compact)
		if err != nil {
			return result, meta
		}
		if contextwindow.EstimateTokens(string(encoded)) <= maxTokens || budget <= 0 {
			meta.Truncated = true
			meta.TruncatedBytes = len(encoded)
			if meta.Strategy == contextwindow.TransformStrategyNone {
				meta.Strategy = contextwindow.TransformStrategyByteCut
			}
			return core.ToolResult{Tool: result.Tool, Output: encoded}, meta
		}
		budget /= 2
	}
	meta.Truncated = true
	meta.Strategy = contextwindow.TransformStrategyByteCut
	fallback := json.RawMessage(`{"truncated":true}`)
	meta.TruncatedBytes = len(fallback)
	return core.ToolResult{Tool: result.Tool, Output: fallback}, meta
}

func (e *Engine) prepareContext(ctx context.Context, agent core.Agent, profile core.LLMProfileRef, req RunRequest, history []llm.Message) ([]llm.Message, contextwindow.Stats) {
	raw := []contextwindow.Message{
		{Role: contextwindow.RoleSystem, Content: agent.Instructions},
	}
	for i, msg := range history {
		metadata := cloneMetadata(msg.Metadata)
		metadata["source_index"] = strconv.Itoa(i)
		raw = append(raw, contextwindow.Message{
			Role:        contextwindow.Role(msg.Role),
			Content:     msg.Content,
			Name:        msg.Name,
			ToolCallID:  msg.ToolCallID,
			ToolCallIDs: toolCallIDs(msg),
			Metadata:    metadata,
		})
	}
	if len(req.Context) > 0 && string(req.Context) != "null" {
		raw = append(raw, contextwindow.Message{
			Role:    contextwindow.RoleUser,
			Content: "Runtime context JSON:\n" + string(req.Context),
			Metadata: map[string]string{
				"priority": "context",
			},
		})
	}
	if req.Prompt != "" {
		raw = append(raw, contextwindow.Message{Role: contextwindow.RoleUser, Content: req.Prompt})
	}
	prepared, stats := e.prepareRawMessages(ctx, req.RunID, agent, raw, profile)
	paired, drops := enforceToolCallPairingWithStats(restorePreparedToolCalls(prepared, history))
	e.emitPairingIncomplete(ctx, req.RunID, drops)
	return paired, stats
}

func (e *Engine) prepareMessages(ctx context.Context, runID string, agent core.Agent, messages []llm.Message, profile core.LLMProfileRef) ([]llm.Message, contextwindow.Stats) {
	raw := make([]contextwindow.Message, 0, len(messages))
	for i, msg := range messages {
		metadata := cloneMetadata(msg.Metadata)
		metadata["source_index"] = strconv.Itoa(i)
		raw = append(raw, contextwindow.Message{
			Role:        contextwindow.Role(msg.Role),
			Content:     msg.Content,
			Name:        msg.Name,
			ToolCallID:  msg.ToolCallID,
			ToolCallIDs: toolCallIDs(msg),
			Metadata:    metadata,
		})
	}
	prepared, stats := e.prepareRawMessages(ctx, runID, agent, raw, profile)
	paired, drops := enforceToolCallPairingWithStats(restorePreparedToolCalls(prepared, messages))
	e.emitPairingIncomplete(ctx, runID, drops)
	return paired, stats
}

func restorePreparedToolCalls(prepared []llm.Message, source []llm.Message) []llm.Message {
	for i := range prepared {
		sourceIndex, ok := prepared[i].Metadata["source_index"]
		if ok {
			delete(prepared[i].Metadata, "source_index")
		}
		index, err := strconv.Atoi(sourceIndex)
		if !ok || err != nil || index < 0 || index >= len(source) {
			continue
		}
		prepared[i].ToolCalls = append([]llm.ToolCall(nil), source[index].ToolCalls...)
	}
	return prepared
}

func (e *Engine) prepareRawMessages(ctx context.Context, runID string, agent core.Agent, raw []contextwindow.Message, profile core.LLMProfileRef) ([]llm.Message, contextwindow.Stats) {
	policy := profile.Context
	if policy.ContextWindowTokens == 0 {
		policy.ContextWindowTokens = profile.ContextWindowTokens
	}
	if policy.ReservedOutputTokens == 0 {
		policy.ReservedOutputTokens = profile.MaxOutputTokens
	}
	result := e.contextManager(ctx, runID, agent, policy).Prepare(raw)
	if policy.InjectCompactReminder && result.Stats.NeedsReminder {
		if reminder := e.compactReminder(ctx, runID); reminder != "" {
			// Codex-aligned contract: reinject above the last user message so
			// the reminder sits before the latest user turn (not at the tail).
			result.Messages = contextwindow.InsertMessage(result.Messages, contextwindow.Message{
				Role:    contextwindow.RoleSystem,
				Content: reminder,
				Metadata: map[string]string{
					"context_window": "compact_reminder",
				},
			}, contextwindow.InsertBeforeLastUserMessage)
		}
	}
	messages := make([]llm.Message, 0, len(result.Messages))
	for _, msg := range result.Messages {
		messages = append(messages, llm.Message{
			Role:       llm.Role(msg.Role),
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Metadata:   msg.Metadata,
		})
	}
	return messages, result.Stats
}

func toolCallIDs(msg llm.Message) []string {
	if len(msg.ToolCalls) == 0 {
		return nil
	}
	ids := make([]string, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		if call.ID != "" {
			ids = append(ids, call.ID)
		}
	}
	return ids
}

func (e *Engine) compactReminder(ctx context.Context, runID string) string {
	snapshot, err := runstate.LoadAuthorized(ctx, e.runs, runID)
	if err != nil {
		return ""
	}
	ref, ok := snapshot.StepOutputs["plan"]
	if !ok || len(ref.Inline) == 0 {
		return ""
	}
	var state planExecutionState
	if err := json.Unmarshal(ref.Inline, &state); err != nil || len(state.Steps) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("<system-reminder>\nActive plan after context compaction:\n")
	for _, step := range state.Steps {
		status := step.Status
		if status == "" {
			status = "pending"
		}
		if step.Tool != "" {
			fmt.Fprintf(&b, "- [%s] %s (tool=%s)\n", status, step.Goal, step.Tool)
		} else {
			fmt.Fprintf(&b, "- [%s] %s\n", status, step.Goal)
		}
	}
	b.WriteString("</system-reminder>")
	return strings.TrimSpace(b.String())
}

// emitPairingIncomplete surfaces an EventContextIncomplete when repairing the
// tool_call/tool_result contract had to drop orphaned tool results or strip
// unanswered tool_calls after truncation, so this silent loss is observable.
func (e *Engine) emitPairingIncomplete(ctx context.Context, runID string, drops pairingDrops) {
	if !drops.any() {
		return
	}
	e.emitJSON(ctx, core.EventContextIncomplete, runID, map[string]any{
		"dropped_orphan_tool_results":    drops.orphanToolResults,
		"stripped_unanswered_tool_calls": drops.unansweredToolCalls,
		"warning":                        "context may be incomplete: tool_call/tool_result pairing was repaired after truncation",
	})
}

func (e *Engine) emitContextPrepared(ctx context.Context, runID string, stats contextwindow.Stats) {
	payload := map[string]any{
		"strategy":                   stats.Strategy,
		"before_tokens":              stats.BeforeTokens,
		"after_tokens":               stats.AfterTokens,
		"max_input_tokens":           stats.MaxInputTokens,
		"dropped_messages":           stats.DroppedMessages,
		"dropped_user_messages":      stats.DroppedUserMessages,
		"dropped_assistant_messages": stats.DroppedAssistantMessages,
		"dropped_tool_messages":      stats.DroppedToolMessages,
		"context_incomplete":         stats.ContextIncomplete,
		"summarized":                 stats.Summarized,
		"summary_tokens":             stats.SummaryTokens,
		"policy_source":              stats.PolicySource,
		"fallback_applied":           stats.FallbackApplied,
		"stale_dropped_tool_turns":   stats.StaleDroppedToolTurns,
		"denial_occupied_slots":      stats.DenialOccupiedSlots,
		"stale_excluded_turns":       stats.StaleExcludedTurns,
	}
	if stats.FallbackApplied {
		payload["warning"] = "context max_input_tokens fell back to 8192; set LLMProfileRef.ContextWindowTokens or Context.MaxInputTokens"
	}
	e.emitJSON(ctx, core.EventContextPrepared, runID, payload)
	if stats.DroppedUserMessages > 0 {
		e.emitJSON(ctx, core.EventContextIncomplete, runID, map[string]any{
			"dropped_user_messages":      stats.DroppedUserMessages,
			"dropped_assistant_messages": stats.DroppedAssistantMessages,
			"dropped_tool_messages":      stats.DroppedToolMessages,
			"warning":                    "context may be incomplete: user messages were dropped during truncation",
		})
	}
}

func (e *Engine) toolSpecs(ctx context.Context, runID string, agent core.Agent) []llm.ToolSpec {
	specs := make([]llm.ToolSpec, 0, len(agent.Tools)+len(agent.SubAgents))
	for _, name := range agent.Tools {
		tool, ok := e.scenario.Tools[name]
		if !ok {
			continue
		}
		specs = append(specs, llm.ToolSpec{
			Name:        name,
			Description: tool.Description,
			Schema:      tool.InputSchema,
		})
	}
	for _, name := range agent.SubAgents {
		sub, ok := e.scenario.Agents[name]
		if !ok {
			continue
		}
		description := sub.Description
		if description == "" {
			description = "Delegate a task to sub-agent " + name
		}
		specs = append(specs, llm.ToolSpec{
			Name:        delegateToolName(name),
			Description: description,
			Schema:      json.RawMessage(`{"type":"object","properties":{"prompt":{"type":"string"},"context":{"type":"object"}},"required":["prompt"]}`),
		})
	}
	// Pruning runs over the full spec list (regular tools and sub-agent
	// delegate tools alike) so it stays correct regardless of how the
	// list above is assembled; planAllowedTools includes delegate tool
	// names precisely so this doesn't strip an agent's ability to
	// delegate while planning-driven schema pruning is active.
	return pruneToolSpecs(specs, planAllowedTools(ctx, e, runID, agent))
}
