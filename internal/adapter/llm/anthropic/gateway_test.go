package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
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
