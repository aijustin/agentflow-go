package async

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type testQueue struct {
	mu   sync.Mutex
	jobs map[string]Job
	// order preserves enqueue order so Lease picks the first-enqueued job
	// deterministically (map iteration order is random).
	order []string
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
	q.order = append(q.order, job.ID)
	return CloneJob(job), nil
}

func (q *testQueue) Lease(_ context.Context, workerID string, ttl time.Duration) (Lease, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for _, id := range q.order {
		job := q.jobs[id]
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

// TestWorkerDrainCompletesInFlightJobWithinTimeout: with a drain timeout
// configured, cancelling the worker context stops new leases but lets the
// in-flight job finish — with lease renewal still running — instead of
// cancelling and requeueing it.
func TestWorkerDrainCompletesInFlightJobWithinTimeout(t *testing.T) {
	queue := &renewCountQueue{Queue: newTestQueue()}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	release := make(chan struct{})
	handler := HandlerFunc(func(ctx context.Context, job Job) error {
		close(started)
		<-release
		return nil
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{
		WorkerID:      "worker-drain",
		PollInterval:  5 * time.Millisecond,
		LeaseTTL:      200 * time.Millisecond,
		RenewInterval: 20 * time.Millisecond,
	}, WithDrainTimeout(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-drain", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	workerErr := make(chan error, 1)
	go func() { workerErr <- worker.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start job")
	}
	// SIGTERM equivalent: the worker stops leasing but must not cancel the
	// in-flight job yet.
	cancel()
	renewsAtCancel := queue.renews.Load()
	// Renewal must keep working through the drain window, otherwise draining
	// would hand the lease to another worker.
	time.Sleep(100 * time.Millisecond)
	if queue.renews.Load() <= renewsAtCancel {
		t.Fatal("expected lease renewal to continue during drain")
	}
	close(release)
	select {
	case err := <-workerErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected worker to stop with context canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after the drained job completed")
	}
	loaded, err := queue.Load(context.Background(), "job-drain")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != JobCompleted {
		t.Fatalf("expected drained job to complete, got %q", loaded.State)
	}
}

// TestWorkerDrainCancelsAndRequeuesAfterTimeout: once the drain window
// elapses the in-flight job falls back to the cancel-and-requeue path, and
// no new jobs are leased after shutdown starts.
func TestWorkerDrainCancelsAndRequeuesAfterTimeout(t *testing.T) {
	queue := newTestQueue()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	cancelledAt := make(chan time.Time, 1)
	handler := HandlerFunc(func(jobCtx context.Context, job Job) error {
		close(started)
		<-jobCtx.Done()
		cancelledAt <- time.Now()
		return jobCtx.Err()
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{
		WorkerID:     "worker-drain-timeout",
		Concurrency:  2,
		PollInterval: 5 * time.Millisecond,
	}, WithDrainTimeout(300*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-drain-timeout", Type: RunJobType, MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	workerErr := make(chan error, 1)
	go func() { workerErr <- worker.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start job")
	}
	shutdownAt := time.Now()
	cancel()
	// After the shutdown signal no new jobs may be leased; give the lease
	// loops a moment to observe the cancellation, then enqueue a fresh job.
	time.Sleep(50 * time.Millisecond)
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-late", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	select {
	case at := <-cancelledAt:
		if elapsed := at.Sub(shutdownAt); elapsed < 200*time.Millisecond {
			t.Fatalf("job cancelled before the drain window elapsed: %v", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("job was not cancelled after the drain timeout")
	}
	select {
	case err := <-workerErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected worker to stop with context canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after the drain timeout")
	}
	loaded, err := queue.Load(context.Background(), "job-drain-timeout")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != JobQueued || !strings.Contains(loaded.LastError, "context canceled") {
		t.Fatalf("expected cancelled job to be requeued, got %+v", loaded)
	}
	late, err := queue.Load(context.Background(), "job-late")
	if err != nil {
		t.Fatal(err)
	}
	if late.State != JobQueued || late.Attempts != 0 {
		t.Fatalf("expected no new leases after shutdown, got %+v", late)
	}
}

// TestWorkerWithoutDrainTimeoutCancelsImmediately: the zero drain timeout
// keeps the previous behaviour — the in-flight job is cancelled as soon as
// the worker context is cancelled.
func TestWorkerWithoutDrainTimeoutCancelsImmediately(t *testing.T) {
	queue := newTestQueue()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{})
	cancelled := make(chan struct{})
	handler := HandlerFunc(func(jobCtx context.Context, job Job) error {
		close(started)
		<-jobCtx.Done()
		close(cancelled)
		return jobCtx.Err()
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{
		WorkerID:     "worker-no-drain",
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-no-drain", Type: RunJobType, MaxAttempts: 2}); err != nil {
		t.Fatal(err)
	}
	workerErr := make(chan error, 1)
	go func() { workerErr <- worker.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not start job")
	}
	cancel()
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("expected immediate job cancellation without a drain timeout")
	}
	select {
	case err := <-workerErr:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected worker to stop with context canceled, got %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after cancel")
	}
	loaded, err := queue.Load(context.Background(), "job-no-drain")
	if err != nil {
		t.Fatal(err)
	}
	if loaded.State != JobQueued {
		t.Fatalf("expected cancelled job to be requeued, got %q", loaded.State)
	}
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

// TestWorkerRecoversJobHandlerPanic: a panicking job handler must not kill
// the worker process. The panic becomes a job error through the normal Fail
// path, and the same worker keeps processing later jobs.
func TestWorkerRecoversJobHandlerPanic(t *testing.T) {
	queue := newTestQueue()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handler := HandlerFunc(func(ctx context.Context, job Job) error {
		if job.ID == "job-panic" {
			panic("handler exploded")
		}
		return nil
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{WorkerID: "worker-1", PollInterval: 10 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-panic", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	workerDone := make(chan error, 1)
	go func() { workerDone <- worker.Run(ctx) }()
	// The panicking job must be failed through the queue, not crash Run.
	deadline := time.Now().Add(2 * time.Second)
	var panicked Job
	for time.Now().Before(deadline) {
		var loadErr error
		panicked, loadErr = queue.Load(context.Background(), "job-panic")
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if panicked.State == JobDeadLetter {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if panicked.State != JobDeadLetter {
		t.Fatalf("expected panicking job to be dead-lettered, got %q", panicked.State)
	}
	if !strings.Contains(panicked.LastError, "panic recovered") {
		t.Fatalf("expected panic recovery error on job, got %q", panicked.LastError)
	}
	// The worker survived: a subsequent job is still processed.
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-after", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(2 * time.Second)
	var after Job
	for time.Now().Before(deadline) {
		var loadErr error
		after, loadErr = queue.Load(context.Background(), "job-after")
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if after.State == JobCompleted {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if after.State != JobCompleted {
		t.Fatalf("worker must keep processing after a handler panic, got %q", after.State)
	}
	cancel()
	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("worker did not stop after cancel")
	}
}

// flakyLoadQueue fails Load a fixed number of times with a transient error,
// mirroring flakyLeaseQueue: a Load failure after a successful Lease must
// fail the job through the queue instead of killing the worker.
type flakyLoadQueue struct {
	Queue
	failures atomic.Int32
}

func (q *flakyLoadQueue) Load(ctx context.Context, jobID string) (Job, error) {
	if q.failures.Load() > 0 {
		q.failures.Add(-1)
		return Job{}, errors.New("queue temporarily unavailable")
	}
	return q.Queue.Load(ctx, jobID)
}

func TestWorkerSurvivesTransientLoadErrors(t *testing.T) {
	queue := &flakyLoadQueue{Queue: newTestQueue()}
	queue.failures.Store(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handled := make(chan string, 2)
	handler := HandlerFunc(func(ctx context.Context, job Job) error {
		handled <- job.ID
		return nil
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{
		WorkerID:     "worker-flaky-load",
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-load-flaky", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-load-after", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	workerErr := make(chan error, 1)
	go func() { workerErr <- worker.Run(ctx) }()
	// The job whose Load failed goes through the normal Fail semantics
	// (dead-letter at MaxAttempts=1)...
	deadline := time.Now().Add(3 * time.Second)
	for {
		job, loadErr := queue.Queue.Load(context.Background(), "job-load-flaky")
		if loadErr != nil {
			t.Fatal(loadErr)
		}
		if job.State == JobDeadLetter {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected load-failed job to be dead-lettered, got %q", job.State)
		}
		select {
		case err := <-workerErr:
			t.Fatalf("worker exited on transient load error: %v", err)
		default:
		}
		time.Sleep(5 * time.Millisecond)
	}
	// ...and the worker keeps polling: the next job is still processed.
	select {
	case id := <-handled:
		if id != "job-load-after" {
			t.Fatalf("unexpected job handled: %q", id)
		}
	case err := <-workerErr:
		t.Fatalf("worker exited on transient load error: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("job was not processed after transient load error")
	}
	cancel()
}

// flakyFailReleaseQueue fails Load and Fail once each, forcing the worker
// onto the lease-release fallback; the released job must be re-leased and
// processed.
type flakyFailReleaseQueue struct {
	*testQueue
	loadFailures atomic.Int32
	failFailures atomic.Int32
	releases     atomic.Int32
}

func (q *flakyFailReleaseQueue) Load(ctx context.Context, jobID string) (Job, error) {
	if q.loadFailures.Load() > 0 {
		q.loadFailures.Add(-1)
		return Job{}, errors.New("queue temporarily unavailable")
	}
	return q.testQueue.Load(ctx, jobID)
}

func (q *flakyFailReleaseQueue) Fail(ctx context.Context, lease Lease, cause error) error {
	if q.failFailures.Load() > 0 {
		q.failFailures.Add(-1)
		return errors.New("queue temporarily unavailable")
	}
	return q.testQueue.Fail(ctx, lease, cause)
}

func (q *flakyFailReleaseQueue) Release(_ context.Context, lease Lease) error {
	q.releases.Add(1)
	q.mu.Lock()
	defer q.mu.Unlock()
	job, ok := q.jobs[lease.JobID]
	if !ok {
		return ErrJobNotFound
	}
	job.State = JobQueued
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	q.jobs[job.ID] = job
	return nil
}

func TestWorkerReleasesLeaseWhenLoadAndFailFail(t *testing.T) {
	queue := &flakyFailReleaseQueue{testQueue: newTestQueue()}
	queue.loadFailures.Store(1)
	queue.failFailures.Store(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	handled := make(chan string, 1)
	handler := HandlerFunc(func(ctx context.Context, job Job) error {
		handled <- job.ID
		return nil
	})
	worker, err := NewWorker(queue, handler, WorkerConfig{
		WorkerID:     "worker-flaky-fail",
		PollInterval: 5 * time.Millisecond,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := queue.Enqueue(context.Background(), Job{ID: "job-release", Type: RunJobType, MaxAttempts: 1}); err != nil {
		t.Fatal(err)
	}
	workerErr := make(chan error, 1)
	go func() { workerErr <- worker.Run(ctx) }()
	select {
	case id := <-handled:
		if id != "job-release" {
			t.Fatalf("unexpected job handled: %q", id)
		}
	case err := <-workerErr:
		t.Fatalf("worker exited on transient load/fail errors: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("job was not re-leased and processed after release fallback")
	}
	if got := queue.releases.Load(); got != 1 {
		t.Fatalf("expected exactly one lease release, got %d", got)
	}
	cancel()
}
