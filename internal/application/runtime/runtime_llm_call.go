package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

// wrapLLMStream forwards provider chunks while keeping the LLM call span open
// for the whole stream. The span closes when the stream terminates - on a
// clean done, on an error chunk, or on context cancellation - mirroring the
// two-path End handling of tool spans.
func (e *Engine) wrapLLMStream(ctx context.Context, runID string, source <-chan llm.ChatChunk, span observability.Span, profileName string, start time.Time) <-chan llm.ChatChunk {
	out := make(chan llm.ChatChunk, 16)
	safecall.GoSafe("runtime: llm stream span", nil, func() {
		defer close(out)
		var streamErr error
		var usage llm.TokenUsage
		sawDone := false
		finished := false
		finish := func() {
			if finished {
				return
			}
			finished = true
			e.recordLLMUsage(ctx, runID, profileName, normalizeEmittedUsage(usage))
			e.finishLLMCall(ctx, span, profileName, start, streamErr)
		}
		defer finish()
		for chunk := range source {
			if chunk.Usage.TotalTokens > 0 || chunk.Usage.InputTokens > 0 || chunk.Usage.OutputTokens > 0 {
				usage = chunk.Usage
			}
			if chunk.Error != "" && streamErr == nil {
				streamErr = chunkError(chunk)
			}
			if chunk.Done {
				sawDone = true
			}
			terminal := chunk.Done || chunk.Error != ""
			if terminal {
				// Complete observability before publishing the terminal chunk.
				// Its channel send then establishes the happens-before edge
				// required by Engine.Stream's user-visible channel closure.
				finish()
			}
			select {
			case out <- chunk:
			case <-ctx.Done():
				// A consumer that walks away after the terminal done chunk is
				// not a stream failure; abandoning mid-stream is.
				if streamErr == nil && !sawDone {
					streamErr = ctx.Err()
				}
				return
			}
			if terminal {
				return
			}
		}
		if streamErr == nil && !sawDone {
			// Mirror the Stream consumer: a provider stream cut off before the
			// done chunk is a failure, not a clean completion.
			if err := ctx.Err(); err != nil {
				streamErr = err
			} else {
				streamErr = errors.New("runtime: llm stream closed without a done chunk")
			}
		}
	})
	return out
}

func (e *Engine) chatWithRetry(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req llm.ChatRequest) (llm.ChatResponse, error) {
	attempts := e.maxAttempts(agent)
	// One span for the whole logical call: retry attempts are stamped as an
	// attribute instead of opening a span per attempt, so a flaky provider
	// cannot flood the trace.
	ctx, span := e.startLLMCallSpan(ctx, runID, agent, profile)
	start := time.Now()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		span.SetAttributes(observability.Attribute{Key: "attempt", Value: strconv.Itoa(attempt)})
		if err := ctx.Err(); err != nil {
			e.finishLLMCall(ctx, span, agent.LLM, start, err)
			return llm.ChatResponse{}, err
		}
		callCtx, cancel := e.withTimeout(ctx, profile.Timeout)
		e.emitJSON(callCtx, core.EventLLMCalled, runID, llmCalledPayload(e.obs.llmPayloadCapture, map[string]any{
			"profile": agent.LLM,
			"tools":   false,
			"attempt": attempt,
		}, req.Messages))
		resp, err := safecall.Invoke("runtime: llm chat", func() (llm.ChatResponse, error) {
			return e.llm.Chat(callCtx, agent.LLM, req)
		})
		cancel()
		if err == nil {
			e.emitJSON(ctx, core.EventLLMReturned, runID, llmReturnedPayload(map[string]any{
				"profile":       agent.LLM,
				"finish_reason": resp.FinishReason,
				"attempt":       attempt,
			}, resp.Message.Content))
			e.finishLLMCall(ctx, span, agent.LLM, start, nil)
			return resp, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == attempts {
			e.finishLLMCall(ctx, span, agent.LLM, start, err)
			return llm.ChatResponse{}, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			e.finishLLMCall(ctx, span, agent.LLM, start, err)
			return llm.ChatResponse{}, err
		}
	}
	e.finishLLMCall(ctx, span, agent.LLM, start, lastErr)
	return llm.ChatResponse{}, lastErr
}

func (e *Engine) chatWithToolsWithRetry(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, req llm.ToolCallRequest, caller llm.ToolCaller, step int, emit streamChunkSink) (llm.ToolCallResponse, error) {
	attempts := e.maxAttempts(agent)
	spanAttrs := []observability.Attribute{
		{Key: "tools", Value: "true"},
		{Key: "step", Value: strconv.Itoa(step)},
	}
	if emit != nil {
		spanAttrs = append(spanAttrs, observability.Attribute{Key: "stream", Value: "true"})
	}
	ctx, span := e.startLLMCallSpan(ctx, runID, agent, profile, spanAttrs...)
	start := time.Now()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		span.SetAttributes(observability.Attribute{Key: "attempt", Value: strconv.Itoa(attempt)})
		if err := ctx.Err(); err != nil {
			e.finishLLMCall(ctx, span, agent.LLM, start, err)
			return llm.ToolCallResponse{}, err
		}
		callCtx, cancel := e.withTimeout(ctx, profile.Timeout)
		e.emitJSON(callCtx, core.EventLLMCalled, runID, llmCalledPayload(e.obs.llmPayloadCapture, map[string]any{
			"profile": agent.LLM,
			"tools":   true,
			"step":    step,
			"attempt": attempt,
			"stream":  emit != nil,
		}, req.Messages))
		var resp llm.ToolCallResponse
		var err error
		if emit != nil {
			if streamer, ok := e.llm.(llm.ToolCallStreamer); ok && e.llm.Supports(agent.LLM, llm.CapStream) {
				resp, err = e.collectStreamChatWithTools(callCtx, streamer, agent.LLM, req, emit)
			} else {
				resp, err = safecall.Invoke("runtime: llm chat with tools", func() (llm.ToolCallResponse, error) {
					return caller.ChatWithTools(callCtx, agent.LLM, req)
				})
			}
		} else {
			resp, err = safecall.Invoke("runtime: llm chat with tools", func() (llm.ToolCallResponse, error) {
				return caller.ChatWithTools(callCtx, agent.LLM, req)
			})
		}
		cancel()
		if err == nil {
			payload := map[string]any{
				"profile":       agent.LLM,
				"finish_reason": resp.FinishReason,
				"tool_calls":    len(resp.ToolCalls),
				"step":          step,
				"attempt":       attempt,
			}
			if names := toolCallNames(resp.ToolCalls); len(names) > 0 {
				payload["tool_names"] = names
			}
			e.emitJSON(ctx, core.EventLLMReturned, runID, llmReturnedPayload(payload, resp.Message.Content))
			e.finishLLMCall(ctx, span, agent.LLM, start, nil)
			return resp, nil
		}
		lastErr = err
		// A provider context-window overflow is classified non-retryable, but
		// it is recoverable: compact the conversation once per run and retry
		// before giving up. The recovery retry must not consume the provider
		// retry budget - the per-run recovery counter already bounds it to a
		// single extra call, and with maxAttempts=1 this would otherwise
		// never fire.
		if llm.IsContextLengthExceeded(err) && e.tryContextLengthRecovery(ctx, runID, agent, profile, &req) {
			attempt--
			continue
		}
		if !shouldRetry(ctx, err) || attempt == attempts {
			e.finishLLMCall(ctx, span, agent.LLM, start, err)
			return llm.ToolCallResponse{}, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			e.finishLLMCall(ctx, span, agent.LLM, start, err)
			return llm.ToolCallResponse{}, err
		}
	}
	e.finishLLMCall(ctx, span, agent.LLM, start, lastErr)
	return llm.ToolCallResponse{}, lastErr
}

func (e *Engine) collectStreamChatWithTools(
	ctx context.Context,
	streamer llm.ToolCallStreamer,
	profile string,
	req llm.ToolCallRequest,
	emit streamChunkSink,
) (llm.ToolCallResponse, error) {
	ch, err := streamer.StreamChatWithTools(ctx, profile, req)
	if err != nil {
		return llm.ToolCallResponse{}, err
	}
	var content strings.Builder
	var toolCalls []llm.ToolCall
	var usage llm.TokenUsage
	finishReason := "stop"
	for chunk := range ch {
		if chunk.Error != "" {
			// Preserve the structured provider error (when the gateway
			// attached one) so retry classification — e.g.
			// llm.APIError.Retryable — works on the streaming path exactly
			// like on the unary path, instead of seeing an opaque string.
			return llm.ToolCallResponse{}, chunkError(chunk)
		}
		if chunk.Usage.TotalTokens > 0 || chunk.Usage.InputTokens > 0 || chunk.Usage.OutputTokens > 0 {
			usage = chunk.Usage
		}
		switch chunk.Kind {
		case llm.ChunkKindToolCall:
			toolCalls = append(toolCalls, llm.ToolCall{
				ID:    chunk.ToolCallID,
				Name:  chunk.ToolName,
				Input: llm.NormalizeToolArguments(chunk.ToolInput),
			})
			finishReason = "tool_calls"
		default:
			if chunk.IsAnswerContent() && chunk.Content != "" {
				content.WriteString(chunk.Content)
				// Forward deltas immediately for live UI. Tool-turn preambles
				// may stream before tool_calls; authoritative message.Content
				// is cleared below when the turn is classified as tool_calls.
				emitStreamChunk(emit, llm.ChatChunk{Content: chunk.Content})
			}
		}
	}
	if err := ctx.Err(); err != nil {
		return llm.ToolCallResponse{}, err
	}
	message := llm.Message{
		Role:    llm.RoleAssistant,
		Content: content.String(),
	}
	if len(toolCalls) > 0 {
		message.ToolCalls = append([]llm.ToolCall(nil), toolCalls...)
		// Drop tool-turn prose from the authoritative assistant message so it
		// cannot persist into StepOutputs["final"] / StreamFrameDone.Result.
		message.Content = ""
	}
	return llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{
			Message:      message,
			Usage:        usage,
			FinishReason: finishReason,
		},
		ToolCalls: toolCalls,
	}, nil
}

func normalizeEmittedUsage(usage llm.TokenUsage) *llm.TokenUsage {
	if usage.TotalTokens == 0 && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	if usage.TotalTokens == 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 && usage.ReasoningTokens == 0 {
		return nil
	}
	return &usage
}

func (e *Engine) structuredWithRetry(ctx context.Context, runID string, agent core.Agent, profile core.LLMProfileRef, schema json.RawMessage, req llm.ChatRequest, outputter llm.StructuredOutputter) (json.RawMessage, error) {
	attempts := e.maxAttempts(agent)
	ctx, span := e.startLLMCallSpan(ctx, runID, agent, profile,
		observability.Attribute{Key: "structured", Value: "true"})
	start := time.Now()
	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		span.SetAttributes(observability.Attribute{Key: "attempt", Value: strconv.Itoa(attempt)})
		if err := ctx.Err(); err != nil {
			e.finishLLMCall(ctx, span, agent.LLM, start, err)
			return nil, err
		}
		callCtx, cancel := e.withTimeout(ctx, profile.Timeout)
		e.emitJSON(callCtx, core.EventLLMCalled, runID, llmCalledPayload(e.obs.llmPayloadCapture, map[string]any{
			"profile":    agent.LLM,
			"structured": true,
			"attempt":    attempt,
		}, req.Messages))
		raw, err := safecall.Invoke("runtime: llm structured chat", func() (json.RawMessage, error) {
			return outputter.StructuredChat(callCtx, agent.LLM, schema, req)
		})
		cancel()
		if err == nil {
			e.emitJSON(ctx, core.EventLLMReturned, runID, llmReturnedPayload(map[string]any{
				"profile":    agent.LLM,
				"structured": true,
				"attempt":    attempt,
			}, string(raw)))
			e.finishLLMCall(ctx, span, agent.LLM, start, nil)
			return raw, nil
		}
		lastErr = err
		if !shouldRetry(ctx, err) || attempt == attempts {
			e.finishLLMCall(ctx, span, agent.LLM, start, err)
			return nil, err
		}
		if err := retryDelay(ctx, attempt); err != nil {
			e.finishLLMCall(ctx, span, agent.LLM, start, err)
			return nil, err
		}
	}
	e.finishLLMCall(ctx, span, agent.LLM, start, lastErr)
	return nil, lastErr
}
