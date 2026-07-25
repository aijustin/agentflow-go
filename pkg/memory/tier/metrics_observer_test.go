package tier

import (
	"context"
	"testing"

	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

type recordingRecorder struct {
	counters  int
	gauges    int
	lastGauge float64
}

func (r *recordingRecorder) IncCounter(context.Context, observability.MetricName, ...observability.Attribute) {
	r.counters++
}

func (r *recordingRecorder) AddCounter(_ context.Context, _ observability.MetricName, value float64, _ ...observability.Attribute) {
	r.counters += int(value)
}

func (r *recordingRecorder) ObserveHistogram(context.Context, observability.MetricName, float64, ...observability.Attribute) {
}

func (r *recordingRecorder) SetGauge(_ context.Context, _ observability.MetricName, value float64, _ ...observability.Attribute) {
	r.gauges++
	r.lastGauge = value
}

func TestMetricsObserverRecordsMigrations(t *testing.T) {
	rec := &recordingRecorder{}
	observer := MetricsObserver{Recorder: rec, Scenario: "tier-test"}
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "metrics", Agent: "assistant"}
	ctx := context.Background()
	observer.Promoted(ctx, ns, "rec-1", LevelHot, LevelWarm)
	observer.Demoted(ctx, ns, "rec-1", LevelWarm, LevelCold)
	observer.Evicted(ctx, ns, "rec-1", LevelCold)
	if rec.counters != 3 {
		t.Fatalf("expected 3 counter increments, got %d", rec.counters)
	}
}

func TestMetricsObserverNilRecorderIsNoop(t *testing.T) {
	var observer MetricsObserver
	observer.Promoted(context.Background(), memory.Namespace{}, "rec-1", LevelHot, LevelWarm)
}

func TestRecordTierDepthPublishesCounts(t *testing.T) {
	ctx := context.Background()
	store := newTestStore()
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "depth", Agent: "assistant"}
	rec := &recordingRecorder{}
	RecordTierDepth(ctx, store, rec, "scenario", ns)
	if rec.gauges != 3 {
		t.Fatalf("expected gauge per tier level, got %d", rec.gauges)
	}
}

func TestNoopMigrationObserverMethods(t *testing.T) {
	var observer NoopMigrationObserver
	ns := memory.Namespace{Scope: memory.ScopeSession, SessionID: "noop", Agent: "assistant"}
	observer.Promoted(context.Background(), ns, "r1", LevelHot, LevelWarm)
	observer.Demoted(context.Background(), ns, "r1", LevelWarm, LevelCold)
	observer.Evicted(context.Background(), ns, "r1", LevelCold)
}
