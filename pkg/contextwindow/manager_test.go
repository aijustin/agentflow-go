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

func TestManagerStrategyNonePassesThroughWithinBudget(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "system"},
		{Role: RoleUser, Content: "hello"},
	}
	result := New(Policy{Strategy: StrategyNone, MaxInputTokens: 100}).Prepare(messages)
	if result.Stats.DroppedMessages != 0 || len(result.Messages) != len(messages) {
		t.Fatalf("expected untouched passthrough within budget, got %+v", result)
	}
}

func TestManagerStrategyNoneStillCapsMaxInputTokens(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "protected system prompt"},
		{Role: RoleUser, Content: strings.Repeat("old ", 100)},
		{Role: RoleAssistant, Content: strings.Repeat("middle ", 100)},
		{Role: RoleUser, Content: "latest question"},
	}
	result := New(Policy{
		Strategy:               StrategyNone,
		MaxInputTokens:         20,
		SystemPromptProtection: true,
	}).Prepare(messages)
	// Even with no active trimming strategy configured, an over-budget
	// request must still be capped instead of growing without bound, so
	// it degrades to the same sliding-window fallback as an explicit
	// strategy would once MaxInputTokens is actually exceeded.
	if result.Stats.DroppedMessages == 0 {
		t.Fatalf("expected StrategyNone to still enforce MaxInputTokens once exceeded, got %+v", result.Stats)
	}
	if result.Stats.AfterTokens > result.Stats.MaxInputTokens {
		t.Fatalf("expected AfterTokens to respect MaxInputTokens cap, got %+v", result.Stats)
	}
	if result.Messages[0].Role != RoleSystem || result.Messages[0].Content != "protected system prompt" {
		t.Fatalf("system prompt was not protected: %+v", result.Messages)
	}
	if result.Messages[len(result.Messages)-1].Content != "latest question" {
		t.Fatalf("latest message was not retained: %+v", result.Messages)
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

func TestManagerCompressionTriggerRatio(t *testing.T) {
	longTool := strings.Repeat("tool-result ", 200)
	messages := []Message{
		{Role: RoleSystem, Content: "system"},
		{Role: RoleTool, Content: longTool},
		{Role: RoleUser, Content: "question"},
	}
	result := New(Policy{
		Strategy:               StrategySlidingWindow,
		MaxInputTokens:         100,
		SystemPromptProtection: true,
		ToolResultMaxTokens:    20,
		Compression: CompressionPolicy{
			Enabled:      true,
			TriggerRatio: 0.5,
		},
	}).Prepare(messages)
	for _, msg := range result.Messages {
		if msg.Role == RoleTool && len(msg.Content) >= len(longTool) {
			t.Fatalf("expected compressed tool message, got len=%d", len(msg.Content))
		}
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

func TestEstimateTokensCJK(t *testing.T) {
	// runes/3 would score 4 CJK characters as a single token; modern
	// tokenizers sit near one token per character, so the estimator must not
	// undercount Chinese-heavy text.
	if got := EstimateTokens("你好世界"); got < 4 {
		t.Fatalf("expected at least 4 tokens for 4 CJK runes, got %d", got)
	}
	mixed := EstimateTokens("你好 world")
	if mixed < EstimateTokens("world")+2 {
		t.Fatalf("mixed text should count CJK runes separately, got %d", mixed)
	}
}

func TestManagerUnknownStrategyFallsBackToTrim(t *testing.T) {
	messages := []Message{
		{Role: RoleSystem, Content: "system"},
		{Role: RoleUser, Content: strings.Repeat("old ", 100)},
		{Role: RoleUser, Content: "latest"},
	}
	result := New(Policy{
		Strategy:               Strategy("unsupported"),
		MaxInputTokens:         20,
		SystemPromptProtection: true,
	}).Prepare(messages)
	if result.Stats.DroppedMessages == 0 {
		t.Fatalf("expected fallback trim for unknown strategy, got %+v", result.Stats)
	}
	if result.Messages[len(result.Messages)-1].Content != "latest" {
		t.Fatalf("expected latest message retained: %+v", result.Messages)
	}
}
