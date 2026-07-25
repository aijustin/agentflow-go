package runtime

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/toolcatalog"
)

func (e *Engine) selfCompactEnabled(agent core.Agent) bool {
	if e.catalogEnabled() {
		return true
	}
	profile, ok := e.scenario.LLMs[agent.LLM]
	if !ok {
		return false
	}
	return profile.Context.ObservationMaskAfterTurns > 0
}

func (e *Engine) markSelfCompactPending(runID string) {
	e.pendingSelfCompact.Store(runID, struct{}{})
}

func (e *Engine) consumeSelfCompactPending(runID string) bool {
	_, ok := e.pendingSelfCompact.LoadAndDelete(runID)
	return ok
}

func (e *Engine) dispatchSelfCompactMetaTool(_ context.Context, runID string, agent core.Agent, call llm.ToolCall) (core.ToolResult, bool, error) {
	if call.Name != toolcatalog.ToolCompactContext || !e.selfCompactEnabled(agent) {
		return core.ToolResult{}, false, nil
	}
	e.markSelfCompactPending(runID)
	payload, err := json.Marshal(map[string]any{"compact": "scheduled"})
	if err != nil {
		return core.ToolResult{}, true, err
	}
	return core.ToolResult{Tool: call.Name, Output: payload}, true, nil
}

func (e *Engine) applySelfCompactIfPending(ctx context.Context, runID string, profile core.LLMProfileRef, messages []llm.Message) []llm.Message {
	if !e.consumeSelfCompactPending(runID) {
		return messages
	}
	maskAfterTurns := profile.Context.ObservationMaskAfterTurns
	if maskAfterTurns <= 0 {
		maskAfterTurns = 1
	}
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
	before := len(raw)
	masked := contextwindow.MaskObservations(raw, maskAfterTurns)
	compacted := contextwindow.CompactContext(masked)
	out := make([]llm.Message, 0, len(compacted))
	for _, msg := range compacted {
		out = append(out, llm.Message{
			Role:       llm.Role(msg.Role),
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Metadata:   msg.Metadata,
		})
	}
	out = restorePreparedToolCalls(out, messages)
	e.emitJSON(ctx, core.EventContextPrepared, runID, map[string]any{
		"self_compact":           true,
		"messages_before":        before,
		"messages_after":         len(compacted),
		"observation_mask_turns": maskAfterTurns,
	})
	return out
}

func (e *Engine) appendSelfCompactMetaToolSpecs(agent core.Agent, specs []llm.ToolSpec) []llm.ToolSpec {
	if !e.selfCompactEnabled(agent) {
		return specs
	}
	meta := toolcatalog.SelfCompactMetaToolSpec()
	return append(specs, llm.ToolSpec{
		Name:        meta.Name,
		Description: meta.Description,
		Schema:      meta.Schema,
	})
}
