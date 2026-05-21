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

// PurgeExpired deletes terminal run snapshots whose UpdatedAt is before now-maxAge.
// Snapshots without UpdatedAt are skipped.
func (f *Framework) PurgeExpired(ctx context.Context, maxAge time.Duration) (int, error) {
	if maxAge <= 0 {
		return 0, nil
	}
	if f.runs == nil {
		return 0, nil
	}
	filter := runstate.ListFilter{ScenarioName: f.scenario.Name}
	if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
		filter.TenantID = principal.Scope.TenantID
	}
	snapshots, err := f.runs.List(ctx, filter)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().UTC().Add(-maxAge)
	removed := 0
	for _, snapshot := range snapshots {
		if snapshot.Status != runstate.RunStatusCompleted && snapshot.Status != runstate.RunStatusFailed && snapshot.Status != runstate.RunStatusCancelled {
			continue
		}
		if snapshot.UpdatedAt.IsZero() || !snapshot.UpdatedAt.Before(cutoff) {
			continue
		}
		if err := f.runs.Delete(ctx, snapshot.RunID); err != nil {
			return removed, err
		}
		removed++
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
		filter.ScenarioName = f.scenario.Name
	}
	return f.PurgeRuns(ctx, filter)
}

func (f *Framework) purgeExpiredWithLimit(ctx context.Context, policy RetentionPolicy) (int, error) {
	if f.runs == nil {
		return 0, nil
	}
	filter := runstate.ListFilter{
		Status:       policy.Status,
		ScenarioName: policy.ScenarioName,
	}
	if filter.ScenarioName == "" {
		filter.ScenarioName = f.scenario.Name
	}
	if filter.TenantID == "" {
		if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
			filter.TenantID = principal.Scope.TenantID
		}
	}
	snapshots, err := f.runs.List(ctx, filter)
	if err != nil {
		return 0, err
	}
	cutoff := time.Now().UTC().Add(-policy.MaxAge)
	removed := 0
	for _, snapshot := range snapshots {
		if policy.Status != "" && snapshot.Status != policy.Status {
			continue
		}
		if snapshot.UpdatedAt.IsZero() || !snapshot.UpdatedAt.Before(cutoff) {
			continue
		}
		if err := f.runs.Delete(ctx, snapshot.RunID); err != nil {
			return removed, err
		}
		removed++
		if policy.Limit > 0 && removed >= policy.Limit {
			break
		}
	}
	return removed, nil
}
