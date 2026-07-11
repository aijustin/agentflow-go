package core

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
