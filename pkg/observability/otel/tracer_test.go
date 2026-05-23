package otel

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/aijustin/agentflow-go/pkg/observability"
)

func TestTracerRecordsSpanAndSyncsTraceContext(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	tracer := NewTracer(provider.Tracer("test"))

	ctx, span := tracer.Start(context.Background(), observability.SpanRun,
		observability.Attribute{Key: "run_id", Value: "run-1"},
	)
	childCtx, childSpan := tracer.Start(ctx, observability.SpanToolCall,
		observability.Attribute{Key: "tool", Value: "echo"},
	)
	childSpan.End()
	span.End()

	if parent := observability.ParentSpanFromContext(childCtx); parent == "" {
		t.Fatal("expected parent span on nested context")
	}

	traceID, spanID := observability.TraceFromContext(ctx)
	if traceID == "" || spanID == "" {
		t.Fatalf("expected trace context, got trace=%q span=%q", traceID, spanID)
	}

	spans := exporter.GetSpans()
	if len(spans) != 2 {
		t.Fatalf("expected two spans, got %d", len(spans))
	}
	names := map[string]bool{}
	for _, span := range spans {
		names[span.Name] = true
	}
	if !names[string(observability.SpanRun)] || !names[string(observability.SpanToolCall)] {
		t.Fatalf("unexpected span names: %+v", names)
	}
}

func TestTracerRecordError(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSyncer(exporter))
	tracer := NewTracer(provider.Tracer("test"))

	_, span := tracer.Start(context.Background(), observability.SpanToolCall)
	span.RecordError(context.Canceled)
	span.End()

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatal("expected span")
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("expected error status, got %+v", spans[0].Status)
	}
}

func TestNilTracerUsesNoop(t *testing.T) {
	var tracer *Tracer
	ctx, span := tracer.Start(context.Background(), observability.SpanRun)
	defer span.End()
	traceID, _ := observability.TraceFromContext(ctx)
	if traceID != "" {
		t.Fatalf("noop tracer should not set trace context, got %q", traceID)
	}
}
