package runstate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

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

type statusTransitionOverrideKey struct{}

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

// ContextWithStatusTransitionOverride allows explicit restore/rerun paths to
// reopen terminal snapshots while ordinary saves still enforce transitions.
func ContextWithStatusTransitionOverride(ctx context.Context) context.Context {
	return context.WithValue(ctx, statusTransitionOverrideKey{}, true)
}

// StatusTransitionOverrideFromContext reports whether transition checks should
// allow an otherwise invalid status change for this save operation.
func StatusTransitionOverrideFromContext(ctx context.Context) bool {
	allowed, _ := ctx.Value(statusTransitionOverrideKey{}).(bool)
	return allowed
}

// ValidateStatusTransition rejects status changes that do not follow the run
// lifecycle, unless ctx carries an explicit restore/rerun override.
func ValidateStatusTransition(ctx context.Context, previous *RunSnapshot, next RunStatus) error {
	if previous == nil || previous.Status == next {
		return nil
	}
	if StatusTransitionOverrideFromContext(ctx) {
		return nil
	}
	if !previous.Status.CanTransitionTo(next) {
		return fmt.Errorf("%w: %s -> %s", ErrInvalidTransition, previous.Status, next)
	}
	return nil
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
	RunID           string                     `json:"run_id"`
	Version         int64                      `json:"version"`
	ScenarioName    string                     `json:"scenario_name"`
	TenantID        string                     `json:"tenant_id,omitempty"`
	CurrentNodeID   string                     `json:"current_node_id,omitempty"`
	Status          RunStatus                  `json:"status"`
	Variables       map[string]json.RawMessage `json:"variables,omitempty"`
	StepOutputs     map[string]StepOutputRef   `json:"step_outputs,omitempty"`
	PendingGate     *core.CheckpointState      `json:"pending_gate,omitempty"`
	ParentRunID     string                     `json:"parent_run_id,omitempty"`
	ForkFromVersion int64                      `json:"fork_from_version,omitempty"`
	ThreadID        string                     `json:"thread_id,omitempty"`
	CreatedAt       time.Time                  `json:"created_at,omitempty"`
	UpdatedAt       time.Time                  `json:"updated_at,omitempty"`
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
	// List returns snapshots that match the filter. Implementations may
	// return results in any order. An empty filter matches all snapshots.
	List(ctx context.Context, filter ListFilter) ([]RunSnapshot, error)
}

// ListFilter controls which snapshots are returned by Repository.List.
// Zero values are treated as "no filter" for the corresponding field.
type ListFilter struct {
	// Status, when non-empty, restricts results to snapshots with this status.
	Status RunStatus
	// ScenarioName, when non-empty, restricts results to a specific scenario.
	ScenarioName string
	// TenantID, when non-empty, restricts results to a specific tenant.
	TenantID string
	// ParentRunID, when non-empty, restricts results to forks of a parent run.
	ParentRunID string
	// ThreadID, when non-empty, restricts results to a fork/thread group.
	ThreadID string
	// Limit is the maximum number of results to return. 0 means no limit.
	Limit int
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
