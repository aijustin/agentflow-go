package mock

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestFallbackGatewayReturnsLastUserMessageWhenQueueEmpty(t *testing.T) {
	gateway := NewFallbackGateway()
	resp, err := gateway.Chat(context.Background(), "default", llmChatRequest("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "hello" {
		t.Fatalf("unexpected fallback content: %q", resp.Message.Content)
	}
}

func TestFallbackGatewayUsesQueuedResponseFirst(t *testing.T) {
	gateway := NewFallbackGateway()
	gateway.QueueChat("default", llmChatResponse("queued"))
	resp, err := gateway.Chat(context.Background(), "default", llmChatRequest("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "queued" {
		t.Fatalf("expected queued response, got %q", resp.Message.Content)
	}
}

func llmChatRequest(content string) llm.ChatRequest {
	return llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: content}}}
}

func llmChatResponse(content string) llm.ChatResponse {
	return llm.ChatResponse{Message: llm.Message{Role: llm.RoleAssistant, Content: content}}
}
