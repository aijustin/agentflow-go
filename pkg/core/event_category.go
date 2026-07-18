package core

import "fmt"

// EventCategory returns a coarse product-facing category for an event type:
// tool, knowledge, skill, llm, memory, or run.
func EventCategory(typ EventType) string {
	switch typ {
	case EventToolCalled, EventToolReturned, EventToolDenied:
		return "tool"
	case EventSkillApplied:
		return "skill"
	case EventLLMCalled, EventLLMReturned, EventLLMTokenUsage:
		return "llm"
	case EventMemoryRead, EventMemoryWrite, EventMemoryPromoted, EventMemoryDemoted, EventMemoryEvicted:
		return "memory"
	case EventContextPrepared, EventContextIncomplete:
		// Context assembly is run-lifecycle plumbing, not a knowledge hit stream.
		return "run"
	default:
		return "run"
	}
}

// DisplayLabel returns a short human-readable label for product UI surfaces.
func DisplayLabel(typ EventType) string {
	switch typ {
	case EventRunStarted:
		return "Run started"
	case EventRunCompleted:
		return "Run completed"
	case EventRunFailed:
		return "Run failed"
	case EventRunCancelled:
		return "Run cancelled"
	case EventRunPaused:
		return "Run paused"
	case EventRunResumed:
		return "Run resumed"
	case EventStepStarted:
		return "Step started"
	case EventStepCompleted:
		return "Step completed"
	case EventStepFailed:
		return "Step failed"
	case EventSubgraphStarted:
		return "Subgraph started"
	case EventSubgraphCompleted:
		return "Subgraph completed"
	case EventToolCalled:
		return "Tool called"
	case EventToolReturned:
		return "Tool returned"
	case EventToolDenied:
		return "Tool denied"
	case EventLLMCalled:
		return "LLM called"
	case EventLLMReturned:
		return "LLM returned"
	case EventLLMTokenUsage:
		return "LLM token usage"
	case EventHumanGateOpened:
		return "Human gate opened"
	case EventHumanGateDecided:
		return "Human gate decided"
	case EventHumanGateExpired:
		return "Human gate expired"
	case EventMemoryRead:
		return "Memory read"
	case EventMemoryWrite:
		return "Memory write"
	case EventMemoryPromoted:
		return "Memory promoted"
	case EventMemoryDemoted:
		return "Memory demoted"
	case EventMemoryEvicted:
		return "Memory evicted"
	case EventContextPrepared:
		return "Context prepared"
	case EventContextIncomplete:
		return "Context incomplete"
	case EventSkillApplied:
		return "Skill applied"
	default:
		return string(typ)
	}
}

// EventFilterPreset names a read-side event view for StreamRun / ListEvents.
// Storage always keeps the full event stream; presets only project it.
type EventFilterPreset string

const (
	// EventFilterProductUI hides high-frequency internal noise for chat UIs.
	EventFilterProductUI EventFilterPreset = "product_ui"
	// EventFilterDiagnostic keeps internal events (MemoryRead, ContextPrepared, …)
	// for Debug / export / SSE trace views.
	EventFilterDiagnostic EventFilterPreset = "diagnostic"
)

// ParseEventFilterPreset accepts API values. Empty means diagnostic (full stream).
func ParseEventFilterPreset(value string) (EventFilterPreset, error) {
	switch EventFilterPreset(value) {
	case "", EventFilterDiagnostic:
		return EventFilterDiagnostic, nil
	case EventFilterProductUI:
		return EventFilterProductUI, nil
	default:
		return "", fmt.Errorf("core: unknown event filter preset %q", value)
	}
}

// NormalizeEventFilterPreset maps empty to diagnostic; unknown values stay as-is
// so callers can reject them explicitly via ParseEventFilterPreset.
func NormalizeEventFilterPreset(preset EventFilterPreset) EventFilterPreset {
	if preset == "" {
		return EventFilterDiagnostic
	}
	return preset
}

// Allows reports whether typ is included in this preset's view.
func (p EventFilterPreset) Allows(typ EventType) bool {
	switch NormalizeEventFilterPreset(p) {
	case EventFilterProductUI:
		return EventFilterPresetProductUI(typ)
	default:
		return EventFilterPresetDiagnostic(typ)
	}
}

// EventFilterPresetProductUI is a preset predicate for product-facing event
// streams. It hides high-frequency internal noise (memory reads and context
// preparation) while keeping tool/LLM/skill/run lifecycle signals visible.
//
// Use with ShouldEmitToProductUI, or pass directly to an event filter:
//
//	if EventFilterPresetProductUI(event.Type) { publish(event) }
func EventFilterPresetProductUI(typ EventType) bool {
	return ShouldEmitToProductUI(typ)
}

// EventFilterPresetDiagnostic keeps the full event stream, including internal
// MemoryRead / ContextPrepared signals used by Debug and export views.
func EventFilterPresetDiagnostic(typ EventType) bool {
	return true
}

// ShouldEmitToDiagnosticUI is an alias for EventFilterPresetDiagnostic (AF-REQ-05).
func ShouldEmitToDiagnosticUI(typ EventType) bool {
	return EventFilterPresetDiagnostic(typ)
}

// ShouldEmitToProductUI reports whether an event type belongs on a product UI
// timeline. MemoryRead and ContextPrepared are treated as internal noise.
func ShouldEmitToProductUI(typ EventType) bool {
	switch typ {
	case EventMemoryRead, EventContextPrepared:
		return false
	default:
		return true
	}
}

// IsLifecycleEvent reports whether typ is a run-lifecycle event that should
// carry Episode/Session correlation in its payload.
func IsLifecycleEvent(typ EventType) bool {
	switch typ {
	case EventRunStarted, EventRunCompleted, EventRunFailed, EventRunCancelled, EventRunPaused, EventRunResumed:
		return true
	default:
		return false
	}
}

