package observability

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

func TestSinkRecordsEventMetricsAndSpans(t *testing.T) {
	recorder := &fakeRecorder{}
	tracer := &fakeTracer{}
	sink := NewSink(Config{Recorder: recorder, Tracer: tracer})

	if err := sink.Emit(context.Background(), core.Event{Type: core.EventRunStarted, RunID: "run-1", ScenarioName: "scenario"}); err != nil {
		t.Fatal(err)
	}

	if len(recorder.counters) != 1 {
		t.Fatalf("expected one counter, got %+v", recorder.counters)
	}
	if recorder.counters[0].name != obspkg.MetricRuntimeEventsTotal {
		t.Fatalf("unexpected metric name: %s", recorder.counters[0].name)
	}
	if recorder.counters[0].attrs["event_type"] != string(core.EventRunStarted) || recorder.counters[0].attrs["scenario_name"] != "scenario" {
		t.Fatalf("unexpected metric attributes: %+v", recorder.counters[0].attrs)
	}
	if len(tracer.spans) != 1 || tracer.spans[0].name != obspkg.SpanRuntimeEvent || !tracer.spans[0].ended {
		t.Fatalf("unexpected spans: %+v", tracer.spans)
	}
}

type fakeRecorder struct{ counters []fakeCounter }

type fakeCounter struct {
	name  obspkg.MetricName
	attrs map[string]string
}

func (r *fakeRecorder) IncCounter(_ context.Context, name obspkg.MetricName, attrs ...obspkg.Attribute) {
	r.counters = append(r.counters, fakeCounter{name: name, attrs: attrsMap(attrs)})
}

func (r *fakeRecorder) ObserveHistogram(context.Context, obspkg.MetricName, float64, ...obspkg.Attribute) {
}

func (r *fakeRecorder) SetGauge(context.Context, obspkg.MetricName, float64, ...obspkg.Attribute) {}

type fakeTracer struct{ spans []*fakeSpan }

func (t *fakeTracer) Start(ctx context.Context, name obspkg.SpanName, attrs ...obspkg.Attribute) (context.Context, obspkg.Span) {
	span := &fakeSpan{name: name, attrs: attrsMap(attrs)}
	t.spans = append(t.spans, span)
	return ctx, span
}

type fakeSpan struct {
	name  obspkg.SpanName
	attrs map[string]string
	ended bool
}

func (s *fakeSpan) RecordError(error) {}

func (s *fakeSpan) SetAttributes(...obspkg.Attribute) {}

func (s *fakeSpan) End() { s.ended = true }

func attrsMap(attrs []obspkg.Attribute) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		out[attr.Key] = attr.Value
	}
	return out
}
