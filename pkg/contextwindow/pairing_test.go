package contextwindow

import (
	"strings"
	"testing"
)

func TestTrimKeepsAssistantToolPairsAtomic(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "old"},
		{
			Role:        RoleAssistant,
			Content:     "calling",
			ToolCallIDs: []string{"c1"},
		},
		{Role: RoleTool, Content: strings.Repeat("tool-output ", 80), ToolCallID: "c1", Name: "echo"},
		{Role: RoleUser, Content: "recent question"},
		{Role: RoleAssistant, Content: "final"},
	}
	disablePin := false
	result := New(Policy{
		Strategy:        StrategySlidingWindow,
		MaxInputTokens:  40,
		PinUserMessages: &disablePin,
	}).Prepare(messages)

	sawAssistantCall := false
	sawTool := false
	for _, msg := range result.Messages {
		if msg.Role == RoleAssistant && len(msg.ToolCallIDs) > 0 {
			sawAssistantCall = true
		}
		if msg.Role == RoleTool && msg.ToolCallID == "c1" {
			sawTool = true
		}
	}
	if sawTool != sawAssistantCall {
		t.Fatalf("tool pair split across trim boundary: assistant=%v tool=%v messages=%+v", sawAssistantCall, sawTool, result.Messages)
	}
}

func TestGroupMessagesForToolPairSafety(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "hi"},
		{Role: RoleAssistant, Content: "go", ToolCallIDs: []string{"a", "b"}},
		{Role: RoleTool, Content: "ra", ToolCallID: "a"},
		{Role: RoleTool, Content: "rb", ToolCallID: "b"},
		{Role: RoleAssistant, Content: "done"},
	}
	groups := groupMessagesForToolPairSafety(messages)
	if len(groups) != 3 {
		t.Fatalf("expected 3 groups, got %d", len(groups))
	}
	if len(groups[1].Messages) != 3 {
		t.Fatalf("expected tool pair group of 3, got %d", len(groups[1].Messages))
	}
}
