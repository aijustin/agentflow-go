package inmem

import (
	"context"
	"errors"
	"testing"
	"time"

	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
)

// A job that fails every attempt must not be re-leasable immediately, or a
// permanently failing payload becomes a hot loop that starves the worker.
func TestFailAppliesRetryBackoff(t *testing.T) {
	ctx := context.Background()
	queue := NewQueue()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return now }
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "poison", Type: "run", MaxAttempts: 5}); err != nil {
		t.Fatal(err)
	}

	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected a lease, ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(ctx, lease, errors.New("boom")); err != nil {
		t.Fatal(err)
	}

	loaded, err := queue.Load(ctx, "poison")
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.AvailableAt.After(now) {
		t.Fatalf("expected the retry to be deferred, available_at=%s now=%s", loaded.AvailableAt, now)
	}
	if _, ok, _ := queue.Lease(ctx, "worker-1", time.Minute); ok {
		t.Fatal("expected no lease while the backoff is pending")
	}

	now = now.Add(time.Second)
	if _, ok, err := queue.Lease(ctx, "worker-1", time.Minute); err != nil || !ok {
		t.Fatalf("expected a lease once the backoff elapsed, ok=%v err=%v", ok, err)
	}
}

// The backoff must match the PostgreSQL queue: 1s doubling per attempt, capped
// at a minute, so dev and production behave the same under a poison message.
func TestRetryBackoffGrowsAndCaps(t *testing.T) {
	cases := []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		{5, 16 * time.Second},
	}
	for _, tc := range cases {
		if got := retryBackoff(tc.attempt); got != tc.want {
			t.Fatalf("retryBackoff(%d)=%s want %s", tc.attempt, got, tc.want)
		}
	}
	if got := retryBackoff(10); got != time.Minute {
		t.Fatalf("expected the backoff to cap at 1m, got %s", got)
	}
}

// A job that exhausts its attempts goes to the dead-letter state instead of
// being deferred for another try.
func TestFailDeadLettersWithoutBackoff(t *testing.T) {
	ctx := context.Background()
	queue := NewQueue()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return now }
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: "run", MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected a lease, ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(ctx, lease, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	loaded, err := queue.Load(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobDeadLetter {
		t.Fatalf("expected dead letter, got %s", loaded.State)
	}
}
