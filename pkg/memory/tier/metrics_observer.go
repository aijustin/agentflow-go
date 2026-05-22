package tier

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/memory"
	"github.com/aijustin/agentflow-go/pkg/observability"
)

// MetricsObserver records tier migration and depth metrics.
type MetricsObserver struct {
	Recorder observability.Recorder
	Scenario string
}

func (o MetricsObserver) Promoted(ctx context.Context, ns memory.Namespace, recordID string, from, to Level) {
	if o.Recorder == nil {
		return
	}
	o.Recorder.IncCounter(ctx, observability.MetricMemoryTierMigrationsTotal,
		observability.Attribute{Key: "from", Value: string(from)},
		observability.Attribute{Key: "to", Value: string(to)},
		observability.Attribute{Key: "scenario", Value: o.Scenario},
	)
}

func (o MetricsObserver) Demoted(ctx context.Context, ns memory.Namespace, recordID string, from, to Level) {
	o.Promoted(ctx, ns, recordID, from, to)
}

func (o MetricsObserver) Evicted(ctx context.Context, ns memory.Namespace, recordID string, from Level) {
	if o.Recorder == nil {
		return
	}
	o.Recorder.IncCounter(ctx, observability.MetricMemoryTierMigrationsTotal,
		observability.Attribute{Key: "from", Value: string(from)},
		observability.Attribute{Key: "to", Value: "evicted"},
		observability.Attribute{Key: "scenario", Value: o.Scenario},
	)
}

// RecordTierDepth publishes per-level record counts for a namespace.
func RecordTierDepth(ctx context.Context, store Store, recorder observability.Recorder, scenario string, ns memory.Namespace) {
	if recorder == nil || store == nil {
		return
	}
	for _, level := range []Level{LevelHot, LevelWarm, LevelCold} {
		count, err := store.Count(ctx, ns, level)
		if err != nil {
			continue
		}
		recorder.SetGauge(ctx, observability.MetricMemoryTierRecords,
			float64(count),
			observability.Attribute{Key: "level", Value: string(level)},
			observability.Attribute{Key: "scenario", Value: scenario},
		)
	}
}
