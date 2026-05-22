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
	"strings"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

const structuredToolName = "structured_output"

type Gateway struct {
	profiles map[string]llm.Profile
	client   *http.Client
}

func NewGateway(profiles []llm.Profile, client *http.Client) *Gateway {
	if client == nil {
		client = http.DefaultClient
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
	httpReq, err := g.messageRequest(ctx, profile, req, nil)
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
		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		sentDone := false
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
				if !sentDone {
					ch <- llm.ChatChunk{Done: true}
				}
				return
			}
			chunk, err := decodeStreamEvent([]byte(data))
			if err != nil {
				ch <- llm.ChatChunk{Done: true, Error: err.Error()}
				return
			}
			if chunk.Content != "" || chunk.Done || chunk.Usage.TotalTokens > 0 || chunk.Usage.OutputTokens > 0 {
				ch <- chunk
				if chunk.Done {
					return
				}
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- llm.ChatChunk{Done: true, Error: err.Error()}
		}
	}()
	return ch, nil
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
	if system != "" {
		body["system"] = system
	}
	if len(tools) > 0 {
		body["tools"] = anthropicTools(tools)
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

func anthropicTools(tools []llm.ToolSpec) []map[string]any {
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
	return out
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
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
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
	usage := llm.TokenUsage{
		InputTokens:  decoded.Usage.InputTokens,
		OutputTokens: decoded.Usage.OutputTokens,
		TotalTokens:  decoded.Usage.InputTokens + decoded.Usage.OutputTokens,
	}
	message := llm.Message{Role: llm.RoleAssistant, Content: text.String(), ToolCalls: toolCalls}
	return llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{Message: message, Usage: usage, FinishReason: decoded.StopReason, Raw: raw},
		ToolCalls:    toolCalls,
	}, nil
}

func decodeStreamEvent(raw []byte) (llm.ChatChunk, error) {
	var decoded struct {
		Type  string `json:"type"`
		Delta struct {
			Type       string `json:"type"`
			Text       string `json:"text"`
			StopReason string `json:"stop_reason"`
		} `json:"delta"`
		Usage struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return llm.ChatChunk{}, err
	}
	chunk := llm.ChatChunk{}
	switch decoded.Type {
	case "content_block_delta":
		if decoded.Delta.Type == "text_delta" {
			chunk.Content = decoded.Delta.Text
		}
	case "message_delta":
		chunk.Done = decoded.Delta.StopReason != ""
	case "message_stop":
		chunk.Done = true
	}
	chunk.Usage = llm.TokenUsage{
		InputTokens:  decoded.Usage.InputTokens,
		OutputTokens: decoded.Usage.OutputTokens,
		TotalTokens:  decoded.Usage.InputTokens + decoded.Usage.OutputTokens,
	}
	return chunk, nil
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
	if len(raw) == 0 {
		return json.RawMessage(`{}`)
	}
	var encoded string
	if err := json.Unmarshal(raw, &encoded); err == nil {
		if strings.TrimSpace(encoded) == "" {
			return json.RawMessage(`{}`)
		}
		return json.RawMessage(encoded)
	}
	return raw
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
