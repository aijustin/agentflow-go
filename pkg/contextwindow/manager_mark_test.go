package contextwindow

import (
	"strings"
	"testing"
)

func markTestMessages() []Message {
	pad := strings.Repeat("x", 60) // ~20 estimated tokens per message
	return []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "u1 " + pad},
		{Role: RoleAssistant, Content: "a1 " + pad},
		{Role: RoleUser, Content: "u2 " + pad},
		{Role: RoleAssistant, Content: "a2 " + pad},
	}
}

func markedContents(messages []Message) map[string]string {
	out := map[string]string{}
	for _, msg := range messages {
		if msg.Metadata[MetadataKeyVisibility] == VisibilityUserOnly {
			out[msg.Content[:2]] = msg.Content
		}
	}
	return out
}

func TestManagerMarkInsteadOfDrop(t *testing.T) {
	strategies := []Strategy{
		StrategySlidingWindow,
		StrategySlidingWindowWithSummary,
		StrategyFullReplace,
		StrategyNone, // over-budget fallback still trims
	}
	pinOff := false
	for _, strategy := range strategies {
		t.Run(string(strategy)+"/mark_on", func(t *testing.T) {
			policy := Policy{
				Strategy:               strategy,
				MaxInputTokens:         40,
				SummaryTokens:          10,
				SystemPromptProtection: true,
				PinUserMessages:        &pinOff,
			}
			input := markTestMessages()
			result := New(policy, WithMarkInsteadOfDrop(true)).Prepare(input)
			if result.Stats.DroppedMessages == 0 {
				t.Fatalf("expected trims, stats=%+v", result.Stats)
			}
			if result.Stats.MarkedMessages != result.Stats.DroppedMessages {
				t.Fatalf("MarkedMessages=%d want DroppedMessages=%d", result.Stats.MarkedMessages, result.Stats.DroppedMessages)
			}
			wantLen := len(input)
			if result.Stats.Summarized {
				wantLen++ // summary message added
			}
			if len(result.Messages) != wantLen {
				t.Fatalf("mark mode must retain the full sequence: got %d messages, want %d", len(result.Messages), wantLen)
			}
			marked := markedContents(result.Messages)
			if len(marked) != result.Stats.DroppedMessages {
				t.Fatalf("marked %d messages, stats say %d dropped", len(marked), result.Stats.DroppedMessages)
			}
			if _, ok := marked["u1"]; !ok {
				t.Fatalf("oldest user message must be marked: %+v", result.Messages)
			}
			for _, msg := range result.Messages {
				if msg.Role == RoleSystem && msg.Metadata[MetadataKeyVisibility] == VisibilityUserOnly {
					t.Fatalf("system/summary messages must stay visible to both: %+v", msg)
				}
				if msg.Metadata["context_window"] == "summary" && msg.Metadata[MetadataKeyVisibility] != "" {
					t.Fatalf("summary message must stay both-visible: %+v", msg)
				}
			}
			if result.Stats.AfterTokens >= result.Stats.BeforeTokens {
				t.Fatalf("AfterTokens must reflect the visible projection: %+v", result.Stats)
			}
		})
		t.Run(string(strategy)+"/mark_off_default", func(t *testing.T) {
			policy := Policy{
				Strategy:               strategy,
				MaxInputTokens:         40,
				SystemPromptProtection: true,
				PinUserMessages:        &pinOff,
			}
			input := markTestMessages()
			result := New(policy).Prepare(input)
			if result.Stats.DroppedMessages == 0 {
				t.Fatalf("expected trims, stats=%+v", result.Stats)
			}
			if result.Stats.MarkedMessages != 0 {
				t.Fatalf("default mode must not mark: %+v", result.Stats)
			}
			if len(result.Messages) >= len(input) {
				t.Fatalf("default mode must physically drop: got %d messages", len(result.Messages))
			}
			for _, msg := range result.Messages {
				if msg.Metadata[MetadataKeyVisibility] != "" {
					t.Fatalf("default mode must not write visibility marks: %+v", msg)
				}
			}
		})
	}
}

func TestManagerMarkInsteadOfDropNoTrimNeeded(t *testing.T) {
	input := []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "hi"},
	}
	result := New(Policy{Strategy: StrategySlidingWindow, MaxInputTokens: 1000}, WithMarkInsteadOfDrop(true)).Prepare(input)
	if len(result.Messages) != len(input) {
		t.Fatalf("got %d messages", len(result.Messages))
	}
	if result.Stats.MarkedMessages != 0 || result.Stats.DroppedMessages != 0 {
		t.Fatalf("unexpected marks: %+v", result.Stats)
	}
	for _, msg := range result.Messages {
		if msg.Metadata[MetadataKeyVisibility] != "" {
			t.Fatalf("no mark expected within budget: %+v", msg)
		}
	}
}

func TestManagerMarkInsteadOfDropDoesNotMutateCallerMetadata(t *testing.T) {
	pinOff := false
	input := markTestMessages()
	New(Policy{
		Strategy:               StrategySlidingWindow,
		MaxInputTokens:         40,
		SystemPromptProtection: true,
		PinUserMessages:        &pinOff,
	}, WithMarkInsteadOfDrop(true)).Prepare(input)
	for _, msg := range input {
		if msg.Metadata[MetadataKeyVisibility] != "" {
			t.Fatalf("Prepare mutated caller message metadata: %+v", msg)
		}
	}
}
