package agentflow

import (
	"context"
	"time"

	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// RetentionPolicy controls run-state cleanup.
type RetentionPolicy struct {
	MaxAge       time.Duration
	Status       runstate.RunStatus
	ScenarioName string
	Limit        int
}

type purgeRunsOptions struct {
	force bool
}

// PurgeRunsOption customizes PurgeRuns.
type PurgeRunsOption func(*purgeRunsOptions)

// WithPurgeForce lets PurgeRuns delete runs in any status, including Running
// and Paused. Without it, non-terminal runs are skipped so a retention sweep
// can never delete a run that is still executing or awaiting a human.
func WithPurgeForce() PurgeRunsOption {
	return func(opts *purgeRunsOptions) {
		opts.force = true
	}
}

// isTerminalRunStatus reports whether the run can never execute again, so
// deleting its snapshot (and checkpoint history) cannot orphan live work.
func isTerminalRunStatus(status runstate.RunStatus) bool {
	switch status {
	case runstate.RunStatusCompleted, runstate.RunStatusFailed, runstate.RunStatusCancelled:
		return true
	default:
		return false
	}
}

// PurgeRuns deletes run snapshots matching the filter. Non-terminal runs
// (Running, Paused) are skipped unless WithPurgeForce is given. Each deleted
// run's checkpoint history is deleted alongside when a history store is
// configured, so time-travel data does not outlive its run.
func (f *Framework) PurgeRuns(ctx context.Context, filter runstate.ListFilter, opts ...PurgeRunsOption) (int, error) {
	if f.runs == nil {
		return 0, nil
	}
	options := purgeRunsOptions{}
	for _, opt := range opts {
		if opt != nil {
			opt(&options)
		}
	}
	snapshots, err := f.runs.List(ctx, filter)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, snapshot := range snapshots {
		if !options.force && !isTerminalRunStatus(snapshot.Status) {
			continue
		}
		if err := f.deleteRunAndHistory(ctx, snapshot.RunID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// deleteRunAndHistory removes the run snapshot and, when a checkpoint history
// store is configured, every recorded revision of the run.
func (f *Framework) deleteRunAndHistory(ctx context.Context, runID string) error {
	if err := f.runs.Delete(ctx, runID); err != nil {
		return err
	}
	if f.checkpointHistory != nil {
		if err := f.checkpointHistory.Delete(ctx, runID); err != nil {
			return err
		}
	}
	return nil
}

// PurgeExpired deletes terminal run snapshots whose UpdatedAt is before now-maxAge.
// Snapshots without UpdatedAt are skipped.
func (f *Framework) PurgeExpired(ctx context.Context, maxAge time.Duration) (int, error) {
	if maxAge <= 0 {
		return 0, nil
	}
	if f.runs == nil {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-maxAge)
	removed := 0
	for _, status := range []runstate.RunStatus{
		runstate.RunStatusCompleted, runstate.RunStatusFailed, runstate.RunStatusCancelled,
	} {
		filter := runstate.ListFilter{
			ScenarioName:  f.currentScenario().Name,
			Status:        status,
			UpdatedBefore: cutoff,
		}
		if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
			filter.TenantID = principal.Scope.TenantID
		}
		snapshots, err := f.runs.List(ctx, filter)
		if err != nil {
			return removed, err
		}
		for _, snapshot := range snapshots {
			if err := f.deleteRunAndHistory(ctx, snapshot.RunID); err != nil {
				return removed, err
			}
			removed++
		}
	}
	return removed, nil
}

// PurgeWithPolicy deletes run snapshots using a retention policy.
func (f *Framework) PurgeWithPolicy(ctx context.Context, policy RetentionPolicy) (int, error) {
	if policy.MaxAge > 0 {
		return f.purgeExpiredWithLimit(ctx, policy)
	}
	filter := runstate.ListFilter{
		Status:       policy.Status,
		ScenarioName: policy.ScenarioName,
		Limit:        policy.Limit,
	}
	if filter.ScenarioName == "" {
		filter.ScenarioName = f.currentScenario().Name
	}
	return f.PurgeRuns(ctx, filter)
}

func (f *Framework) purgeExpiredWithLimit(ctx context.Context, policy RetentionPolicy) (int, error) {
	if f.runs == nil {
		return 0, nil
	}
	cutoff := time.Now().UTC().Add(-policy.MaxAge)
	statuses := []runstate.RunStatus{policy.Status}
	if policy.Status == "" {
		statuses = []runstate.RunStatus{
			runstate.RunStatusCompleted, runstate.RunStatusFailed, runstate.RunStatusCancelled,
		}
	}
	removed := 0
	for _, status := range statuses {
		if status == "" {
			continue
		}
		// A policy that explicitly targets a non-terminal status would
		// otherwise delete live runs; clamp it to the terminal set unless the
		// caller goes through PurgeRuns with WithPurgeForce.
		if !isTerminalRunStatus(status) {
			continue
		}
		filter := runstate.ListFilter{
			Status:        status,
			ScenarioName:  policy.ScenarioName,
			UpdatedBefore: cutoff,
			Limit:         policy.Limit,
		}
		if filter.ScenarioName == "" {
			filter.ScenarioName = f.currentScenario().Name
		}
		if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
			filter.TenantID = principal.Scope.TenantID
		}
		if policy.Limit > 0 {
			remaining := policy.Limit - removed
			if remaining <= 0 {
				break
			}
			filter.Limit = remaining
		}
		snapshots, err := f.runs.List(ctx, filter)
		if err != nil {
			return removed, err
		}
		for _, snapshot := range snapshots {
			if err := f.deleteRunAndHistory(ctx, snapshot.RunID); err != nil {
				return removed, err
			}
			removed++
			if policy.Limit > 0 && removed >= policy.Limit {
				return removed, nil
			}
		}
	}
	return removed, nil
}

// --- Orphan Blob GC ---

// PurgeOrphanBlobs deletes blob objects that are no longer referenced by any run snapshot
// for the current scenario (and tenant, when a principal is present).
func (f *Framework) PurgeOrphanBlobs(ctx context.Context) (int, error) {
	if f.runs == nil || f.blobs == nil {
		return 0, nil
	}
	filter := runstate.ListFilter{ScenarioName: f.currentScenario().Name}
	if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
		filter.TenantID = principal.Scope.TenantID
	}
	return runstate.PurgeOrphanBlobs(ctx, f.runs, f.blobs, filter)
}
