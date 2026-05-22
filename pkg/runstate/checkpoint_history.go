package runstate

import (
	"context"
	"time"
)

// CheckpointSummary describes one append-only run snapshot revision.
type CheckpointSummary struct {
	RunID         string    `json:"run_id"`
	Version       int64     `json:"version"`
	Status        RunStatus `json:"status"`
	CurrentNodeID string    `json:"current_node_id,omitempty"`
	StepCount     int       `json:"step_count"`
	RecordedAt    time.Time `json:"recorded_at"`
}

// CheckpointHistory stores immutable run snapshot revisions for time-travel.
type CheckpointHistory interface {
	Append(ctx context.Context, snapshot RunSnapshot) error
	List(ctx context.Context, runID string, limit int) ([]CheckpointSummary, error)
	Load(ctx context.Context, runID string, version int64) (RunSnapshot, error)
}
