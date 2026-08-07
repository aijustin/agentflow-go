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
	e.coord.pendingSelfCompact.Store(runID, struct{}{})
}

func (e *Engine) consumeSelfCompactPending(runID string) bool {
	_, ok := e.coord.pendingSelfCompact.LoadAndDelete(runID)
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
	masked := contextwindow.MaskObservations(raw, maskAfterTurns, profile.Context.ExcludeToolNamesFromStaleWindow...)
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

// maxContextLengthRecoveryAttempts caps provider context-overflow recovery to
// one compaction+retry per run. The counter lives in the checkpointed usage
// tracker, so a pause/resume cycle cannot farm extra recovery attempts.
const maxContextLengthRecoveryAttempts = 1

// tryContextLengthRecovery reacts to a provider context_length_exceeded
// rejection: it compacts the outgoing request messages (observation masking
// plus a forced sliding_window_with_summary pass, reusing the self-compact
// conversion path) and reports whether the call should be retried. At most
// one recovery happens per run; a second overflow fails the run. The
// authoritative conversation in the tool loop is left untouched here, but a
// self-compact is scheduled so the next loop iteration compacts it too -
// otherwise every subsequent call would overflow again.
func (e *Engine) tryContextLengthRecovery(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req *llm.ToolCallRequest) bool {
	tracker := e.usageTrackerFor(runID)
	if tracker.contextRecoveryAttempts() >= maxContextLengthRecoveryAttempts {
		return false
	}
	tracker.markContextRecovery()
	maskAfterTurns := profile.Context.ObservationMaskAfterTurns
	if maskAfterTurns <= 0 {
		maskAfterTurns = 1
	}
	raw := make([]contextwindow.Message, 0, len(req.Messages))
	for i, msg := range req.Messages {
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
	masked := contextwindow.MaskObservations(raw, maskAfterTurns, profile.Context.ExcludeToolNamesFromStaleWindow...)
	policy := profile.Context
	if policy.ContextWindowTokens == 0 {
		policy.ContextWindowTokens = profile.ContextWindowTokens
	}
	if policy.ReservedOutputTokens == 0 {
		policy.ReservedOutputTokens = profile.MaxOutputTokens
	}
	policy = policy.Normalize()
	// Force the summary strategy and disable the optional tool-message
	// compression stage: the provider already told us the real count exceeds
	// the window, so the recovery must deterministically trim to budget even
	// when the heuristic estimate says the messages would fit.
	policy.Strategy = contextwindow.StrategySlidingWindowWithSummary
	policy.Compression.Enabled = false
	result := e.contextManager(ctx, runID, agent, policy).PrepareWithOptions(masked, contextwindow.PrepareOptions{
		KnownInputTokens: policy.MaxInputTokens + 1,
	})
	out := make([]llm.Message, 0, len(result.Messages))
	for _, msg := range result.Messages {
		out = append(out, llm.Message{
			Role:       llm.Role(msg.Role),
			Content:    msg.Content,
			Name:       msg.Name,
			ToolCallID: msg.ToolCallID,
			Metadata:   msg.Metadata,
		})
	}
	out = restorePreparedToolCalls(out, req.Messages)
	paired, drops := enforceToolCallPairingWithStats(out)
	e.emitPairingIncomplete(ctx, runID, drops)
	messagesBefore := len(req.Messages)
	req.Messages = paired
	e.markSelfCompactPending(runID)
	e.emitJSON(ctx, core.EventContextPrepared, runID, map[string]any{
		"context_recovery":  true,
		"messages_before":   messagesBefore,
		"messages_after":    len(paired),
		"summarized":        result.Stats.Summarized,
		"dropped_messages":  result.Stats.DroppedMessages,
		"recovery_attempts": tracker.contextRecoveryAttempts(),
	})
	return true
}
