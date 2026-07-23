package agentflow

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// TestStreamEventTeeCountsDrops: events that do not fit the tee buffer are
// counted so StreamRun can surface an events_lost marker frame instead of
// dropping them silently.
func TestStreamEventTeeCountsDrops(t *testing.T) {
	tee := &streamEventTee{runID: "run-1", events: make(chan core.Event, 1)}
	event := core.Event{Type: core.EventLLMCalled, RunID: "run-1"}
	for i := 0; i < 3; i++ {
		if err := tee.Emit(context.Background(), event); err != nil {
			t.Fatal(err)
		}
	}
	if got := tee.dropped.Load(); got != 2 {
		t.Fatalf("expected 2 counted drops, got %d", got)
	}
	// Events for other runs are ignored, not counted.
	_ = tee.Emit(context.Background(), core.Event{Type: core.EventLLMCalled, RunID: "run-other"})
	if got := tee.dropped.Load(); got != 2 {
		t.Fatalf("foreign run events must not count as drops, got %d", got)
	}
	// After the stream is done, Emit is a no-op.
	tee.done.Store(true)
	_ = tee.Emit(context.Background(), event)
	if got := tee.dropped.Load(); got != 2 {
		t.Fatalf("post-done emits must not count as drops, got %d", got)
	}
}
