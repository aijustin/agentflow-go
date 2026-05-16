package contextwindow

import (
	"strings"
	"testing"
)

func TestManagerNoopWithinBudget(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "system"},
		{Role: RoleUser, Content: "hello"},
	}
	result := New(Policy{Strategy: StrategySlidingWindow, MaxInputTokens: 100}).Prepare(messages)
	if result.Stats.DroppedMessages != 0 {
		t.Fatalf("unexpected drops: %+v", result.Stats)
	}
	if len(result.Messages) != len(messages) {
		t.Fatalf("got %d messages", len(result.Messages))
	}
}

func TestManagerSlidingWindowProtectsSystemPrompt(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "protected system prompt"},
		{Role: RoleUser, Content: strings.Repeat("old ", 100)},
		{Role: RoleAssistant, Content: strings.Repeat("middle ", 100)},
		{Role: RoleUser, Content: "latest question"},
	}
	result := New(Policy{
		Strategy:               StrategySlidingWindow,
		MaxInputTokens:         20,
		SystemPromptProtection: true,
	}).Prepare(messages)
	if result.Messages[0].Role != RoleSystem || result.Messages[0].Content != "protected system prompt" {
		t.Fatalf("system prompt was not protected: %+v", result.Messages)
	}
	if result.Messages[len(result.Messages)-1].Content != "latest question" {
		t.Fatalf("latest message was not retained: %+v", result.Messages)
	}
	if result.Stats.DroppedMessages == 0 {
		t.Fatalf("expected dropped messages: %+v", result.Stats)
	}
}

func TestManagerSlidingWindowWithSummary(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "system"},
		{Role: RoleUser, Content: strings.Repeat("first topic ", 80)},
		{Role: RoleAssistant, Content: strings.Repeat("second topic ", 80)},
		{Role: RoleUser, Content: "final question"},
	}
	result := New(Policy{
		Strategy:               StrategySlidingWindowWithSummary,
		MaxInputTokens:         80,
		SummaryTokens:          40,
		SystemPromptProtection: true,
	}).Prepare(messages)
	if !result.Stats.Summarized {
		t.Fatalf("expected summary stats: %+v", result.Stats)
	}
	foundSummary := false
	for _, msg := range result.Messages {
		if strings.Contains(msg.Content, "Earlier context summary") {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Fatalf("expected summary message: %+v", result.Messages)
	}
	if result.Stats.AfterTokens > result.Stats.MaxInputTokens {
		t.Fatalf("after tokens exceeded budget: %+v", result.Stats)
	}
}

func TestEstimateTokens(t *testing.T) {
	if EstimateTokens("") != 0 {
		t.Fatal("empty text should have zero tokens")
	}
	if EstimateTokens("hello") == 0 {
		t.Fatal("non-empty text should have tokens")
	}
}
