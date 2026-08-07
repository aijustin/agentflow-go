package emit

import (
	"context"
	"errors"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

type recordingSink struct {
	mu     sync.Mutex
	events []core.Event
	// block, when non-nil, gates non-lifecycle delivery (tests close it to
	// release a stuck dispatcher).
	block chan struct{}
}

func (s *recordingSink) Emit(_ context.Context, event core.Event) error {
	if s.block != nil && !IsCriticalLifecycleEvent(event.Type) {
		<-s.block
	}
	s.mu.Lock()
	s.events = append(s.events, event)
	s.mu.Unlock()
	return nil
}

func (s *recordingSink) types() []core.EventType {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]core.EventType, 0, len(s.events))
	for _, event := range s.events {
		out = append(out, event.Type)
	}
	return out
}

type countingRecorder struct {
	dropped atomic.Int64
}

func (r *countingRecorder) IncCounter(_ context.Context, name observability.MetricName, attrs ...observability.Attribute) {
	if name == observability.MetricRuntimeEventsDroppedTotal {
		r.dropped.Add(1)
	}
}

func (r *countingRecorder) AddCounter(context.Context, observability.MetricName, float64, ...observability.Attribute) {
}

func (r *countingRecorder) ObserveHistogram(context.Context, observability.MetricName, float64, ...observability.Attribute) {
}

func (r *countingRecorder) SetGauge(context.Context, observability.MetricName, float64, ...observability.Attribute) {
}

// TestPipelineDeliversQueuedEventsInOrder: the single dispatcher preserves
// enqueue order, and a trailing lifecycle event observes the full backlog.
func TestPipelineDeliversQueuedEventsInOrder(t *testing.T) {
	sink := &recordingSink{}
	p := NewPipeline(Config{Sink: sink})
	defer p.Close()
	ctx := context.Background()
	for range 8 {
		p.Emit(ctx, "scenario", nil, core.EventToolCalled, "run-1", nil)
	}
	// The lifecycle event flushes the queue before its own synchronous
	// delivery, so after Emit returns everything must be at the sink.
	p.Emit(ctx, "scenario", nil, core.EventRunCompleted, "run-1", nil)
	got := sink.types()
	if len(got) != 9 {
		t.Fatalf("expected 9 events, got %d (%v)", len(got), got)
	}
	for i, typ := range got[:8] {
		if typ != core.EventToolCalled {
			t.Fatalf("event %d out of order: %v", i, got)
		}
	}
	if got[8] != core.EventRunCompleted {
		t.Fatalf("lifecycle event must be last: %v", got)
	}
}

// TestPipelineDropsWhenQueueFull: a stuck sink must not block emitters; once
// the bounded queue is full, events are dropped and counted (counter +
// metric).
func TestPipelineDropsWhenQueueFull(t *testing.T) {
	sink := &recordingSink{block: make(chan struct{})}
	recorder := &countingRecorder{}
	p := NewPipeline(Config{Sink: sink, Recorder: recorder, QueueCapacity: 2, FlushTimeout: 10 * time.Millisecond})
	ctx := context.Background()
	// First event occupies the dispatcher; the next two fill the queue.
	for range 12 {
		p.Emit(ctx, "scenario", nil, core.EventToolCalled, "run-1", nil)
	}
	if got := p.DroppedEvents(); got == 0 {
		t.Fatal("expected drops with a stuck sink and a full queue")
	}
	if got := recorder.dropped.Load(); got != p.DroppedEvents() {
		t.Fatalf("metric counter %d != dropped %d", got, p.DroppedEvents())
	}
	close(sink.block)
	p.Close()
}

// TestPipelineCriticalEventsDeliveredSynchronously: lifecycle events never
// enter the queue — a stuck dispatcher cannot delay or drop them.
func TestPipelineCriticalEventsDeliveredSynchronously(t *testing.T) {
	sink := &recordingSink{block: make(chan struct{})}
	p := NewPipeline(Config{Sink: sink, QueueCapacity: 1, FlushTimeout: 10 * time.Millisecond})
	defer close(sink.block)
	defer p.Close()
	ctx := context.Background()
	p.Emit(ctx, "scenario", nil, core.EventToolCalled, "run-1", nil) // occupies dispatcher
	p.Emit(ctx, "scenario", nil, core.EventToolCalled, "run-1", nil) // fills queue
	start := time.Now()
	p.Emit(ctx, "scenario", nil, core.EventRunCompleted, "run-1", nil)
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("lifecycle emit blocked behind stuck queue: %v", elapsed)
	}
	for _, typ := range sink.types() {
		if typ == core.EventRunCompleted {
			return
		}
	}
	t.Fatalf("lifecycle event not delivered synchronously: %v", sink.types())
}

// TestPipelineSlowSinkDoesNotBlockEmit: with queue headroom, emission cost is
// independent of sink latency.
func TestPipelineSlowSinkDoesNotBlockEmit(t *testing.T) {
	sink := core.EventSinkFunc(func(context.Context, core.Event) error {
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	p := NewPipeline(Config{Sink: sink, QueueCapacity: 64})
	defer p.Close()
	start := time.Now()
	for range 32 {
		p.Emit(context.Background(), "scenario", nil, core.EventToolCalled, "run-1", nil)
	}
	if elapsed := time.Since(start); elapsed > 200*time.Millisecond {
		t.Fatalf("enqueue blocked on slow sink: %v for 32 events", elapsed)
	}
}

// TestPipelineTeeRunsSynchronously: the stream tee observes the event before
// Emit returns, even when durable delivery is queued behind a stuck sink.
func TestPipelineTeeRunsSynchronously(t *testing.T) {
	sink := &recordingSink{block: make(chan struct{})}
	defer close(sink.block)
	p := NewPipeline(Config{Sink: sink, QueueCapacity: 1})
	defer p.Close()
	teed := &recordingSink{}
	ctx := ContextWithEventTee(context.Background(), teed)
	p.Emit(ctx, "scenario", nil, core.EventToolCalled, "run-1", nil) // sticks dispatcher
	p.Emit(ctx, "scenario", nil, core.EventToolCalled, "run-1", nil)
	if got := len(teed.types()); got != 2 {
		t.Fatalf("tee must observe events synchronously, got %d", got)
	}
}

// TestPipelineCloseDrainsBacklog: Close waits for the queued backlog and then
// stops the dispatcher goroutine.
func TestPipelineCloseDrainsBacklog(t *testing.T) {
	before := runtime.NumGoroutine()
	sink := &recordingSink{}
	p := NewPipeline(Config{Sink: sink, DrainTimeout: 2 * time.Second})
	for range 16 {
		p.Emit(context.Background(), "scenario", nil, core.EventToolCalled, "run-1", nil)
	}
	p.Close()
	if got := len(sink.types()); got != 16 {
		t.Fatalf("Close must drain the backlog, got %d of 16", got)
	}
	// Emits after Close are dropped (counted), never delivered, and the
	// dispatcher goroutine is gone.
	p.Emit(context.Background(), "scenario", nil, core.EventToolCalled, "run-1", nil)
	if got := p.DroppedEvents(); got != 1 {
		t.Fatalf("expected post-Close emit to be counted as dropped, got %d", got)
	}
	if got := len(sink.types()); got != 16 {
		t.Fatalf("post-Close emit must not be delivered, got %d events", got)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("dispatcher goroutine still running after Close (before=%d now=%d)", before, runtime.NumGoroutine())
}

// TestEmitWithLifecycleRetryRetriesStartedAndResumed: the critical set covers
// every lifecycle event, including RunStarted/RunResumed.
func TestEmitWithLifecycleRetryRetriesStartedAndResumed(t *testing.T) {
	for _, typ := range []core.EventType{core.EventRunStarted, core.EventRunResumed} {
		calls := 0
		sink := core.EventSinkFunc(func(context.Context, core.Event) error {
			calls++
			if calls < 2 {
				return errors.New("blip")
			}
			return nil
		})
		if err := EmitWithLifecycleRetry(context.Background(), sink, core.Event{Type: typ, RunID: "run-1"}); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
		if calls != 2 {
			t.Fatalf("%s: expected retry, got %d calls", typ, calls)
		}
	}
}
