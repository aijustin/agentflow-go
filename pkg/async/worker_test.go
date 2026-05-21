package async

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"
)

type testQueue struct {
	mu   sync.Mutex
	jobs map[string]Job
}

func newTestQueue() *testQueue {
	return &testQueue{jobs: make(map[string]Job)}
}

func (q *testQueue) Enqueue(_ context.Context, job Job) (Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if _, exists := q.jobs[job.ID]; exists {
		return Job{}, fmt.Errorf("job exists")
	}
	if job.State == "" {
		job.State = JobQueued
	}
	q.jobs[job.ID] = CloneJob(job)
	return CloneJob(job), nil
}

func (q *testQueue) Lease(_ context.Context, workerID string, ttl time.Duration) (Lease, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for id, job := range q.jobs {
		if job.State != JobQueued {
			continue
		}
		job.State = JobRunning
		job.Attempts++
		job.LeaseWorkerID = workerID
		job.LeaseExpiresAt = time.Now().UTC().Add(ttl)
		q.jobs[id] = job
		return Lease{JobID: id, WorkerID: workerID, Attempt: job.Attempts, ExpiresAt: job.LeaseExpiresAt}, true, nil
	}
	return Lease{}, false, nil
}

func (q *testQueue) Load(_ context.Context, jobID string) (Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.jobs[jobID]
	if !ok {
		return Job{}, ErrJobNotFound
	}
	return CloneJob(job), nil
}

func (q *testQueue) Complete(_ context.Context, lease Lease) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.leasedJob(lease)
	if err != nil {
		return err
	}
	job.State = JobCompleted
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	q.jobs[job.ID] = job
	return nil
}

func (q *testQueue) Fail(_ context.Context, lease Lease, cause error) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.leasedJob(lease)
	if err != nil {
		return err
	}
	if cause != nil {
		job.LastError = cause.Error()
	}
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	if job.Attempts >= job.MaxAttempts {
		job.State = JobDeadLetter
	} else {
		job.State = JobQueued
	}
	q.jobs[job.ID] = job
	return nil
}

func (q *testQueue) Cancel(_ context.Context, jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.jobs[jobID]
	if !ok {
		return ErrJobNotFound
	}
	job.State = JobCancelled
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	q.jobs[jobID] = job
	return nil
}

func (q *testQueue) ListJobs(_ context.Context, filter JobFilter) ([]Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := make([]Job, 0, len(q.jobs))
	for _, job := range q.jobs {
		if filter.State != "" && job.State != filter.State {
			continue
		}
		out = append(out, CloneJob(job))
	}
	return out, nil
}

func (q *testQueue) Requeue(_ context.Context, jobID string) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.jobs[jobID]
	if !ok {
		return ErrJobNotFound
	}
	if job.State != JobDeadLetter {
		return ErrInvalidJob
	}
	job.State = JobQueued
	job.Attempts = 0
	q.jobs[jobID] = job
	return nil
}

func (q *testQueue) leasedJob(lease Lease) (Job, error) {
	job, ok := q.jobs[lease.JobID]
	if !ok {
		return Job{}, ErrJobNotFound
	}
	if job.State != JobRunning || job.LeaseWorkerID != lease.WorkerID || job.Attempts != lease.Attempt {
		return Job{}, ErrStaleLease
	}
	return job, nil
}

func TestWorkerPropagatesQueueCancel(t *testing.T) {
	queue := newTestQueue()
	ctx, stopWorker := context.WithCancel(context.Background())
	defer stopWorker()
	started := make(chan struct{})
	handlerDone := make(chan error, 1)
	handler := HandlerFunc(func(ctx context.Context, job Job) error {
		close(started)
		err := waitForContext(ctx)
		handlerDone <- err
		return err
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{WorkerID: "worker-1", PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	job, err := queue.Enqueue(context.Background(), Job{ID: "job-1", Type: RunJobType, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = worker.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("worker did not start job")
	}
	if err := queue.Cancel(context.Background(), job.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-handlerDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handler did not stop after cancel")
	}
	loaded, err := queue.Load(context.Background(), job.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != JobCancelled {
		t.Fatalf("expected cancelled job, got %q", loaded.State)
	}
	stopWorker()
}

func waitForContext(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestCollectQueueMetrics(t *testing.T) {
	queue := newTestQueue()
	ctx := context.Background()
	if _, err := queue.Enqueue(ctx, Job{ID: "q1", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	dead, err := queue.Enqueue(ctx, Job{ID: "dl1", Type: RunJobType, MaxAttempts: 1})
	if err != nil {
		t.Fatal(err)
	}
	lease, ok, err := queue.Lease(ctx, "worker-test", time.Minute)
	if err != nil || !ok || lease.JobID != dead.ID {
		t.Fatalf("lease dead job: ok=%v err=%v", ok, err)
	}
	if err := queue.Fail(ctx, lease, errors.New("boom")); err != nil {
		t.Fatal(err)
	}
	metrics, err := CollectQueueMetrics(ctx, queue)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.Queued != 1 || metrics.DeadLetter != 1 {
		t.Fatalf("unexpected metrics: %+v", metrics)
	}
}
