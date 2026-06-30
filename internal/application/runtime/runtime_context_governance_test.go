package runtime

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestEvictStaleToolMessagesRemovesOrphanedToolCalls(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "old-1", Name: "echo"}, {ID: "old-2", Name: "echo"}}},
		{Role: llm.RoleTool, ToolCallID: "old-1", Content: `{"output":"a"}`},
		{Role: llm.RoleTool, ToolCallID: "old-2", Content: `{"output":"b"}`},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "new-1", Name: "echo"}}},
		{Role: llm.RoleTool, ToolCallID: "new-1", Content: `{"output":"c"}`},
	}
	evicted := evictStaleToolMessages(messages, 1)
	if len(evicted) != 2 {
		t.Fatalf("expected assistant+tool pair kept, got %+v", evicted)
	}
	if len(evicted[0].ToolCalls) != 1 || evicted[0].ToolCalls[0].ID != "new-1" {
		t.Fatalf("expected only new tool call on assistant, got %+v", evicted[0].ToolCalls)
	}
	if evicted[1].ToolCallID != "new-1" {
		t.Fatalf("expected new tool response, got %+v", evicted[1])
	}
}
