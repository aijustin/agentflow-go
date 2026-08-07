package emit

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// captureLogger records warn/error messages for assertions.
type captureLogger struct {
	mu     sync.Mutex
	warns  []string
	errors []string
}

func (l *captureLogger) Warn(_ context.Context, msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.warns = append(l.warns, msg)
}

func (l *captureLogger) Error(_ context.Context, msg string, _ ...any) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.errors = append(l.errors, msg)
}

func (l *captureLogger) warnCount() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return len(l.warns)
}

// panicOnTypeSink panics when delivering panicOn, and records everything else.
type panicOnTypeSink struct {
	panicOn core.EventType
	inner   recordingSink
}

func (s *panicOnTypeSink) Emit(_ context.Context, event core.Event) error {
	if event.Type == s.panicOn {
		panic("sink exploded")
	}
	return s.inner.Emit(context.Background(), event)
}

// F3 (DEFECT_REPORT second round): a panicking host sink must not crash the
// process. The dispatcher recovers per delivery, counts the event as dropped,
// logs the failure, and keeps delivering subsequent events.
func TestPipelineDispatcherSurvivesSinkPanic(t *testing.T) {
	sink := &panicOnTypeSink{panicOn: core.EventToolCalled}
	logger := &captureLogger{}
	p := NewPipeline(Config{Sink: sink, Logger: logger})
	ctx := context.Background()
	p.Emit(ctx, "scenario", nil, core.EventToolCalled, "run-1", nil)   // panics at the sink
	p.Emit(ctx, "scenario", nil, core.EventToolReturned, "run-1", nil) // must still be delivered
	// The trailing lifecycle event flushes the queue first, so by the time
	// Emit returns the dispatcher has attempted both queued events.
	p.Emit(ctx, "scenario", nil, core.EventRunCompleted, "run-1", nil)
	p.Close()

	got := sink.inner.types()
	if len(got) != 2 || got[0] != core.EventToolReturned || got[1] != core.EventRunCompleted {
		t.Fatalf("dispatcher must keep delivering after a sink panic, got %v", got)
	}
	if dropped := p.DroppedEvents(); dropped != 1 {
		t.Fatalf("panicked delivery must be counted as dropped, got %d", dropped)
	}
	if logger.warnCount() == 0 {
		t.Fatal("panicked delivery must be logged")
	}
}

// F3: a panic inside a synchronous lifecycle delivery (EmitWithLifecycleRetry)
// surfaces as an error to the caller instead of crashing its goroutine.
func TestEmitWithLifecycleRetrySurvivesSinkPanic(t *testing.T) {
	sink := &panicOnTypeSink{panicOn: core.EventRunCompleted}
	err := EmitWithLifecycleRetry(context.Background(), sink, core.Event{Type: core.EventRunCompleted, RunID: "run-1"})
	if err == nil || !strings.Contains(err.Error(), "panic") {
		t.Fatalf("expected a recovered-panic error, got %v", err)
	}
}

// F6 (DEFECT_REPORT second round): an enqueue whose send wins the race against
// Close's final sweep used to leave the event stranded in the queue — never
// delivered, never counted. The post-send recheck hands stranded items to
// sweepUndelivered, which counts them and releases pending flush barriers.
func TestPipelineSweepUndeliveredCountsStrandedEvents(t *testing.T) {
	sink := &recordingSink{}
	p := NewPipeline(Config{Sink: sink})
	ctx := context.Background()
	// Simulate the post-race state directly: the dispatcher has exited
	// (stop closed, stopped=true) while these items were in flight.
	close(p.stop)
	<-p.dispatchDone
	flushAck := make(chan struct{})
	p.queue <- queueItem{ctx: ctx, event: core.Event{Type: core.EventToolCalled, RunID: "run-1"}}
	p.queue <- queueItem{flush: flushAck}
	p.sweepUndelivered()
	if dropped := p.DroppedEvents(); dropped != 1 {
		t.Fatalf("stranded event must be counted as dropped, got %d", dropped)
	}
	select {
	case <-flushAck:
	default:
		t.Fatal("stranded flush barrier must be released")
	}
	if got := sink.types(); len(got) != 0 {
		t.Fatalf("stranded events must not be delivered after stop, got %v", got)
	}
	// No p.Close(): the test already stopped the dispatcher by hand.
}

// F6 stress net: across many emit-vs-Close races every emitted event is
// accounted for exactly once (delivered or dropped) — never silently lost.
func TestPipelineEnqueueCloseRaceAccountsEveryEvent(t *testing.T) {
	for i := range 300 {
		sink := &recordingSink{block: make(chan struct{})}
		p := NewPipeline(Config{Sink: sink, DrainTimeout: time.Millisecond})
		var wg sync.WaitGroup
		var sent atomic.Int64
		for range 4 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p.Emit(context.Background(), "scenario", nil, core.EventToolCalled, "run-1", nil)
				sent.Add(1)
			}()
		}
		if i%3 == 0 {
			close(sink.block) // let the dispatcher drain before/during Close
		}
		p.Close()
		wg.Wait()
		if i%3 != 0 {
			close(sink.block)
		}
		// The dispatcher may still be inside a blocked delivery when Close
		// gives up; releasing the sink lets it finish and exit.
		<-p.dispatchDone
		delivered := int64(len(sink.types()))
		if delivered+p.DroppedEvents() != sent.Load() {
			t.Fatalf("iteration %d: delivered %d + dropped %d != sent %d", i, delivered, p.DroppedEvents(), sent.Load())
		}
	}
}
