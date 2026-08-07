// Package emit provides the shared runtime event emission pipeline used by
// both the autonomous engine (internal/application/runtime) and the framework
// facade (root package): lifecycle payload wrapping, redaction, tenant/trace
// stamping, durable sink delivery, and the context-scoped stream tee.
//
// Delivery is split by event class. Critical lifecycle events (RunStarted /
// Completed / Failed / Cancelled / Paused / Resumed — see
// core.IsLifecycleEvent) are delivered synchronously with bounded retries:
// they decide external state consistency and must not be lost or reordered
// behind a queue. All other events go through a bounded queue drained by a
// single dispatcher goroutine (preserving enqueue order); a slow or stuck
// sink therefore cannot stall the tool loop. When the queue is full the
// event is dropped and counted (DroppedEvents + the
// agentflow_runtime_events_dropped_total metric + a rate-limited warning),
// mirroring the drop semantics of the stream tee / event hub.
package emit

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/governance"
	"github.com/aijustin/agentflow-go/pkg/log"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/retry"
)

const (
	// DefaultQueueCapacity bounds the asynchronous event queue when the host
	// does not configure one. Non-critical events beyond this depth are
	// dropped (and counted) instead of blocking the run.
	DefaultQueueCapacity = 1024
	// DefaultDrainTimeout bounds how long Close waits for the dispatcher to
	// deliver the queued backlog before giving up and dropping the rest.
	DefaultDrainTimeout = 2 * time.Second
	// defaultFlushTimeout bounds the ordering barrier taken before a
	// synchronous lifecycle event: long enough for a healthy sink to clear
	// the backlog, short enough that a stuck sink only delays a lifecycle
	// transition by this much.
	defaultFlushTimeout = 250 * time.Millisecond
)

// Config parameterizes a Pipeline.
type Config struct {
	// Sink receives durable event delivery. Nil installs a no-op sink.
	Sink core.EventSink
	// Logger receives drop / delivery-failure warnings. Nil discards them.
	Logger log.Logger
	// Recorder receives the dropped-events counter. Nil discards metrics.
	Recorder observability.Recorder
	// QueueCapacity bounds the async queue. <= 0 selects DefaultQueueCapacity.
	QueueCapacity int
	// FlushTimeout bounds the pre-lifecycle ordering barrier. <= 0 selects
	// defaultFlushTimeout.
	FlushTimeout time.Duration
	// DrainTimeout bounds Close. <= 0 selects DefaultDrainTimeout.
	DrainTimeout time.Duration
}

// queueItem is either an event to deliver or a flush barrier (flush != nil)
// that the dispatcher acknowledges once every item enqueued before it has
// been delivered.
type queueItem struct {
	ctx   context.Context
	event core.Event
	flush chan struct{}
}

// Pipeline builds and delivers runtime events. Safe for concurrent use.
type Pipeline struct {
	sink         core.EventSink
	logger       log.Logger
	recorder     observability.Recorder
	flushTimeout time.Duration
	drainTimeout time.Duration

	queue        chan queueItem
	stop         chan struct{}
	dispatchDone chan struct{}
	closed       atomic.Bool
	stopped      atomic.Bool
	dropped      atomic.Int64
}

// NewPipeline constructs a Pipeline and starts its dispatcher goroutine.
// Call Close to drain and stop it.
func NewPipeline(cfg Config) *Pipeline {
	sink := cfg.Sink
	if sink == nil {
		sink = core.EventSinkFunc(func(context.Context, core.Event) error { return nil })
	}
	capacity := cfg.QueueCapacity
	if capacity <= 0 {
		capacity = DefaultQueueCapacity
	}
	flushTimeout := cfg.FlushTimeout
	if flushTimeout <= 0 {
		flushTimeout = defaultFlushTimeout
	}
	drainTimeout := cfg.DrainTimeout
	if drainTimeout <= 0 {
		drainTimeout = DefaultDrainTimeout
	}
	p := &Pipeline{
		sink:         sink,
		logger:       cfg.Logger,
		recorder:     cfg.Recorder,
		flushTimeout: flushTimeout,
		drainTimeout: drainTimeout,
		queue:        make(chan queueItem, capacity),
		stop:         make(chan struct{}),
		dispatchDone: make(chan struct{}),
	}
	go p.dispatch()
	return p
}

// BuildEvent assembles a core.Event exactly as the engine and facade
// historically did inline: lifecycle correlation payload wrapping, payload
// redaction, tenant stamping, and trace/span propagation from ctx.
func BuildEvent(ctx context.Context, scenarioName string, redactor governance.OutputRedactor, typ core.EventType, runID string, payload json.RawMessage) core.Event {
	corr := core.EpisodeCorrelationFromContext(ctx)
	if core.IsLifecycleEvent(typ) {
		payload = core.BuildLifecyclePayload(typ, payload, corr)
	}
	payload = governance.RedactEventPayload(ctx, redactor, runID, typ, payload)
	event := core.Event{
		Type:         typ,
		RunID:        runID,
		ScenarioName: scenarioName,
		EpisodeID:    corr.EpisodeID,
		SessionID:    corr.SessionID,
		TriggerKind:  corr.TriggerKind,
		Timestamp:    time.Now().UTC(),
		Category:     core.EventCategory(typ),
		DisplayLabel: core.DisplayLabel(typ),
		Payload:      payload,
	}
	observability.StampEventTenant(ctx, &event)
	if traceID, spanID := observability.TraceFromContext(ctx); traceID != "" {
		event.TraceID = traceID
		event.SpanID = spanID
	}
	if parentSpanID := observability.ParentSpanFromContext(ctx); parentSpanID != "" {
		event.ParentSpanID = parentSpanID
	}
	return event
}

// Emit builds the event, fans it out to the context-scoped stream tee
// synchronously (so StreamRun / the stream hub see it in real time, ahead of
// any queue delay), then routes durable delivery by event class: critical
// lifecycle events are delivered synchronously with retries, everything else
// is queued for the dispatcher (dropped and counted when the queue is full).
func (p *Pipeline) Emit(ctx context.Context, scenarioName string, redactor governance.OutputRedactor, typ core.EventType, runID string, payload json.RawMessage) {
	event := BuildEvent(ctx, scenarioName, redactor, typ, runID, payload)
	// The tee runs even when durable delivery later fails: a live consumer
	// should still observe the transition.
	if tee := EventTeeFromContext(ctx); tee != nil {
		if err := tee.Emit(ctx, event); err != nil {
			WarnFailure(p.logger, ctx, runID, err)
		}
	}
	if IsCriticalLifecycleEvent(typ) {
		// Flush the queued backlog first (bounded) so the synchronous
		// lifecycle event does not overtake earlier queued events at the sink.
		p.flush()
		if err := EmitWithLifecycleRetry(ctx, p.sink, event); err != nil {
			ErrorFailure(p.logger, ctx, runID, typ, err)
		}
		return
	}
	p.enqueue(ctx, event)
}

// DeliverSync builds and delivers one event without a queue: tee fan-out,
// then a single synchronous sink delivery with lifecycle retry semantics.
// It backs hosts that emit without an owning pipeline (e.g. facade value
// receivers constructed by hand in tests).
func DeliverSync(ctx context.Context, logger log.Logger, sink core.EventSink, scenarioName string, redactor governance.OutputRedactor, typ core.EventType, runID string, payload json.RawMessage) {
	if sink == nil {
		sink = core.EventSinkFunc(func(context.Context, core.Event) error { return nil })
	}
	event := BuildEvent(ctx, scenarioName, redactor, typ, runID, payload)
	if tee := EventTeeFromContext(ctx); tee != nil {
		if err := tee.Emit(ctx, event); err != nil {
			WarnFailure(logger, ctx, runID, err)
		}
	}
	if err := EmitWithLifecycleRetry(ctx, sink, event); err != nil {
		if IsCriticalLifecycleEvent(typ) {
			ErrorFailure(logger, ctx, runID, typ, err)
		} else {
			WarnFailure(logger, ctx, runID, err)
		}
	}
}

// DroppedEvents reports how many queued events were dropped because the
// queue was full (or the pipeline was already closed).
func (p *Pipeline) DroppedEvents() int64 {
	return p.dropped.Load()
}

// Flush waits (bounded by the configured flush timeout) until every event
// enqueued so far has been delivered to the sink. Emit already takes this
// barrier before synchronous lifecycle events; Flush exposes it for tests
// and hosts that assert queued events without a trailing lifecycle event.
func (p *Pipeline) Flush() {
	p.flush()
}

// Close stops accepting new queued events and waits (bounded by the
// configured drain timeout) for the dispatcher to deliver the backlog.
// Events still undelivered after the timeout are counted as dropped.
// Critical lifecycle events emitted after Close are unaffected — they were
// never queued. Close is idempotent.
func (p *Pipeline) Close() {
	if p.closed.CompareAndSwap(false, true) {
		close(p.stop)
	}
	select {
	case <-p.dispatchDone:
	case <-time.After(p.drainTimeout):
	}
	// Sweep anything the dispatcher did not drain in time so the dropped
	// counter reflects the loss, and release any pending flush waiters.
	for {
		select {
		case item := <-p.queue:
			if item.flush != nil {
				close(item.flush)
				continue
			}
			p.noteDrop(context.Background(), item.event)
		default:
			return
		}
	}
}

func (p *Pipeline) enqueue(ctx context.Context, event core.Event) {
	if p.closed.Load() || p.stopped.Load() {
		p.noteDrop(ctx, event)
		return
	}
	select {
	case p.queue <- queueItem{ctx: context.WithoutCancel(ctx), event: event}:
	default:
		p.noteDrop(ctx, event)
	}
}

func (p *Pipeline) noteDrop(ctx context.Context, event core.Event) {
	n := p.dropped.Add(1)
	if p.recorder != nil {
		p.recorder.IncCounter(ctx, observability.MetricRuntimeEventsDroppedTotal, observability.Attribute{Key: "event_type", Value: string(event.Type)})
	}
	// Rate-limit the warning: first drop, then every 100th, so a saturated
	// sink neither stays silent nor spins the log.
	if p.logger != nil && (n == 1 || n%100 == 0) {
		p.logger.Warn(ctx, "emit: event queue full, dropping event", "run_id", event.RunID, "event_type", string(event.Type), "dropped_total", n)
	}
}

// flush enqueues a barrier and waits until the dispatcher has delivered
// everything enqueued before it, bounded by flushTimeout. A saturated or
// stuck sink makes the wait give up instead of blocking the caller.
func (p *Pipeline) flush() {
	if p.closed.Load() || p.stopped.Load() {
		return
	}
	ack := make(chan struct{})
	timer := time.NewTimer(p.flushTimeout)
	defer timer.Stop()
	select {
	case p.queue <- queueItem{flush: ack}:
	case <-timer.C:
		return
	}
	select {
	case <-ack:
	case <-timer.C:
	}
}

func (p *Pipeline) dispatch() {
	defer close(p.dispatchDone)
	defer p.stopped.Store(true)
	for {
		select {
		case item := <-p.queue:
			p.deliver(item)
		case <-p.stop:
			// Drain whatever is queued right now; emitters racing with Close
			// past this point are swept (and counted) by Close itself.
			for {
				select {
				case item := <-p.queue:
					p.deliver(item)
				default:
					return
				}
			}
		}
	}
}

func (p *Pipeline) deliver(item queueItem) {
	if item.flush != nil {
		close(item.flush)
		return
	}
	if err := p.sink.Emit(item.ctx, item.event); err != nil {
		WarnFailure(p.logger, item.ctx, item.event.RunID, err)
	}
}

// IsCriticalLifecycleEvent reports whether typ is a run-lifecycle event whose
// silent loss would corrupt downstream state tracking (RunStarted /
// RunCompleted / RunFailed / RunCancelled / RunPaused / RunResumed). These
// are delivered synchronously with bounded retries, never queued.
func IsCriticalLifecycleEvent(typ core.EventType) bool {
	return core.IsLifecycleEvent(typ)
}

// lifecycleEmitAttempts bounds how many times a critical lifecycle event is
// delivered before giving up (first try plus backoff-spaced retries).
const lifecycleEmitAttempts = 3

// EmitWithLifecycleRetry delivers one event via sink. Critical lifecycle
// events are retried a limited number of times with backoff so a transient
// sink outage (e.g. a DB blip) does not silently drop them; all other events
// are delivered on a best-effort single attempt.
func EmitWithLifecycleRetry(ctx context.Context, sink core.EventSink, event core.Event) error {
	err := sink.Emit(ctx, event)
	for attempt := 1; err != nil && IsCriticalLifecycleEvent(event.Type) && attempt < lifecycleEmitAttempts; attempt++ {
		if delayErr := retry.Backoff(ctx, attempt); delayErr != nil {
			break
		}
		err = sink.Emit(ctx, event)
	}
	return err
}

// warnGate prevents recursive Warn/Error if the logger itself emits events.
var warnGate atomic.Bool

// WarnFailure logs a best-effort event delivery failure at warn level.
func WarnFailure(logger log.Logger, ctx context.Context, runID string, err error) {
	if logger == nil || err == nil {
		return
	}
	if !warnGate.CompareAndSwap(false, true) {
		return
	}
	defer warnGate.Store(false)
	logger.Warn(ctx, "emit: event delivery failed", "run_id", runID, "error", err)
}

// ErrorFailure reports a lifecycle event that could not be delivered even
// after the bounded retries. Unlike WarnFailure it logs at error level:
// losing a lifecycle event corrupts downstream state tracking and must page
// an operator.
func ErrorFailure(logger log.Logger, ctx context.Context, runID string, typ core.EventType, err error) {
	if logger == nil || err == nil {
		return
	}
	if !warnGate.CompareAndSwap(false, true) {
		return
	}
	defer warnGate.Store(false)
	logger.Error(ctx, "emit: lifecycle event delivery failed after retries", "run_id", runID, "event_type", string(typ), "error", err)
}

type eventTeeKey struct{}

// ContextWithEventTee attaches a side-channel EventSink used by
// Framework.StreamRun to observe runtime events without requiring an EventHub
// subscription.
func ContextWithEventTee(ctx context.Context, sink core.EventSink) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if sink == nil {
		return ctx
	}
	return context.WithValue(ctx, eventTeeKey{}, sink)
}

// EventTeeFromContext returns the side-channel sink attached by
// ContextWithEventTee, or nil. The emission pipeline consults it so engine,
// workflow-runner, and facade emissions all reach a StreamRun tee.
func EventTeeFromContext(ctx context.Context) core.EventSink {
	if ctx == nil {
		return nil
	}
	sink, _ := ctx.Value(eventTeeKey{}).(core.EventSink)
	return sink
}
