package inmem

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type Repository struct {
	mu        sync.RWMutex
	snapshots map[string]runstate.RunSnapshot
	// fences records the highest fencing token accepted per run, mirroring
	// the fence_token column of the PostgreSQL adapter. Save leaves it
	// untouched; SaveFenced rejects tokens below it with ErrStaleFence.
	fences map[string]uint64
}

func NewRepository() *Repository {
	return &Repository{snapshots: make(map[string]runstate.RunSnapshot), fences: make(map[string]uint64)}
}

func (r *Repository) Save(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return runstate.ErrNotFound
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.snapshots[snapshot.RunID]
	currentVersion := int64(0)
	if ok {
		currentVersion = current.Version
	}
	if currentVersion != expectedVersion {
		return runstate.ErrStaleSnapshot
	}
	return r.saveLocked(ctx, snapshot, current, ok)
}

// SaveFenced implements runstate.FencedRepository: the version CAS of Save
// plus a fence check — a token below the highest token already recorded for
// the run fails with ErrStaleFence, and an accepted token becomes the new
// high-water mark. Version is checked first, matching the PostgreSQL
// adapter's classification order.
func (r *Repository) SaveFenced(ctx context.Context, snapshot *runstate.RunSnapshot, expectedVersion int64, fenceToken uint64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if snapshot == nil {
		return runstate.ErrNotFound
	}
	if err := snapshot.Validate(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	current, ok := r.snapshots[snapshot.RunID]
	currentVersion := int64(0)
	if ok {
		currentVersion = current.Version
	}
	if currentVersion != expectedVersion {
		return runstate.ErrStaleSnapshot
	}
	if fenceToken < r.fences[snapshot.RunID] {
		return runstate.ErrStaleFence
	}
	if err := r.saveLocked(ctx, snapshot, current, ok); err != nil {
		return err
	}
	r.fences[snapshot.RunID] = fenceToken
	return nil
}

func (r *Repository) saveLocked(ctx context.Context, snapshot *runstate.RunSnapshot, current runstate.RunSnapshot, exists bool) error {
	if snapshot.Version <= current.Version {
		if exists {
			snapshot.Version = current.Version + 1
		} else if snapshot.Version < 1 {
			snapshot.Version = 1
		}
	}
	var previous *runstate.RunSnapshot
	if exists {
		prev := current
		previous = &prev
	}
	if err := runstate.ValidateStatusTransition(ctx, previous, snapshot.Status); err != nil {
		return err
	}
	runstate.StampSnapshot(snapshot, previous, time.Now().UTC())
	r.snapshots[snapshot.RunID] = cloneSnapshot(*snapshot)
	return nil
}

func (r *Repository) Load(ctx context.Context, runID string) (runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return runstate.RunSnapshot{}, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	snapshot, ok := r.snapshots[runID]
	if !ok {
		return runstate.RunSnapshot{}, runstate.ErrNotFound
	}
	return cloneSnapshot(snapshot), nil
}

func (r *Repository) Delete(ctx context.Context, runID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.snapshots, runID)
	delete(r.fences, runID)
	return nil
}

func (r *Repository) List(ctx context.Context, filter runstate.ListFilter) ([]runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]runstate.RunSnapshot, 0, len(r.snapshots))
	for _, snap := range r.snapshots {
		if filter.Status != "" && snap.Status != filter.Status {
			continue
		}
		if filter.ScenarioName != "" && snap.ScenarioName != filter.ScenarioName {
			continue
		}
		if filter.TenantID != "" && snap.TenantID != filter.TenantID {
			continue
		}
		if filter.ParentRunID != "" && snap.ParentRunID != filter.ParentRunID {
			continue
		}
		if filter.ThreadID != "" && runstate.ResolveThreadID(snap) != filter.ThreadID {
			continue
		}
		if !filter.UpdatedBefore.IsZero() && (snap.UpdatedAt.IsZero() || !snap.UpdatedAt.Before(filter.UpdatedBefore)) {
			continue
		}
		out = append(out, cloneSnapshot(snap))
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

// ListStale implements runstate.StaleRepository with the local clock — an
// in-process store shares the caller's clock, so there is no skew to defend
// against. A zero UpdatedAt counts as stale, matching the reaper's fallback.
func (r *Repository) ListStale(ctx context.Context, filter runstate.ListFilter, grace time.Duration) ([]runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if grace < 0 {
		grace = 0
	}
	cutoff := time.Now().UTC().Add(-grace)
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]runstate.RunSnapshot, 0, len(r.snapshots))
	for _, snap := range r.snapshots {
		if !snap.UpdatedAt.IsZero() && snap.UpdatedAt.After(cutoff) {
			continue
		}
		if filter.Status != "" && snap.Status != filter.Status {
			continue
		}
		if filter.ScenarioName != "" && snap.ScenarioName != filter.ScenarioName {
			continue
		}
		if filter.TenantID != "" && snap.TenantID != filter.TenantID {
			continue
		}
		if filter.ParentRunID != "" && snap.ParentRunID != filter.ParentRunID {
			continue
		}
		if filter.ThreadID != "" && runstate.ResolveThreadID(snap) != filter.ThreadID {
			continue
		}
		if !filter.UpdatedBefore.IsZero() && (snap.UpdatedAt.IsZero() || !snap.UpdatedAt.Before(filter.UpdatedBefore)) {
			continue
		}
		out = append(out, cloneSnapshot(snap))
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func cloneSnapshot(snapshot runstate.RunSnapshot) runstate.RunSnapshot {
	if snapshot.Variables != nil {
		variables := make(map[string]json.RawMessage, len(snapshot.Variables))
		for k, v := range snapshot.Variables {
			variables[k] = clone(v)
		}
		snapshot.Variables = variables
	}
	if snapshot.StepOutputs != nil {
		outputs := make(map[string]runstate.StepOutputRef, len(snapshot.StepOutputs))
		for k, v := range snapshot.StepOutputs {
			outputs[k] = runstate.StepOutputRef{
				Inline: clone(v.Inline),
				Blob:   v.Blob,
			}
		}
		snapshot.StepOutputs = outputs
	}
	if snapshot.PendingGate != nil {
		gate := *snapshot.PendingGate
		gate.Payload = clone(gate.Payload)
		snapshot.PendingGate = &gate
	}
	return snapshot
}

func clone(value []byte) []byte {
	if value == nil {
		return nil
	}
	out := make([]byte, len(value))
	copy(out, value)
	return out
}
