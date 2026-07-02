package contextwindow

import (
	"strings"
	"testing"
)

func TestKeepRecentPinUserRetainsAllUserMessages(t *testing.T) {
	messages := []Message{
		{Role: RoleUser, Content: "POS 无法打印"},
		{Role: RoleUser, Content: "POS 无法打印"},
		{Role: RoleAssistant, Content: strings.Repeat("tool-heavy ", 200)},
		{Role: RoleTool, Content: strings.Repeat("json ", 300)},
		{Role: RoleUser, Content: "解决过几次"},
	}
	result := New(Policy{
		Strategy:       StrategyNone,
		MaxInputTokens: 40,
	}).Prepare(messages)
	for _, want := range []string{"POS 无法打印", "解决过几次"} {
		found := false
		for _, msg := range result.Messages {
			if msg.Content == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("expected user message %q to remain after pin-user trim, got %+v", want, result.Messages)
		}
	}
	if result.Stats.DroppedUserMessages != 0 {
		t.Fatalf("expected no dropped user messages, got %+v", result.Stats)
	}
	if result.Stats.ContextIncomplete {
		t.Fatalf("expected context to remain complete for user messages, got %+v", result.Stats)
	}
}

func TestTrimReportsDroppedUserMessages(t *testing.T) {
	disablePin := false
	messages := []Message{
		{Role: RoleUser, Content: strings.Repeat("old-user ", 100)},
		{Role: RoleAssistant, Content: "recent"},
	}
	result := New(Policy{
		Strategy:        StrategySlidingWindow,
		MaxInputTokens:  10,
		PinUserMessages: &disablePin,
	}).Prepare(messages)
	if result.Stats.DroppedUserMessages == 0 {
		t.Fatalf("expected dropped user messages when pin disabled, got %+v", result.Stats)
	}
	if !result.Stats.ContextIncomplete {
		t.Fatal("expected context_incomplete when user messages were dropped")
	}
}

func TestPinUserDisabledUsesRecentWindow(t *testing.T) {
	disablePin := false
	messages := []Message{
		{Role: RoleUser, Content: "old question"},
		{Role: RoleAssistant, Content: strings.Repeat("middle ", 100)},
		{Role: RoleUser, Content: "latest question"},
	}
	result := New(Policy{
		Strategy:        StrategySlidingWindow,
		MaxInputTokens:  20,
		PinUserMessages: &disablePin,
	}).Prepare(messages)
	if result.Messages[len(result.Messages)-1].Content != "latest question" {
		t.Fatalf("expected latest message retained, got %+v", result.Messages)
	}
}
