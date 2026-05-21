package mock

import (
	"context"
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

// FallbackGateway wraps a mock gateway and returns a deterministic assistant
// reply when no queued response is available. This makes local development
// usable without manually seeding mock responses.
type FallbackGateway struct {
	*Gateway
}

// NewFallbackGateway creates a mock gateway with echo-style fallbacks.
func NewFallbackGateway() *FallbackGateway {
	return &FallbackGateway{Gateway: NewGateway()}
}

func (g *FallbackGateway) Chat(ctx context.Context, profile string, req llm.ChatRequest) (llm.ChatResponse, error) {
	resp, err := g.Gateway.Chat(ctx, profile, req)
	if err == nil {
		return resp, nil
	}
	if err != ErrNoResponse {
		return llm.ChatResponse{}, err
	}
	return llm.ChatResponse{
		Message: llm.Message{Role: llm.RoleAssistant, Content: fallbackContent(req.Messages)},
	}, nil
}

func (g *FallbackGateway) ChatWithTools(ctx context.Context, profile string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	resp, err := g.Gateway.ChatWithTools(ctx, profile, req)
	if err == nil {
		return resp, nil
	}
	if err != ErrNoResponse {
		return llm.ToolCallResponse{}, err
	}
	return llm.ToolCallResponse{
		ChatResponse: llm.ChatResponse{
			Message: llm.Message{Role: llm.RoleAssistant, Content: fallbackContent(req.Messages)},
		},
	}, nil
}

func (g *FallbackGateway) StructuredChat(ctx context.Context, profile string, schema json.RawMessage, req llm.ChatRequest) (json.RawMessage, error) {
	out, err := g.Gateway.StructuredChat(ctx, profile, schema, req)
	if err == nil {
		return out, nil
	}
	if err != ErrNoResponse {
		return nil, err
	}
	payload, err := json.Marshal(map[string]string{"answer": fallbackContent(req.Messages)})
	if err != nil {
		return nil, err
	}
	return payload, nil
}

func fallbackContent(messages []llm.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == llm.RoleUser && messages[i].Content != "" {
			return messages[i].Content
		}
	}
	return "ok"
}
