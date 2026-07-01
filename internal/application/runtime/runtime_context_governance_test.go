package runtime

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
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

func TestEvictStaleToolMessagesHandlesEmptyToolCallID(t *testing.T) {
	messages := []llm.Message{
		// An old tool call/result pair where the result has no ToolCallID
		// (e.g. a malformed or legacy record). Since it can't be matched by
		// ID, the assistant's empty-ID call must be conservatively stripped
		// too instead of being left dangling once the result is evicted.
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "", Name: "echo"}}},
		{Role: llm.RoleTool, ToolCallID: "", Content: `{"output":"a"}`},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "new-1", Name: "echo"}}},
		{Role: llm.RoleTool, ToolCallID: "new-1", Content: `{"output":"b"}`},
	}
	evicted := evictStaleToolMessages(messages, 1)
	if len(evicted) != 2 {
		t.Fatalf("expected only the newest pair kept, got %+v", evicted)
	}
	if len(evicted[0].ToolCalls) != 1 || evicted[0].ToolCalls[0].ID != "new-1" {
		t.Fatalf("expected empty-ID tool_call stripped, got %+v", evicted[0].ToolCalls)
	}
}

func TestCompactToolResultForContextRespectsFinalTokenBudget(t *testing.T) {
	result := core.ToolResult{
		Tool:   "echo",
		Output: json.RawMessage(`{"data":"` + strings.Repeat("x", 5000) + `"}`),
	}
	compacted := compactToolResultForContext(result, 20)
	raw, err := json.Marshal(compacted)
	if err != nil {
		t.Fatal(err)
	}
	// The bug being guarded against: truncating only the raw content
	// substring to maxTokens*3 runes while ignoring the metadata fields
	// and JSON structural overhead of the final serialized message can
	// leave the actual payload far over budget, or - if the shrinking
	// budget bottoms out at zero - fall back to returning the entire
	// untruncated content unchanged.
	if got := contextwindow.EstimateTokens(string(raw)); got > 20*3 {
		t.Fatalf("expected final compacted JSON to roughly respect the token budget, got %d estimated tokens: %s", got, raw)
	}
	if !strings.Contains(string(raw), `"truncated":true`) {
		t.Fatalf("expected truncated marker, got %s", raw)
	}
}

func TestCompactToolResultForContextFoldsErrorIntoContent(t *testing.T) {
	result := core.ToolResult{
		Tool:   "echo",
		Output: json.RawMessage(`"` + strings.Repeat("a", 200) + `"`),
		Error:  strings.Repeat("boom ", 200),
	}
	compacted := compactToolResultForContext(result, 10)
	if compacted.Error != "" {
		t.Fatalf("expected the returned ToolResult to not carry an unbounded Error field, got %q", compacted.Error)
	}
	raw, err := json.Marshal(compacted)
	if err != nil {
		t.Fatal(err)
	}
	// At very small budgets the fixed metadata overhead alone can exceed
	// maxTokens, but the unbounded original content/error must never leak
	// through untruncated.
	if strings.Contains(string(raw), strings.Repeat("boom ", 200)) || strings.Contains(string(raw), strings.Repeat("a", 200)) {
		t.Fatalf("expected original content/error not to leak through untruncated: %s", raw)
	}
}

func TestEnforceToolCallPairingDropsOrphanedToolResult(t *testing.T) {
	// Simulates context-window truncation dropping the assistant message
	// that issued a tool call while its tool result survives.
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "hi"},
		{Role: llm.RoleTool, ToolCallID: "call-1", Content: `{"output":"a"}`},
	}
	out := enforceToolCallPairing(messages)
	if len(out) != 1 || out[0].Role != llm.RoleUser {
		t.Fatalf("expected orphaned tool result dropped, got %+v", out)
	}
}

func TestEnforceToolCallPairingStripsUnansweredToolCall(t *testing.T) {
	// Simulates context-window truncation dropping a tool result while the
	// assistant message that issued the call survives. The assistant
	// message has other content, so it is kept minus the dangling call.
	messages := []llm.Message{
		{Role: llm.RoleAssistant, Content: "let me check that", ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo"}}},
		{Role: llm.RoleUser, Content: "next turn"},
	}
	out := enforceToolCallPairing(messages)
	if len(out) != 2 {
		t.Fatalf("expected assistant message kept without tool_calls, got %+v", out)
	}
	if len(out[0].ToolCalls) != 0 {
		t.Fatalf("expected unanswered tool_call stripped, got %+v", out[0].ToolCalls)
	}
}

func TestEnforceToolCallPairingDropsEmptyAssistantAfterStrip(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo"}}},
	}
	out := enforceToolCallPairing(messages)
	if len(out) != 0 {
		t.Fatalf("expected empty assistant-only-tool-call message dropped entirely, got %+v", out)
	}
}

func TestEnforceToolCallPairingKeepsBalancedPairs(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "call-1", Name: "echo"}}},
		{Role: llm.RoleTool, ToolCallID: "call-1", Content: `{"output":"a"}`},
	}
	out := enforceToolCallPairing(messages)
	if len(out) != 2 {
		t.Fatalf("expected balanced pair untouched, got %+v", out)
	}
}
