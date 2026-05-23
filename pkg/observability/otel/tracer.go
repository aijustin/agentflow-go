package otel

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/aijustin/agentflow-go/pkg/observability"
)

// Tracer adapts an OpenTelemetry trace.Tracer to observability.Tracer.
type Tracer struct {
	tracer oteltrace.Tracer
}

// NewTracer wraps a configured OpenTelemetry tracer.
func NewTracer(tracer oteltrace.Tracer) *Tracer {
	if tracer == nil {
		return nil
	}
	return &Tracer{tracer: tracer}
}

func (t *Tracer) Start(ctx context.Context, name observability.SpanName, attrs ...observability.Attribute) (context.Context, observability.Span) {
	if t == nil || t.tracer == nil {
		return observability.NoopTracer{}.Start(ctx, name, attrs...)
	}
	spanCtx, otelSpan := t.tracer.Start(ctx, string(name), oteltrace.WithAttributes(toAttributes(attrs)...))
	return syncObservabilityTrace(spanCtx, otelSpan), &span{span: otelSpan}
}

type span struct {
	span oteltrace.Span
}

func (s *span) RecordError(err error) {
	if s == nil || s.span == nil || err == nil {
		return
	}
	s.span.RecordError(err)
	s.span.SetStatus(codes.Error, err.Error())
}

func (s *span) SetAttributes(attrs ...observability.Attribute) {
	if s == nil || s.span == nil {
		return
	}
	s.span.SetAttributes(toAttributes(attrs)...)
}

func (s *span) End() {
	if s == nil || s.span == nil {
		return
	}
	s.span.End()
}

func syncObservabilityTrace(ctx context.Context, otelSpan oteltrace.Span) context.Context {
	if otelSpan == nil {
		return ctx
	}
	_, parentSpanID := observability.TraceFromContext(ctx)
	sc := otelSpan.SpanContext()
	if !sc.IsValid() {
		return ctx
	}
	return observability.WithTraceParent(ctx, sc.TraceID().String(), sc.SpanID().String(), parentSpanID)
}

func toAttributes(attrs []observability.Attribute) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	out := make([]attribute.KeyValue, 0, len(attrs))
	for _, attr := range attrs {
		out = append(out, attribute.String(attr.Key, attr.Value))
	}
	return out
}
