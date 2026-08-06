package runtime

import (
	"context"
	"strings"
	"testing"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/contextwindow"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/llm"
	llmmock "github.com/aijustin/agentflow-go/pkg/llm/mock"
)

func visibilityTestProfile() (core.LLMProfileRef, []llm.Message) {
	pinOff := false
	profile := core.LLMProfileRef{Provider: "mock", Model: "test"}
	profile.Context = contextwindow.Policy{
		Strategy:        contextwindow.StrategySlidingWindow,
		MaxInputTokens:  40,
		PinUserMessages: &pinOff,
	}
	pad := strings.Repeat("x", 56) // ~19 estimated tokens per message; the window keeps the newest pair
	messages := []llm.Message{
		{Role: llm.RoleUser, Content: "u1 " + pad},
		{Role: llm.RoleAssistant, Content: "a1 " + pad},
		{Role: llm.RoleUser, Content: "u2 " + pad},
		{Role: llm.RoleAssistant, Content: "a2 " + pad},
	}
	return profile, messages
}

func TestPrepareMessagesDualVisibilityMarksAndBackfills(t *testing.T) {
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	engine, err := NewEngine(scenario, Dependencies{
		Runs:                   runstateinmem.NewRepository(),
		LLM:                    llmmock.NewGateway(),
		DualVisibilityMessages: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, messages := visibilityTestProfile()
	prepared, stats := engine.prepareMessages(context.Background(), "run-visibility", scenario.Agents["assistant"], messages, profile)
	if stats.DroppedMessages == 0 || stats.MarkedMessages != stats.DroppedMessages {
		t.Fatalf("expected marked trims, stats=%+v", stats)
	}
	// The prepared projection retains the full sequence; trimmed messages
	// carry the user-only mark instead of being dropped.
	if len(prepared) != len(messages) {
		t.Fatalf("dual visibility must retain the full sequence: got %d of %d", len(prepared), len(messages))
	}
	if !llm.IsAgentVisible(prepared[len(prepared)-1]) {
		t.Fatal("newest message must stay agent-visible")
	}
	markedPrepared := 0
	for _, msg := range prepared {
		if !llm.IsAgentVisible(msg) {
			markedPrepared++
		}
	}
	if markedPrepared != stats.DroppedMessages {
		t.Fatalf("prepared has %d marked messages, stats say %d", markedPrepared, stats.DroppedMessages)
	}
	// The marks are backfilled onto the source history so checkpoints and
	// resume keep the projection state.
	markedSource := 0
	for i, msg := range messages {
		if !llm.IsAgentVisible(msg) {
			markedSource++
			if msg.Content[:2] != "u1" && msg.Content[:2] != "a1" {
				t.Fatalf("unexpected source message marked at %d: %+v", i, msg)
			}
		}
	}
	if markedSource != markedPrepared {
		t.Fatalf("source backfill has %d marks, prepared has %d", markedSource, markedPrepared)
	}
	// The model-visible projection of the prepared sequence matches what a
	// physically-trimming default run would send.
	projection := llm.AgentVisibleMessages(prepared)
	if len(projection) != len(messages)-stats.DroppedMessages {
		t.Fatalf("projection has %d messages, want %d", len(projection), len(messages)-stats.DroppedMessages)
	}
}

func TestPrepareMessagesDualVisibilityDefaultOff(t *testing.T) {
	scenario := toolScenario(core.ApprovalNever, core.SideEffectRead, 4)
	engine, err := NewEngine(scenario, Dependencies{
		Runs: runstateinmem.NewRepository(),
		LLM:  llmmock.NewGateway(),
	})
	if err != nil {
		t.Fatal(err)
	}
	profile, messages := visibilityTestProfile()
	prepared, stats := engine.prepareMessages(context.Background(), "run-visibility-off", scenario.Agents["assistant"], messages, profile)
	if stats.DroppedMessages == 0 {
		t.Fatalf("expected trims, stats=%+v", stats)
	}
	if stats.MarkedMessages != 0 {
		t.Fatalf("default mode must not mark: %+v", stats)
	}
	if len(prepared) != len(messages)-stats.DroppedMessages {
		t.Fatalf("default mode must physically drop: got %d messages", len(prepared))
	}
	for _, msg := range prepared {
		if !llm.IsAgentVisible(msg) {
			t.Fatalf("default mode must not write visibility marks: %+v", msg)
		}
	}
	for _, msg := range messages {
		if msg.Metadata != nil {
			t.Fatalf("default mode must not touch source metadata: %+v", msg)
		}
	}
}
