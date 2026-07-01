package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/pkg/coordination"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

const defaultRunLeaseTTL = 30 * time.Second

// WithRunLease enables distributed run-lease coordination: every Run,
// RunStructured, ResumeAndContinue, and RetryFailedRun holds (and renews) a
// lease on the run for as long as it executes. A run left in Running whose
// lease has expired belonged to a crashed or partitioned worker and can be
// reaped with MarkAbandonedRuns.
//
// owner identifies this worker in lease ownership; when empty, a random
// worker ID is generated. ttl defaults to 30s when non-positive.
func WithRunLease(locker coordination.Locker, owner string, ttl time.Duration) Option {
	return func(o *options) error {
		if locker == nil {
			return fmt.Errorf("agentflow: run lease locker is nil")
		}
		if owner == "" {
			owner = "worker-" + generateRunID()[len("run-"):]
		}
		if ttl <= 0 {
			ttl = defaultRunLeaseTTL
		}
		o.runLocker = locker
		o.runLeaseOwner = owner
		o.runLeaseTTL = ttl
		return nil
	}
}

func runLeaseKey(runID string) string {
	return "run:" + runID
}

// acquireRunLease takes the run's lease and starts a background renewal loop.
// The returned release function stops renewal and frees the lease; it is a
// no-op when run-lease coordination is not configured. The run ID is
// generated here when the request left it empty, so the lease and the run
// snapshot always use the same ID.
func (f *Framework) acquireRunLease(ctx context.Context, req *RunRequest) (func(), error) {
	if f.runLocker == nil {
		return func() {}, nil
	}
	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	return f.holdRunLease(ctx, req.RunID)
}

func (f *Framework) holdRunLease(ctx context.Context, runID string) (func(), error) {
	lease, ok, err := f.runLocker.Acquire(ctx, runLeaseKey(runID), f.runLeaseOwner, f.runLeaseTTL)
	if err != nil {
		return nil, fmt.Errorf("agentflow: acquire run lease for %q: %w", runID, err)
	}
	if !ok {
		return nil, fmt.Errorf("agentflow: run %q is leased by another worker: %w", runID, ErrRunInProgress)
	}
	var mu sync.Mutex
	current := lease
	renewCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(f.runLeaseTTL / 3)
		defer ticker.Stop()
		for {
			select {
			case <-renewCtx.Done():
				return
			case <-ticker.C:
				mu.Lock()
				held := current
				mu.Unlock()
				renewed, ok, err := f.runLocker.Renew(renewCtx, held, f.runLeaseTTL)
				if err != nil || !ok {
					if f.logger != nil {
						f.logger.Warn(renewCtx, "agentflow: run lease renewal failed", "run_id", runID, "renewed", ok, "error", err)
					}
					continue
				}
				mu.Lock()
				current = renewed
				mu.Unlock()
			}
		}
	}()
	release := func() {
		cancel()
		<-done
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer releaseCancel()
		mu.Lock()
		held := current
		mu.Unlock()
		if err := f.runLocker.Release(releaseCtx, held); err != nil && f.logger != nil {
			f.logger.Warn(releaseCtx, "agentflow: run lease release failed", "run_id", runID, "error", err)
		}
	}
	return release, nil
}

// MarkAbandonedRuns scans this scenario's Running runs and marks as Failed
// (run_error_message="worker lost") every run whose lease is no longer held:
// its worker crashed or was partitioned away, so nothing will ever move the
// run out of Running. It returns the IDs of the runs it marked. Requires
// WithRunLease, and assumes all workers executing this scenario's runs use
// run-lease coordination as well.
func (f *Framework) MarkAbandonedRuns(ctx context.Context) ([]string, error) {
	if f.runLocker == nil {
		return nil, fmt.Errorf("agentflow: run lease coordination is not configured; use WithRunLease")
	}
	filter := runstate.ListFilter{ScenarioName: f.scenario.Name, Status: runstate.RunStatusRunning}
	if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
		filter.TenantID = principal.Scope.TenantID
	}
	snapshots, err := f.runs.List(ctx, filter)
	if err != nil {
		return nil, err
	}
	var marked []string
	for _, snapshot := range snapshots {
		// Acquiring the run's lease succeeds only when no live worker holds
		// it; that is exactly the zombie condition.
		lease, ok, err := f.runLocker.Acquire(ctx, runLeaseKey(snapshot.RunID), f.runLeaseOwner, f.runLeaseTTL)
		if err != nil {
			return marked, err
		}
		if !ok {
			continue
		}
		reaped, err := f.markRunAbandoned(ctx, snapshot.RunID)
		if releaseErr := f.runLocker.Release(ctx, lease); releaseErr != nil && f.logger != nil {
			f.logger.Warn(ctx, "agentflow: abandoned-run lease release failed", "run_id", snapshot.RunID, "error", releaseErr)
		}
		if err != nil {
			return marked, err
		}
		if reaped {
			marked = append(marked, snapshot.RunID)
		}
	}
	return marked, nil
}

func (f *Framework) markRunAbandoned(ctx context.Context, runID string) (bool, error) {
	_, err := f.saveRunSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Status != runstate.RunStatusRunning {
			return runNotRunningError{runID: runID, status: snapshot.Status}
		}
		snapshot.Status = runstate.RunStatusFailed
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		snapshot.Variables["run_error_message"] = json.RawMessage(`"worker lost"`)
		return nil
	})
	if err != nil {
		var conflict runNotRunningError
		if errors.As(err, &conflict) {
			// A concurrent writer already moved the run out of Running
			// between the List and this save; nothing left to reap.
			return false, nil
		}
		return false, err
	}
	f.emit(ctx, core.EventRunFailed, runID, []byte(`{"error":"worker lost"}`))
	return true, nil
}
