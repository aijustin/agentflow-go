package mock

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestGatewayChatQueueAndCapabilities(t *testing.T) {
	gateway := NewGateway()
	gateway.SetCapabilities("planner", llm.CapChat, llm.CapToolCall)
	gateway.QueueChat("planner", llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: "answer"}})

	if !gateway.Supports("planner", llm.CapToolCall) {
		t.Fatal("expected tool-call capability")
	}
	resp, err := gateway.Chat(context.Background(), "planner", llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "answer" {
		t.Fatalf("got %q", resp.Message.Content)
	}
	if _, err := gateway.Chat(context.Background(), "planner", llm.ChatRequest{}); !errors.Is(err, ErrNoResponse) {
		t.Fatalf("expected ErrNoResponse, got %v", err)
	}
}

func TestGatewayToolCallAndStream(t *testing.T) {
	gateway := NewGateway()
	gateway.QueueToolCall("planner", llm.ToolCallResponse{
		ToolCalls: []llm.ToolCall{{ID: "1", Name: "echo", Input: []byte(`{"x":1}`)}},
	})
	toolResp, err := gateway.ChatWithTools(context.Background(), "planner", llm.ToolCallRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if len(toolResp.ToolCalls) != 1 || toolResp.ToolCalls[0].Name != "echo" {
		t.Fatalf("unexpected tool response: %+v", toolResp)
	}

	gateway.QueueChat("planner", llm.ChatResponse{Message: llm.Message{Content: "streamed"}})
	ch, err := gateway.StreamChat(context.Background(), "planner", llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	chunk := <-ch
	if chunk.Content != "streamed" || !chunk.Done {
		t.Fatalf("unexpected chunk: %+v", chunk)
	}
}

func TestGatewayStructured(t *testing.T) {
	gateway := NewGateway()
	gateway.QueueStructured("planner", json.RawMessage(`{"answer":"ok"}`))
	raw, err := gateway.StructuredChat(context.Background(), "planner", json.RawMessage(`{"type":"object"}`), llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"answer":"ok"}` {
		t.Fatalf("unexpected structured response: %s", raw)
	}
}

func TestGatewayEmbed(t *testing.T) {
	gateway := NewGateway()
	gateway.QueueEmbedding("embed", [][]float32{{0.1, 0.2}})
	vectors, err := gateway.Embed(context.Background(), "embed", []string{"hello"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 1 || vectors[0][1] != 0.2 {
		t.Fatalf("unexpected vectors: %+v", vectors)
	}
	if _, err := gateway.Embed(context.Background(), "embed", []string{"missing"}); !errors.Is(err, ErrNoResponse) {
		t.Fatalf("expected ErrNoResponse, got %v", err)
	}
}
