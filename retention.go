package agentflow

import (
	"context"
	"time"

	"github.com/aijustin/agentflow-go/pkg/observability"
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
	var err error
	filter, err = runstate.ScopeListFilter(ctx, filter)
	if err != nil {
		return 0, err
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

// deleteRunAndHistory removes the run snapshot and, when configured, every
// recorded revision of the run. It also cascades to the run's durable event
// history (when an event store is wired with WithEventStore and supports
// purging) and to its outbox rows (when the run-state repository implements
// runstate.OutboxRepository), so deleting a run never leaves orphaned events
// or undeliverable parked rows behind.
func (f *Framework) deleteRunAndHistory(ctx context.Context, runID string) error {
	if err := f.runs.Delete(ctx, runID); err != nil {
		return err
	}
	if f.checkpointHistory != nil {
		if err := f.checkpointHistory.Delete(ctx, runID); err != nil {
			return err
		}
	}
	if _, err := observability.DeleteEventsForRun(ctx, f.eventStore, runID); err != nil {
		return err
	}
	if outbox, ok := unwrapRunstate(f.runs).(runstate.OutboxRepository); ok && outbox != nil {
		if _, err := outbox.DeleteOutboxForRun(ctx, runID); err != nil {
			return err
		}
	}
	return nil
}

// purgeEventSideData ages out event history and published outbox rows past
// cutoff alongside the run purge that computed it. Unpublished outbox rows
// are never touched: they are undelivered events, not garbage. Note the
// event purge is purely age-based, so it also trims history of runs still
// alive past cutoff — pick maxAge to match how long event history must
// remain queryable.
func (f *Framework) purgeEventSideData(ctx context.Context, cutoff time.Time) error {
	if _, err := observability.PurgeEventsBefore(ctx, f.eventStore, cutoff); err != nil {
		return err
	}
	if outbox, ok := unwrapRunstate(f.runs).(runstate.OutboxRepository); ok && outbox != nil {
		if _, err := outbox.PurgeOutboxPublishedBefore(ctx, cutoff); err != nil {
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
		filter, err := runstate.ScopeListFilter(ctx, filter)
		if err != nil {
			return removed, err
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
	if err := f.purgeEventSideData(ctx, cutoff); err != nil {
		return removed, err
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
		filter, err := runstate.ScopeListFilter(ctx, filter)
		if err != nil {
			return removed, err
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
				break
			}
		}
		if policy.Limit > 0 && removed >= policy.Limit {
			break
		}
	}
	if err := f.purgeEventSideData(ctx, cutoff); err != nil {
		return removed, err
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
	filter, err := runstate.ScopeListFilter(ctx, filter)
	if err != nil {
		return 0, err
	}
	return runstate.PurgeOrphanBlobs(ctx, f.runs, f.blobs, filter)
}
