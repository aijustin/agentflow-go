package runtime

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

func compactToolResultForContext(result core.ToolResult, maxTokens int) core.ToolResult {
	if maxTokens <= 0 {
		return result
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return result
	}
	originalTokens := contextwindow.EstimateTokens(string(raw))
	if originalTokens <= maxTokens {
		return result
	}
	content := string(result.Output)
	if result.Error != "" {
		if content != "" {
			content = content + "\nerror: " + result.Error
		} else {
			content = result.Error
		}
	}
	// Shrink the content budget until the fully serialized compacted
	// payload - not just the raw content substring - actually fits within
	// maxTokens. Truncating only the content string and leaving the
	// "truncated"/"original_tokens"/"max_tokens" metadata fields and JSON
	// structural overhead unaccounted for can otherwise still push the
	// final message back over the caller's budget.
	budget := maxTokens
	for attempt := 0; attempt < 8; attempt++ {
		// truncateForTokenBudget treats a non-positive budget as "no
		// limit" (pass content through unchanged), so once the shrinking
		// budget bottoms out at 0 it must be treated as "keep nothing"
		// here instead of calling into that helper with 0 and getting the
		// full, untruncated content back.
		truncated := ""
		if budget > 0 {
			truncated = truncateForTokenBudget(content, budget)
		}
		compact := map[string]any{
			"truncated":       true,
			"original_tokens": originalTokens,
			"max_tokens":      maxTokens,
			"content":         truncated,
		}
		output, err := json.Marshal(compact)
		if err != nil {
			return result
		}
		if contextwindow.EstimateTokens(string(output)) <= maxTokens || budget <= 0 {
			return core.ToolResult{Tool: result.Tool, Output: output}
		}
		budget /= 2
	}
	return core.ToolResult{Tool: result.Tool, Output: json.RawMessage(`{"truncated":true}`)}
}

func truncateForTokenBudget(content string, maxTokens int) string {
	if maxTokens <= 0 || contextwindow.EstimateTokens(content) <= maxTokens {
		return content
	}
	limit := maxTokens * 3
	runes := []rune(content)
	if limit <= 0 || len(runes) <= limit {
		return content
	}
	return string(runes[:limit]) + "..."
}

func (e *Engine) prepareContext(ctx context.Context, agent core.Agent, profile core.LLMProfileRef, req RunRequest, history []llm.Message) ([]llm.Message, contextwindow.Stats) {
	raw := []contextwindow.Message{
		{Role: contextwindow.RoleSystem, Content: agent.Instructions},
	}
	for i, msg := range history {
		metadata := cloneMetadata(msg.Metadata)
		metadata["source_index"] = strconv.Itoa(i)
		raw = append(raw, contextwindow.Message{
			Role:       contextwindow.Role(msg.Role),
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Metadata:   metadata,
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
	prepared, stats := e.prepareRawMessages(ctx, req.RunID, raw, profile)
	return enforceToolCallPairing(restorePreparedToolCalls(prepared, history)), stats
}

func (e *Engine) prepareMessages(ctx context.Context, runID string, messages []llm.Message, profile core.LLMProfileRef) ([]llm.Message, contextwindow.Stats) {
	raw := make([]contextwindow.Message, 0, len(messages))
	for i, msg := range messages {
		metadata := cloneMetadata(msg.Metadata)
		metadata["source_index"] = strconv.Itoa(i)
		raw = append(raw, contextwindow.Message{
			Role:       contextwindow.Role(msg.Role),
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Metadata:   metadata,
		})
	}
	prepared, stats := e.prepareRawMessages(ctx, runID, raw, profile)
	return enforceToolCallPairing(restorePreparedToolCalls(prepared, messages)), stats
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

func (e *Engine) prepareRawMessages(ctx context.Context, runID string, raw []contextwindow.Message, profile core.LLMProfileRef) ([]llm.Message, contextwindow.Stats) {
	policy := profile.Context
	if policy.ContextWindowTokens == 0 {
		policy.ContextWindowTokens = profile.ContextWindowTokens
	}
	if policy.ReservedOutputTokens == 0 {
		policy.ReservedOutputTokens = profile.MaxOutputTokens
	}
	result := e.contextManager(ctx, runID, policy).Prepare(raw)
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

func (e *Engine) toolSpecs(agent core.Agent) []llm.ToolSpec {
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
	return pruneToolSpecs(specs, planAllowedTools(e, agent))
}
