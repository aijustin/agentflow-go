package postgres

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aijustin/agentflow-go/pkg/core"
	obspkg "github.com/aijustin/agentflow-go/pkg/observability"
)

// DefaultOutboxTableName is the transactional event outbox drained by the
// framework relay (migration 0005). It must match the runstate repository's
// outbox table when both are customized.
const DefaultOutboxTableName = "agentflow_outbox"

// OutboxSinkConfig configures NewOutboxSink.
type OutboxSinkConfig struct {
	// Store is the durable event store this sink writes to first.
	Store *Store
	// OutboxTableName overrides the fallback outbox table; defaults to
	// DefaultOutboxTableName.
	OutboxTableName string
	// Publishers receive every successfully stored event (same semantics as
	// observability.EventStoreSink, e.g. an EventHub for live push).
	Publishers []obspkg.EventPublisher
}

// OutboxSink is a core.EventSink that keeps the durable event store and the
// run-state outbox consistent without touching the engine's emit path. Emit
// first tries Store.Append (the historical behavior, including live
// publication); when the durable append fails, the event is parked in the
// run-state outbox in the same database as the run snapshots — a single
// INSERT that survives the transient store failure — and Emit returns nil so
// the runtime's lifecycle retry does not re-append a duplicate. The
// framework's outbox relay (WithOutboxRelay) later redelivers parked events
// with their minted sequences, and the event store's
// UNIQUE (run_id, sequence) constraint deduplicates retries.
//
// Live publication is skipped for parked events, matching the pre-existing
// behavior of a failed append (EventStoreSink publishes only after a
// successful store write).
type OutboxSink struct {
	store      *Store
	outbox     string
	publishers []obspkg.EventPublisher
}

func NewOutboxSink(config OutboxSinkConfig) (*OutboxSink, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("postgres observability: outbox sink store is nil")
	}
	outbox := config.OutboxTableName
	if outbox == "" {
		outbox = DefaultOutboxTableName
	}
	if !validTableName(outbox) {
		return nil, fmt.Errorf("postgres observability: invalid outbox table name %q", outbox)
	}
	publishers := make([]obspkg.EventPublisher, 0, len(config.Publishers))
	for _, publisher := range config.Publishers {
		if publisher != nil {
			publishers = append(publishers, publisher)
		}
	}
	return &OutboxSink{store: config.Store, outbox: outbox, publishers: publishers}, nil
}

func (sink *OutboxSink) Emit(ctx context.Context, event core.Event) error {
	if sink == nil || sink.store == nil {
		return fmt.Errorf("postgres observability: outbox sink store is nil")
	}
	record, err := sink.store.Append(ctx, event)
	if err == nil {
		var publishErr error
		for _, publisher := range sink.publishers {
			publishErr = errors.Join(publishErr, publisher.PublishEvent(ctx, record))
		}
		return publishErr
	}
	if parkErr := sink.park(ctx, event); parkErr != nil {
		// Both the durable store and the outbox failed: the event is lost
		// after the caller's retries exactly as before, so surface the full
		// error (runtime logs critical lifecycle emit failures at error level).
		return errors.Join(err, parkErr)
	}
	// Parked safely; the relay takes over delivery. Returning nil is
	// deliberate: reporting the original append error would make the
	// runtime's lifecycle retry re-append the event with a fresh sequence,
	// duplicating it once the relay also delivers the parked copy.
	return nil
}

// park inserts the event into the outbox with the run's next sequence, minted
// under the same per-run advisory lock Store.Append takes and continuing from
// the max sequence in the event table and unpublished outbox rows, so parked
// and directly appended events share one dense per-run sequence space.
func (sink *OutboxSink) park(ctx context.Context, event core.Event) error {
	if event.RunID == "" {
		return fmt.Errorf("postgres observability: run id is required")
	}
	event = obspkg.NormalizeEvent(event, time.Now().UTC())
	payload, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("postgres observability: marshal outbox event for run %q: %w", event.RunID, err)
	}
	tx, err := sink.store.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("postgres observability: begin outbox park: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, event.RunID); err != nil {
		return fmt.Errorf("postgres observability: lock run %q: %w", event.RunID, err)
	}
	sequenceQuery := fmt.Sprintf(`SELECT GREATEST(
	COALESCE((SELECT MAX(sequence) FROM %s WHERE run_id = $1), 0),
	COALESCE((SELECT MAX(sequence) FROM %s WHERE run_id = $1), 0)
) + 1`, sink.store.table, sink.outbox)
	var sequence int64
	if err := tx.QueryRowContext(ctx, sequenceQuery, event.RunID).Scan(&sequence); err != nil {
		return fmt.Errorf("postgres observability: next sequence for run %q: %w", event.RunID, err)
	}
	insertQuery := fmt.Sprintf(`INSERT INTO %s (run_id, sequence, event_type, scenario_name, payload_json)
VALUES ($1, $2, $3, $4, $5)`, sink.outbox)
	if _, err := tx.ExecContext(ctx, insertQuery, event.RunID, sequence, string(event.Type), event.ScenarioName, payload); err != nil {
		return fmt.Errorf("postgres observability: park event for run %q: %w", event.RunID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("postgres observability: commit outbox park for run %q: %w", event.RunID, err)
	}
	return nil
}

// compile-time checks: the sink replaces EventStoreSink in a fanout, and the
// wrapped store covers the relay and retention extension interfaces.
var (
	_ core.EventSink             = (*OutboxSink)(nil)
	_ obspkg.SequencedEventStore = (*Store)(nil)
	_ obspkg.EventRunPurger      = (*Store)(nil)
	_ obspkg.EventStorePurger    = (*Store)(nil)
)
