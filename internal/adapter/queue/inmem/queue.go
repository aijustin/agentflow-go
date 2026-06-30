package inmem

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	asyncpkg "github.com/aijustin/agentflow-go/pkg/async"
)

type Queue struct {
	mu    sync.Mutex
	jobs  map[string]asyncpkg.Job
	order []string
	now   func() time.Time
}

func NewQueue() *Queue {
	return &Queue{
		jobs: make(map[string]asyncpkg.Job),
		now:  func() time.Time { return time.Now().UTC() },
	}
}

func (queue *Queue) Enqueue(ctx context.Context, job asyncpkg.Job) (asyncpkg.Job, error) {
	if err := ctx.Err(); err != nil {
		return asyncpkg.Job{}, err
	}
	if err := job.Validate(); err != nil {
		return asyncpkg.Job{}, err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	if _, exists := queue.jobs[job.ID]; exists {
		return asyncpkg.Job{}, fmt.Errorf("async inmem: job %q already exists", job.ID)
	}
	now := queue.now()
	if job.State == "" {
		job.State = asyncpkg.JobQueued
	}
	if job.State != asyncpkg.JobQueued {
		return asyncpkg.Job{}, asyncpkg.ErrInvalidJob
	}
	if job.MaxAttempts == 0 {
		job.MaxAttempts = 1
	}
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	if job.UpdatedAt.IsZero() {
		job.UpdatedAt = now
	}
	if job.AvailableAt.IsZero() {
		job.AvailableAt = now
	}
	job = asyncpkg.CloneJob(job)
	queue.jobs[job.ID] = job
	queue.order = append(queue.order, job.ID)
	return asyncpkg.CloneJob(job), nil
}

func (queue *Queue) Lease(ctx context.Context, workerID string, ttl time.Duration) (asyncpkg.Lease, bool, error) {
	if err := ctx.Err(); err != nil {
		return asyncpkg.Lease{}, false, err
	}
	if workerID == "" || ttl <= 0 {
		return asyncpkg.Lease{}, false, asyncpkg.ErrStaleLease
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	now := queue.now()
	for _, jobID := range queue.order {
		job := queue.jobs[jobID]
		if !leaseable(job, now) {
			continue
		}
		job.State = asyncpkg.JobRunning
		job.Attempts++
		job.LeaseWorkerID = workerID
		job.LeaseExpiresAt = now.Add(ttl)
		job.UpdatedAt = now
		queue.jobs[job.ID] = job
		return asyncpkg.Lease{JobID: job.ID, WorkerID: workerID, Attempt: job.Attempts, ExpiresAt: job.LeaseExpiresAt}, true, nil
	}
	return asyncpkg.Lease{}, false, nil
}

func (queue *Queue) Load(ctx context.Context, jobID string) (asyncpkg.Job, error) {
	if err := ctx.Err(); err != nil {
		return asyncpkg.Job{}, err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	job, exists := queue.jobs[jobID]
	if !exists {
		return asyncpkg.Job{}, asyncpkg.ErrJobNotFound
	}
	return asyncpkg.CloneJob(job), nil
}

func (queue *Queue) Renew(ctx context.Context, lease asyncpkg.Lease, ttl time.Duration) (asyncpkg.Lease, bool, error) {
	if err := ctx.Err(); err != nil {
		return asyncpkg.Lease{}, false, err
	}
	if err := lease.Validate(); err != nil {
		return asyncpkg.Lease{}, false, err
	}
	if ttl <= 0 {
		return asyncpkg.Lease{}, false, asyncpkg.ErrStaleLease
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	job, err := queue.leasedJob(lease)
	if err != nil {
		return asyncpkg.Lease{}, false, err
	}
	now := queue.now()
	job.LeaseExpiresAt = now.Add(ttl)
	job.UpdatedAt = now
	queue.jobs[job.ID] = job
	lease.ExpiresAt = job.LeaseExpiresAt
	return lease, true, nil
}

func (queue *Queue) Complete(ctx context.Context, lease asyncpkg.Lease) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := lease.Validate(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	job, err := queue.leasedJob(lease)
	if err != nil {
		return err
	}
	job.State = asyncpkg.JobCompleted
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = queue.now()
	queue.jobs[job.ID] = job
	return nil
}

func (queue *Queue) Pause(ctx context.Context, lease asyncpkg.Lease, result asyncpkg.PauseResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := lease.Validate(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	job, err := queue.leasedJob(lease)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	job.State = asyncpkg.JobPaused
	job.LastError = string(raw)
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = queue.now()
	queue.jobs[job.ID] = job
	return nil
}

func (queue *Queue) Fail(ctx context.Context, lease asyncpkg.Lease, cause error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := lease.Validate(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	job, err := queue.leasedJob(lease)
	if err != nil {
		return err
	}
	if cause != nil {
		job.LastError = cause.Error()
	}
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = queue.now()
	if job.Attempts >= job.MaxAttempts {
		job.State = asyncpkg.JobDeadLetter
	} else {
		job.State = asyncpkg.JobQueued
		job.AvailableAt = job.UpdatedAt
	}
	queue.jobs[job.ID] = job
	return nil
}

func (queue *Queue) Cancel(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	job, exists := queue.jobs[jobID]
	if !exists {
		return asyncpkg.ErrJobNotFound
	}
	if job.State == asyncpkg.JobCompleted || job.State == asyncpkg.JobDeadLetter || job.State == asyncpkg.JobPaused {
		return nil
	}
	job.State = asyncpkg.JobCancelled
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	job.UpdatedAt = queue.now()
	queue.jobs[job.ID] = job
	return nil
}

func (queue *Queue) leasedJob(lease asyncpkg.Lease) (asyncpkg.Job, error) {
	job, exists := queue.jobs[lease.JobID]
	if !exists {
		return asyncpkg.Job{}, asyncpkg.ErrJobNotFound
	}
	if job.State != asyncpkg.JobRunning || job.LeaseWorkerID != lease.WorkerID || job.Attempts != lease.Attempt {
		return asyncpkg.Job{}, asyncpkg.ErrStaleLease
	}
	return job, nil
}

func leaseable(job asyncpkg.Job, now time.Time) bool {
	if job.State == asyncpkg.JobQueued {
		return !job.AvailableAt.After(now)
	}
	if job.State == asyncpkg.JobRunning && job.LeaseExpiresAt.Before(now) {
		return true
	}
	return false
}

func (queue *Queue) ListJobs(ctx context.Context, filter asyncpkg.JobFilter) ([]asyncpkg.Job, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	out := make([]asyncpkg.Job, 0, len(queue.order))
	for _, jobID := range queue.order {
		job := queue.jobs[jobID]
		if filter.State != "" && job.State != filter.State {
			continue
		}
		out = append(out, asyncpkg.CloneJob(job))
		if filter.Limit > 0 && len(out) >= filter.Limit {
			break
		}
	}
	return out, nil
}

func (queue *Queue) Requeue(ctx context.Context, jobID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	queue.mu.Lock()
	defer queue.mu.Unlock()
	job, exists := queue.jobs[jobID]
	if !exists {
		return asyncpkg.ErrJobNotFound
	}
	if job.State != asyncpkg.JobDeadLetter {
		return asyncpkg.ErrInvalidJob
	}
	now := queue.now()
	job.State = asyncpkg.JobQueued
	job.Attempts = 0
	job.LastError = ""
	job.AvailableAt = now
	job.UpdatedAt = now
	job.LeaseWorkerID = ""
	job.LeaseExpiresAt = time.Time{}
	queue.jobs[jobID] = job
	return nil
}
