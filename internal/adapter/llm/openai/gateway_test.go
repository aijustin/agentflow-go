package openai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestGatewayChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["model"] != "test-model" {
			t.Fatalf("unexpected model %v", req["model"])
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok","reasoning_content":"hidden"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3,"completion_tokens_details":{"reasoning_tokens":1}}}`))
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "test-model", Endpoint: server.URL + "/v1"}}, server.Client())
	resp, err := gateway.Chat(context.Background(), "default", llm.ChatRequest{Messages: []llm.Message{{Role: llm.RoleUser, Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Message.Content != "ok" || resp.Message.ReasoningContent != "hidden" || resp.FinishReason != "stop" || resp.Usage.InputTokens != 1 || resp.Usage.OutputTokens != 2 || resp.Usage.ReasoningTokens != 1 || resp.Usage.TotalTokens != 3 || len(resp.Raw) == 0 {
		t.Fatalf("unexpected response: %+v", resp)
	}
}

func TestGatewayChatSendsRuntimeConfig(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["max_tokens"].(float64) != 128 || req["top_p"].(float64) != 0.7 || req["reasoning_effort"] != "high" {
			t.Fatalf("runtime config not sent: %+v", req)
		}
		if req["custom_flag"] != "on" {
			t.Fatalf("extra body not merged: %+v", req)
		}
		thinking, ok := req["thinking"].(map[string]any)
		if !ok || thinking["enabled"] != true || thinking["budget_tokens"].(float64) != 1024 {
			t.Fatalf("thinking config not sent: %+v", req)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`))
	}))
	defer server.Close()

	topP := float32(0.7)
	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "test-model", Endpoint: server.URL + "/v1"}}, server.Client())
	_, err := gateway.Chat(context.Background(), "default", llm.ChatRequest{
		TopP:            &topP,
		MaxTokens:       128,
		Thinking:        llm.ThinkingConfig{Enabled: true, BudgetTokens: 1024},
		ReasoningEffort: "high",
		ExtraBody:       map[string]any{"custom_flag": "on"},
	})
	if err != nil {
		t.Fatal(err)
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
		fn := tool["function"].(map[string]any)
		if tool["type"] != "function" || fn["name"] != "echo" {
			t.Fatalf("unexpected tool spec: %+v", tool)
		}
		messages := req["messages"].([]any)
		if len(messages) != 3 {
			t.Fatalf("unexpected messages: %+v", messages)
		}
		assistant := messages[1].(map[string]any)
		assistantCalls, ok := assistant["tool_calls"].([]any)
		if !ok {
			t.Fatalf("assistant tool calls not encoded: %+v", assistant)
		}
		assistantCall := assistantCalls[0].(map[string]any)
		assistantFn := assistantCall["function"].(map[string]any)
		if _, ok := assistantFn["arguments"].(string); !ok {
			t.Fatalf("assistant tool call arguments must be string: %+v", assistantFn)
		}
		toolMsg := messages[2].(map[string]any)
		if toolMsg["role"] != "tool" || toolMsg["tool_call_id"] != "call-0" {
			t.Fatalf("tool message not encoded: %+v", toolMsg)
		}
		_, _ = w.Write([]byte(`{
			"choices":[{
				"message":{
					"role":"assistant",
					"content":"",
					"tool_calls":[{
						"id":"call-1",
						"type":"function",
						"function":{"name":"echo","arguments":"{\"query\":\"hello\"}"}
					}]
				},
				"finish_reason":"tool_calls"
			}]
		}`))
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "test-model", Endpoint: server.URL + "/v1"}}, server.Client())
	resp, err := gateway.ChatWithTools(context.Background(), "default", llm.ToolCallRequest{
		ChatRequest: llm.ChatRequest{Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "call echo"},
			{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-0", Name: "echo", Input: json.RawMessage(`{"query":"old"}`)}}},
			{Role: llm.RoleTool, ToolCallID: "call-0", Name: "echo", Content: `{"ok":true}`},
		}},
		Tools: []llm.ToolSpec{{Name: "echo", Description: "Echo input", Schema: json.RawMessage(`{"type":"object"}`)}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "echo" || string(resp.ToolCalls[0].Input) != `{"query":"hello"}` {
		t.Fatalf("unexpected tool call response: %+v", resp)
	}
}

func TestGatewayStructuredChat(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		format, ok := req["response_format"].(map[string]any)
		if !ok || format["type"] != "json_schema" {
			t.Fatalf("structured response_format not sent: %+v", req)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"answer\":\"ok\"}"},"finish_reason":"stop"}]}`))
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "test-model", Endpoint: server.URL + "/v1"}}, server.Client())
	raw, err := gateway.StructuredChat(context.Background(), "default", json.RawMessage(`{"type":"object"}`), llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"answer":"ok"}` {
		t.Fatalf("unexpected structured response: %s", raw)
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
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hel\"}}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"lo\"},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":2,\"total_tokens\":3}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "test-model", Endpoint: server.URL + "/v1"}}, server.Client())
	ch, err := gateway.StreamChat(context.Background(), "default", llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	var got string
	done := false
	for chunk := range ch {
		if chunk.Error != "" {
			t.Fatal(chunk.Error)
		}
		got += chunk.Content
		done = done || chunk.Done
	}
	if got != "hello" || !done {
		t.Fatalf("unexpected stream: got=%q done=%v", got, done)
	}
}

// TestGatewayStreamChatCancelStopsGoroutine verifies that cancelling the
// context unblocks and tears down the streaming goroutine (and its response
// body) even when the consumer abandons the channel mid-stream.
func TestGatewayStreamChatCancelStopsGoroutine(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		for i := 0; i < 2; i++ {
			_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"x\"}}]}\n\n"))
			if flusher != nil {
				flusher.Flush()
			}
		}
		// Hold the stream open so the gateway goroutine must rely on context
		// cancellation (not EOF) to exit.
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)

	gateway := NewGateway([]llm.Profile{{Name: "default", Model: "test-model", Endpoint: server.URL + "/v1"}}, server.Client())
	ctx, cancel := context.WithCancel(context.Background())
	ch, err := gateway.StreamChat(ctx, "default", llm.ChatRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := <-ch; !ok {
		t.Fatal("expected at least one chunk before cancel")
	}
	cancel()

	closed := make(chan struct{})
	go func() {
		for range ch {
		}
		close(closed)
	}()
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("stream goroutine leaked: channel not closed after context cancel")
	}
}

func TestGatewayEmbed(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		var req map[string]any
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatal(err)
		}
		if req["model"] != "embed-model" {
			t.Fatalf("unexpected model %v", req["model"])
		}
		input := req["input"].([]any)
		if len(input) != 2 || input[0] != "hello" || input[1] != "world" {
			t.Fatalf("unexpected input %+v", input)
		}
		_, _ = w.Write([]byte(`{"data":[{"embedding":[0.1,0.2]},{"embedding":[0.3,0.4]}]}`))
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{Name: "embed", Model: "embed-model", Endpoint: server.URL + "/v1", Capabilities: []llm.Capability{llm.CapEmbed}}}, server.Client())
	if !gateway.Supports("embed", llm.CapEmbed) {
		t.Fatal("expected embed capability")
	}
	vectors, err := gateway.Embed(context.Background(), "embed", []string{"hello", "world"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vectors) != 2 || len(vectors[0]) != 2 || vectors[0][0] != 0.1 || vectors[1][1] != 0.4 {
		t.Fatalf("unexpected vectors: %+v", vectors)
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
