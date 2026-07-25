package runstate

import (
	"context"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
)

// OutboxEvent is one row of the transactional event outbox: a runtime event
// parked in the run-state database (migration 0005, agentflow_outbox) until
// the framework relay delivers it to the durable event store. Sequence is
// the event's per-run sequence in the event store, minted when the row was
// written; the relay redelivers with this exact sequence so the event
// store's UNIQUE (run_id, sequence) constraint deduplicates retries.
type OutboxEvent struct {
	ID        int64
	Sequence  int64
	Event     core.Event
	CreatedAt time.Time
}

// EventOutboxRepository is an optional Repository extension that persists a
// run snapshot and a batch of pending events in ONE transaction: the
// snapshot update (with the same version CAS and fence validation as
// SaveFenced) and the outbox inserts either both commit or both roll back,
// so a run's state and its lifecycle events can never diverge. A zero
// fenceToken behaves like Save; a non-zero token behaves like SaveFenced and
// fails with ErrStaleFence when a newer lease holder has written.
//
// Only PostgreSQL runstate implements this today; the outbox lives in the
// same database as the snapshots so a single transaction can cover both.
type EventOutboxRepository interface {
	Repository
	SaveWithEvents(ctx context.Context, snapshot *RunSnapshot, expectedVersion int64, events []core.Event, fenceToken uint64) error
}

// OutboxRepository is an optional Repository extension backing the
// framework's outbox relay and retention cascade. FetchUnpublishedOutbox
// returns parked events in insertion order (id ASC); the relay delivers
// each to the durable event store and then calls MarkOutboxPublished, which
// marks conditionally (WHERE published_at IS NULL) so concurrent relays on
// several nodes cannot double-mark — delivery itself is idempotent through
// the event store's UNIQUE (run_id, sequence) constraint.
type OutboxRepository interface {
	Repository
	// FetchUnpublishedOutbox returns up to limit unpublished outbox rows in
	// insertion order. limit <= 0 means an implementation-defined default.
	FetchUnpublishedOutbox(ctx context.Context, limit int) ([]OutboxEvent, error)
	// MarkOutboxPublished marks the row published_at=NOW() when it is still
	// unpublished. An already-published or missing row is not an error: a
	// concurrent relay simply won the race.
	MarkOutboxPublished(ctx context.Context, id int64) error
	// DeleteOutboxForRun removes every outbox row of a run (published or
	// not); used by the retention cascade when the run itself is deleted.
	DeleteOutboxForRun(ctx context.Context, runID string) (int64, error)
	// PurgeOutboxPublishedBefore removes published rows older than cutoff,
	// scoped to the authenticated tenant when the context carries one.
	// Unpublished rows are never purged here: they are undelivered events,
	// not garbage.
	PurgeOutboxPublishedBefore(ctx context.Context, cutoff time.Time) (int64, error)
}
