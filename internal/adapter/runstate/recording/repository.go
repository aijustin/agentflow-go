package recording

import (
	"context"
	"fmt"
	"time"

	"github.com/aijustin/agentflow-go/pkg/log"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// Repository wraps a run-state repository and appends successful saves to checkpoint history.
type Repository struct {
	Inner   runstate.Repository
	History runstate.CheckpointHistory
	// Logger reports History.Append failures before they are returned.
	Logger log.Logger
}

func (r *Repository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if err := r.Inner.Save(ctx, snapshot, expectedVersion); err != nil {
		return err
	}
	return r.appendHistory(ctx, snapshot)
}

// appendHistory records the snapshot just persisted by Inner.Save/SaveFenced.
// The inner save stamps timestamps and the new version onto the pointer in
// place, so this records the exact version written here and avoids a re-Load
// that a concurrent writer could advance past.
func (r *Repository) appendHistory(ctx context.Context, snapshot *runstate.RunSnapshot) error {
	if r.History == nil || snapshot == nil {
		return nil
	}
	if err := r.History.Append(ctx, *snapshot); err != nil {
		if r.Logger != nil {
			r.Logger.Warn(ctx, "runstate recording: failed to append checkpoint history", "run_id", snapshot.RunID, "version", snapshot.Version, "error", err)
		}
		// Surface the failure so upper layers at least learn the audit
		// trail has a gap, instead of silently losing history entries.
		// Inner.Save already committed the authoritative snapshot (and
		// bumped its version), so callers that retry the whole operation
		// reload the committed version and converge.
		return fmt.Errorf("runstate recording: checkpoint history append failed for run %q version %d: %w",
			snapshot.RunID, snapshot.Version, err)
	}
	return nil
}

func (r *Repository) Load(ctx context.Context, runID string) (runstate.RunSnapshot, error) {
	return r.Inner.Load(ctx, runID)
}

func (r *Repository) Delete(ctx context.Context, runID string) error {
	return r.Inner.Delete(ctx, runID)
}

func (r *Repository) List(ctx context.Context, filter runstate.ListFilter) ([]runstate.RunSnapshot, error) {
	return r.Inner.List(ctx, filter)
}

// SaveFenced forwards fenced saves to the inner repository when it supports
// them (the framework wraps the production PostgreSQL repository with this
// recorder, so the wrapper must not hide the fencing capability). It refuses
// to degrade to an unfenced save: callers relying on fencing must see a loud
// error instead of a silently unprotected write. Checkpoint history is
// recorded exactly like Save.
func (r *Repository) SaveFenced(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64, fenceToken uint64) error {
	fenced, ok := r.Inner.(runstate.FencedRepository)
	if !ok {
		return fmt.Errorf("runstate recording: inner repository %T does not support fenced saves", r.Inner)
	}
	if err := fenced.SaveFenced(ctx, snapshot, expectedVersion, fenceToken); err != nil {
		return err
	}
	return r.appendHistory(ctx, snapshot)
}

// ListStale forwards to the inner repository's store-clock implementation
// when available; otherwise it falls back to filtering List results with the
// local clock, which matches what callers would do without the capability.
func (r *Repository) ListStale(ctx context.Context, filter runstate.ListFilter, grace time.Duration) ([]runstate.RunSnapshot, error) {
	if stale, ok := r.Inner.(runstate.StaleRepository); ok {
		return stale.ListStale(ctx, filter, grace)
	}
	if grace < 0 {
		grace = 0
	}
	cutoff := time.Now().UTC().Add(-grace)
	snapshots, err := r.Inner.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	out := snapshots[:0]
	for _, snapshot := range snapshots {
		if snapshot.UpdatedAt.IsZero() || !snapshot.UpdatedAt.After(cutoff) {
			out = append(out, snapshot)
		}
	}
	return out, nil
}
