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

func TestEventFilterPresetDiagnosticKeepsInternalEvents(t *testing.T) {
	if !core.EventFilterPresetDiagnostic(core.EventMemoryRead) {
		t.Fatal("Diagnostic should keep MemoryRead")
	}
	if !core.EventFilterPresetDiagnostic(core.EventContextPrepared) {
		t.Fatal("Diagnostic should keep ContextPrepared")
	}
	if !core.EventFilterDiagnostic.Allows(core.EventMemoryRead) {
		t.Fatal("EventFilterDiagnostic.Allows should keep MemoryRead")
	}
	if core.EventFilterProductUI.Allows(core.EventMemoryRead) {
		t.Fatal("EventFilterProductUI.Allows should hide MemoryRead")
	}
	if !core.EventFilterProductUI.Allows(core.EventToolCalled) {
		t.Fatal("EventFilterProductUI.Allows should keep ToolCalled")
	}
}

func TestParseEventFilterPreset(t *testing.T) {
	preset, err := core.ParseEventFilterPreset("")
	if err != nil || preset != core.EventFilterDiagnostic {
		t.Fatalf("empty preset: got %q err=%v", preset, err)
	}
	preset, err = core.ParseEventFilterPreset("product_ui")
	if err != nil || preset != core.EventFilterProductUI {
		t.Fatalf("product_ui: got %q err=%v", preset, err)
	}
	if _, err := core.ParseEventFilterPreset("nope"); err == nil {
		t.Fatal("expected unknown preset error")
	}
	if core.NormalizeEventFilterPreset("") != core.EventFilterDiagnostic {
		t.Fatal("empty should normalize to diagnostic")
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
