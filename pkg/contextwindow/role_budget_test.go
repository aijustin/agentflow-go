package contextwindow

import "testing"

func TestApplyRoleBudgets(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "system prompt with enough tokens"},
		{Role: RoleUser, Content: "u1"},
		{Role: RoleUser, Content: "u2"},
		{Role: RoleTool, Content: "tool output"},
	}
	out := applyRoleBudgets(messages, RoleBudgets{System: 1000, User: 1, Tool: 1000})
	if len(out) != 3 {
		t.Fatalf("expected three messages after role budget, got %d", len(out))
	}
	if out[len(out)-2].Content != "u2" {
		t.Fatalf("expected most recent user message to be kept, got %+v", out)
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
