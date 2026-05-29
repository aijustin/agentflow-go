package recording

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// Repository wraps a run-state repository and appends successful saves to checkpoint history.
type Repository struct {
	Inner   runstate.Repository
	History runstate.CheckpointHistory
}

func (r *Repository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if err := r.Inner.Save(ctx, snapshot, expectedVersion); err != nil {
		return err
	}
	if r.History != nil && snapshot != nil {
		// Append the snapshot we just persisted. Inner.Save stamps timestamps
		// and the new version onto the pointer in place, so this records the
		// exact version written here and avoids a re-Load that a concurrent
		// writer could advance past.
		return r.History.Append(ctx, *snapshot)
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
