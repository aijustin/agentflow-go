package runstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/aijustin/agentflow-go/pkg/core"
)

var (
	ErrNotFound          = errors.New("runstate: not found")
	ErrStaleSnapshot     = errors.New("runstate: snapshot version is stale")
	ErrInvalidStatus     = errors.New("runstate: invalid status")
	ErrInvalidTransition = errors.New("runstate: invalid status transition")
	ErrTokenSuperseded   = errors.New("humangate: token superseded by newer version")
)

type RunStatus string

const (
	RunStatusRunning   RunStatus = "running"
	RunStatusPaused    RunStatus = "paused"
	RunStatusFailed    RunStatus = "failed"
	RunStatusCompleted RunStatus = "completed"
	RunStatusCancelled RunStatus = "cancelled"
)

func (s RunStatus) Valid() bool {
	switch s {
	case RunStatusRunning, RunStatusPaused, RunStatusFailed, RunStatusCompleted, RunStatusCancelled:
		return true
	default:
		return false
	}
}

func (s RunStatus) CanTransitionTo(next RunStatus) bool {
	if !s.Valid() || !next.Valid() {
		return false
	}
	switch s {
	case RunStatusRunning:
		return next == RunStatusPaused || next == RunStatusFailed || next == RunStatusCompleted || next == RunStatusCancelled
	case RunStatusPaused:
		return next == RunStatusRunning || next == RunStatusFailed || next == RunStatusCancelled
	case RunStatusFailed, RunStatusCompleted, RunStatusCancelled:
		return false
	default:
		return false
	}
}

type BlobRef struct {
	ID     string `json:"id"`
	Size   int64  `json:"size"`
	Sha256 string `json:"sha256"`
}

type StepOutputRef struct {
	Inline json.RawMessage `json:"inline,omitempty"`
	Blob   *BlobRef        `json:"blob,omitempty"`
}

type RunSnapshot struct {
	RunID         string                     `json:"run_id"`
	Version       int64                      `json:"version"`
	ScenarioName  string                     `json:"scenario_name"`
	CurrentNodeID string                     `json:"current_node_id,omitempty"`
	Status        RunStatus                  `json:"status"`
	Variables     map[string]json.RawMessage `json:"variables,omitempty"`
	StepOutputs   map[string]StepOutputRef   `json:"step_outputs,omitempty"`
	PendingGate   *core.CheckpointState      `json:"pending_gate,omitempty"`
}

func (s RunSnapshot) Validate() error {
	if s.RunID == "" {
		return fmt.Errorf("runstate: run_id is required")
	}
	if !s.Status.Valid() {
		return fmt.Errorf("%w: %q", ErrInvalidStatus, s.Status)
	}
	return nil
}

type Repository interface {
	Save(ctx context.Context, snapshot *RunSnapshot, expectedVersion int64) error
	Load(ctx context.Context, runID string) (RunSnapshot, error)
	Delete(ctx context.Context, runID string) error
}

type BlobStore interface {
	Put(ctx context.Context, data []byte) (BlobRef, error)
	Get(ctx context.Context, ref BlobRef) ([]byte, error)
}

func NewBlobRef(id string, data []byte) BlobRef {
	sum := sha256.Sum256(data)
	return BlobRef{
		ID:     id,
		Size:   int64(len(data)),
		Sha256: hex.EncodeToString(sum[:]),
	}
}
