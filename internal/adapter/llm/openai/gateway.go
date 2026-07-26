package openai

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/aijustin/agentflow-go/internal/httpclient"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

type Gateway struct {
	profiles map[string]llm.Profile
	client   *http.Client
}

func NewGateway(profiles []llm.Profile, client *http.Client) *Gateway {
	if client == nil {
		client = httpclient.NewLongResponse()
	}
	index := make(map[string]llm.Profile, len(profiles))
	for _, profile := range profiles {
		index[profile.Name] = profile
	}
	return &Gateway{profiles: index, client: client}
}

func (g *Gateway) Supports(profile string, cap llm.Capability) bool {
	profileConfig, ok := g.profiles[profile]
	if !ok {
		return false
	}
	return llm.ProfileSupports(profileConfig, cap, llm.CapChat, llm.CapToolCall, llm.CapStructuredOutput, llm.CapStream)
}

func (g *Gateway) Chat(ctx context.Context, profileName string, req llm.ChatRequest) (llm.ChatResponse, error) {
	resp, err := g.chat(ctx, profileName, req, nil)
	if err != nil {
		return llm.ChatResponse{}, err
	}
	return resp.ChatResponse, nil
}

func (g *Gateway) ChatWithTools(ctx context.Context, profileName string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	resp, err := g.chat(ctx, profileName, req.ChatRequest, req.Tools)
	if err != nil {
		return llm.ToolCallResponse{}, err
	}
	return normalizeContentToolCalls(resp, req.Tools), nil
}

func (g *Gateway) StructuredChat(ctx context.Context, profileName string, schema json.RawMessage, req llm.ChatRequest) (json.RawMessage, error) {
	if len(schema) == 0 || !json.Valid(schema) {
		return nil, fmt.Errorf("openai: structured output schema must be valid JSON")
	}
	if req.ExtraBody == nil {
		req.ExtraBody = make(map[string]any)
	} else {
		req.ExtraBody = cloneExtraBody(req.ExtraBody)
	}
	req.ExtraBody["response_format"] = map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   "structured_output",
			"schema": json.RawMessage(schema),
			"strict": true,
		},
	}
	resp, err := g.Chat(ctx, profileName, req)
	if err != nil {
		return nil, err
	}
	raw := json.RawMessage(strings.TrimSpace(resp.Message.Content))
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, fmt.Errorf("openai: structured response was not valid JSON")
	}
	return raw, nil
}

func (g *Gateway) StreamChat(ctx context.Context, profileName string, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	return g.streamChat(ctx, profileName, req, nil)
}

// StreamChatWithTools streams a completion with tools enabled. Content deltas
// are forwarded immediately for live UI. On finish, tool-call turns emit only
// tool-call chunks (no bulk content re-emit); final-answer turns emit Done only
// because prose was already streamed. Content-encoded tool calls are still
// normalized from the aggregated buffer before the terminal Done chunk.
// Consumers must treat streamed content as presentation-only: tool-turn
// preambles may appear before tool_call chunks; authoritative final text comes
// from the completed run snapshot / StreamFrameDone.Result.Output.
func (g *Gateway) StreamChatWithTools(ctx context.Context, profileName string, req llm.ToolCallRequest) (<-chan llm.ChatChunk, error) {
	return g.streamChat(ctx, profileName, req.ChatRequest, req.Tools)
}

func (g *Gateway) streamChat(ctx context.Context, profileName string, req llm.ChatRequest, tools []llm.ToolSpec) (<-chan llm.ChatChunk, error) {
	profile, ok := g.profiles[profileName]
	if !ok {
		return nil, fmt.Errorf("openai: profile %q not found", profileName)
	}
	if req.ExtraBody == nil {
		req.ExtraBody = make(map[string]any)
	} else {
		req.ExtraBody = cloneExtraBody(req.ExtraBody)
	}
	req.ExtraBody["stream"] = true
	// OpenAI-compatible providers only return usage on the final stream chunk when
	// include_usage is requested; without it, TokenUsage stays zero for StreamRun.
	if _, ok := req.ExtraBody["stream_options"]; !ok {
		req.ExtraBody["stream_options"] = map[string]any{"include_usage": true}
	}
	httpReq, err := g.chatRequest(ctx, profile, req, tools)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, openAIAPIError(resp)
	}
	ch := make(chan llm.ChatChunk)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		// send blocks until the consumer reads or the context is cancelled, so
		// an abandoned stream cannot leak this goroutine or the response body.
		send := func(c llm.ChatChunk) bool {
			select {
			case ch <- c:
				return true
			case <-ctx.Done():
				return false
			}
		}
		var aggregated strings.Builder
		var usage llm.TokenUsage
		normalizeTools := len(tools) > 0
		// Providers that honor stream_options.include_usage emit usage in a chunk
		// *after* finish_reason. Do not finalize on Done; wait for [DONE]/EOF.
		finished := false
		// Aggregate tool-enabled content for content-encoded tool-call detection
		// at finish, while still forwarding each delta for live presentation.
		nativeCalls := map[int]*streamToolCallAcc{}
		finish := func(doneChunk llm.ChatChunk) {
			if normalizeTools {
				if doneChunk.Error != "" {
					send(llm.ChatChunk{Done: true, Error: doneChunk.Error, Err: doneChunk.Err, Usage: usage})
					return
				}
				if calls := finalizeStreamToolCalls(nativeCalls); len(calls) > 0 {
					for _, call := range calls {
						if !send(llm.ChatChunk{
							Kind:       llm.ChunkKindToolCall,
							ToolCallID: call.ID,
							ToolName:   call.Name,
							ToolInput:  call.Input,
						}) {
							return
						}
					}
					send(llm.ChatChunk{Done: true, Usage: usage})
					return
				}
				normalized := normalizeContentToolCalls(llm.ToolCallResponse{
					ChatResponse: llm.ChatResponse{
						Message:      llm.Message{Role: llm.RoleAssistant, Content: aggregated.String()},
						Usage:        usage,
						FinishReason: "stop",
					},
				}, tools)
				if len(normalized.ToolCalls) > 0 {
					for _, call := range normalized.ToolCalls {
						if !send(llm.ChatChunk{
							Kind:       llm.ChunkKindToolCall,
							ToolCallID: call.ID,
							ToolName:   call.Name,
							ToolInput:  call.Input,
						}) {
							return
						}
					}
					send(llm.ChatChunk{Done: true, Usage: usage})
					return
				}
				// Final prose was already forwarded per delta; only signal Done.
				send(llm.ChatChunk{Done: true, Usage: usage})
				return
			}
			if doneChunk.Error != "" {
				send(llm.ChatChunk{Done: true, Error: doneChunk.Error, Err: doneChunk.Err, Usage: usage})
				return
			}
			send(llm.ChatChunk{Done: true, Usage: usage})
		}
		scanner := bufio.NewScanner(resp.Body)
		// SSE frames carry full JSON deltas on one line and OpenAI-compatible
		// providers emit large single chunks (base64 images, big tool calls);
		// the 64KB default would abort the stream on them. Align with the MCP
		// stdio client's 16MB line cap.
		scanner.Buffer(make([]byte, 64*1024), 16<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				finish(llm.ChatChunk{Done: true, Usage: usage})
				return
			}
			if streamErr := decodeStreamErrorPayload([]byte(data)); streamErr != nil {
				// OpenAI-compatible providers signal mid-stream failures as a
				// data line carrying an error object; surface it with the
				// structured error attached so retry classification works.
				finish(llm.ChatChunk{Done: true, Error: streamErr.Error(), Err: streamErr, Usage: usage})
				return
			}
			delta, err := decodeStreamDelta([]byte(data))
			if err != nil {
				finish(llm.ChatChunk{Done: true, Error: err.Error(), Err: err, Usage: usage})
				return
			}
			if tokenUsagePresent(delta.Usage) {
				usage = normalizeTokenUsage(delta.Usage)
			}
			if normalizeTools {
				if len(delta.ToolCalls) > 0 {
					mergeStreamToolCallDeltas(nativeCalls, delta.ToolCalls)
				}
				if delta.Content != "" {
					aggregated.WriteString(delta.Content)
					if !send(llm.ChatChunk{Content: delta.Content, Usage: usage}) {
						return
					}
				}
				if delta.Done {
					finished = true
				}
				continue
			}
			if delta.Content != "" {
				if !send(llm.ChatChunk{Content: delta.Content, Usage: usage}) {
					return
				}
			}
			if delta.Done {
				finished = true
			}
		}
		if err := scanner.Err(); err != nil {
			finish(llm.ChatChunk{Done: true, Error: err.Error(), Err: err, Usage: usage})
			return
		}
		if normalizeTools || finished {
			finish(llm.ChatChunk{Done: true, Usage: usage})
		}
	}()
	return ch, nil
}

func (g *Gateway) Embed(ctx context.Context, profileName string, input []string) ([][]float32, error) {
	profile, ok := g.profiles[profileName]
	if !ok {
		return nil, fmt.Errorf("openai: profile %q not found", profileName)
	}
	if len(input) == 0 {
		return nil, fmt.Errorf("openai: embedding input is empty")
	}
	endpoint := strings.TrimRight(profile.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}
	payload, err := json.Marshal(map[string]any{"model": profile.Model, "input": input})
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/embeddings", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	authorizeRequest(httpReq, profile)
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, openAIAPIError(resp)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var decoded struct {
		Data []struct {
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return nil, embedNonJSONError(endpoint+"/embeddings", resp, raw, err)
	}
	if len(decoded.Data) != len(input) {
		return nil, fmt.Errorf("openai: embedding response count %d did not match input count %d", len(decoded.Data), len(input))
	}
	vectors := make([][]float32, len(decoded.Data))
	for index, item := range decoded.Data {
		vectors[index] = append([]float32(nil), item.Embedding...)
	}
	return vectors, nil
}

func (g *Gateway) chat(ctx context.Context, profileName string, req llm.ChatRequest, tools []llm.ToolSpec) (llm.ToolCallResponse, error) {
	profile, ok := g.profiles[profileName]
	if !ok {
		return llm.ToolCallResponse{}, fmt.Errorf("openai: profile %q not found", profileName)
	}
	httpReq, err := g.chatRequest(ctx, profile, req, tools)
	if err != nil {
		return llm.ToolCallResponse{}, err
	}
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return llm.ToolCallResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llm.ToolCallResponse{}, openAIAPIError(resp)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.ToolCallResponse{}, err
	}
	return decodeChatResponse(raw)
}

func (g *Gateway) chatRequest(ctx context.Context, profile llm.Profile, req llm.ChatRequest, tools []llm.ToolSpec) (*http.Request, error) {
	endpoint := strings.TrimRight(profile.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://api.openai.com/v1"
	}
	body := map[string]any{
		"model":    profile.Model,
		"messages": openAIMessages(req.Messages),
	}
	if len(tools) > 0 {
		body["tools"] = openAITools(tools)
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.MaxTokens > 0 {
		body["max_tokens"] = req.MaxTokens
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if req.ReasoningEffort != "" {
		body["reasoning_effort"] = req.ReasoningEffort
	}
	if req.Thinking.Enabled {
		body["thinking"] = map[string]any{
			"enabled":       true,
			"budget_tokens": req.Thinking.BudgetTokens,
		}
	}
	for key, value := range req.ExtraBody {
		body[key] = value
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/chat/completions", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	authorizeRequest(httpReq, profile)
	return httpReq, nil
}

func authorizeRequest(httpReq *http.Request, profile llm.Profile) {
	if key := profile.Metadata["api_key"]; key != "" {
		httpReq.Header.Set("Authorization", "Bearer "+key)
	} else if profile.APIKeyEnv != "" {
		if key := os.Getenv(profile.APIKeyEnv); key != "" {
			httpReq.Header.Set("Authorization", "Bearer "+key)
		}
	}
}

func openAIAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return llm.APIError{Provider: "openai", StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(body))}
}

// embedNonJSONError wraps json.Unmarshal failures for 2xx embedding responses that are
// HTML/SPA fallbacks or other non-JSON bodies (often caused by a base_url missing /v1).
func embedNonJSONError(url string, resp *http.Response, raw []byte, cause error) error {
	preview := strings.TrimSpace(string(raw))
	if len(preview) > 160 {
		preview = preview[:160]
	}
	ct := resp.Header.Get("Content-Type")
	kind := "non-JSON"
	trimmed := strings.TrimLeft(preview, " \t\r\n")
	if strings.HasPrefix(trimmed, "<") {
		kind = "HTML"
	}
	return fmt.Errorf("openai: embed %s returned %s body (status=%d content-type=%q): %w; body_prefix=%q",
		url, kind, resp.StatusCode, ct, cause, preview)
}

func decodeChatResponse(raw []byte) (llm.ToolCallResponse, error) {
	var decoded struct {
		Choices []struct {
			Message struct {
				Role             llm.Role `json:"role"`
				Content          string   `json:"content"`
				ReasoningContent string   `json:"reasoning_content"`
				ToolCalls        []struct {
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string          `json:"name"`
						Arguments json.RawMessage `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens            int `json:"prompt_tokens"`
			CompletionTokens        int `json:"completion_tokens"`
			TotalTokens             int `json:"total_tokens"`
			InputTokens             int `json:"input_tokens"`
			OutputTokens            int `json:"output_tokens"`
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return llm.ToolCallResponse{}, err
	}
	if len(decoded.Choices) == 0 {
		return llm.ToolCallResponse{}, fmt.Errorf("openai: response contained no choices")
	}
	choice := decoded.Choices[0]
	usage := normalizeTokenUsage(llm.TokenUsage{
		InputTokens:     firstNonZero(decoded.Usage.PromptTokens, decoded.Usage.InputTokens),
		OutputTokens:    firstNonZero(decoded.Usage.CompletionTokens, decoded.Usage.OutputTokens),
		ReasoningTokens: decoded.Usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:     decoded.Usage.TotalTokens,
	})
	message := llm.Message{
		Role:             choice.Message.Role,
		Content:          choice.Message.Content,
		ReasoningContent: choice.Message.ReasoningContent,
	}
	toolCalls := make([]llm.ToolCall, 0, len(choice.Message.ToolCalls))
	for _, call := range choice.Message.ToolCalls {
		toolCall := llm.ToolCall{
			ID:    call.ID,
			Name:  call.Function.Name,
			Input: normalizeToolArguments(call.Function.Arguments),
		}
		message.ToolCalls = append(message.ToolCalls, toolCall)
		toolCalls = append(toolCalls, toolCall)
	}
	return llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{
			Message:      message,
			Usage:        usage,
			FinishReason: choice.FinishReason,
			Raw:          raw,
		},
		ToolCalls: toolCalls,
	}, nil
}

type streamToolCallDelta struct {
	Index     int
	ID        string
	Name      string
	Arguments string
}

type streamToolCallAcc struct {
	ID        string
	Name      string
	Arguments strings.Builder
}

type streamDelta struct {
	Content   string
	Done      bool
	Usage     llm.TokenUsage
	ToolCalls []streamToolCallDelta
}

// decodeStreamErrorPayload recognizes the error object OpenAI-compatible
// providers emit as a data line when a stream fails mid-flight
// ({"error":{"message","type","code"}}). It returns nil for ordinary delta
// payloads. The mapped llm.APIError keeps retry classification working on
// the streaming path: numeric codes are honored directly, well-known string
// codes map to their HTTP equivalents, and anything else is treated as a
// server-side (retryable) failure because the provider already accepted the
// request.
func decodeStreamErrorPayload(raw []byte) error {
	var decoded struct {
		Error *struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil || decoded.Error == nil {
		return nil
	}
	status := 500
	switch code := decoded.Error.Code.(type) {
	case float64:
		status = int(code)
	case string:
		switch code {
		case "rate_limit_exceeded", "rate_limit_error":
			status = 429
		case "context_length_exceeded", "invalid_request_error":
			status = 400
		}
	}
	return llm.APIError{
		Provider:   "openai",
		StatusCode: status,
		Status:     fmt.Sprintf("%d", status),
		Body:       decoded.Error.Message,
	}
}

func decodeStreamDelta(raw []byte) (streamDelta, error) {
	var decoded struct {
		Choices []struct {
			Delta struct {
				Content   string `json:"content"`
				ToolCalls []struct {
					Index    int    `json:"index"`
					ID       string `json:"id"`
					Type     string `json:"type"`
					Function struct {
						Name      string `json:"name"`
						Arguments string `json:"arguments"`
					} `json:"function"`
				} `json:"tool_calls"`
			} `json:"delta"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens            int `json:"prompt_tokens"`
			CompletionTokens        int `json:"completion_tokens"`
			TotalTokens             int `json:"total_tokens"`
			InputTokens             int `json:"input_tokens"`
			OutputTokens            int `json:"output_tokens"`
			CompletionTokensDetails struct {
				ReasoningTokens int `json:"reasoning_tokens"`
			} `json:"completion_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return streamDelta{}, err
	}
	out := streamDelta{
		Usage: normalizeTokenUsage(llm.TokenUsage{
			InputTokens:     firstNonZero(decoded.Usage.PromptTokens, decoded.Usage.InputTokens),
			OutputTokens:    firstNonZero(decoded.Usage.CompletionTokens, decoded.Usage.OutputTokens),
			ReasoningTokens: decoded.Usage.CompletionTokensDetails.ReasoningTokens,
			TotalTokens:     decoded.Usage.TotalTokens,
		}),
	}
	if len(decoded.Choices) > 0 {
		choice := decoded.Choices[0]
		out.Content = choice.Delta.Content
		out.Done = choice.FinishReason != ""
		if len(choice.Delta.ToolCalls) > 0 {
			out.ToolCalls = make([]streamToolCallDelta, 0, len(choice.Delta.ToolCalls))
			for _, call := range choice.Delta.ToolCalls {
				out.ToolCalls = append(out.ToolCalls, streamToolCallDelta{
					Index:     call.Index,
					ID:        call.ID,
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				})
			}
		}
	}
	return out, nil
}

func mergeStreamToolCallDeltas(dst map[int]*streamToolCallAcc, deltas []streamToolCallDelta) {
	for _, delta := range deltas {
		acc, ok := dst[delta.Index]
		if !ok {
			acc = &streamToolCallAcc{}
			dst[delta.Index] = acc
		}
		if delta.ID != "" {
			acc.ID = delta.ID
		}
		if delta.Name != "" {
			acc.Name = delta.Name
		}
		if delta.Arguments != "" {
			acc.Arguments.WriteString(delta.Arguments)
		}
	}
}

func finalizeStreamToolCalls(acc map[int]*streamToolCallAcc) []llm.ToolCall {
	if len(acc) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(acc))
	for index := range acc {
		indexes = append(indexes, index)
	}
	slices.Sort(indexes)
	out := make([]llm.ToolCall, 0, len(indexes))
	for _, index := range indexes {
		item := acc[index]
		if item == nil || strings.TrimSpace(item.Name) == "" {
			continue
		}
		id := item.ID
		if id == "" {
			id = fmt.Sprintf("call_%d", index)
		}
		out = append(out, llm.ToolCall{
			ID:    id,
			Name:  item.Name,
			Input: normalizeToolArguments(json.RawMessage(item.Arguments.String())),
		})
	}
	return out
}

func cloneExtraBody(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func openAIMessages(messages []llm.Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		item := map[string]any{
			"role": msg.Role,
		}
		if msg.Content != "" || msg.Role != llm.RoleAssistant || len(msg.ToolCalls) == 0 {
			item["content"] = msg.Content
		}
		if msg.Name != "" && msg.Role != llm.RoleTool {
			item["name"] = msg.Name
		}
		if msg.Role == llm.RoleTool {
			item["tool_call_id"] = msg.ToolCallID
			item["content"] = msg.Content
		}
		if len(msg.ToolCalls) > 0 {
			item["tool_calls"] = openAIToolCalls(msg.ToolCalls)
		}
		out = append(out, item)
	}
	return out
}

func openAITools(tools []llm.ToolSpec) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		parameters := json.RawMessage(`{"type":"object"}`)
		if len(tool.Schema) > 0 {
			parameters = tool.Schema
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        tool.Name,
				"description": tool.Description,
				"parameters":  parameters,
			},
		})
	}
	return out
}

func openAIToolCalls(calls []llm.ToolCall) []map[string]any {
	out := make([]map[string]any, 0, len(calls))
	for _, call := range calls {
		args := "{}"
		if len(call.Input) > 0 {
			args = string(normalizeToolArguments(call.Input))
		}
		out = append(out, map[string]any{
			"id":   call.ID,
			"type": "function",
			"function": map[string]any{
				"name":      call.Name,
				"arguments": args,
			},
		})
	}
	return out
}

func normalizeToolArguments(raw json.RawMessage) json.RawMessage {
	return llm.NormalizeToolArguments(raw)
}

func tokenUsagePresent(usage llm.TokenUsage) bool {
	return usage.TotalTokens > 0 || usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.ReasoningTokens > 0
}

func normalizeTokenUsage(usage llm.TokenUsage) llm.TokenUsage {
	if usage.TotalTokens == 0 && (usage.InputTokens > 0 || usage.OutputTokens > 0) {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
