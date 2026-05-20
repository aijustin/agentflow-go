package async

import "context"

type JobFilter struct {
	State JobState
	Limit int
}

// JobAdmin extends Queue with dead-letter inspection and requeue support.
type JobAdmin interface {
	ListJobs(ctx context.Context, filter JobFilter) ([]Job, error)
	Requeue(ctx context.Context, jobID string) error
}
