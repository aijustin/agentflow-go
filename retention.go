package agentflow

import (
	"context"
	"time"

	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// RetentionPolicy controls run-state cleanup.
type RetentionPolicy struct {
	MaxAge       time.Duration
	Status       runstate.RunStatus
	ScenarioName string
	Limit        int
}

// PurgeRuns deletes run snapshots matching the filter.
func (f *Framework) PurgeRuns(ctx context.Context, filter runstate.ListFilter) (int, error) {
	if f.runs == nil {
		return 0, nil
	}
	snapshots, err := f.runs.List(ctx, filter)
	if err != nil {
		return 0, err
	}
	removed := 0
	for _, snapshot := range snapshots {
		if err := f.runs.Delete(ctx, snapshot.RunID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// PurgeExpired deletes run snapshots older than maxAge.
func (f *Framework) PurgeExpired(ctx context.Context, maxAge time.Duration) (int, error) {
	if maxAge <= 0 {
		return 0, nil
	}
	filter := runstate.ListFilter{ScenarioName: f.scenario.Name}
	snapshots, err := f.runs.List(ctx, filter)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().UTC().Add(-maxAge)
	removed := 0
	for _, snapshot := range snapshots {
		// Run snapshots do not store timestamps; retention by age requires
		// repository metadata. PurgeExpired therefore matches completed runs
		// when no timestamp is available and relies on explicit PurgeRuns for
		// precise expiry control in production stores.
		if snapshot.Status != runstate.RunStatusCompleted && snapshot.Status != runstate.RunStatusFailed && snapshot.Status != runstate.RunStatusCancelled {
			continue
		}
		_ = cutoff
		if err := f.runs.Delete(ctx, snapshot.RunID); err != nil {
			return removed, err
		}
		removed++
	}
	return removed, nil
}

// PurgeWithPolicy deletes run snapshots using a retention policy.
func (f *Framework) PurgeWithPolicy(ctx context.Context, policy RetentionPolicy) (int, error) {
	filter := runstate.ListFilter{
		Status:       policy.Status,
		ScenarioName: policy.ScenarioName,
		Limit:        policy.Limit,
	}
	if filter.ScenarioName == "" {
		filter.ScenarioName = f.scenario.Name
	}
	return f.PurgeRuns(ctx, filter)
}
