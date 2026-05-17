package router

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	llmmock "github.com/aijustin/agentflow-go/internal/adapter/llm/mock"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestGatewayRoutesByProfile(t *testing.T) {
	mock := llmmock.NewGateway()
	mock.QueueChat("planner", llm.ChatResponse{Message: llm.Message{Content: "planned"}})
	gateway := New(map[string]llm.Gateway{"planner": mock})

	resp, err := gateway.Chat(context.Background(), "planner", llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "planned" {
		t.Fatalf("got %q", resp.Message.Content)
	}
}

func TestGatewayRoutesToolCallsByProfile(t *testing.T) {
	mock := llmmock.NewGateway()
	mock.SetCapabilities("planner", llm.CapChat, llm.CapToolCall)
	mock.QueueToolCall("planner", llm.ToolCallResponse{ToolCalls: []llm.ToolCall{{ID: "1", Name: "echo"}}})
	gateway := New(map[string]llm.Gateway{"planner": mock})

	resp, err := gateway.ChatWithTools(context.Background(), "planner", llm.ToolCallRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "echo" {
		t.Fatalf("unexpected tool call response: %+v", resp)
	}
}

func TestGatewayRoutesStructuredAndStreamByProfile(t *testing.T) {
	mock := llmmock.NewGateway()
	mock.SetCapabilities("planner", llm.CapChat, llm.CapStructuredOutput, llm.CapStream)
	mock.QueueStructured("planner", json.RawMessage(`{"ok":true}`))
	mock.QueueChat("planner", llm.ChatResponse{Message: llm.Message{Content: "stream"}})
	gateway := New(map[string]llm.Gateway{"planner": mock})

	raw, err := gateway.StructuredChat(context.Background(), "planner", json.RawMessage(`{"type":"object"}`), llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"ok":true}` {
		t.Fatalf("unexpected structured response: %s", raw)
	}
	ch, err := gateway.StreamChat(context.Background(), "planner", llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if chunk := <-ch; chunk.Content != "stream" || !chunk.Done {
		t.Fatalf("unexpected stream chunk: %+v", chunk)
	}
}

func TestGatewayRoutesEmbeddingsByProfile(t *testing.T) {
	mock := llmmock.NewGateway()
	mock.SetCapabilities("embedding", llm.CapChat, llm.CapEmbed)
	mock.QueueEmbedding("embedding", [][]float32{{1, 2, 3}})
	gateway := New(map[string]llm.Gateway{"embedding": mock})

	vectors, err := gateway.Embed(context.Background(), "embedding", []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || len(vectors[0]) != 3 || vectors[0][2] != 3 {
		t.Fatalf("unexpected embeddings: %+v", vectors)
	}
}

func TestGatewayUnknownProfile(t *testing.T) {
	_, err := New(nil).Chat(context.Background(), "missing", llm.ChatRequest{})
	if err == nil || !strings.Contains(err.Error(), "profile") {
		t.Fatalf("expected profile error, got %v", err)
	}
}

func TestGatewayPropagatesRouteError(t *testing.T) {
	mock := llmmock.NewGateway()
	_, err := New(map[string]llm.Gateway{"planner": mock}).Chat(context.Background(), "planner", llm.ChatRequest{})
	if !errors.Is(err, llmmock.ErrNoResponse) {
		t.Fatalf("expected ErrNoResponse, got %v", err)
	}
}
