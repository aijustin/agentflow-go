package runtime

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/aijustin/agentflow-go/pkg/llm"
)

// usageTracker accumulates provider-reported token usage for one run. The
// heuristic EstimateTokens counter systematically under- or over-counts
// tool-heavy transcripts, so the last call's real usage is what drives
// context-window trigger decisions; the accumulated totals feed terminal
// usage reporting, and ContextRecoveryAttempts caps context-length recovery
// retries across pause/resume boundaries (the tracker is checkpointed).
type usageTracker struct {
	mu                      sync.Mutex
	InputTokens             int `json:"input_tokens,omitempty"`
	OutputTokens            int `json:"output_tokens,omitempty"`
	TotalTokens             int `json:"total_tokens,omitempty"`
	LastCallInputTokens     int `json:"last_call_input_tokens,omitempty"`
	LastCallOutputTokens    int `json:"last_call_output_tokens,omitempty"`
	ContextRecoveryAttempts int `json:"context_recovery_attempts,omitempty"`
}

func newUsageTracker() *usageTracker {
	return &usageTracker{}
}

// record folds one call's usage into the totals and remembers it as the
// most recent call, replacing (not adding to) the previous last-call values.
func (t *usageTracker) record(usage llm.TokenUsage) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.InputTokens += usage.InputTokens
	t.OutputTokens += usage.OutputTokens
	t.TotalTokens += usage.TotalTokens
	t.LastCallInputTokens = usage.InputTokens
	t.LastCallOutputTokens = usage.OutputTokens
}

// lastCallTokens returns the previous call's real input+output token count:
// the best available predictor of the next request's size, because the last
// response's output tokens become part of the next call's input.
func (t *usageTracker) lastCallTokens() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.LastCallInputTokens + t.LastCallOutputTokens
}

// totalTokens returns the run's accumulated provider-reported total.
func (t *usageTracker) totalTokens() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.TotalTokens
}

func (t *usageTracker) contextRecoveryAttempts() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.ContextRecoveryAttempts
}

func (t *usageTracker) markContextRecovery() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.ContextRecoveryAttempts++
}

// MarshalJSON exports tracker totals without the mutex.
func (t *usageTracker) MarshalJSON() ([]byte, error) {
	if t == nil {
		return []byte(`{}`), nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	type wire struct {
		InputTokens             int `json:"input_tokens,omitempty"`
		OutputTokens            int `json:"output_tokens,omitempty"`
		TotalTokens             int `json:"total_tokens,omitempty"`
		LastCallInputTokens     int `json:"last_call_input_tokens,omitempty"`
		LastCallOutputTokens    int `json:"last_call_output_tokens,omitempty"`
		ContextRecoveryAttempts int `json:"context_recovery_attempts,omitempty"`
	}
	return json.Marshal(wire{
		InputTokens:             t.InputTokens,
		OutputTokens:            t.OutputTokens,
		TotalTokens:             t.TotalTokens,
		LastCallInputTokens:     t.LastCallInputTokens,
		LastCallOutputTokens:    t.LastCallOutputTokens,
		ContextRecoveryAttempts: t.ContextRecoveryAttempts,
	})
}

// decodeUsageTracker restores a checkpointed tracker. An empty payload (a
// snapshot written before checkpoint_usage existed) decodes to a zero
// tracker, preserving the old heuristic-only context behavior.
func decodeUsageTracker(raw json.RawMessage) (*usageTracker, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return newUsageTracker(), nil
	}
	var tracker usageTracker
	if err := json.Unmarshal(raw, &tracker); err != nil {
		return nil, fmt.Errorf("decode usage tracker: %w", err)
	}
	return &tracker, nil
}

// usageTrackerFor returns the run's tracker, creating it on first use. The
// entry is dropped by clearRunScopedState once the run reaches a terminal
// state, so a long-lived worker does not accumulate stale entries.
func (e *Engine) usageTrackerFor(runID string) *usageTracker {
	tracker, _ := e.usageTrackers.LoadOrStore(runID, newUsageTracker())
	return tracker.(*usageTracker)
}

// restoreUsageTracker replaces the run's tracker with checkpointed state on
// resume, so recovery budgets and usage totals survive a pause.
func (e *Engine) restoreUsageTracker(runID string, tracker *usageTracker) {
	if tracker == nil {
		tracker = newUsageTracker()
	}
	e.usageTrackers.Store(runID, tracker)
}
