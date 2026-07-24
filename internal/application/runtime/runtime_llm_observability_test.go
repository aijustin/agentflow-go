package runtime

import (
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestLLMCalledPayloadRedactsByDefault(t *testing.T) {
	payload := llmCalledPayload(false, map[string]any{"profile": "chat", "tools": true}, []llm.Message{
		{Role: llm.RoleSystem, Content: "you are helpful"},
		{Role: llm.RoleUser, Content: "print a test page"},
	})
	if payload["message_count"] != 2 {
		t.Fatalf("message_count=%v", payload["message_count"])
	}
	if _, ok := payload["prompt"]; ok {
		t.Fatalf("redacted payload must not carry prompt plaintext: %#v", payload["prompt"])
	}
	hash, ok := payload["messages_hash"].(string)
	if !ok || len(hash) != llmCalledPayloadHashChars {
		t.Fatalf("messages_hash=%v", payload["messages_hash"])
	}
	msgs, ok := payload["messages"].([]map[string]any)
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected 2 observability messages, got %#v", payload["messages"])
	}
	for i, m := range msgs {
		if _, ok := m["content"]; ok {
			t.Fatalf("redacted message %d must not carry content plaintext: %#v", i, m)
		}
		if _, ok := m["content_chars"]; !ok {
			t.Fatalf("redacted message %d must carry content_chars: %#v", i, m)
		}
	}
	if msgs[0]["role"] != "system" || msgs[0]["content_chars"] != len([]rune("you are helpful")) {
		t.Fatalf("system message=%#v", msgs[0])
	}
	if msgs[1]["content_chars"] != len([]rune("print a test page")) {
		t.Fatalf("user message=%#v", msgs[1])
	}
}

func TestLLMCalledPayloadRedactedHashIsStable(t *testing.T) {
	messages := []llm.Message{{Role: llm.RoleUser, Content: "same input"}}
	first := llmCalledPayload(false, nil, messages)["messages_hash"]
	second := llmCalledPayload(false, nil, messages)["messages_hash"]
	if first != second {
		t.Fatalf("hash not stable: %v vs %v", first, second)
	}
	other := llmCalledPayload(false, nil, []llm.Message{{Role: llm.RoleUser, Content: "different input"}})["messages_hash"]
	if first == other {
		t.Fatal("distinct payloads must not share a hash")
	}
}

func TestLLMCalledPayloadCaptureIncludesMessagesAndPrompt(t *testing.T) {
	payload := llmCalledPayload(true, map[string]any{"profile": "chat", "tools": true}, []llm.Message{
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

func TestLLMCalledPayloadCaptureTruncatesLongContent(t *testing.T) {
	long := strings.Repeat("汉", maxLLMCalledMessageChars+10)
	payload := llmCalledPayload(true, nil, []llm.Message{{Role: llm.RoleUser, Content: long}})
	msgs := payload["messages"].([]map[string]any)
	content, _ := msgs[0]["content"].(string)
	if !strings.HasSuffix(content, "…") {
		t.Fatalf("expected truncated content with ellipsis, len=%d", len([]rune(content)))
	}
	if got := len([]rune(strings.TrimSuffix(content, "…"))); got != maxLLMCalledMessageChars {
		t.Fatalf("truncated rune count=%d want=%d", got, maxLLMCalledMessageChars)
	}
}
