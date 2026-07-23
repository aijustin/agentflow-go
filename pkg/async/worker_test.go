package async

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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

func (q *testQueue) Pause(_ context.Context, lease Lease, result PauseResult) error {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.leasedJob(lease)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	job.State = JobPaused
	job.LastError = string(raw)
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

func (q *testQueue) Renew(_ context.Context, lease Lease, ttl time.Duration) (Lease, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	job, err := q.leasedJob(lease)
	if err != nil {
		return Lease{}, false, nil
	}
	job.LeaseExpiresAt = time.Now().UTC().Add(ttl)
	q.jobs[job.ID] = job
	return Lease{JobID: job.ID, WorkerID: lease.WorkerID, Attempt: job.Attempts, ExpiresAt: job.LeaseExpiresAt}, true, nil
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

func TestWorkerCompletesJob(t *testing.T) {
	queue := newTestQueue()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	handler := HandlerFunc(func(ctx context.Context, job Job) error {
		close(done)
		return nil
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{
		WorkerID:     "worker-1",
		PollInterval: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-ok", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = worker.Run(ctx) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("job was not processed")
	}
	deadline := time.Now().Add(2 * time.Second)
	var loaded Job
	for time.Now().Before(deadline) {
		var loadErr error
		loaded, loadErr = queue.Load(context.Background(), "job-ok")
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if loaded.State == JobCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if loaded.State != JobCompleted {
		t.Fatalf("expected completed job, got %q", loaded.State)
	}
	cancel()
}

func TestNewWorkerValidation(t *testing.T) {
	if _, err := NewWorker(nil, HandlerFunc(func(context.Context, Job) error { return nil }), WorkerConfig{WorkerID: "w1"}); err == nil {
		t.Fatal("expected nil queue error")
	}
	queue := newTestQueue()
	if _, err := NewWorker(queue, nil, WorkerConfig{WorkerID: "w1"}); err == nil {
		t.Fatal("expected nil handler error")
	}
	if _, err := NewWorker(queue, HandlerFunc(func(context.Context, Job) error { return nil }), WorkerConfig{}); err == nil {
		t.Fatal("expected missing worker id error")
	}
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
	_ = dead
	lease, ok, err := queue.Lease(ctx, "worker-test", time.Minute)
	if err != nil || !ok {
		t.Fatalf("lease job: ok=%v err=%v", ok, err)
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

func TestWorkerRenewsLeaseWhileHandlingJob(t *testing.T) {
	queue := &renewCountQueue{Queue: newTestQueue()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := HandlerFunc(func(context.Context, Job) error {
		time.Sleep(150 * time.Millisecond)
		return nil
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{
		WorkerID:      "worker-renew",
		PollInterval:  5 * time.Millisecond,
		LeaseTTL:      50 * time.Millisecond,
		RenewInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-renew", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = worker.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if queue.renews.Load() > 0 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected lease renewal during long-running job")
}

type renewCountQueue struct {
	Queue
	renews atomic.Int32
}

func (q *renewCountQueue) Renew(ctx context.Context, lease Lease, ttl time.Duration) (Lease, bool, error) {
	renewer, ok := q.Queue.(LeaseRenewer)
	if !ok {
		return Lease{}, false, nil
	}
	renewed, ok, err := renewer.Renew(ctx, lease, ttl)
	if ok {
		q.renews.Add(1)
	}
	return renewed, ok, err
}

func TestWorkerPausesJobOnRunPausedError(t *testing.T) {
	queue := newTestQueue()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := HandlerFunc(func(context.Context, Job) error {
		return RunPausedError{RunID: "run-pause", Token: "tok-1"}
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{WorkerID: "worker-pause", PollInterval: 5 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-pause", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = worker.Run(ctx) }()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := queue.Load(context.Background(), "job-pause")
		if err != nil {
			t.Fatal(err)
		}
		if loaded.State == JobPaused {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("expected paused job state")
}

// flakyLeaseQueue fails Lease a fixed number of times before delegating, to
// prove a transient queue outage no longer kills the worker loop.
type flakyLeaseQueue struct {
	Queue
	failures atomic.Int32
}

func (q *flakyLeaseQueue) Lease(ctx context.Context, workerID string, ttl time.Duration) (Lease, bool, error) {
	if q.failures.Load() > 0 {
		q.failures.Add(-1)
		return Lease{}, false, errors.New("queue temporarily unavailable")
	}
	return q.Queue.Lease(ctx, workerID, ttl)
}

func TestWorkerSurvivesTransientLeaseErrors(t *testing.T) {
	queue := &flakyLeaseQueue{Queue: newTestQueue()}
	queue.failures.Store(3)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	handler := HandlerFunc(func(ctx context.Context, job Job) error {
		close(done)
		return nil
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{
		WorkerID:     "worker-flaky",
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-flaky", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	workerErr := make(chan error, 1)
	go func() { workerErr <- worker.Run(ctx) }()
	select {
	case <-done:
	case err := <-workerErr:
		t.Fatalf("worker exited on transient lease errors: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("job was not processed after transient lease errors")
	}
	cancel()
}

// failRenewQueue reports every renewal as not-held, so the worker must cancel
// the in-flight job instead of letting it run unowned.
type failRenewQueue struct {
	Queue
}

func (q *failRenewQueue) Renew(ctx context.Context, lease Lease, ttl time.Duration) (Lease, bool, error) {
	return Lease{}, false, nil
}

func TestWorkerCancelsJobWhenRenewalFails(t *testing.T) {
	queue := &failRenewQueue{Queue: newTestQueue()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cancelled := make(chan struct{})
	handler := HandlerFunc(func(jobCtx context.Context, job Job) error {
		select {
		case <-jobCtx.Done():
			close(cancelled)
			return jobCtx.Err()
		case <-time.After(3 * time.Second):
			return nil
		}
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{
		WorkerID:      "worker-lost",
		PollInterval:  5 * time.Millisecond,
		LeaseTTL:      time.Minute,
		RenewInterval: 20 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-lost", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = worker.Run(ctx) }()
	select {
	case <-cancelled:
	case <-time.After(3 * time.Second):
		t.Fatal("job context was not cancelled after lease renewal failure")
	}
	cancel()
}
