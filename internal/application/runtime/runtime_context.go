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
	if err != nil || contextwindow.EstimateTokens(string(raw)) <= maxTokens {
		return result
	}
	content := string(result.Output)
	if content == "" {
		content = result.Error
	}
	compact := map[string]any{
		"truncated":       true,
		"original_tokens": contextwindow.EstimateTokens(string(raw)),
		"max_tokens":      maxTokens,
		"content":         truncateForTokenBudget(content, maxTokens),
	}
	output, err := json.Marshal(compact)
	if err != nil {
		return result
	}
	return core.ToolResult{Tool: result.Tool, Output: output, Error: result.Error}
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
	for _, msg := range history {
		raw = append(raw, contextwindow.Message{
			Role:       contextwindow.Role(msg.Role),
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Metadata:   msg.Metadata,
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
	raw = append(raw, contextwindow.Message{Role: contextwindow.RoleUser, Content: req.Prompt})
	return e.prepareRawMessages(ctx, raw, profile)
}

func (e *Engine) prepareMessages(ctx context.Context, messages []llm.Message, profile core.LLMProfileRef) ([]llm.Message, contextwindow.Stats) {
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
	prepared, stats := e.prepareRawMessages(ctx, raw, profile)
	for i := range prepared {
		sourceIndex, ok := prepared[i].Metadata["source_index"]
		if ok {
			delete(prepared[i].Metadata, "source_index")
		}
		if index, err := strconv.Atoi(sourceIndex); ok && err == nil && index >= 0 && index < len(messages) {
			prepared[i].ToolCalls = messages[index].ToolCalls
		}
	}
	return prepared, stats
}

func (e *Engine) prepareRawMessages(ctx context.Context, raw []contextwindow.Message, profile core.LLMProfileRef) ([]llm.Message, contextwindow.Stats) {
	policy := profile.Context
	if policy.ContextWindowTokens == 0 {
		policy.ContextWindowTokens = profile.ContextWindowTokens
	}
	if policy.ReservedOutputTokens == 0 {
		policy.ReservedOutputTokens = profile.MaxOutputTokens
	}
	result := e.contextManager(ctx, policy).Prepare(raw)
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
	allowed := planAllowedTools(e, agent)
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
	specs = pruneToolSpecs(specs, allowed)
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
	return specs
}
