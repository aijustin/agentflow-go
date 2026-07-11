package core_test

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

func TestEventSinkFuncEmit(t *testing.T) {
	var emitted core.Event
	sink := core.EventSinkFunc(func(_ context.Context, event core.Event) error {
		emitted = event
		return nil
	})
	event := core.Event{Type: core.EventRunStarted, RunID: "run-1"}
	if err := sink.Emit(context.Background(), event); err != nil {
		t.Fatal(err)
	}
	if emitted.RunID != "run-1" {
		t.Fatalf("unexpected event: %+v", emitted)
	}
}

func TestEventCategoryAndDisplayLabel(t *testing.T) {
	if got := core.EventCategory(core.EventToolCalled); got != "tool" {
		t.Fatalf("tool category=%q", got)
	}
	if got := core.EventCategory(core.EventSkillApplied); got != "skill" {
		t.Fatalf("skill category=%q", got)
	}
	if got := core.EventCategory(core.EventLLMCalled); got != "llm" {
		t.Fatalf("llm category=%q", got)
	}
	if got := core.EventCategory(core.EventMemoryWrite); got != "memory" {
		t.Fatalf("memory category=%q", got)
	}
	if got := core.EventCategory(core.EventRunStarted); got != "run" {
		t.Fatalf("run category=%q", got)
	}
	if got := core.DisplayLabel(core.EventSkillApplied); got != "Skill applied" {
		t.Fatalf("display label=%q", got)
	}
}

func TestShouldEmitToProductUI(t *testing.T) {
	if core.ShouldEmitToProductUI(core.EventMemoryRead) {
		t.Fatal("MemoryRead should be filtered from product UI")
	}
	if core.ShouldEmitToProductUI(core.EventContextPrepared) {
		t.Fatal("ContextPrepared should be filtered from product UI")
	}
	if !core.EventFilterPresetProductUI(core.EventToolCalled) {
		t.Fatal("ToolCalled should be visible in product UI")
	}
	if !core.ShouldEmitToProductUI(core.EventSkillApplied) {
		t.Fatal("SkillApplied should be visible in product UI")
	}
}

func TestEventStructCarriesCategoryFields(t *testing.T) {
	event := core.Event{
		Type:         core.EventToolCalled,
		Category:     core.EventCategory(core.EventToolCalled),
		DisplayLabel: core.DisplayLabel(core.EventToolCalled),
	}
	if event.Category != "tool" || event.DisplayLabel != "Tool called" {
		t.Fatalf("unexpected event enrichment: %+v", event)
	}
}
