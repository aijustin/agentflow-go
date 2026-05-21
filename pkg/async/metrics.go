package async

import "context"

// QueueMetrics summarizes queue depth by state.
type QueueMetrics struct {
	Queued     int
	Running    int
	DeadLetter int
}

// CollectQueueMetrics counts jobs by state when the queue implements JobAdmin.
func CollectQueueMetrics(ctx context.Context, queue Queue) (QueueMetrics, error) {
	admin, ok := queue.(JobAdmin)
	if !ok {
		return QueueMetrics{}, nil
	}
	jobs, err := admin.ListJobs(ctx, JobFilter{})
	if err != nil {
		return QueueMetrics{}, err
	}
	var metrics QueueMetrics
	for _, job := range jobs {
		switch job.State {
		case JobQueued:
			metrics.Queued++
		case JobRunning:
			metrics.Running++
		case JobDeadLetter:
			metrics.DeadLetter++
		}
	}
	return metrics, nil
}
