package router

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

type Gateway struct {
	routes map[string]llm.Gateway
}

func New(routes map[string]llm.Gateway) *Gateway {
	return &Gateway{routes: routes}
}

func (g *Gateway) Supports(profile string, cap llm.Capability) bool {
	route, ok := g.routes[profile]
	return ok && route.Supports(profile, cap)
}

func (g *Gateway) Chat(ctx context.Context, profile string, req llm.ChatRequest) (llm.ChatResponse, error) {
	route, ok := g.routes[profile]
	if !ok {
		return llm.ChatResponse{}, fmt.Errorf("llm router: profile %q not found", profile)
	}
	return route.Chat(ctx, profile, req)
}

func (g *Gateway) ChatWithTools(ctx context.Context, profile string, req llm.ToolCallRequest) (llm.ToolCallResponse, error) {
	route, ok := g.routes[profile]
	if !ok {
		return llm.ToolCallResponse{}, fmt.Errorf("llm router: profile %q not found", profile)
	}
	caller, ok := route.(llm.ToolCaller)
	if !ok {
		return llm.ToolCallResponse{}, fmt.Errorf("llm router: profile %q does not support tool calling", profile)
	}
	return caller.ChatWithTools(ctx, profile, req)
}

func (g *Gateway) StructuredChat(ctx context.Context, profile string, schema json.RawMessage, req llm.ChatRequest) (json.RawMessage, error) {
	route, ok := g.routes[profile]
	if !ok {
		return nil, fmt.Errorf("llm router: profile %q not found", profile)
	}
	outputter, ok := route.(llm.StructuredOutputter)
	if !ok {
		return nil, fmt.Errorf("llm router: profile %q does not support structured output", profile)
	}
	return outputter.StructuredChat(ctx, profile, schema, req)
}

func (g *Gateway) StreamChat(ctx context.Context, profile string, req llm.ChatRequest) (<-chan llm.ChatChunk, error) {
	route, ok := g.routes[profile]
	if !ok {
		return nil, fmt.Errorf("llm router: profile %q not found", profile)
	}
	streamer, ok := route.(llm.Streamer)
	if !ok {
		return nil, fmt.Errorf("llm router: profile %q does not support streaming", profile)
	}
	return streamer.StreamChat(ctx, profile, req)
}
