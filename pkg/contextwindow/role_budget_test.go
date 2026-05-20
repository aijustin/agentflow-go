package contextwindow

import "testing"

func TestApplyRoleBudgets(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "system prompt with enough tokens"},
		{Role: RoleUser, Content: "user one"},
		{Role: RoleUser, Content: "user two"},
		{Role: RoleTool, Content: "tool output"},
	}
	out := applyRoleBudgets(messages, RoleBudgets{System: 1000, User: 1, Tool: 1000})
	if len(out) != 2 {
		t.Fatalf("expected user budget to drop one message, got %d", len(out))
	}
}

func TestCustomSummarizer(t *testing.T) {
	manager := NewWithSummarizer(Policy{Strategy: StrategySlidingWindowWithSummary, MaxInputTokens: 20, SummaryTokens: 5, SummaryMode: "custom"}, func([]Message, int) string {
		return "custom summary"
	})
	result := manager.Prepare([]Message{
		{Role: RoleUser, Content: "one two three four five six seven eight nine ten"},
		{Role: RoleUser, Content: "eleven twelve thirteen fourteen fifteen sixteen seventeen eighteen nineteen twenty"},
	})
	found := false
	for _, msg := range result.Messages {
		if msg.Content == "custom summary" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected custom summary in messages: %+v", result.Messages)
	}
}
