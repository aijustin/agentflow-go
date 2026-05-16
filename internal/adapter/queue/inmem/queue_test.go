package inmem

import (
	"context"
	"errors"
	"testing"
	"time"

	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
)

func TestQueueLeasesAndCompletesJobs(t *testing.T) {
	ctx := context.Background()
	queue := NewQueue()
	job, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: "run"})
	if err != nil {
		t.Fatal(err)
	}
	if job.State != asyncpkg.JobQueued || job.MaxAttempts != 1 {
		t.Fatalf("unexpected enqueued job: %+v", job)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if !ok || lease.JobID != "job-1" || lease.Attempt != 1 {
		t.Fatalf("unexpected lease ok=%v lease=%+v", ok, lease)
	}
	if _, ok, err := queue.Lease(ctx, "worker-2", time.Minute); err != nil || ok {
		t.Fatalf("expected no second lease, ok=%v err=%v", ok, err)
	}
	if err := queue.Complete(ctx, lease); err != nil {
		t.Fatal(err)
	}
	loaded, err := queue.Load(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobCompleted || loaded.LeaseWorkerID != "" {
		t.Fatalf("unexpected completed job: %+v", loaded)
	}
}

func TestQueueRetriesUntilDeadLetter(t *testing.T) {
	ctx := context.Background()
	queue := NewQueue()
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: "run", MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected first lease, ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(ctx, lease, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	loaded, err := queue.Load(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobQueued || loaded.LastError != "boom" {
		t.Fatalf("expected queued retry, got %+v", loaded)
	}
	lease, ok, err = queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected second lease, ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(ctx, lease, errors.New("boom again")); err != nil {
		t.Fatal(err)
	}
	loaded, err = queue.Load(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobDeadLetter || loaded.LastError != "boom again" {
		t.Fatalf("expected dead letter, got %+v", loaded)
	}
}

func TestQueueRecoversExpiredLeases(t *testing.T) {
	ctx := context.Background()
	queue := NewQueue()
	now := time.Date(2026, 5, 16, 12, 0, 0, 0, time.UTC)
	queue.now = func() time.Time { return now }
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: "run", MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker-1", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected lease, ok=%v err=%v", ok, err)
	}
	now = now.Add(2 * time.Minute)
	recovered, ok, err := queue.Lease(ctx, "worker-2", time.Minute)
	if err != nil || !ok {
		t.Fatalf("expected recovered lease, ok=%v err=%v", ok, err)
	}
	if recovered.Attempt != lease.Attempt+1 || recovered.WorkerID != "worker-2" {
		t.Fatalf("unexpected recovered lease: %+v", recovered)
	}
	if err := queue.Complete(ctx, lease); !errors.Is(err, asyncpkg.ErrStaleLease) {
		t.Fatalf("expected stale original lease, got %v", err)
	}
}

func TestQueueCancelsJobs(t *testing.T) {
	ctx := context.Background()
	queue := NewQueue()
	if _, err := queue.Enqueue(ctx, asyncpkg.Job{ID: "job-1", Type: "run"}); err != nil {
		t.Fatal(err)
	}
	if err := queue.Cancel(ctx, "job-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := queue.Load(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobCancelled {
		t.Fatalf("expected cancelled, got %+v", loaded)
	}
}
