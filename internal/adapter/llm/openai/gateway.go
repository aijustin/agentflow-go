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
	"strings"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

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
	httpReq, err := g.chatRequest(ctx, profile, req, nil)
	if err != nil {
		return nil, err
	}
	resp, err := g.client.Do(httpReq)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		defer resp.Body.Close()
		return nil, fmt.Errorf("openai: unexpected status %s", resp.Status)
	}
	ch := make(chan llm.ChatChunk)
	go func() {
		defer close(ch)
		defer resp.Body.Close()
		scanner := bufio.NewScanner(resp.Body)
		sentDone := false
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
				if !sentDone {
					ch <- llm.ChatChunk{Done: true}
				}
				return
			}
			chunk, err := decodeStreamChunk([]byte(data))
			if err != nil {
				ch <- llm.ChatChunk{Done: true, Error: err.Error()}
				return
			}
			if chunk.Content != "" || chunk.Done || chunk.Usage.TotalTokens > 0 {
				ch <- chunk
				if chunk.Done {
					sentDone = true
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
		return nil, fmt.Errorf("openai: unexpected status %s", resp.Status)
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
		return nil, err
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
		return llm.ToolCallResponse{}, fmt.Errorf("openai: unexpected status %s", resp.Status)
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
	usage := llm.TokenUsage{
		InputTokens:     firstNonZero(decoded.Usage.PromptTokens, decoded.Usage.InputTokens),
		OutputTokens:    firstNonZero(decoded.Usage.CompletionTokens, decoded.Usage.OutputTokens),
		ReasoningTokens: decoded.Usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:     decoded.Usage.TotalTokens,
	}
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

func decodeStreamChunk(raw []byte) (llm.ChatChunk, error) {
	var decoded struct {
		Choices []struct {
			Delta struct {
				Content string `json:"content"`
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
		return llm.ChatChunk{}, err
	}
	chunk := llm.ChatChunk{}
	if len(decoded.Choices) > 0 {
		chunk.Content = decoded.Choices[0].Delta.Content
		chunk.Done = decoded.Choices[0].FinishReason != ""
	}
	chunk.Usage = llm.TokenUsage{
		InputTokens:     firstNonZero(decoded.Usage.PromptTokens, decoded.Usage.InputTokens),
		OutputTokens:    firstNonZero(decoded.Usage.CompletionTokens, decoded.Usage.OutputTokens),
		ReasoningTokens: decoded.Usage.CompletionTokensDetails.ReasoningTokens,
		TotalTokens:     decoded.Usage.TotalTokens,
	}
	return chunk, nil
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

func firstNonZero(values ...int) int {
	for _, value := range values {
		if value != 0 {
			return value
		}
	}
	return 0
}
