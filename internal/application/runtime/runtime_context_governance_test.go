package runtime

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
)

func TestEvictStaleToolMessagesExcludesDeniedAndEmpty(t *testing.T) {
	messages := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "s1", Name: "knowledge_retrieve"}}},
		{Role: llm.RoleTool, ToolCallID: "s1", Name: "knowledge_retrieve", Content: `{"tool":"knowledge_retrieve","output":{"summary":"ok","chunks":[{"content":"/dev-api/login"}]}}`, Metadata: map[string]string{"tool_result_class": "success"}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "e1", Name: "knowledge_retrieve"}}},
		{Role: llm.RoleTool, ToolCallID: "e1", Name: "knowledge_retrieve", Content: ``, Metadata: map[string]string{"tool_result_class": "empty"}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "d1", Name: "knowledge_retrieve"}}},
		{Role: llm.RoleTool, ToolCallID: "d1", Name: "knowledge_retrieve", Content: `{"tool":"knowledge_retrieve","error":"run_tool_budget_exceeded"}`, Metadata: map[string]string{"tool_result_class": "denied"}},
	}
	evicted, stats := evictStaleToolMessagesWithPolicy(messages, 2, nil, nil)
	foundSuccess := false
	for _, msg := range evicted {
		if msg.Role == llm.RoleTool && strings.Contains(msg.Content, "/dev-api/login") {
			foundSuccess = true
		}
	}
	if !foundSuccess {
		t.Fatalf("expected successful retrieve to remain, got %+v stats=%+v", evicted, stats)
	}
	if stats.ExcludedTurns < 2 {
		t.Fatalf("expected denied/empty excluded from stale accounting, got %+v", stats)
	}
}

func TestClassifyToolResultMessagePrefersMetadataOverContent(t *testing.T) {
	msg := llm.Message{
		Role:    llm.RoleTool,
		Content: `{"tool":"search","error":"run_tool_budget_exceeded"}`,
		Metadata: map[string]string{
			"tool_result_class": string(contextwindow.ToolResultClassSuccess),
		},
	}
	if got := classifyToolResultMessage(msg); got != contextwindow.ToolResultClassSuccess {
		t.Fatalf("metadata must win over error field, got %q", got)
	}
}

func TestClassifyToolResultMessageDoesNotFalsePositiveOnSubstring(t *testing.T) {
	// Content mentions a denial phrase inside a successful payload; without
	// metadata or a top-level error field this must stay success.
	msg := llm.Message{
		Role:    llm.RoleTool,
		Content: `{"tool":"docs","output":{"text":"see docs about tool_denied handling"}}`,
	}
	if got := classifyToolResultMessage(msg); got != contextwindow.ToolResultClassSuccess {
		t.Fatalf("expected success for structured output mentioning denial phrase, got %q", got)
	}
}

func TestClassifyToolResultMessageUsesStructuredError(t *testing.T) {
	msg := llm.Message{
		Role:    llm.RoleTool,
		Content: `{"tool":"risky","error":"policy denied"}`,
	}
	if got := classifyToolResultMessage(msg); got != contextwindow.ToolResultClassDenied {
		t.Fatalf("expected denied from structured error, got %q", got)
	}
}

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

func TestEvictStaleToolMessagesCountsParallelResultsAsOneTurn(t *testing.T) {
	calls := make([]llm.ToolCall, 0, 17)
	messages := []llm.Message{}
	for day := 1; day <= 17; day++ {
		callID := fmt.Sprintf("day-%02d", day)
		calls = append(calls, llm.ToolCall{ID: callID, Name: "calendar_query"})
	}
	messages = append(messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: calls})
	for day := 1; day <= 17; day++ {
		callID := fmt.Sprintf("day-%02d", day)
		messages = append(messages, llm.Message{
			Role:       llm.RoleTool,
			Name:       "calendar_query",
			ToolCallID: callID,
			Content:    fmt.Sprintf(`{"tool":"calendar_query","output":{"day":%d}}`, day),
		})
	}

	evicted, stats := evictStaleToolMessagesWithPolicy(messages, 2, nil, nil)
	if len(evicted) != 18 {
		t.Fatalf("expected assistant plus all 17 parallel results, got %d messages: %+v", len(evicted), evicted)
	}
	if stats.DroppedToolTurns != 0 {
		t.Fatalf("one parallel batch must count as one retained turn, got %+v", stats)
	}
	for day := 1; day <= 17; day++ {
		callID := fmt.Sprintf("day-%02d", day)
		if !slices.ContainsFunc(evicted, func(msg llm.Message) bool {
			return msg.Role == llm.RoleTool && msg.ToolCallID == callID
		}) {
			t.Fatalf("parallel result %q was evicted", callID)
		}
	}
}

func TestEvictStaleToolMessagesCompactsRepeatedDenials(t *testing.T) {
	calls := make([]llm.ToolCall, 0, 5)
	messages := []llm.Message{}
	for attempt := 1; attempt <= 5; attempt++ {
		callID := fmt.Sprintf("denied-%d", attempt)
		calls = append(calls, llm.ToolCall{ID: callID, Name: "calendar_query"})
	}
	messages = append(messages, llm.Message{Role: llm.RoleAssistant, ToolCalls: calls})
	for attempt := 1; attempt <= 5; attempt++ {
		messages = append(messages, llm.Message{
			Role:       llm.RoleTool,
			Name:       "calendar_query",
			ToolCallID: fmt.Sprintf("denied-%d", attempt),
			Content: fmt.Sprintf(
				`{"tool":"calendar_query","error":"run_tool_loop_guard: tool=calendar_query repeated=%d limit=3; stop retrying"}`,
				attempt,
			),
			Metadata: map[string]string{"tool_result_class": "denied"},
		})
	}
	messages = append(
		messages,
		llm.Message{
			Role:      llm.RoleAssistant,
			ToolCalls: []llm.ToolCall{{ID: "success-1", Name: "calendar_query"}},
		},
		llm.Message{
			Role:       llm.RoleTool,
			Name:       "calendar_query",
			ToolCallID: "success-1",
			Content:    `{"tool":"calendar_query","output":{"day":17}}`,
			Metadata:   map[string]string{"tool_result_class": "success"},
		},
	)

	evicted, stats := evictStaleToolMessagesWithPolicy(messages, 2, nil, nil)
	denials := 0
	for _, msg := range evicted {
		if msg.Role == llm.RoleTool && classifyToolResultMessage(msg) == contextwindow.ToolResultClassDenied {
			denials++
		}
	}
	if denials != 1 {
		t.Fatalf("expected one actionable denial after compaction, got %d: %+v", denials, evicted)
	}
	if stats.CompactedDenials != 4 {
		t.Fatalf("expected four repeated denials compacted, got %+v", stats)
	}
	if !slices.ContainsFunc(evicted, func(msg llm.Message) bool {
		return msg.Role == llm.RoleTool && msg.ToolCallID == "success-1"
	}) {
		t.Fatalf("successful result must remain after denial compaction: %+v", evicted)
	}
	evicted = enforceToolCallPairing(evicted)
	if len(evicted) != 4 {
		t.Fatalf("expected balanced denial and success pairs, got %+v", evicted)
	}
}

func TestCompactToolResultForContextRespectsFinalTokenBudget(t *testing.T) {
	engine := &Engine{}
	result := core.ToolResult{
		Tool:   "echo",
		Output: json.RawMessage(`{"data":"` + strings.Repeat("x", 5000) + `"}`),
	}
	compacted, _ := engine.compactToolResultForContext(result, 20)
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

func TestContextwindowSummaryFallbackRespectsBudget(t *testing.T) {
	messages := []contextwindow.Message{
		{Role: "user", Content: "first message"},
		{Role: "assistant", Content: "second message"},
		{Role: "user", Content: strings.Repeat("long ", 200)},
	}
	summary := contextwindowSummaryFallback(messages, 20)
	if summary == "" || !strings.HasPrefix(summary, "Earlier context summary:") {
		t.Fatalf("unexpected summary: %q", summary)
	}
	if contextwindow.EstimateTokens(summary) > 20*3 {
		t.Fatalf("summary exceeded budget: %q", summary)
	}
}

func TestCompactToolResultForContextFoldsErrorIntoContent(t *testing.T) {
	engine := &Engine{}
	result := core.ToolResult{
		Tool:   "echo",
		Output: json.RawMessage(`"` + strings.Repeat("a", 200) + `"`),
		Error:  strings.Repeat("boom ", 200),
	}
	compacted, _ := engine.compactToolResultForContext(result, 10)
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

func TestEvictStaleToolMessagesKeepsExcludedToolNames(t *testing.T) {
	// Mirrors multi-card HITL: early request_user_interaction values must survive
	// after later dictionary/business tool turns push StaleToolTurns past keep=2.
	messages := []llm.Message{
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "rui-1", Name: "request_user_interaction"}}},
		{Role: llm.RoleTool, ToolCallID: "rui-1", Name: "request_user_interaction", Content: `{"title":"核心信息","values":{"Item_strItemDescription":"张亮专用01","Item_curRetailPrice":"90"}}`},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "list-1", Name: "ho_vendor_list"}}},
		{Role: llm.RoleTool, ToolCallID: "list-1", Name: "ho_vendor_list", Content: `{"vendors":[]}`},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "rui-2", Name: "request_user_interaction"}}},
		{Role: llm.RoleTool, ToolCallID: "rui-2", Name: "request_user_interaction", Content: `{"title":"确认方案","values":{"vendorCode":"01000013","Item_strItemDescription":"张亮专用01"}}`},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "add-1", Name: "ho_item_add"}}},
		{Role: llm.RoleTool, ToolCallID: "add-1", Name: "ho_item_add", Content: `{"error":"missing stakeGrpCode"}`, Metadata: map[string]string{"tool_result_class": "denied"}},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "list-2", Name: "ho_stocktake_group_list"}}},
		{Role: llm.RoleTool, ToolCallID: "list-2", Name: "ho_stocktake_group_list", Content: `{"groups":[{"code":"0000000004"}]}`},
		{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "list-3", Name: "ho_item_class_list"}}},
		{Role: llm.RoleTool, ToolCallID: "list-3", Name: "ho_item_class_list", Content: `{"classes":[]}`},
	}
	evicted, stats := evictStaleToolMessagesWithPolicy(
		messages, 2, nil, []string{"request_user_interaction"},
	)
	foundCore := false
	foundConfirm := false
	for _, msg := range evicted {
		if msg.Role != llm.RoleTool {
			continue
		}
		if msg.ToolCallID == "rui-1" && strings.Contains(msg.Content, "张亮专用01") {
			foundCore = true
		}
		if msg.ToolCallID == "rui-2" && strings.Contains(msg.Content, "vendorCode") {
			foundConfirm = true
		}
	}
	if !foundCore || !foundConfirm {
		t.Fatalf("expected both HITL form results retained, core=%v confirm=%v stats=%+v messages=%+v", foundCore, foundConfirm, stats, evicted)
	}
	if stats.ExcludedTurns < 2 {
		t.Fatalf("expected HITL tools excluded from stale accounting, got %+v", stats)
	}
}

func TestSortedLLMProfileName(t *testing.T) {
	if sortedLLMProfileName(nil) != "" {
		t.Fatal("expected empty for nil profiles")
	}
	got := sortedLLMProfileName(map[string]core.LLMProfileRef{
		"zeta":  {Provider: "mock"},
		"alpha": {Provider: "mock"},
	})
	if got != "alpha" {
		t.Fatalf("expected sorted first name alpha, got %q", got)
	}
}

func TestPruneToolSpecs(t *testing.T) {
	specs := []llm.ToolSpec{{Name: "echo"}, {Name: "search"}}
	if got := pruneToolSpecs(specs, nil); len(got) != 2 {
		t.Fatalf("expected all specs when allowlist empty, got %+v", got)
	}
	got := pruneToolSpecs(specs, map[string]struct{}{"echo": {}})
	if len(got) != 1 || got[0].Name != "echo" {
		t.Fatalf("expected only echo, got %+v", got)
	}
}
