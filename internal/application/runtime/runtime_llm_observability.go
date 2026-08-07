package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

// maxLLMCalledMessageChars caps each message content in LLMCalled payloads so
// EventStore / diagnostic drawers stay usable without unbounded tool dumps.
const maxLLMCalledMessageChars = 8000

// llmCalledPayloadHashChars truncates the sha256 fingerprint of the redacted
// LLMCalled payload to 16 hex chars (64 bits) - enough to correlate identical
// payloads without persisting any plaintext.
const llmCalledPayloadHashChars = 16

// llmCalledPayload builds the LLMCalled event payload. By default
// (capture=false) it carries only shape metadata - message count, per-message
// role/content lengths, and a truncated content hash - so user prompts, which
// may contain PII, are never persisted to the event store. With capture=true
// (WithLLMPayloadCapture) the messages actually sent to the model and the last
// user prompt are attached so Debug drawers can show LLM 入参 instead of
// metadata-only cards; the payload still passes through the configured output
// redactor before it is emitted.
func llmCalledPayload(capture bool, base map[string]any, messages []llm.Message) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	if len(messages) == 0 {
		return base
	}
	base["message_count"] = len(messages)
	obs := make([]map[string]any, 0, len(messages))
	if !capture {
		hash := sha256.New()
		for _, m := range messages {
			entry := llmCalledMessageMeta(m)
			entry["content_chars"] = len([]rune(m.Content))
			obs = append(obs, entry)
			hash.Write([]byte(string(m.Role)))
			hash.Write([]byte{0})
			hash.Write([]byte(m.Content))
			hash.Write([]byte{0})
		}
		base["messages"] = obs
		base["messages_hash"] = hex.EncodeToString(hash.Sum(nil))[:llmCalledPayloadHashChars]
		return base
	}
	for _, m := range messages {
		entry := llmCalledMessageMeta(m)
		if c := strings.TrimSpace(m.Content); c != "" {
			entry["content"] = truncateObservabilityText(c, maxLLMCalledMessageChars)
		}
		obs = append(obs, entry)
	}
	base["messages"] = obs
	if prompt := lastUserMessageContent(messages); prompt != "" {
		base["prompt"] = truncateObservabilityText(prompt, maxLLMCalledMessageChars)
	}
	return base
}

// llmCalledMessageMeta carries the non-content message fields (role, tool
// routing metadata) that are safe to persist regardless of payload capture.
func llmCalledMessageMeta(m llm.Message) map[string]any {
	entry := map[string]any{"role": string(m.Role)}
	if id := strings.TrimSpace(m.ToolCallID); id != "" {
		entry["tool_call_id"] = id
	}
	if name := strings.TrimSpace(m.Name); name != "" {
		entry["name"] = name
	}
	if names := toolCallNames(m.ToolCalls); len(names) > 0 {
		entry["tool_names"] = names
	}
	return entry
}

// startLLMCallSpan opens one span for a logical LLM invocation. Retry
// attempts are stamped as an "attempt" attribute on this span instead of
// opening a span per attempt, so a flaky provider cannot flood the trace.
func (e *Engine) startLLMCallSpan(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, attrs ...observability.Attribute) (context.Context, observability.Span) {
	base := []observability.Attribute{
		{Key: "run_id", Value: runID},
		{Key: "agent", Value: agent.Name},
		{Key: "profile", Value: agent.LLM},
		{Key: "scenario_name", Value: e.scenario.Name},
	}
	if profile.Model != "" {
		base = append(base, observability.Attribute{Key: "model", Value: profile.Model})
	}
	return e.startSpan(ctx, observability.SpanLLMCall, append(base, attrs...)...)
}

// finishLLMCall closes an LLM call span: a failure is recorded on the span
// and counted, and the logical-call latency is observed either way.
func (e *Engine) finishLLMCall(ctx context.Context, span observability.Span, profileName string, start time.Time, err error) {
	attrs := []observability.Attribute{{Key: "profile", Value: profileName}}
	e.obs.recorder.ObserveHistogram(ctx, observability.MetricLLMDurationSeconds, time.Since(start).Seconds(), attrs...)
	if err != nil {
		span.RecordError(err)
		e.obs.recorder.IncCounter(ctx, observability.MetricLLMErrorsTotal, attrs...)
	}
	span.End()
}

// recordLLMUsage accumulates token counters wherever an LLMTokenUsage event is
// emitted, so metrics consumers get usage without scanning the event store.
// It also folds the call into the run-level usage tracker: the last call's
// real token count is what lets context preparation override the heuristic
// EstimateTokens trigger, and the accumulated totals survive pause/resume via
// the checkpoint_usage variable.
func (e *Engine) recordLLMUsage(ctx context.Context, runID string, profileName string, usage *llm.TokenUsage) {
	if usage == nil {
		return
	}
	e.usageTrackerFor(runID).record(*usage)
	for _, bucket := range []struct {
		kind   string
		tokens int
	}{
		{"prompt", usage.InputTokens},
		{"completion", usage.OutputTokens},
	} {
		if bucket.tokens > 0 {
			e.obs.recorder.AddCounter(ctx, observability.MetricLLMTokensTotal, float64(bucket.tokens),
				observability.Attribute{Key: "profile", Value: profileName},
				observability.Attribute{Key: "kind", Value: bucket.kind})
		}
	}
}

// llmReturnedPayload attaches assistant text for Debug/EventStore consumers.
// Older emitters omitted text; ProductUI and diagnostic drawers need it.
func llmReturnedPayload(base map[string]any, text string) map[string]any {
	if base == nil {
		base = map[string]any{}
	}
	if trimmed := strings.TrimSpace(text); trimmed != "" {
		base["text"] = trimmed
	}
	return base
}

func lastUserMessageContent(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role != llm.RoleUser {
			continue
		}
		if c := strings.TrimSpace(messages[i].Content); c != "" {
			return c
		}
	}
	return ""
}

func truncateObservabilityText(text string, maxChars int) string {
	if maxChars <= 0 || len(text) <= maxChars {
		return text
	}
	// Prefer rune-safe cut when the limit lands mid-codepoint.
	runes := []rune(text)
	if len(runes) <= maxChars {
		return text
	}
	return string(runes[:maxChars]) + "…"
}

func toolCallNames(calls []llm.ToolCall) []string {
	if len(calls) == 0 {
		return nil
	}
	names := make([]string, 0, len(calls))
	for _, tc := range calls {
		if name := strings.TrimSpace(tc.Name); name != "" {
			names = append(names, name)
		}
	}
	return names
}
