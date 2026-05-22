package inmem

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

type checkpointRecord struct {
	snapshot   runstate.RunSnapshot
	recordedAt time.Time
}

// CheckpointHistory is an append-only in-memory checkpoint store.
type CheckpointHistory struct {
	mu      sync.RWMutex
	records map[string][]checkpointRecord
}

func NewCheckpointHistory() *CheckpointHistory {
	return &CheckpointHistory{records: make(map[string][]checkpointRecord)}
}

func (h *CheckpointHistory) Append(ctx context.Context, snapshot runstate.RunSnapshot) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	list := h.records[snapshot.RunID]
	for _, record := range list {
		if record.snapshot.Version == snapshot.Version {
			return nil
		}
	}
	h.records[snapshot.RunID] = append(list, checkpointRecord{
		snapshot:   cloneSnapshot(snapshot),
		recordedAt: time.Now().UTC(),
	})
	return nil
}

func (h *CheckpointHistory) List(ctx context.Context, runID string, limit int) ([]runstate.CheckpointSummary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	list := h.records[runID]
	out := make([]runstate.CheckpointSummary, 0, len(list))
	for _, record := range list {
		out = append(out, summarize(record))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Version < out[j].Version })
	if limit > 0 && len(out) > limit {
		out = out[len(out)-limit:]
	}
	return out, nil
}

func (h *CheckpointHistory) Load(ctx context.Context, runID string, version int64) (runstate.RunSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return runstate.RunSnapshot{}, err
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, record := range h.records[runID] {
		if record.snapshot.Version == version {
			return cloneSnapshot(record.snapshot), nil
		}
	}
	return runstate.RunSnapshot{}, runstate.ErrNotFound
}

func summarize(record checkpointRecord) runstate.CheckpointSummary {
	return runstate.CheckpointSummary{
		RunID:         record.snapshot.RunID,
		Version:       record.snapshot.Version,
		Status:        record.snapshot.Status,
		CurrentNodeID: record.snapshot.CurrentNodeID,
		StepCount:     len(record.snapshot.StepOutputs),
		RecordedAt:    record.recordedAt,
	}
}
