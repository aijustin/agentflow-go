package async

import (
	"context"
	"encoding/json"
	"errors"
	"time"
)

var (
	ErrJobNotFound = errors.New("async: job not found")
	ErrStaleLease  = errors.New("async: stale job lease")
	ErrInvalidJob  = errors.New("async: invalid job")
)

type JobState string

const (
	JobQueued     JobState = "queued"
	JobRunning    JobState = "running"
	JobCompleted  JobState = "completed"
	JobPaused     JobState = "paused"
	JobFailed     JobState = "failed"
	JobCancelled  JobState = "cancelled"
	JobDeadLetter JobState = "dead_letter"
)

type Job struct {
	ID             string          `json:"id"`
	Type           string          `json:"type"`
	RunID          string          `json:"run_id,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	State          JobState        `json:"state"`
	Attempts       int             `json:"attempts"`
	MaxAttempts    int             `json:"max_attempts"`
	LastError      string          `json:"last_error,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
	AvailableAt    time.Time       `json:"available_at"`
	LeaseWorkerID  string          `json:"lease_worker_id,omitempty"`
	LeaseExpiresAt time.Time       `json:"lease_expires_at,omitempty"`
}

func (job Job) Validate() error {
	if job.ID == "" || job.Type == "" {
		return ErrInvalidJob
	}
	if job.MaxAttempts < 0 {
		return ErrInvalidJob
	}
	return nil
}

type Lease struct {
	JobID     string
	WorkerID  string
	Attempt   int
	ExpiresAt time.Time
}

func (lease Lease) Validate() error {
	if lease.JobID == "" || lease.WorkerID == "" || lease.Attempt <= 0 {
		return ErrStaleLease
	}
	return nil
}

type Queue interface {
	Enqueue(ctx context.Context, job Job) (Job, error)
	Lease(ctx context.Context, workerID string, ttl time.Duration) (Lease, bool, error)
	Load(ctx context.Context, jobID string) (Job, error)
	Complete(ctx context.Context, lease Lease) error
	Pause(ctx context.Context, lease Lease, result PauseResult) error
	Fail(ctx context.Context, lease Lease, cause error) error
	Cancel(ctx context.Context, jobID string) error
}

type LeaseRenewer interface {
	Renew(ctx context.Context, lease Lease, ttl time.Duration) (Lease, bool, error)
}

func CloneJob(job Job) Job {
	if job.Payload != nil {
		payload := make([]byte, len(job.Payload))
		copy(payload, job.Payload)
		job.Payload = payload
	}
	return job
}
