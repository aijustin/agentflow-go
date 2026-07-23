package runtime

import (
	"context"
	"errors"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
)

type flakySink struct {
	failures int
	calls    int
	err      error
}

func (s *flakySink) Emit(_ context.Context, _ core.Event) error {
	s.calls++
	if s.calls <= s.failures {
		return s.err
	}
	return nil
}

func lifecycleEvent() core.Event {
	return core.Event{Type: core.EventRunCompleted, RunID: "run-1"}
}

// TestEmitWithLifecycleRetryRetriesLifecycleEvents: a transient sink outage
// must not silently drop lifecycle events.
func TestEmitWithLifecycleRetryRetriesLifecycleEvents(t *testing.T) {
	sink := &flakySink{failures: 2, err: errors.New("db blip")}
	if err := EmitWithLifecycleRetry(context.Background(), sink, lifecycleEvent()); err != nil {
		t.Fatal(err)
	}
	if sink.calls != 3 {
		t.Fatalf("expected 3 attempts (1 + 2 retries), got %d", sink.calls)
	}
}

// TestEmitWithLifecycleRetryGivesUpAfterBoundedAttempts: a permanent failure
// surfaces after the bounded attempts so the caller can log it at error
// level.
func TestEmitWithLifecycleRetryGivesUpAfterBoundedAttempts(t *testing.T) {
	sink := &flakySink{failures: 10, err: errors.New("db down")}
	if err := EmitWithLifecycleRetry(context.Background(), sink, lifecycleEvent()); err == nil {
		t.Fatal("expected error after bounded retries")
	}
	if sink.calls != lifecycleEmitAttempts {
		t.Fatalf("expected %d attempts, got %d", lifecycleEmitAttempts, sink.calls)
	}
}

// TestEmitWithLifecycleRetryDoesNotRetryRegularEvents: non-lifecycle events
// stay single-attempt best-effort.
func TestEmitWithLifecycleRetryDoesNotRetryRegularEvents(t *testing.T) {
	sink := &flakySink{failures: 10, err: errors.New("db down")}
	event := core.Event{Type: core.EventLLMCalled, RunID: "run-1"}
	if err := EmitWithLifecycleRetry(context.Background(), sink, event); err == nil {
		t.Fatal("expected error")
	}
	if sink.calls != 1 {
		t.Fatalf("regular events must not be retried, got %d calls", sink.calls)
	}
}
