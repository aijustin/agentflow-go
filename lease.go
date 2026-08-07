package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	inmemcoord "github.com/aijustin/agentflow-go/internal/adapter/coordination/inmem"
	rediscoord "github.com/aijustin/agentflow-go/internal/adapter/coordination/redis"
	appexec "github.com/aijustin/agentflow-go/internal/application/runtime"
	"github.com/aijustin/agentflow-go/internal/safecall"
	"github.com/aijustin/agentflow-go/pkg/coordination"
	"github.com/aijustin/agentflow-go/pkg/core"
	"github.com/aijustin/agentflow-go/pkg/identity"
	"github.com/aijustin/agentflow-go/pkg/runstate"
	"github.com/redis/go-redis/v9"
)

// --- Run Lease ---

const defaultRunLeaseTTL = 30 * time.Second

// ErrRunLeaseLost reports that this worker no longer holds the run lease
// (renewal returned not-held or failed) and must stop executing before
// another worker reaps or takes over the run. It aliases the coordination
// package's sentinel so the runtime engine classifies the same error the
// facade returns.
var ErrRunLeaseLost = coordination.ErrRunLeaseLost

// ErrStaleFence reports that a fenced snapshot save presented a fencing token
// below the highest token already recorded for the run: this worker's run
// lease was superseded by a newer holder and its writes are rejected. It is
// handled exactly like ErrRunLeaseLost — the worker stops executing
// immediately and settles the run through the lease-lost path, never
// retrying the save. It aliases the runstate package's sentinel so facade
// and engine classify the same error.
var ErrStaleFence = runstate.ErrStaleFence

// ErrFenceRequired reports that a leased run save requires a run-state
// repository implementing runstate.FencedRepository.
var ErrFenceRequired = runstate.ErrFenceRequired

// WithRunLease enables distributed run-lease coordination: every Run,
// RunStructured, ResumeAndContinue, and RetryFailedRun holds (and renews) a
// lease on the run for as long as it executes. A run left in Running whose
// lease has expired belonged to a crashed or partitioned worker and can be
// reaped with MarkAbandonedRuns; pair it with WithRunReaper on multi-node
// deployments so reaping happens automatically.
//
// The lease's fencing token travels with execution: when the run-state
// repository implements runstate.FencedRepository, every snapshot save is
// validated against the run's fence high-water mark, so a zombie writer
// whose lease was superseded fails with ErrStaleFence instead of clobbering
// the new owner's state. Framework construction fails with ErrFenceRequired
// when the configured run-state repository cannot fence; leased execution
// never falls back to an unprotected plain save.
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

const defaultRunReaperInterval = time.Minute

// WithRunReaper starts a background loop that periodically calls
// MarkAbandonedRuns, failing Running runs whose worker crashed or was
// partitioned away (their run lease is no longer held). It requires
// WithRunLease; New returns an error otherwise.
//
// The sweep is lease-probe based and idempotent, so any number of nodes may
// run it concurrently against shared storage — a run is reaped exactly once
// no matter how many reapers race it. Multi-node deployments are strongly
// encouraged to enable it on every worker: without it, a crashed worker
// leaves its runs in Running forever.
//
// interval defaults to 1m when zero. gracePeriod overrides how long a
// Running run must go without snapshot updates before it becomes reapable;
// it defaults to the run-lease TTL, which covers the Resume and gate→lease
// windows. The reaper stops on Framework.Close.
func WithRunReaper(interval, gracePeriod time.Duration) Option {
	return func(o *options) error {
		if interval < 0 {
			return fmt.Errorf("agentflow: run reaper interval must be >= 0")
		}
		if gracePeriod < 0 {
			return fmt.Errorf("agentflow: run reaper grace period must be >= 0")
		}
		if interval == 0 {
			interval = defaultRunReaperInterval
		}
		o.runReaperInterval = interval
		o.runReaperGrace = gracePeriod
		return nil
	}
}

// startRunReaper launches the abandoned-run reaper loop and returns a closer
// that stops it and waits for the loop to exit. Each sweep runs under
// safecall.Do so a panicking sweep cannot kill the loop; the loop itself
// runs under GoSafe so even a loop-level panic cannot crash the process.
func (f *Framework) startRunReaper(interval time.Duration) func(context.Context) error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	safecall.GoSafe("agentflow: abandoned-run reaper", func(err error) {
		if f.logger != nil {
			f.logger.Error(context.Background(), "agentflow: abandoned-run reaper crashed", "error", err)
		}
	}, func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				f.reapAbandonedRunsOnce(ctx)
			}
		}
	})
	return func(context.Context) error {
		cancel()
		<-done
		return nil
	}
}

func (f *Framework) reapAbandonedRunsOnce(ctx context.Context) {
	err := safecall.Do("agentflow: abandoned-run reaper sweep", func() error {
		marked, err := f.MarkAbandonedRuns(ctx)
		if err != nil {
			return err
		}
		if len(marked) > 0 && f.logger != nil {
			f.logger.Warn(ctx, "agentflow: reaped abandoned runs", "run_ids", marked)
		}
		return nil
	})
	if err != nil && ctx.Err() == nil && f.logger != nil {
		f.logger.Warn(ctx, "agentflow: abandoned-run reaper sweep failed", "error", err)
	}
}

// reaperGrace returns how long a Running run must go without snapshot
// updates before the reaper may treat it as abandoned: the WithRunReaper
// grace period when set, otherwise the run-lease TTL. The window protects
// Resume (approve without continue) and the short gate→lease window in
// ResumeAndContinue from being reaped before a worker can take the lease.
func (f *Framework) reaperGrace() time.Duration {
	if f.runReaperGrace > 0 {
		return f.runReaperGrace
	}
	if f.runLeaseTTL > 0 {
		return f.runLeaseTTL
	}
	return defaultRunLeaseTTL
}

// reapZombieRun is the single-run equivalent of MarkAbandonedRuns, used by
// the async run-job handler when a redelivered run job finds its run still
// Running (ErrRunInProgress): the worker that started it has either crashed
// — its lease is gone and the run is a zombie — or is still alive on another
// node. It returns true only when the run was confirmed unowned and marked
// Failed, making it eligible for RetryFailedRun re-entry.
func (f *Framework) reapZombieRun(ctx context.Context, runID string) (bool, error) {
	snapshot, err := runstate.LoadAuthorized(ctx, f.runs, runID)
	if err != nil {
		return false, err
	}
	if snapshot.Status != runstate.RunStatusRunning {
		return false, nil
	}
	if variableJSONString(snapshot.Variables, runstate.VarRunLeaseOwner) == "" {
		// Same rule as MarkAbandonedRuns: a run without the lease-owner
		// marker is not lease-managed, and probing its lease would always
		// succeed, reaping live work.
		return false, nil
	}
	if !snapshot.UpdatedAt.IsZero() && time.Since(snapshot.UpdatedAt) < f.reaperGrace() {
		return false, nil
	}
	// Probe with a distinct reaper owner: Acquire is reentrant for the
	// holding owner, so probing with f.runLeaseOwner would steal (and then
	// reap) this worker's own live run.
	lease, ok, err := f.runLocker.Acquire(ctx, runLeaseKey(runID), f.runLeaseOwner+":reaper", f.runLeaseTTL)
	if err != nil {
		return false, err
	}
	if !ok {
		// A live worker still holds the lease; the run is genuinely in
		// progress.
		return false, nil
	}
	// Stamp the probe lease's fencing token so the abandoned-marking save is
	// validated like any leased writer: the probe just minted a fresh token,
	// so the save passes the fence check, and a not-actually-dead original
	// worker holding an older token can no longer overwrite the Failed
	// status afterwards.
	reaped, err := f.markRunAbandoned(runstate.ContextWithFenceToken(ctx, lease.Token), runID)
	if releaseErr := f.runLocker.Release(ctx, lease); releaseErr != nil && f.logger != nil {
		f.logger.Warn(ctx, "agentflow: abandoned-run lease release failed", "run_id", runID, "error", releaseErr)
	}
	if err != nil {
		return false, err
	}
	return reaped, nil
}

// mapLeaseLostError remaps a context cancellation caused by lease renewal
// failure back to ErrRunLeaseLost so callers can distinguish worker-lease
// aborts from ordinary cancellations.
func mapLeaseLostError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	cause := context.Cause(ctx)
	if cause != nil && errors.Is(cause, ErrRunLeaseLost) {
		return cause
	}
	if errors.Is(err, ErrRunLeaseLost) {
		return err
	}
	return err
}

// acquireRunLease takes the run's lease and starts a background renewal loop.
// The returned context is canceled with ErrRunLeaseLost when renewal reports
// the lease as lost, or when transient renewal errors persist for a full TTL,
// so the in-flight Run/Stream aborts instead of continuing without ownership.
// The release function stops renewal and frees the lease; it is a no-op when
// run-lease coordination is not configured. The run ID is generated here when
// the request left it empty, so the lease and the run snapshot always use the
// same ID.
func (f *Framework) acquireRunLease(ctx context.Context, req *RunRequest) (context.Context, func(), error) {
	if req.RunID == "" {
		req.RunID = generateRunID()
	}
	releaseSlot, err := f.tryEnterExecution(req.RunID, false)
	if err != nil {
		return ctx, nil, err
	}
	if f.runLocker == nil {
		return ctx, releaseSlot, nil
	}
	runCtx, releaseLease, err := f.holdRunLease(ctx, req.RunID)
	if err != nil {
		releaseSlot()
		return ctx, nil, err
	}
	return runCtx, func() {
		releaseLease()
		releaseSlot()
	}, nil
}

func (f *Framework) holdRunLease(ctx context.Context, runID string) (context.Context, func(), error) {
	lease, ok, err := f.runLocker.Acquire(ctx, runLeaseKey(runID), f.runLeaseOwner, f.runLeaseTTL)
	if err != nil {
		return ctx, nil, fmt.Errorf("agentflow: acquire run lease for %q: %w", runID, err)
	}
	if !ok {
		return ctx, nil, fmt.Errorf("agentflow: run %q is leased by another worker: %w", runID, ErrRunInProgress)
	}
	var mu sync.Mutex
	current := lease
	// Child of the caller ctx so caller cancel still stops the run; renewal
	// uses a WithoutCancel parent so a brief call-site cancel does not stop
	// lease heartbeats while release is still running. On renewal loss we
	// cancel this runCtx so execution cannot race a reaper. The owner is
	// stamped onto the context so run snapshots created under this lease
	// record it (MarkAbandonedRuns only reaps owner-marked runs), and the
	// lease's fencing token is stamped alongside it so every snapshot save
	// during execution is validated against the run's fence high-water mark
	// (see runstate.SaveWithFence). Renew never changes the token and there
	// is no re-Acquire path, so the stamped token stays valid for the whole
	// execution.
	leaseCtx := runstate.ContextWithFenceToken(appexec.ContextWithRunLeaseOwner(ctx, f.runLeaseOwner), lease.Token)
	runCtx, cancelRun := context.WithCancelCause(leaseCtx)
	renewCtx, cancelRenew := context.WithCancel(context.WithoutCancel(ctx))
	done := make(chan struct{})
	safecall.GoSafe("agentflow: run lease renewal", func(err error) {
		// The deferred close(done) below has already run, so release() does
		// not deadlock. A crashed renewer can no longer prove ownership:
		// abort the run as lease-lost instead of letting it execute unowned
		// while another worker takes over.
		if f.logger != nil {
			f.logger.Error(renewCtx, "agentflow: run lease renewal crashed; aborting run", "run_id", runID, "error", err)
		}
		cancelRun(fmt.Errorf("%w: run %q", ErrRunLeaseLost, runID))
	}, func() {
		defer close(done)
		interval := f.runLeaseTTL / 3
		if interval < time.Millisecond {
			interval = time.Millisecond
		}
		// A renewal that reports the lease as not-held is a definitive loss
		// and aborts immediately; a transient renewal *error* (store blip,
		// network partition) does not prove loss, so it is tolerated until
		// consecutive failures span a full TTL — by then the lease has
		// genuinely expired and a reaper may take over.
		maxTransientFailures := int(f.runLeaseTTL / interval)
		if maxTransientFailures < 1 {
			maxTransientFailures = 1
		}
		transientFailures := 0
		ticker := time.NewTicker(interval)
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
				switch {
				case err == nil && ok:
					transientFailures = 0
					mu.Lock()
					current = renewed
					mu.Unlock()
				case err == nil && !ok:
					if f.logger != nil {
						f.logger.Warn(renewCtx, "agentflow: run lease no longer held; aborting run", "run_id", runID)
					}
					cancelRun(fmt.Errorf("%w: run %q", ErrRunLeaseLost, runID))
					return
				default:
					transientFailures++
					if f.logger != nil {
						f.logger.Warn(renewCtx, "agentflow: run lease renewal failed", "run_id", runID, "attempt", transientFailures, "max_attempts", maxTransientFailures, "error", err)
					}
					if transientFailures >= maxTransientFailures {
						cancelRun(fmt.Errorf("%w: run %q", ErrRunLeaseLost, runID))
						return
					}
				}
			}
		}
	})
	release := func() {
		cancelRenew()
		<-done
		releaseCtx, releaseCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer releaseCancel()
		mu.Lock()
		held := current
		mu.Unlock()
		if err := f.runLocker.Release(releaseCtx, held); err != nil && f.logger != nil {
			f.logger.Warn(releaseCtx, "agentflow: run lease release failed", "run_id", runID, "error", err)
		}
		cancelRun(nil)
	}
	return runCtx, release, nil
}

// MarkAbandonedRuns scans this scenario's Running runs and marks as Failed
// (run_error_message="worker lost") every lease-managed run whose lease is no
// longer held: its worker crashed or was partitioned away, so nothing will
// ever move the run out of Running. It returns the IDs of the runs it marked.
// Requires WithRunLease.
//
// Only runs stamped with a lease owner (run_lease_owner variable, written
// when the run executes under WithRunLease) are eligible: a Running run
// without the marker belongs to a worker that does not use lease
// coordination, and probing its lease would always succeed and reap live
// work. Tenant scope: when ctx carries a tenant-scoped principal, only that
// tenant's runs are scanned and reaped; runs belonging to other tenants are
// never touched even if the repository's List filtering is lax. Without a
// tenant principal this is an admin operation across all tenants.
func (f *Framework) MarkAbandonedRuns(ctx context.Context) ([]string, error) {
	if f.runLocker == nil {
		return nil, fmt.Errorf("agentflow: run lease coordination is not configured; use WithRunLease")
	}
	filter := runstate.ListFilter{ScenarioName: f.currentScenario().Name, Status: runstate.RunStatusRunning}
	if principal, ok := identity.PrincipalFromContext(ctx); ok && principal.Scope.TenantID != "" {
		filter.TenantID = principal.Scope.TenantID
	}
	// Skip recently-updated Running runs so Resume (approve without continue)
	// and the short gate→lease window in ResumeAndContinue are not reaped
	// before a worker can take the lease. When the repository supports
	// ListStale, the grace comparison runs on the store's own clock (e.g.
	// PostgreSQL NOW()), so application-clock skew cannot shrink or stretch
	// the window; otherwise fall back to filtering List results locally.
	grace := f.reaperGrace()
	var snapshots []runstate.RunSnapshot
	var err error
	graceApplied := false
	if staleRepo, ok := f.runs.(runstate.StaleRepository); ok {
		snapshots, err = staleRepo.ListStale(ctx, filter, grace)
		graceApplied = true
	} else {
		snapshots, err = f.runs.List(ctx, filter)
	}
	if err != nil {
		return nil, err
	}
	// Enforce the tenant boundary here as well instead of trusting the
	// repository filter alone: a snapshot the caller is not authorized for
	// must never be probed or reaped.
	authorized := make([]runstate.RunSnapshot, 0, len(snapshots))
	for _, snapshot := range snapshots {
		if err := runstate.AuthorizeTenant(ctx, snapshot); err != nil {
			continue
		}
		authorized = append(authorized, snapshot)
	}
	snapshots = authorized
	// A distinct reaper owner is required: Acquire is reentrant for the
	// holding owner, so probing with f.runLeaseOwner would steal (and then
	// reap) this very worker's own live runs.
	reaperOwner := f.runLeaseOwner + ":reaper"
	var marked []string
	for _, snapshot := range snapshots {
		if variableJSONString(snapshot.Variables, runstate.VarRunLeaseOwner) == "" {
			// No lease-owner marker: this run is executed by a worker without
			// lease coordination (or predates owner stamping). Probing its
			// lease would always succeed, so the old heuristic would have
			// reaped a live run; skip it instead.
			continue
		}
		if !graceApplied && !snapshot.UpdatedAt.IsZero() && time.Since(snapshot.UpdatedAt) < grace {
			continue
		}
		// Acquiring the run's lease succeeds only when no live worker holds
		// it; that is exactly the zombie condition.
		lease, ok, err := f.runLocker.Acquire(ctx, runLeaseKey(snapshot.RunID), reaperOwner, f.runLeaseTTL)
		if err != nil {
			return marked, err
		}
		if !ok {
			continue
		}
		// Stamp the probe lease's fencing token: see reapZombieRun for why the
		// abandoned-marking save must be fenced.
		reaped, err := f.markRunAbandoned(runstate.ContextWithFenceToken(ctx, lease.Token), snapshot.RunID)
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
	unconfirmedCheckpoint := false
	_, err := f.saveRunSnapshotWithRetry(ctx, runID, func(snapshot *runstate.RunSnapshot) error {
		if snapshot.Status != runstate.RunStatusRunning {
			return runNotRunningError{runID: runID, status: snapshot.Status}
		}
		snapshot.Status = runstate.RunStatusFailed
		if snapshot.Variables == nil {
			snapshot.Variables = make(map[string]json.RawMessage)
		}
		// A run that crashed between writing the pause checkpoint and
		// gate.Pause carries checkpoint metadata nobody ever approved; drop
		// it so a later RetryFailedRun cannot execute the unapproved state.
		if appexec.ClearUnconfirmedCheckpoint(snapshot) {
			unconfirmedCheckpoint = true
			snapshot.Variables[runstate.VarRunErrorMessage] = json.RawMessage(`"worker lost (unconfirmed pause checkpoint discarded)"`)
		} else {
			snapshot.Variables[runstate.VarRunErrorMessage] = json.RawMessage(`"worker lost"`)
		}
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
	if unconfirmedCheckpoint {
		f.emit(ctx, core.EventRunFailed, runID, []byte(`{"error":"worker lost","unconfirmed_checkpoint_discarded":true}`))
		if f.logger != nil {
			f.logger.Warn(ctx, "agentflow: reaped run with unconfirmed pause checkpoint; checkpoint discarded", "run_id", runID)
		}
		return true, nil
	}
	f.emit(ctx, core.EventRunFailed, runID, []byte(`{"error":"worker lost"}`))
	return true, nil
}

// --- Lockers ---

// RedisLocker is a coordination.Locker with a Close method releasing the
// underlying connection pool. Lockers built from a caller-provided go-redis
// client do not close that client on Close.
type RedisLocker interface {
	coordination.Locker
	Close() error
}

type RedisLockerConfig struct {
	Addr         string
	Password     string
	DB           int
	KeyPrefix    string
	DialTimeout  time.Duration
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
}

// NewInMemoryLocker creates an in-process lease manager for tests and
// single-process deployments.
func NewInMemoryLocker() coordination.Locker {
	return inmemcoord.NewLocker()
}

// NewRedisLocker creates a Redis-backed lease manager for distributed worker
// and workflow coordination. Every Acquire mints a monotonically increasing
// fencing token (Lease.Token); Renew and Release compare the full
// "{owner}:{token}" lock value, so a stale handle from a superseded holder —
// including a different process configured with the same owner name — is
// rejected. The returned locker owns a pooled go-redis client; close it with
// Close when done.
func NewRedisLocker(config RedisLockerConfig) (RedisLocker, error) {
	return rediscoord.NewLocker(rediscoord.Config{
		Addr:         config.Addr,
		Password:     config.Password,
		DB:           config.DB,
		KeyPrefix:    config.KeyPrefix,
		DialTimeout:  config.DialTimeout,
		ReadTimeout:  config.ReadTimeout,
		WriteTimeout: config.WriteTimeout,
	})
}

// NewRedisLockerFromClient wraps an existing pooled go-redis client so
// several subsystems (run lease, device registry, command routing) can share
// one connection pool. The client stays owned by the caller and is not
// closed by the locker.
func NewRedisLockerFromClient(client *redis.Client, keyPrefix string) (RedisLocker, error) {
	return rediscoord.NewLockerFromClient(client, keyPrefix)
}
