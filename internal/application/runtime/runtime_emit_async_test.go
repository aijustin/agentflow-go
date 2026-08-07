package runtime

import (
	"context"
	"testing"
	"time"

	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/internal/application/emit"
	"github.com/aijustin/agentflow-go/pkg/core"
)

// blockingNonLifecycleSink passes lifecycle events through immediately but
// blocks non-lifecycle delivery until released, simulating a durable sink
// that fell behind (slow DB, network partition).
type blockingNonLifecycleSink struct {
	release chan struct{}
}

func (s *blockingNonLifecycleSink) Emit(_ context.Context, event core.Event) error {
	if emit.IsCriticalLifecycleEvent(event.Type) {
		return nil
	}
	<-s.release
	return nil
}

// TestEngineRunDoesNotBlockOnSlowEventSink: a sink stuck on non-lifecycle
// delivery must not stall the tool loop or run completion. Queued events
// beyond capacity are dropped and counted; lifecycle events (RunStarted /
// RunCompleted) are still delivered synchronously.
func TestEngineRunDoesNotBlockOnSlowEventSink(t *testing.T) {
	repo := runstateinmem.NewRepository()
	sink := &blockingNonLifecycleSink{release: make(chan struct{})}
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs:               repo,
		LLM:                &capturingGateway{response: "ok"},
		Events:             sink,
		EventQueueCapacity: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	result, err := engine.Run(context.Background(), RunRequest{RunID: "run-slow-sink", Agent: "assistant", Prompt: "hi"})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatal(err)
	}
	if result.Output != "ok" {
		t.Fatalf("unexpected output %q", result.Output)
	}
	// Without the async queue this run would block forever; allow generous
	// headroom over the two bounded lifecycle flush barriers.
	if elapsed > 5*time.Second {
		t.Fatalf("run blocked on stuck event sink: %v", elapsed)
	}
	close(sink.release)
	engine.Close()
	if got := engine.DroppedEvents(); got == 0 {
		t.Fatal("expected queued events to be dropped behind the stuck sink")
	}
}

// TestEngineRunDeliversEventsWithFastSink: with a healthy sink the run's
// events (queued non-lifecycle included) are all delivered by the time Run
// returns, preserving the pre-async observable behavior.
func TestEngineRunDeliversEventsWithFastSink(t *testing.T) {
	repo := runstateinmem.NewRepository()
	events := &captureEvents{}
	engine, err := NewEngine(baseScenario(false), Dependencies{
		Runs:   repo,
		LLM:    &capturingGateway{response: "ok"},
		Events: events,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer engine.Close()
	if _, err := engine.Run(context.Background(), RunRequest{RunID: "run-fast-sink", Agent: "assistant", Prompt: "hi"}); err != nil {
		t.Fatal(err)
	}
	for _, typ := range []core.EventType{core.EventRunStarted, core.EventLLMCalled, core.EventRunCompleted} {
		if !events.has(typ) {
			t.Fatalf("expected %s after Run returned, got %+v", typ, events.types())
		}
	}
}
