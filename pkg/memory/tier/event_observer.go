package tier

import (
	"context"
	"encoding/json"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/memory"
)

type migrationContextKey struct{}

// WithMigrationRunID attaches a run ID to ctx for tier migration events.
func WithMigrationRunID(ctx context.Context, runID string) context.Context {
	if runID == "" {
		return ctx
	}
	return context.WithValue(ctx, migrationContextKey{}, runID)
}

func migrationRunID(ctx context.Context) string {
	runID, _ := ctx.Value(migrationContextKey{}).(string)
	return runID
}

// ChainedMigrationObserver fans out migration notifications to multiple observers.
type ChainedMigrationObserver struct {
	Observers []MigrationObserver
}

func ChainMigrationObservers(observers ...MigrationObserver) MigrationObserver {
	filtered := make([]MigrationObserver, 0, len(observers))
	for _, observer := range observers {
		if observer == nil {
			continue
		}
		if _, ok := observer.(NoopMigrationObserver); ok {
			continue
		}
		filtered = append(filtered, observer)
	}
	if len(filtered) == 0 {
		return NoopMigrationObserver{}
	}
	if len(filtered) == 1 {
		return filtered[0]
	}
	return ChainedMigrationObserver{Observers: filtered}
}

func (o ChainedMigrationObserver) Promoted(ctx context.Context, ns memory.Namespace, recordID string, from, to Level) {
	for _, observer := range o.Observers {
		observer.Promoted(ctx, ns, recordID, from, to)
	}
}

func (o ChainedMigrationObserver) Demoted(ctx context.Context, ns memory.Namespace, recordID string, from, to Level) {
	for _, observer := range o.Observers {
		observer.Demoted(ctx, ns, recordID, from, to)
	}
}

func (o ChainedMigrationObserver) Evicted(ctx context.Context, ns memory.Namespace, recordID string, from Level) {
	for _, observer := range o.Observers {
		observer.Evicted(ctx, ns, recordID, from)
	}
}

// EventSinkMigrationObserver emits tier migration events to a core.EventSink.
type EventSinkMigrationObserver struct {
	Sink     core.EventSink
	Scenario string
}

func (o EventSinkMigrationObserver) Promoted(ctx context.Context, ns memory.Namespace, recordID string, from, to Level) {
	o.emit(ctx, core.EventMemoryPromoted, ns, recordID, from, to)
}

func (o EventSinkMigrationObserver) Demoted(ctx context.Context, ns memory.Namespace, recordID string, from, to Level) {
	o.emit(ctx, core.EventMemoryDemoted, ns, recordID, from, to)
}

func (o EventSinkMigrationObserver) Evicted(ctx context.Context, ns memory.Namespace, recordID string, from Level) {
	o.emit(ctx, core.EventMemoryEvicted, ns, recordID, from, "")
}

func (o EventSinkMigrationObserver) emit(ctx context.Context, typ core.EventType, ns memory.Namespace, recordID string, from, to Level) {
	if o.Sink == nil {
		return
	}
	payload := map[string]any{
		"namespace": ns,
		"record_id": recordID,
		"from_tier": string(from),
	}
	if to != "" {
		payload["to_tier"] = string(to)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = o.Sink.Emit(ctx, core.Event{
		Type:         typ,
		RunID:        migrationRunID(ctx),
		ScenarioName: o.Scenario,
		Timestamp:    time.Now().UTC(),
		Payload:      raw,
	})
}
