package observability

import (
	"context"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// SequencedEventStore is an optional EventStore extension used by the
// framework outbox relay: AppendSequenced inserts an event with a per-run
// sequence minted earlier (when the event was parked in the outbox), instead
// of assigning a fresh one. Delivery is idempotent: when (run_id, sequence)
// is already present the store returns the existing record and a nil error,
// so relay retries and concurrent relays never duplicate an event.
type SequencedEventStore interface {
	EventStore
	AppendSequenced(ctx context.Context, sequence int64, event core.Event) (EventRecord, error)
}

// EventRunPurger is an optional EventStore extension for the retention
// cascade: DeleteEventsForRun removes every stored event of a run when the
// run itself is deleted, so event history does not outlive its run.
type EventRunPurger interface {
	EventStore
	DeleteEventsForRun(ctx context.Context, runID string) (int64, error)
}

// EventStorePurger is an optional EventStore extension for age-based
// retention: PurgeEventsBefore removes events that occurred before cutoff,
// bounding the otherwise append-only event table.
type EventStorePurger interface {
	EventStore
	PurgeEventsBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

// DeleteEventsForRun deletes all stored events of runID when the store
// implements EventRunPurger; otherwise it is a no-op reporting 0, so
// retention callers work unchanged against stores without purge support.
func DeleteEventsForRun(ctx context.Context, store EventStore, runID string) (int64, error) {
	purger, ok := store.(EventRunPurger)
	if !ok || purger == nil {
		return 0, nil
	}
	return purger.DeleteEventsForRun(ctx, runID)
}

// PurgeEventsBefore deletes events older than cutoff when the store
// implements EventStorePurger; otherwise it is a no-op reporting 0.
func PurgeEventsBefore(ctx context.Context, store EventStore, cutoff time.Time) (int64, error) {
	purger, ok := store.(EventStorePurger)
	if !ok || purger == nil {
		return 0, nil
	}
	return purger.PurgeEventsBefore(ctx, cutoff)
}
