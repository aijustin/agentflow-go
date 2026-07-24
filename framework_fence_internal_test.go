package agentflow

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	inmemcoord "github.com/aijustin/agentflow-go/internal/adapter/coordination/inmem"
	runstateinmem "github.com/aijustin/agentflow-go/internal/adapter/runstate/inmem"
	"github.com/aijustin/agentflow-go/pkg/coordination"
	"github.com/aijustin/agentflow-go/pkg/runstate"
)

// stallLocker simulates a worker GC pause: once stalled, Renew blocks until
// unblocked (or the renewal context is canceled), so the lease silently
// expires in the store while the holder keeps executing, unaware.
type stallLocker struct {
	inner     coordination.Locker
	stalled   atomic.Bool
	unblock   chan struct{}
	unblocked atomic.Bool
}

func newStallLocker(inner coordination.Locker) *stallLocker {
	return &stallLocker{inner: inner, unblock: make(chan struct{})}
}

func (l *stallLocker) Acquire(ctx context.Context, key, owner string, ttl time.Duration) (coordination.Lease, bool, error) {
	return l.inner.Acquire(ctx, key, owner, ttl)
}

func (l *stallLocker) Renew(ctx context.Context, lease coordination.Lease, ttl time.Duration) (coordination.Lease, bool, error) {
	if l.stalled.Load() {
		select {
		case <-l.unblock:
		case <-ctx.Done():
			return coordination.Lease{}, false, ctx.Err()
		}
	}
	return l.inner.Renew(ctx, lease, ttl)
}

func (l *stallLocker) Release(ctx context.Context, lease coordination.Lease) error {
	return l.inner.Release(ctx, lease)
}

func (l *stallLocker) resume() {
	if l.unblocked.CompareAndSwap(false, true) {
		close(l.unblock)
	}
}

// TestHoldRunLeaseStampsFenceToken verifies the lease's fencing token is
// stamped onto the run context so execution-time snapshot saves can present
// it.
func TestHoldRunLeaseStampsFenceToken(t *testing.T) {
	fw := &Framework{
		runs:          runstateinmem.NewRepository(),
		runLocker:     inmemcoord.NewLocker(),
		runLeaseOwner: "worker-1",
		runLeaseTTL:   time.Minute,
	}
	runCtx, release, err := fw.holdRunLease(context.Background(), "run-token-stamp")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if token := runstate.FenceTokenFromContext(runCtx); token == 0 {
		t.Fatal("expected holdRunLease to stamp a non-zero fence token on the run context")
	}
	// Without a lease the context carries no token and fencing stays off.
	if token := runstate.FenceTokenFromContext(context.Background()); token != 0 {
		t.Fatalf("expected zero fence token without a lease, got %d", token)
	}
}

// TestFencedSaveRejectsZombieWriter is the zombie-writer adversarial test:
// worker-1 holds the run lease and executes; a simulated GC pause lets the
// lease expire; worker-2 acquires it (new fencing token) and advances the
// run; worker-1's late fenced save with the stale token must fail with
// ErrStaleFence and must not roll back worker-2's state.
func TestFencedSaveRejectsZombieWriter(t *testing.T) {
	repo := runstateinmem.NewRepository()
	locker := newStallLocker(inmemcoord.NewLocker())
	const ttl = 300 * time.Millisecond
	worker1 := &Framework{runs: repo, runLocker: locker, runLeaseOwner: "worker-1", runLeaseTTL: ttl}
	worker2 := &Framework{runs: repo, runLocker: locker, runLeaseOwner: "worker-2", runLeaseTTL: ttl}

	// Worker-1 takes the lease and writes the initial Running snapshot.
	ctx1, release1, err := worker1.holdRunLease(context.Background(), "run-a")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		locker.resume()
		release1()
	}()
	token1 := runstate.FenceTokenFromContext(ctx1)
	if token1 == 0 {
		t.Fatal("worker-1 run context carries no fence token")
	}
	snapshot := runstate.RunSnapshot{
		RunID:        "run-a",
		ScenarioName: "sc",
		Status:       runstate.RunStatusRunning,
		Variables:    map[string]json.RawMessage{"writer": json.RawMessage(`"w1"`)},
	}
	if err := worker1.saveRunSnapshot(ctx1, &snapshot, 0); err != nil {
		t.Fatalf("worker-1 initial fenced save: %v", err)
	}

	// GC pause: worker-1 stops renewing; the lease expires in the store.
	locker.stalled.Store(true)
	time.Sleep(ttl + 200*time.Millisecond)

	// Worker-2 takes over and advances the run under a newer token.
	ctx2, release2, err := worker2.holdRunLease(context.Background(), "run-a")
	if err != nil {
		t.Fatalf("worker-2 must acquire the expired lease: %v", err)
	}
	defer release2()
	token2 := runstate.FenceTokenFromContext(ctx2)
	if token2 <= token1 {
		t.Fatalf("worker-2 token %d must exceed worker-1 token %d", token2, token1)
	}
	if _, err := worker2.saveRunSnapshotWithRetry(ctx2, "run-a", func(s *runstate.RunSnapshot) error {
		s.Variables["writer"] = json.RawMessage(`"w2"`)
		return nil
	}); err != nil {
		t.Fatalf("worker-2 fenced save: %v", err)
	}

	// Version conflicts still classify as ErrStaleSnapshot (checked before
	// the fence) even when the token is also stale, so the existing
	// retry-on-stale-snapshot semantics are unchanged by fencing.
	stale := snapshot // version 0, long superseded
	if err := worker1.saveRunSnapshot(ctx1, &stale, stale.Version); !errors.Is(err, runstate.ErrStaleSnapshot) {
		t.Fatalf("expected ErrStaleSnapshot for a version conflict, got %v", err)
	}

	// Worker-1 wakes up and saves with a fresh version but the stale token:
	// this must be rejected as ErrStaleFence, straight through (no retry
	// exhaustion), leaving worker-2's state intact.
	err = func() error {
		_, err := worker1.saveRunSnapshotWithRetry(ctx1, "run-a", func(s *runstate.RunSnapshot) error {
			s.Variables["writer"] = json.RawMessage(`"w1-late"`)
			s.Status = runstate.RunStatusFailed // zombie tries to roll back the run
			return nil
		})
		return err
	}()
	if !errors.Is(err, ErrStaleFence) {
		t.Fatalf("expected ErrStaleFence for the zombie writer, got %v", err)
	}
	if strings.Contains(err.Error(), "after stale retries") {
		t.Fatalf("ErrStaleFence must not be retried as a version conflict, got %v", err)
	}
	got, err := repo.Load(context.Background(), "run-a")
	if err != nil {
		t.Fatal(err)
	}
	if string(got.Variables["writer"]) != `"w2"` {
		t.Fatalf("zombie write must not clobber worker-2 state, got writer=%s", got.Variables["writer"])
	}
	if got.Status != runstate.RunStatusRunning {
		t.Fatalf("zombie write must not roll back run status, got %s", got.Status)
	}
}

// TestSaveWithFenceWithoutTokenOrSupport pins the fallback contract: no
// token in context behaves exactly like Save, and a repository without
// FencedRepository support falls back to Save reporting fellBack=true.
func TestSaveWithFenceWithoutTokenOrSupport(t *testing.T) {
	repo := runstateinmem.NewRepository()
	snapshot := runstate.RunSnapshot{RunID: "run-plain", Status: runstate.RunStatusRunning}

	// No token: plain save, no fallback reported.
	fellBack, err := runstate.SaveWithFence(context.Background(), repo, &snapshot, 0)
	if err != nil || fellBack {
		t.Fatalf("tokenless save must be a plain save: fellBack=%v err=%v", fellBack, err)
	}

	// Token present but repository hides SaveFenced: fallback save.
	type unfencedRepo struct{ runstate.Repository }
	fellBack, err = runstate.SaveWithFence(runstate.ContextWithFenceToken(context.Background(), 7),
		unfencedRepo{repo}, &snapshot, 1)
	if err != nil || !fellBack {
		t.Fatalf("unsupported repo must fall back to Save: fellBack=%v err=%v", fellBack, err)
	}

	// Token present and repository fenced: fenced save, no fallback.
	fellBack, err = runstate.SaveWithFence(runstate.ContextWithFenceToken(context.Background(), 7),
		repo, &snapshot, 2)
	if err != nil || fellBack {
		t.Fatalf("fenced repo must save fenced: fellBack=%v err=%v", fellBack, err)
	}
}
