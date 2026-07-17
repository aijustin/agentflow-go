package runtime

import (
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestLLMCalledPayloadIncludesMessagesAndPrompt(t *testing.T) {
	payload := llmCalledPayload(map[string]any{"profile": "chat", "tools": true}, []llm.Message{
		{Role: llm.RoleSystem, Content: "you are helpful"},
		{Role: llm.RoleUser, Content: "print a test page"},
	})
	msgs, ok := payload["messages"].([]map[string]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected 2 observability messages, got %#v", payload["messages"])
	}
	if payload["message_count"] != 2 {
		t.Fatalf("message_count=%v", payload["message_count"])
	}
	if payload["prompt"] != "print a test page" {
		t.Fatalf("prompt=%v", payload["prompt"])
	}
	if msgs[0]["role"] != "system" || msgs[0]["content"] != "you are helpful" {
		t.Fatalf("system message=%#v", msgs[0])
	}
}

func TestLLMCalledPayloadTruncatesLongContent(t *testing.T) {
	long := strings.Repeat("汉", maxLLMCalledMessageChars+10)
	payload := llmCalledPayload(nil, []llm.Message{{Role: llm.RoleUser, Content: long}})
	msgs := payload["messages"].([]map[string]any)
	content, _ := msgs[0]["content"].(string)
	if !strings.HasSuffix(content, "…") {
		t.Fatalf("expected truncated content with ellipsis, len=%d", len([]rune(content)))
	}
	if got := len([]rune(strings.TrimSuffix(content, "…"))); got != maxLLMCalledMessageChars {
		t.Fatalf("truncated rune count=%d want=%d", got, maxLLMCalledMessageChars)
	}
}
