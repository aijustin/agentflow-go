package anthropic

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestGatewayChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["model"] != "claude-test" {
			t.Fatalf("unexpected model %v", req["model"])
		}
		_, _ = w.Write([]byte(`{"content":[{"text":"ok"}],"usage":{"input_tokens":2,"output_tokens":3}}`))
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "claude-test", Endpoint: server.URL + "/v1"}}, server.Client())
	resp, err := gateway.Chat(context.Background(), "default", llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "ok" || resp.Usage.TotalTokens != 5 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestGatewaySupportsConfiguredCapabilities(t *testing.T) {
	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "claude-test"}}, nil)

	for _, capability := range []llm.Capability{llm.CapChat, llm.CapToolCall, llm.CapStructuredOutput, llm.CapStream} {
		if !gateway.Supports("default", capability) {
			t.Fatalf("expected capability %s", capability)
		}
	}
	if gateway.Supports("default", llm.CapEmbed) {
		t.Fatal("anthropic should not advertise embeddings by default")
	}
}

func TestGatewayChatWithTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		tools, ok := req["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("tools not sent: %+v", req)
		}
		tool := tools[0].(map[string]any)
		if tool["name"] != "echo" {
			t.Fatalf("unexpected tool spec: %+v", tool)
		}
		messages := req["messages"].([]any)
		if len(messages) != 3 {
			t.Fatalf("unexpected messages: %+v", messages)
		}
		assistant := messages[1].(map[string]any)
		assistantContent := assistant["content"].([]any)
		if assistantContent[0].(map[string]any)["type"] != "tool_use" {
			t.Fatalf("assistant tool call not encoded: %+v", assistant)
		}
		toolResult := messages[2].(map[string]any)
		resultContent := toolResult["content"].([]any)
		if toolResult["role"] != "user" || resultContent[0].(map[string]any)["type"] != "tool_result" {
			t.Fatalf("tool result not encoded: %+v", toolResult)
		}
		_, _ = w.Write([]byte(`{
			"content":[{"type":"tool_use","id":"toolu_1","name":"echo","input":{"query":"hello"}}],
			"stop_reason":"tool_use",
			"usage":{"input_tokens":2,"output_tokens":3}
		}`))
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "claude-test", Endpoint: server.URL + "/v1"}}, server.Client())
	resp, err := gateway.ChatWithTools(context.Background(), "default", llm.ToolCallRequest{
		ChatRequest: llm.ChatRequest{Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "call echo"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "toolu_old", Name: "echo", Input: json.RawMessage(`{"query":"old"}`)}}},
			{Role: llm.RoleTool, ToolCallID: "toolu_old", Name: "echo", Content: `{"ok":true}`},
		}},
		Tools: []llm.ToolSpec{{Name: "echo", Description: "Echo input", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "toolu_1" || resp.ToolCalls[0].Name != "echo" || string(resp.ToolCalls[0].Input) != `{"query":"hello"}` {
		t.Fatalf("unexpected tool response: %+v", resp)
	}
	if resp.Usage.TotalTokens != 5 || resp.FinishReason != "tool_use" || len(resp.Raw) == 0 {
		t.Fatalf("unexpected response metadata: %+v", resp)
	}
}

func TestGatewayStructuredChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		choice, ok := req["tool_choice"].(map[string]any)
		if !ok || choice["type"] != "tool" || choice["name"] != "structured_output" {
			t.Fatalf("structured tool_choice not sent: %+v", req)
		}
		_, _ = w.Write([]byte(`{
			"content":[{"type":"tool_use","id":"toolu_json","name":"structured_output","input":{"answer":"ok"}}],
			"stop_reason":"tool_use"
		}`))
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "claude-test", Endpoint: server.URL + "/v1"}}, server.Client())
	raw, err := gateway.StructuredChat(context.Background(), "default", json.RawMessage(`{"type":"object"}`), llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"answer":"ok"}` {
		t.Fatalf("unexpected structured output: %s", raw)
	}
}

func TestGatewayStreamChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["stream"] != true {
			t.Fatalf("stream flag not sent: %+v", req)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writer := bufio.NewWriter(w)
		_, _ = writer.WriteString("event: content_block_delta\n")
		_, _ = writer.WriteString("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hel\"}}\n\n")
		_, _ = writer.WriteString("event: content_block_delta\n")
		_, _ = writer.WriteString("data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"lo\"}}\n\n")
		_, _ = writer.WriteString("event: message_stop\n")
		_, _ = writer.WriteString("data: {\"type\":\"message_stop\"}\n\n")
		_ = writer.Flush()
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "claude-test", Endpoint: server.URL + "/v1"}}, server.Client())
	ch, err := gateway.StreamChat(context.Background(), "default", llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got strings.Builder
	done := false
	for chunk := range ch {
		if chunk.Error != "" {
			t.Fatal(chunk.Error)
		}
		got.WriteString(chunk.Content)
		done = done || chunk.Done
	}
	if got.String() != "hello" || !done {
		t.Fatalf("unexpected stream got=%q done=%v", got.String(), done)
	}
}

func TestGatewayChatErrors(t *testing.T) {
	t.Run("unknown profile", func(t *testing.T) {
		_, err := NewGateway(nil, nil).Chat(context.Background(), "missing", llm.ChatRequest{})
		if err == nil {
			t.Fatal("expected error")
		}
	})

	t.Run("non success status", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "nope", http.StatusBadGateway)
		}))
		defer server.Close()
		gateway := NewGateway([]llm.Profile{{Name: "default", Model: "test", Endpoint: server.URL + "/v1"}}, server.Client())
		_, err := gateway.Chat(context.Background(), "default", llm.ChatRequest{})
		if err == nil {
			t.Fatal("expected status error")
		}
	})
}
