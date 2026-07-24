package agentflow

import (
	"context"
	"fmt"
	"time"

	runstaterecording "github.com/aijustin/agentflow-go/internal/adapter/runstate/recording"
	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/pkg/observability"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// --- Event Outbox Relay ---

const (
	// defaultOutboxRelayInterval is the sweep cadence WithOutboxRelay uses
	// when called with a non-positive interval.
	defaultOutboxRelayInterval = 2 * time.Second
	// outboxRelayBatchSize bounds how many unpublished rows one relay
	// delivery pass takes.
	outboxRelayBatchSize = 200
	// outboxRelayMaxBatchesPerSweep caps back-to-back batches in one tick so
	// a large backlog cannot starve the relay loop's shutdown check.
	outboxRelayMaxBatchesPerSweep = 10
)

// WithEventStore wires the durable runtime event store so the framework can
// cascade retention deletions to event history (see PurgeRuns/PurgeExpired)
// and deliver outbox-parked events (see WithOutboxRelay). The store is
// typically the same one wrapped by the EventStoreSink passed to
// WithEventSink; the sink chain itself stays opaque to the framework.
func WithEventStore(store observability.EventStore) Option {
	return func(o *options) error {
		if store == nil {
			return fmt.Errorf("agentflow: event store is nil")
		}
		o.eventStore = store
		return nil
	}
}

// WithOutboxRelay starts a background loop (same GoSafe + closers pattern as
// the run reaper) that drains the run-state repository's event outbox:
// unpublished rows are delivered to the durable event store wired with
// WithEventStore and marked published, at-least-once — delivery failures
// leave the row unpublished for the next sweep, and redelivery is
// deduplicated by the event store's UNIQUE (run_id, sequence) constraint.
// The relay stops on Framework.Close.
//
// Pair it with the outbox-capable event sink (adapters.NewPostgresOutboxEventSink)
// in the WithEventSink fanout: that sink parks events in the outbox whenever
// the durable append fails, and the relay closes the gap. Multiple nodes may
// relay concurrently against the same database; marking is a conditional
// update (WHERE published_at IS NULL) and duplicate delivery collapses on the
// sequence constraint.
//
// interval defaults to 2s when non-positive. New returns an error unless the
// event store implements observability.SequencedEventStore and the run-state
// repository implements runstate.OutboxRepository — today that means the
// PostgreSQL runstate repository; combining Redis runstate with a PostgreSQL
// outbox is not supported because the outbox must share the snapshots'
// database.
func WithOutboxRelay(interval time.Duration) Option {
	return func(o *options) error {
		if interval <= 0 {
			interval = defaultOutboxRelayInterval
		}
		o.outboxRelay = true
		o.outboxRelayInterval = interval
		return nil
	}
}

// unwrapRunstate peels the checkpoint-history recording wrapper New adds when
// WithCheckpointHistory is configured, so capability checks see the real
// repository underneath.
func unwrapRunstate(repo runstate.Repository) runstate.Repository {
	if recording, ok := repo.(*runstaterecording.Repository); ok && recording.Inner != nil {
		return recording.Inner
	}
	return repo
}

// startOutboxRelay launches the outbox relay loop and returns a closer that
// stops it and waits for the loop to exit, mirroring startRunReaper.
func (f *Framework) startOutboxRelay(interval time.Duration, store observability.SequencedEventStore, outbox runstate.OutboxRepository) func(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	safecall.GoSafe("agentflow: outbox relay", func(err error) {
		if f.logger != nil {
			f.logger.Error(context.Background(), "agentflow: outbox relay crashed", "error", err)
		}
	}, func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				f.relayOutboxTick(ctx, store, outbox)
			}
		}
	})
	return func(context.Context) error {
		cancel()
		<-done
		return nil
	}
}

func (f *Framework) relayOutboxTick(ctx context.Context, store observability.SequencedEventStore, outbox runstate.OutboxRepository) {
	err := safecall.Do("agentflow: outbox relay sweep", func() error {
		return f.relayOutboxOnce(ctx, store, outbox)
	})
	if err != nil && ctx.Err() == nil && f.logger != nil {
		f.logger.Warn(ctx, "agentflow: outbox relay sweep failed", "error", err)
	}
}

// relayOutboxOnce delivers unpublished outbox rows until the backlog drains
// or the per-sweep batch cap is hit. A row whose delivery fails stays
// unpublished and is retried on a later sweep; a row that delivers but
// cannot be marked is redelivered later and deduplicated by the event
// store's (run_id, sequence) uniqueness.
func (f *Framework) relayOutboxOnce(ctx context.Context, store observability.SequencedEventStore, outbox runstate.OutboxRepository) error {
	for batch := 0; batch < outboxRelayMaxBatchesPerSweep; batch++ {
		rows, err := outbox.FetchUnpublishedOutbox(ctx, outboxRelayBatchSize)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return nil
		}
		for _, row := range rows {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if _, err := store.AppendSequenced(ctx, row.Sequence, row.Event); err != nil {
				if f.logger != nil {
					f.logger.Warn(ctx, "agentflow: outbox relay delivery failed; will retry", "outbox_id", row.ID, "run_id", row.Event.RunID, "sequence", row.Sequence, "error", err)
				}
				continue
			}
			if err := outbox.MarkOutboxPublished(ctx, row.ID); err != nil && f.logger != nil {
				f.logger.Warn(ctx, "agentflow: outbox relay mark published failed; redelivery will dedup", "outbox_id", row.ID, "error", err)
			}
		}
		if len(rows) < outboxRelayBatchSize {
			return nil
		}
	}
	return nil
}
