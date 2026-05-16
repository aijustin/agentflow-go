package async

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestWorkerProcessesJobs(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := newTestQueue()
	if _, err := queue.Enqueue(ctx, Job{ID: "job-1", Type: "run"}); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	worker, err := NewWorker(queue, HandlerFunc(func(ctx context.Context, job Job) error {
		if job.ID != "job-1" {
			t.Fatalf("unexpected job: %+v", job)
		}
		close(done)
		return nil
	}), WorkerConfig{WorkerID: "worker-1", PollInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- worker.Run(ctx) }()
	select {
	case <-done:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("worker did not process job")
	}
	if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected worker error: %v", err)
	}
	loaded, err := queue.Load(context.Background(), "job-1")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != JobCompleted {
		t.Fatalf("expected completed, got %+v", loaded)
	}
}

func TestWorkerRenewsLeaseWhileJobRuns(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queue := newRenewingTestQueue()
	if _, err := queue.Enqueue(ctx, Job{ID: "job-1", Type: "run"}); err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{})
	allowFinish := make(chan struct{})
	worker, err := NewWorker(queue, HandlerFunc(func(ctx context.Context, job Job) error {
		close(started)
		<-allowFinish
		return nil
	}), WorkerConfig{
		WorkerID:      "worker-1",
		LeaseTTL:      20 * time.Millisecond,
		RenewInterval: 5 * time.Millisecond,
		PollInterval:  time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	errCh := make(chan error, 1)
	go func() { errCh <- worker.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start job")
	}
	deadline := time.After(time.Second)
	for queue.renewCount.Load() == 0 {
		select {
		case <-deadline:
			t.Fatal("worker did not renew lease")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	close(allowFinish)
	cancel()
	if err := <-errCh; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("unexpected worker error: %v", err)
	}
}

func TestNewWorkerValidatesInputs(t *testing.T) {
	queue := newTestQueue()
	if _, err := NewWorker(nil, HandlerFunc(func(context.Context, Job) error { return nil }), WorkerConfig{WorkerID: "worker"}); err == nil {
		t.Fatal("expected nil queue error")
	}
	if _, err := NewWorker(queue, nil, WorkerConfig{WorkerID: "worker"}); err == nil {
		t.Fatal("expected nil handler error")
	}
	if _, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) error { return nil }), WorkerConfig{}); err == nil {
		t.Fatal("expected missing worker id error")
	}
}

type testQueue struct {
	jobs map[string]Job
}

type renewingTestQueue struct {
	*testQueue
	renewCount atomic.Int64
}

func newTestQueue() *testQueue {
	return &testQueue{jobs: make(map[string]Job)}
}

func newRenewingTestQueue() *renewingTestQueue {
	return &renewingTestQueue{testQueue: newTestQueue()}
}

func (queue *renewingTestQueue) Renew(_ context.Context, lease Lease, ttl time.Duration) (Lease, bool, error) {
	if err := lease.Validate(); err != nil {
		return Lease{}, false, err
	}
	job := queue.jobs[lease.JobID]
	if job.State != JobRunning || job.LeaseWorkerID != lease.WorkerID || job.Attempts != lease.Attempt {
		return Lease{}, false, ErrStaleLease
	}
	job.LeaseExpiresAt = time.Now().Add(ttl)
	queue.jobs[job.ID] = job
	queue.renewCount.Add(1)
	lease.ExpiresAt = job.LeaseExpiresAt
	return lease, true, nil
}

func (queue *testQueue) Enqueue(_ context.Context, job Job) (Job, error) {
	if job.MaxAttempts == 0 {
		job.MaxAttempts = 1
	}
	job.State = JobQueued
	queue.jobs[job.ID] = CloneJob(job)
	return CloneJob(job), nil
}

func (queue *testQueue) Lease(_ context.Context, workerID string, ttl time.Duration) (Lease, bool, error) {
	for _, job := range queue.jobs {
		if job.State != JobQueued {
			continue
		}
		job.State = JobRunning
		job.Attempts++
		job.LeaseWorkerID = workerID
		job.LeaseExpiresAt = time.Now().Add(ttl)
		queue.jobs[job.ID] = job
		return Lease{JobID: job.ID, WorkerID: workerID, Attempt: job.Attempts, ExpiresAt: job.LeaseExpiresAt}, true, nil
	}
	return Lease{}, false, nil
}

func (queue *testQueue) Load(_ context.Context, jobID string) (Job, error) {
	job, exists := queue.jobs[jobID]
	if !exists {
		return Job{}, ErrJobNotFound
	}
	return CloneJob(job), nil
}

func (queue *testQueue) Complete(_ context.Context, lease Lease) error {
	job := queue.jobs[lease.JobID]
	job.State = JobCompleted
	queue.jobs[job.ID] = job
	return nil
}

func (queue *testQueue) Fail(_ context.Context, lease Lease, cause error) error {
	job := queue.jobs[lease.JobID]
	job.State = JobDeadLetter
	if cause != nil {
		job.LastError = cause.Error()
	}
	queue.jobs[job.ID] = job
	return nil
}

func (queue *testQueue) Cancel(_ context.Context, jobID string) error {
	job := queue.jobs[jobID]
	job.State = JobCancelled
	queue.jobs[job.ID] = job
	return nil
}
