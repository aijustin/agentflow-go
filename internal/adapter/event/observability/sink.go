package observability

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

type Config struct {
	Recorder obspkg.Recorder
	Tracer   obspkg.Tracer
	Next     core.EventSink
}

type Sink struct {
	recorder obspkg.Recorder
	tracer   obspkg.Tracer
	next     core.EventSink
}

func NewSink(config Config) *Sink {
	recorder := config.Recorder
	if recorder == nil {
		recorder = obspkg.NoopRecorder{}
	}
	tracer := config.Tracer
	if tracer == nil {
		tracer = obspkg.NoopTracer{}
	}
	next := config.Next
	if next == nil {
		next = core.EventSinkFunc(func(context.Context, core.Event) error { return nil })
	}
	return &Sink{recorder: recorder, tracer: tracer, next: next}
}

func (sink *Sink) Emit(ctx context.Context, event core.Event) error {
	attrs := []obspkg.Attribute{
		{Key: "event_type", Value: string(event.Type)},
		{Key: "scenario_name", Value: event.ScenarioName},
	}
	sink.recorder.IncCounter(ctx, obspkg.MetricRuntimeEventsTotal, attrs...)
	spanCtx, span := sink.tracer.Start(ctx, obspkg.SpanRuntimeEvent,
		append(attrs, obspkg.Attribute{Key: "run_id", Value: event.RunID})...,
	)
	defer span.End()
	if err := sink.next.Emit(spanCtx, event); err != nil {
		span.RecordError(err)
		return err
	}
	return nil
}
