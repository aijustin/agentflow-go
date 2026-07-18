package interjection

// DrainPhase identifies when the runtime may drain pending interjections.
type DrainPhase string

const (
	// DrainBeforeSample is the start of a sampling step (before LLM call).
	DrainBeforeSample DrainPhase = "before_sample"
	// DrainAfterToolBatch is after a tool batch completes.
	DrainAfterToolBatch DrainPhase = "after_tool_batch"
	// DrainPostCompact is after context compaction reinjected reminder/context.
	DrainPostCompact DrainPhase = "post_compact"
)

// DrainPolicy controls when mid-turn interjections enter the message list.
// Zero value means DefaultDrainPolicy().
type DrainPolicy struct {
	// BeforeSample drains at the start of each sampling step (default true).
	BeforeSample bool
	// AfterToolBatch drains after tools complete (default true).
	AfterToolBatch bool
	// DeferUntilPostCompact skips BeforeSample when compaction needs reminder
	// reinjection; drain happens at PostCompact instead (Codex steer alignment).
	DeferUntilPostCompact bool
}

// DefaultDrainPolicy matches historical behavior: drain before sample and after tools.
func DefaultDrainPolicy() DrainPolicy {
	return DrainPolicy{BeforeSample: true, AfterToolBatch: true}
}

// Normalize fills defaults for a zero-value policy.
func (p DrainPolicy) Normalize() DrainPolicy {
	if !p.BeforeSample && !p.AfterToolBatch && !p.DeferUntilPostCompact {
		return DefaultDrainPolicy()
	}
	return p
}

// Allow reports whether draining is permitted at phase.
// justCompacted is true when the latest prepareMessages set NeedsReminder.
func (p DrainPolicy) Allow(phase DrainPhase, justCompacted bool) bool {
	p = p.Normalize()
	switch phase {
	case DrainBeforeSample:
		if p.DeferUntilPostCompact && justCompacted {
			return false
		}
		return p.BeforeSample
	case DrainAfterToolBatch:
		return p.AfterToolBatch
	case DrainPostCompact:
		return p.DeferUntilPostCompact && justCompacted
	default:
		return false
	}
}
