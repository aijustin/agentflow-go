// Package testutil provides helpers for testing applications built on agentflow.
package testutil

import (
	"context"
	"encoding/json"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

// StaticGateway returns a fixed assistant message for every chat request.
type StaticGateway struct {
	Content string
}

func (g StaticGateway) Supports(string, llm.Capability) bool { return true }

func (g StaticGateway) Chat(_ context.Context, _ string, _ llm.ChatRequest) (llm.ChatResponse, error) {
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: g.Content}}, nil
}

// StructuredGateway returns a fixed structured payload for every structured chat request.
type StructuredGateway struct {
	Payload json.RawMessage
}

func (g StructuredGateway) Supports(string, llm.Capability) bool { return true }

func (g StructuredGateway) StructuredChat(_ context.Context, _ string, _ json.RawMessage, _ llm.ChatRequest) (json.RawMessage, error) {
	return g.Payload, nil
}

// NoopTool is a tool executor that returns an empty successful result.
type NoopTool struct{}

func (NoopTool) Execute(context.Context, core.ToolCall) (core.ToolResult, error) {
	return core.ToolResult{}, nil
}
