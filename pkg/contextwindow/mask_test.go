package contextwindow_test

import (
	"testing"

	"github.com/aijustin/agentflow-go/pkg/contextwindow"
)

func TestMaskObservationsAndCompactContext(t *testing.T) {
	messages := []contextwindow.Message{
		{Role: contextwindow.RoleUser, Content: "u1"},
		{Role: contextwindow.RoleAssistant, Content: "a1"},
		{Role: contextwindow.RoleTool, Content: "old tool output"},
		{Role: contextwindow.RoleUser, Content: "u2"},
		{Role: contextwindow.RoleAssistant, Content: "a2"},
		{Role: contextwindow.RoleTool, Content: "recent tool output"},
	}

	masked := contextwindow.MaskObservations(messages, 1)
	if masked[2].Content == "old tool output" {
		t.Fatalf("expected old tool result masked: %q", masked[2].Content)
	}
	if masked[5].Content != "recent tool output" {
		t.Fatalf("expected recent tool result preserved: %q", masked[5].Content)
	}

	compacted := contextwindow.CompactContext(masked)
	if len(compacted) != 5 {
		t.Fatalf("expected masked tool dropped, got %d messages", len(compacted))
	}
}

func TestManagerAppliesObservationMaskBeforeSummarization(t *testing.T) {
	messages := []contextwindow.Message{
		{Role: contextwindow.RoleSystem, Content: "sys"},
		{Role: contextwindow.RoleUser, Content: "u1"},
		{Role: contextwindow.RoleAssistant, Content: "a1"},
		{Role: contextwindow.RoleTool, Content: "old tool output that should be masked"},
		{Role: contextwindow.RoleUser, Content: "u2"},
		{Role: contextwindow.RoleAssistant, Content: "a2"},
		{Role: contextwindow.RoleTool, Content: "recent"},
	}
	result := contextwindow.New(contextwindow.Policy{
		Strategy:                  contextwindow.StrategyNone,
		MaxInputTokens:            10000,
		ObservationMaskAfterTurns: 1,
	}).Prepare(messages)
	if result.Messages[3].Content == "old tool output that should be masked" {
		t.Fatalf("manager did not mask old tool output: %q", result.Messages[3].Content)
	}
}
