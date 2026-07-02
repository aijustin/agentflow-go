package observability_test

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/observability"
)

func TestRecorderFuncDelegatesAllMethods(t *testing.T) {
	called := 0
	fn := observability.RecorderFunc(func(ctx context.Context, name observability.MetricName, attrs ...observability.Attribute) {
		called++
	})
	fn.IncCounter(context.Background(), observability.MetricRuntimeEventsTotal)
	fn.ObserveHistogram(context.Background(), observability.MetricRunDurationSeconds, 1.0)
	fn.SetGauge(context.Background(), observability.MetricMemoryTierRecords, 2.0)
	if called != 1 {
		t.Fatalf("expected IncCounter only to invoke func, got %d", called)
	}
}

func TestNoopRecorderAndTracer(t *testing.T) {
	ctx := context.Background()
	var recorder observability.NoopRecorder
	recorder.IncCounter(ctx, observability.MetricRuntimeEventsTotal)
	recorder.ObserveHistogram(ctx, observability.MetricRunDurationSeconds, 1.0)
	recorder.SetGauge(ctx, observability.MetricMemoryTierRecords, 1.0)

	var tracer observability.NoopTracer
	spanCtx, span := tracer.Start(ctx, observability.SpanRun)
	span.RecordError(nil)
	span.SetAttributes(observability.Attribute{Key: "run_id", Value: "r1"})
	span.End()
	if spanCtx == nil {
		t.Fatal("expected context from noop tracer")
	}
}

func TestRecorderFuncDelegatesIncCounter(t *testing.T) {
	called := false
	fn := observability.RecorderFunc(func(ctx context.Context, name observability.MetricName, attrs ...observability.Attribute) {
		called = true
		if name != observability.MetricRuntimeEventsTotal {
			t.Fatalf("unexpected metric: %s", name)
		}
	})
	fn.IncCounter(context.Background(), observability.MetricRuntimeEventsTotal)
	if !called {
		t.Fatal("expected recorder func to be invoked")
	}
}
