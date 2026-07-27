package anthropic

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/aijustin/agentflow-go/internal/httpclient"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

const structuredToolName = "structured_output"

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
	if cap == llm.CapEmbed {
		return false
	}
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
	return g.chat(ctx, profileName, req.ChatRequest, req.Tools)
}

func (g *Gateway) StructuredChat(ctx context.Context, profileName string, schema json.RawMessage, req llm.ChatRequest) (json.RawMessage, error) {
	if len(schema) == 0 || !json.Valid(schema) {
		return nil, fmt.Errorf("anthropic: structured output schema must be valid JSON")
	}
	if req.ExtraBody == nil {
		req.ExtraBody = make(map[string]any)
	} else {
		req.ExtraBody = cloneExtraBody(req.ExtraBody)
	}
	req.ExtraBody["tool_choice"] = map[string]any{"type": "tool", "name": structuredToolName}
	resp, err := g.ChatWithTools(ctx, profileName, llm.ToolCallRequest{
		ChatRequest: req,
		Tools: []llm.ToolSpec{{
			Name:        structuredToolName,
			Description: "Return the response as JSON matching the requested schema.",
			Schema:      schema,
		}},
	})
	if err != nil {
		return nil, err
	}
	for _, call := range resp.ToolCalls {
		if call.Name != structuredToolName {
			continue
		}
		raw := normalizeToolInput(call.Input)
		if !json.Valid(raw) {
			return nil, fmt.Errorf("anthropic: structured tool input was not valid JSON")
		}
		return raw, nil
	}
	raw := json.RawMessage(strings.TrimSpace(resp.Message.Content))
	if len(raw) == 0 || !json.Valid(raw) {
		return nil, fmt.Errorf("anthropic: structured response did not contain JSON")
	}
	return raw, nil
}

func (g *Gateway) StreamChat(ctx context.Context, profileName string, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	return g.streamChat(ctx, profileName, req, nil)
}

// StreamChatWithTools streams a completion with tools enabled. Text deltas are
// forwarded as they arrive for live presentation; tool_use blocks are
// accumulated from their input_json_delta fragments and emitted as tool-call
// chunks just before the terminal Done, matching the OpenAI adapter's
// contract.
func (g *Gateway) StreamChatWithTools(ctx context.Context, profileName string, req llm.ToolCallRequest) (<-chan llm.ChatChunk, error) {
	return g.streamChat(ctx, profileName, req.ChatRequest, req.Tools)
}

func (g *Gateway) streamChat(ctx context.Context, profileName string, req llm.ChatRequest, tools []llm.ToolSpec) (<-chan llm.ChatChunk, error) {
	profile, ok := g.profiles[profileName]
	if !ok {
		return nil, fmt.Errorf("anthropic: profile %q not found", profileName)
	}
	if req.ExtraBody == nil {
		req.ExtraBody = make(map[string]any)
	} else {
		req.ExtraBody = cloneExtraBody(req.ExtraBody)
	}
	req.ExtraBody["stream"] = true
	httpReq, err := g.messageRequest(ctx, profile, req, tools)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, anthropicAPIError(resp)
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
		var usage llm.TokenUsage
		blocks := map[int]*streamBlock{}
		sentDone := false
		// finish emits the accumulated tool calls followed by exactly one
		// terminal Done. Every exit path routes through it: a consumer that
		// blocks on Done would otherwise hang when the stream ends without an
		// explicit message_stop, which a truncated connection or a proxy
		// timeout can easily produce.
		finish := func(streamErr error) {
			if sentDone {
				return
			}
			sentDone = true
			if streamErr != nil {
				send(llm.ChatChunk{Done: true, Error: streamErr.Error(), Err: streamErr, Usage: usage})
				return
			}
			for _, call := range finalizeStreamBlocks(blocks) {
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
		}
		scanner := bufio.NewScanner(resp.Body)
		// A tool_use block's arguments arrive as one SSE frame per fragment but
		// a single frame can still be large; match the OpenAI adapter's cap
		// rather than aborting the stream on the 64KB default.
		scanner.Buffer(make([]byte, 64*1024), 16<<20)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
				continue
			}
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				finish(nil)
				return
			}
			event, err := decodeStreamEvent([]byte(data))
			if err != nil {
				finish(err)
				return
			}
			if event.Err != nil {
				// An in-stream error event (e.g. overloaded_error); the
				// structured error rides along for retry classification.
				finish(event.Err)
				return
			}
			if tokenUsagePresent(event.Usage) {
				usage = mergeTokenUsage(usage, event.Usage)
			}
			switch event.Type {
			case "content_block_start":
				if event.ToolUse {
					blocks[event.Index] = &streamBlock{id: event.ToolID, name: event.ToolName}
				}
			case "content_block_delta":
				if event.Text != "" {
					if !send(llm.ChatChunk{Content: event.Text, Usage: usage}) {
						return
					}
				}
				if event.PartialJSON != "" {
					if block, ok := blocks[event.Index]; ok {
						block.input.WriteString(event.PartialJSON)
					}
				}
			case "message_delta":
				if event.StopReason != "" {
					finish(nil)
					return
				}
			case "message_stop":
				finish(nil)
				return
			}
		}
		if err := scanner.Err(); err != nil {
			finish(err)
			return
		}
		// Clean EOF without message_stop: the peer went away mid-stream. The
		// consumer still needs its terminal chunk.
		finish(nil)
	}()
	return ch, nil
}

// streamBlock accumulates one tool_use content block across its
// input_json_delta fragments.
type streamBlock struct {
	id    string
	name  string
	input strings.Builder
}

func finalizeStreamBlocks(blocks map[int]*streamBlock) []llm.ToolCall {
	if len(blocks) == 0 {
		return nil
	}
	indexes := make([]int, 0, len(blocks))
	for index := range blocks {
		indexes = append(indexes, index)
	}
	// Content block indexes define the order the model emitted the calls in.
	sort.Ints(indexes)
	calls := make([]llm.ToolCall, 0, len(indexes))
	for _, index := range indexes {
		block := blocks[index]
		if block.name == "" {
			continue
		}
		calls = append(calls, llm.ToolCall{
			ID:    block.id,
			Name:  block.name,
			Input: normalizeToolInput(json.RawMessage(block.input.String())),
		})
	}
	return calls
}

func tokenUsagePresent(usage llm.TokenUsage) bool {
	return usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.TotalTokens > 0 ||
		usage.CachedInputTokens > 0 || usage.CacheWriteTokens > 0
}

// mergeTokenUsage folds a usage update into the running total. Anthropic
// reports input and cache tokens on message_start and output tokens on
// message_delta, so a later event carrying only output tokens must not erase
// the input side.
func mergeTokenUsage(current, update llm.TokenUsage) llm.TokenUsage {
	if update.InputTokens > 0 {
		current.InputTokens = update.InputTokens
	}
	if update.OutputTokens > 0 {
		current.OutputTokens = update.OutputTokens
	}
	if update.CachedInputTokens > 0 {
		current.CachedInputTokens = update.CachedInputTokens
	}
	if update.CacheWriteTokens > 0 {
		current.CacheWriteTokens = update.CacheWriteTokens
	}
	current.TotalTokens = current.InputTokens + current.OutputTokens
	return current
}

func (g *Gateway) chat(ctx context.Context, profileName string, req llm.ChatRequest, tools []llm.ToolSpec) (llm.ToolCallResponse, error) {
	profile, ok := g.profiles[profileName]
	if !ok {
		return llm.ToolCallResponse{}, fmt.Errorf("anthropic: profile %q not found", profileName)
	}
	httpReq, err := g.messageRequest(ctx, profile, req, tools)
	if err != nil {
		return llm.ToolCallResponse{}, err
	}
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return llm.ToolCallResponse{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return llm.ToolCallResponse{}, anthropicAPIError(resp)
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return llm.ToolCallResponse{}, err
	}
	return decodeMessageResponse(raw)
}

func (g *Gateway) messageRequest(ctx context.Context, profile llm.Profile, req llm.ChatRequest, tools []llm.ToolSpec) (*http.Request, error) {
	endpoint := strings.TrimRight(profile.Endpoint, "/")
	if endpoint == "" {
		endpoint = "https://api.anthropic.com/v1"
	}
	system, messages := anthropicMessages(req.Messages)
	body := map[string]any{
		"model":      profile.Model,
		"messages":   messages,
		"max_tokens": req.MaxTokens,
	}
	if body["max_tokens"] == 0 {
		body["max_tokens"] = 1024
	}
	cache := profile.PromptCache.Enabled
	if system != "" {
		if cache {
			body["system"] = cacheableSystem(system)
		} else {
			body["system"] = system
		}
	}
	if len(tools) > 0 {
		body["tools"] = anthropicTools(tools, cache)
	}
	if cache {
		markLastMessageCacheable(messages)
	}
	if req.Temperature != nil {
		body["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		body["top_p"] = *req.TopP
	}
	if req.Thinking.Enabled {
		body["thinking"] = map[string]any{"type": "enabled", "budget_tokens": req.Thinking.BudgetTokens}
	}
	for key, value := range req.ExtraBody {
		body[key] = value
	}
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint+"/messages", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	authorizeRequest(httpReq, profile)
	return httpReq, nil
}

func authorizeRequest(httpReq *http.Request, profile llm.Profile) {
	if key := profile.Metadata["api_key"]; key != "" {
		httpReq.Header.Set("x-api-key", key)
		return
	}
	if profile.APIKeyEnv == "" {
		return
	}
	if key := os.Getenv(profile.APIKeyEnv); key != "" {
		httpReq.Header.Set("x-api-key", key)
	}
}

func anthropicMessages(messages []llm.Message) (string, []map[string]any) {
	system := make([]string, 0)
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case llm.RoleSystem:
			if msg.Content != "" {
				system = append(system, msg.Content)
			}
		case llm.RoleTool:
			out = append(out, map[string]any{
				"role": "user",
				"content": []map[string]any{{
					"type":        "tool_result",
					"tool_use_id": msg.ToolCallID,
					"content":     msg.Content,
				}},
			})
		case llm.RoleAssistant:
			out = append(out, anthropicAssistantMessage(msg))
		default:
			out = append(out, map[string]any{"role": "user", "content": msg.Content})
		}
	}
	return strings.Join(system, "\n\n"), out
}

func anthropicAssistantMessage(msg llm.Message) map[string]any {
	if len(msg.ToolCalls) == 0 {
		return map[string]any{"role": "assistant", "content": msg.Content}
	}
	content := make([]map[string]any, 0, len(msg.ToolCalls)+1)
	if msg.Content != "" {
		content = append(content, map[string]any{"type": "text", "text": msg.Content})
	}
	for _, call := range msg.ToolCalls {
		content = append(content, map[string]any{
			"type":  "tool_use",
			"id":    call.ID,
			"name":  call.Name,
			"input": toolInputValue(call.Input),
		})
	}
	return map[string]any{"role": "assistant", "content": content}
}

func anthropicTools(tools []llm.ToolSpec, cache bool) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, tool := range tools {
		schema := json.RawMessage(`{"type":"object"}`)
		if len(tool.Schema) > 0 {
			schema = tool.Schema
		}
		out = append(out, map[string]any{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": schema,
		})
	}
	// A breakpoint covers everything before it, so one marker on the last
	// tool caches the whole catalog. Tool schemas sit at the very front of
	// the prompt and are re-sent verbatim on every turn of a tool loop, which
	// makes them the single most valuable thing to cache.
	if cache && len(out) > 0 {
		out[len(out)-1]["cache_control"] = ephemeralCacheControl()
	}
	return out
}

// ephemeralCacheControl is Anthropic's cache breakpoint marker.
func ephemeralCacheControl() map[string]any {
	return map[string]any{"type": "ephemeral"}
}

// cacheableSystem rewrites the system prompt as a content block so it can
// carry a breakpoint, extending the cached prefix past the tool catalog.
func cacheableSystem(system string) []map[string]any {
	return []map[string]any{{
		"type":          "text",
		"text":          system,
		"cache_control": ephemeralCacheControl(),
	}}
}

// markLastMessageCacheable puts a breakpoint at the end of the conversation so
// the next turn, which appends to it, reads this turn's history from cache.
//
// Anthropic only accepts a breakpoint on a content block, so a message whose
// content is still a plain string is rewritten as a single text block. Empty
// content is skipped: a block with no text is rejected by the API.
func markLastMessageCacheable(messages []map[string]any) {
	for i := len(messages) - 1; i >= 0; i-- {
		switch content := messages[i]["content"].(type) {
		case string:
			if strings.TrimSpace(content) == "" {
				continue
			}
			messages[i]["content"] = []map[string]any{{
				"type":          "text",
				"text":          content,
				"cache_control": ephemeralCacheControl(),
			}}
			return
		case []map[string]any:
			if len(content) == 0 {
				continue
			}
			content[len(content)-1]["cache_control"] = ephemeralCacheControl()
			return
		}
	}
}

func decodeMessageResponse(raw []byte) (llm.ToolCallResponse, error) {
	var decoded struct {
		Content []struct {
			Type  string          `json:"type"`
			Text  string          `json:"text"`
			ID    string          `json:"id"`
			Name  string          `json:"name"`
			Input json.RawMessage `json:"input"`
		} `json:"content"`
		StopReason string       `json:"stop_reason"`
		Usage      usagePayload `json:"usage"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return llm.ToolCallResponse{}, err
	}
	var text strings.Builder
	toolCalls := make([]llm.ToolCall, 0)
	for _, block := range decoded.Content {
		switch block.Type {
		case "", "text":
			text.WriteString(block.Text)
		case "tool_use":
			toolCalls = append(toolCalls, llm.ToolCall{ID: block.ID, Name: block.Name, Input: normalizeToolInput(block.Input)})
		}
	}
	usage := decoded.Usage.tokenUsage()
	message := llm.Message{Role: llm.RoleAssistant, Content: text.String(), ToolCalls: toolCalls}
	return llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: message, Usage: usage, FinishReason: decoded.StopReason, Raw: raw},
		ToolCalls:    toolCalls,
	}, nil
}

// streamEvent is one decoded Anthropic SSE frame. Anthropic describes a
// response as indexed content blocks rather than a flat delta, so the decoder
// has to preserve which block a fragment belongs to: text and tool arguments
// interleave across blocks in a single turn.
type streamEvent struct {
	Type        string
	Index       int
	Text        string
	PartialJSON string
	ToolUse     bool
	ToolID      string
	ToolName    string
	StopReason  string
	Usage       llm.TokenUsage
	Err         error
}

func decodeStreamEvent(raw []byte) (streamEvent, error) {
	var decoded struct {
		Type         string `json:"type"`
		Index        int    `json:"index"`
		ContentBlock struct {
			Type string `json:"type"`
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"content_block"`
		Delta struct {
			Type        string `json:"type"`
			Text        string `json:"text"`
			PartialJSON string `json:"partial_json"`
			StopReason  string `json:"stop_reason"`
		} `json:"delta"`
		Message struct {
			Usage usagePayload `json:"usage"`
		} `json:"message"`
		Error *struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
		Usage usagePayload `json:"usage"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return streamEvent{}, err
	}
	event := streamEvent{Type: decoded.Type, Index: decoded.Index}
	switch decoded.Type {
	case "content_block_start":
		if decoded.ContentBlock.Type == "tool_use" {
			event.ToolUse = true
			event.ToolID = decoded.ContentBlock.ID
			event.ToolName = decoded.ContentBlock.Name
		}
	case "content_block_delta":
		switch decoded.Delta.Type {
		case "text_delta":
			event.Text = decoded.Delta.Text
		case "input_json_delta":
			event.PartialJSON = decoded.Delta.PartialJSON
		}
	case "message_delta":
		event.StopReason = decoded.Delta.StopReason
	case "error":
		// Anthropic signals mid-stream failures as an error event; attach the
		// structured APIError so retry classification works on the streaming
		// path exactly like on the unary path.
		event.Err = anthropicStreamError(decoded.Error)
	}
	// Usage rides on message_start under "message", and on message_delta at
	// the top level.
	payload := decoded.Usage
	if payload.empty() {
		payload = decoded.Message.Usage
	}
	event.Usage = payload.tokenUsage()
	return event, nil
}

// usagePayload is Anthropic's usage object. Cache reads and writes are
// reported *alongside* input_tokens rather than inside it, so input_tokens
// alone counts only the uncached remainder.
type usagePayload struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

func (u usagePayload) empty() bool {
	return u.InputTokens == 0 && u.OutputTokens == 0 &&
		u.CacheReadInputTokens == 0 && u.CacheCreationInputTokens == 0
}

// tokenUsage normalizes to the llm.TokenUsage contract, where InputTokens is
// the whole prompt and CachedInputTokens is the part of it served from cache.
// Anthropic splits those three counts apart, so the prompt total has to be
// reassembled before a cache hit rate means anything.
func (u usagePayload) tokenUsage() llm.TokenUsage {
	inputTokens := u.InputTokens + u.CacheReadInputTokens + u.CacheCreationInputTokens
	return llm.TokenUsage{
		InputTokens:       inputTokens,
		OutputTokens:      u.OutputTokens,
		CachedInputTokens: u.CacheReadInputTokens,
		CacheWriteTokens:  u.CacheCreationInputTokens,
		TotalTokens:       inputTokens + u.OutputTokens,
	}
}

func toolInputValue(raw json.RawMessage) any {
	var value any = map[string]any{}
	if len(raw) == 0 {
		return value
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return map[string]any{}
	}
	if encoded, ok := value.(string); ok {
		var decoded any
		if err := json.Unmarshal([]byte(encoded), &decoded); err == nil {
			return decoded
		}
	}
	return value
}

func normalizeToolInput(raw json.RawMessage) json.RawMessage {
	return llm.NormalizeToolArguments(raw)
}

func cloneExtraBody(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for key, value := range in {
		out[key] = value
	}
	return out
}

func anthropicAPIError(resp *http.Response) error {
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
	return llm.APIError{Provider: "anthropic", StatusCode: resp.StatusCode, Status: resp.Status, Body: strings.TrimSpace(string(body))}
}

// anthropicStreamError maps an in-stream error event to the equivalent
// APIError so streaming and unary failures classify identically. The status
// codes mirror what the API would have returned for the same condition:
// overloaded_error -> 529, rate_limit_error -> 429, everything else -> 500
// (server-side, retryable).
func anthropicStreamError(errPayload *struct {
	Type    string `json:"type"`
	Message string `json:"message"`
}) llm.APIError {
	status := 500
	typ := "api_error"
	if errPayload != nil && errPayload.Type != "" {
		typ = errPayload.Type
	}
	switch typ {
	case "overloaded_error":
		status = 529
	case "rate_limit_error":
		status = 429
	}
	body := typ
	if errPayload != nil && errPayload.Message != "" {
		body = errPayload.Message
	}
	return llm.APIError{Provider: "anthropic", StatusCode: status, Status: fmt.Sprintf("%d", status), Body: body}
}
