package async

import (
	"context"
	"fmt"
	"sync"
	"time"
)

type Handler interface {
	HandleJob(ctx context.Context, job Job) error
}

type HandlerFunc func(ctx context.Context, job Job) error

func (fn HandlerFunc) HandleJob(ctx context.Context, job Job) error {
	return fn(ctx, job)
}

type WorkerConfig struct {
	WorkerID     string
	Concurrency  int
	LeaseTTL     time.Duration
	JobTimeout   time.Duration
	PollInterval time.Duration
}

type Worker struct {
	queue        Queue
	handler      Handler
	workerID     string
	concurrency  int
	leaseTTL     time.Duration
	jobTimeout   time.Duration
	pollInterval time.Duration
}

func NewWorker(queue Queue, handler Handler, config WorkerConfig) (*Worker, error) {
	if queue == nil {
		return nil, fmt.Errorf("async worker: queue is nil")
	}
	if handler == nil {
		return nil, fmt.Errorf("async worker: handler is nil")
	}
	if config.WorkerID == "" {
		return nil, fmt.Errorf("async worker: worker id is required")
	}
	concurrency := config.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}
	leaseTTL := config.LeaseTTL
	if leaseTTL <= 0 {
		leaseTTL = time.Minute
	}
	pollInterval := config.PollInterval
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
	return &Worker{
		queue:        queue,
		handler:      handler,
		workerID:     config.WorkerID,
		concurrency:  concurrency,
		leaseTTL:     leaseTTL,
		jobTimeout:   config.JobTimeout,
		pollInterval: pollInterval,
	}, nil
}

func (worker *Worker) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for range worker.concurrency {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := worker.loop(ctx); err != nil {
				select {
				case errCh <- err:
					cancel()
				default:
				}
			}
		}()
	}
	waitCh := make(chan struct{})
	go func() {
		wg.Wait()
		close(waitCh)
	}()
	select {
	case err := <-errCh:
		<-waitCh
		return err
	case <-waitCh:
		return ctx.Err()
	}
}

func (worker *Worker) loop(ctx context.Context) error {
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		lease, ok, err := worker.queue.Lease(ctx, worker.workerID, worker.leaseTTL)
		if err != nil {
			return err
		}
		if !ok {
			if err := wait(ctx, worker.pollInterval); err != nil {
				return nil
			}
			continue
		}
		if err := worker.handleLeasedJob(ctx, lease); err != nil {
			return err
		}
	}
}

func (worker *Worker) handleLeasedJob(ctx context.Context, lease Lease) error {
	job, err := worker.queue.Load(ctx, lease.JobID)
	if err != nil {
		return err
	}
	jobCtx := ctx
	cancel := func() {}
	if worker.jobTimeout > 0 {
		jobCtx, cancel = context.WithTimeout(ctx, worker.jobTimeout)
	}
	defer cancel()
	if err := worker.handler.HandleJob(jobCtx, job); err != nil {
		return worker.queue.Fail(ctx, lease, err)
	}
	return worker.queue.Complete(ctx, lease)
}

func wait(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
