package anthropic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

// captureRequest runs one ChatWithTools call against a stub and returns the
// decoded request body the adapter sent.
func captureRequest(t *testing.T, promptCache bool, req llm.ToolCallRequest) map[string]any {
	t.Helper()
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request: %v", err)
			return
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("decode request: %v", err)
			return
		}
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1}}`))
	}))
	defer server.Close()

	gateway := NewGateway([]llm.Profile{{
		Name:        "claude",
		Model:       "claude-sonnet",
		Endpoint:    server.URL,
		PromptCache: llm.PromptCacheConfig{Enabled: promptCache},
	}}, server.Client())

	if _, err := gateway.ChatWithTools(context.Background(), "claude", req); err != nil {
		t.Fatal(err)
	}
	return body
}

func toolRequest() llm.ToolCallRequest {
	return llm.ToolCallRequest{
		ChatRequest: llm.ChatRequest{Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: "You are a careful assistant."},
			{Role: llm.RoleUser, Content: "find the invoice"},
		}},
		Tools: []llm.ToolSpec{
			{Name: "search", Description: "search things"},
			{Name: "fetch", Description: "fetch things"},
		},
	}
}

// countBreakpoints walks the request and counts cache_control markers.
func countBreakpoints(value any) int {
	switch typed := value.(type) {
	case map[string]any:
		count := 0
		for key, nested := range typed {
			if key == "cache_control" {
				count++
				continue
			}
			count += countBreakpoints(nested)
		}
		return count
	case []any:
		count := 0
		for _, item := range typed {
			count += countBreakpoints(item)
		}
		return count
	default:
		return 0
	}
}

func TestPromptCacheDisabledSendsNoBreakpoints(t *testing.T) {
	body := captureRequest(t, false, toolRequest())
	if got := countBreakpoints(body); got != 0 {
		t.Fatalf("expected no cache_control markers when disabled, got %d", got)
	}
	if _, ok := body["system"].(string); !ok {
		t.Fatalf("expected the system prompt to stay a plain string, got %T", body["system"])
	}
}

// One breakpoint covers everything before it, so the catalog needs exactly one
// marker on its last entry rather than one per tool.
func TestPromptCacheMarksOnlyLastToolInCatalog(t *testing.T) {
	body := captureRequest(t, true, toolRequest())
	tools, ok := body["tools"].([]any)
	if !ok || len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %#v", body["tools"])
	}
	first := tools[0].(map[string]any)
	last := tools[1].(map[string]any)
	if _, marked := first["cache_control"]; marked {
		t.Fatal("expected no breakpoint on the first tool; one marker covers the whole catalog")
	}
	if _, marked := last["cache_control"]; !marked {
		t.Fatal("expected a breakpoint on the last tool")
	}
}

func TestPromptCacheMarksSystemPrompt(t *testing.T) {
	body := captureRequest(t, true, toolRequest())
	blocks, ok := body["system"].([]any)
	if !ok || len(blocks) != 1 {
		t.Fatalf("expected the system prompt as one content block, got %#v", body["system"])
	}
	block := blocks[0].(map[string]any)
	if block["text"] != "You are a careful assistant." {
		t.Fatalf("system text lost: %#v", block)
	}
	if _, marked := block["cache_control"]; !marked {
		t.Fatal("expected a breakpoint on the system prompt")
	}
}

// The conversation tail must be marked too, so the next turn reads this turn's
// history from cache instead of re-billing it.
func TestPromptCacheMarksConversationTail(t *testing.T) {
	body := captureRequest(t, true, toolRequest())
	messages, ok := body["messages"].([]any)
	if !ok || len(messages) == 0 {
		t.Fatalf("expected messages, got %#v", body["messages"])
	}
	last := messages[len(messages)-1].(map[string]any)
	blocks, ok := last["content"].([]any)
	if !ok || len(blocks) == 0 {
		t.Fatalf("expected the final message rewritten as content blocks, got %#v", last["content"])
	}
	tail := blocks[len(blocks)-1].(map[string]any)
	if tail["text"] != "find the invoice" {
		t.Fatalf("message text lost: %#v", tail)
	}
	if _, marked := tail["cache_control"]; !marked {
		t.Fatal("expected a breakpoint on the conversation tail")
	}
}

// Anthropic rejects a request carrying more than four breakpoints.
func TestPromptCacheStaysWithinBreakpointLimit(t *testing.T) {
	body := captureRequest(t, true, toolRequest())
	if got := countBreakpoints(body); got > 4 {
		t.Fatalf("Anthropic allows at most 4 cache breakpoints, sent %d", got)
	}
	if got := countBreakpoints(body); got != 3 {
		t.Fatalf("expected breakpoints on tools, system and the tail, got %d", got)
	}
}

// A tool result already arrives as content blocks; the marker must attach to
// the existing block rather than replacing the structure.
func TestPromptCacheMarksExistingContentBlocks(t *testing.T) {
	body := captureRequest(t, true, llm.ToolCallRequest{
		ChatRequest: llm.ChatRequest{Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "go"},
			{Role: llm.RoleTool, ToolCallID: "call-1", Content: "42"},
		}},
	})
	messages := body["messages"].([]any)
	last := messages[len(messages)-1].(map[string]any)
	blocks := last["content"].([]any)
	block := blocks[len(blocks)-1].(map[string]any)
	if block["type"] != "tool_result" {
		t.Fatalf("expected the tool_result block preserved, got %#v", block)
	}
	if _, marked := block["cache_control"]; !marked {
		t.Fatal("expected a breakpoint on the existing tool_result block")
	}
}

// A trailing empty message cannot carry a breakpoint, so the marker has to
// fall back to the last message that actually has content.
func TestPromptCacheSkipsEmptyTrailingMessage(t *testing.T) {
	body := captureRequest(t, true, llm.ToolCallRequest{
		ChatRequest: llm.ChatRequest{Messages: []llm.Message{
			{Role: llm.RoleUser, Content: "real content"},
			{Role: llm.RoleAssistant, Content: ""},
		}},
	})
	messages := body["messages"].([]any)
	if got := countBreakpoints(messages); got != 1 {
		t.Fatalf("expected exactly one message breakpoint, got %d", got)
	}
	first := messages[0].(map[string]any)
	blocks, ok := first["content"].([]any)
	if !ok {
		t.Fatalf("expected the breakpoint on the non-empty message, got %#v", first["content"])
	}
	if _, marked := blocks[0].(map[string]any)["cache_control"]; !marked {
		t.Fatal("expected the non-empty message to carry the breakpoint")
	}
}
