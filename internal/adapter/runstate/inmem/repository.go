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
}

func NewRepository() *Repository {
	return &Repository{snapshots: make(map[string]runstate.RunSnapshot)}
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
	if snapshot.Version <= expectedVersion {
		snapshot.Version = expectedVersion + 1
	}
	var previous *runstate.RunSnapshot
	if ok {
		prev := current
		previous = &prev
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
