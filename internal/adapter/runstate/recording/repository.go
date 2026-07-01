package recording

import (
	"context"

	"github.com/aijustin/agentflow-go/pkg/log"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// Repository wraps a run-state repository and appends successful saves to checkpoint history.
type Repository struct {
	Inner   runstate.Repository
	History runstate.CheckpointHistory
	// Logger reports History.Append failures. Save intentionally does not
	// fail the caller's write when this happens: Inner.Save already
	// committed the authoritative run snapshot (and bumped its version), so
	// returning an error here would make the caller believe the save itself
	// failed and could lead it to retry with the now-stale expectedVersion.
	// History is a secondary audit trail, not the source of truth used for
	// resume, so a lost entry is logged rather than treated as fatal.
	Logger log.Logger
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
		if err := r.History.Append(ctx, *snapshot); err != nil && r.Logger != nil {
			r.Logger.Warn(ctx, "runstate recording: failed to append checkpoint history", "run_id", snapshot.RunID, "version", snapshot.Version, "error", err)
		}
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
