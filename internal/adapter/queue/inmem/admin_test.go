package inmem

import (
	"context"
	"errors"
	"testing"
	"time"

	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
)

func TestQueueListJobsAndRequeue(t *testing.T) {
	queue := NewQueue()
	now := time.Now().UTC()
	_, err := queue.Enqueue(context.Background(), asyncpkg.Job{
		ID: "job-1", Type: asyncpkg.RunJobType, MaxAttempts: 1, CreatedAt: now, UpdatedAt: now, AvailableAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(context.Background(), "worker", time.Minute)
	if err != nil || !ok {
		t.Fatalf("lease: ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(context.Background(), lease, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	jobs, err := queue.ListJobs(context.Background(), asyncpkg.JobFilter{State: asyncpkg.JobDeadLetter})
	if err != nil || len(jobs) != 1 {
		t.Fatalf("list dead letter: %+v err=%v", jobs, err)
	}
	if err := queue.Requeue(context.Background(), "job-1"); err != nil {
		t.Fatal(err)
	}
	loaded, err := queue.Load(context.Background(), "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != asyncpkg.JobQueued {
		t.Fatalf("expected queued state, got %s", loaded.State)
	}
}

func TestQueueAdminOperationsAreTenantScoped(t *testing.T) {
	queue := NewQueue()
	tenantA := tenantQueueContext("tenant-a")
	tenantB := tenantQueueContext("tenant-b")
	for _, item := range []struct {
		ctx context.Context
		id  string
	}{
		{tenantA, "job-a"},
		{tenantB, "job-b"},
	} {
		if _, err := queue.Enqueue(item.ctx, asyncpkg.Job{ID: item.id, Type: asyncpkg.RunJobType, MaxAttempts: 1}); err != nil {
			t.Fatal(err)
		}
		lease, ok, err := queue.Lease(context.Background(), "worker", time.Minute)
		if err != nil || !ok {
			t.Fatalf("lease %s: ok=%v err=%v", item.id, ok, err)
		}
		if err := queue.Fail(context.Background(), lease, errors.New("boom")); err != nil {
			t.Fatal(err)
		}
	}

	jobs, err := queue.ListJobs(tenantA, asyncpkg.JobFilter{State: asyncpkg.JobDeadLetter})
	if err != nil {
		t.Fatal(err)
	}
	if len(jobs) != 1 || jobs[0].ID != "job-a" {
		t.Fatalf("expected only tenant-a job, got %+v", jobs)
	}
	if err := queue.Requeue(tenantA, "job-b"); !errors.Is(err, asyncpkg.ErrTenantMismatch) {
		t.Fatalf("expected cross-tenant requeue rejection, got %v", err)
	}
}
