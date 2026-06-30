package async

import (
	"context"
	"errors"
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
	WorkerID      string
	Concurrency   int
	LeaseTTL      time.Duration
	RenewInterval time.Duration
	JobTimeout    time.Duration
	PollInterval  time.Duration
}

type Worker struct {
	queue         Queue
	handler       Handler
	workerID      string
	concurrency   int
	leaseTTL      time.Duration
	renewInterval time.Duration
	jobTimeout    time.Duration
	pollInterval  time.Duration
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
	renewInterval := config.RenewInterval
	if renewInterval <= 0 {
		renewInterval = leaseTTL / 2
	}
	if renewInterval <= 0 || renewInterval >= leaseTTL {
		renewInterval = leaseTTL / 2
	}
	pollInterval := config.PollInterval
	if pollInterval <= 0 {
		pollInterval = 100 * time.Millisecond
	}
	return &Worker{
		queue:         queue,
		handler:       handler,
		workerID:      config.WorkerID,
		concurrency:   concurrency,
		leaseTTL:      leaseTTL,
		renewInterval: renewInterval,
		jobTimeout:    config.JobTimeout,
		pollInterval:  pollInterval,
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
	renewCtx, stopRenew := context.WithCancel(ctx)
	defer stopRenew()
	worker.startLeaseRenewal(renewCtx, lease)
	jobCtx, jobCancel := context.WithCancel(ctx)
	defer jobCancel()
	if worker.jobTimeout > 0 {
		var timeoutCancel context.CancelFunc
		jobCtx, timeoutCancel = context.WithTimeout(jobCtx, worker.jobTimeout)
		defer timeoutCancel()
	}
	stopWatch := worker.watchJobCancellation(ctx, lease.JobID, jobCancel)
	defer stopWatch()
	err = worker.handler.HandleJob(jobCtx, job)
	if err != nil {
		if errors.Is(err, context.Canceled) {
			if current, loadErr := worker.queue.Load(ctx, lease.JobID); loadErr == nil && current.State == JobCancelled {
				return nil
			}
		}
		var paused RunPausedError
		if errors.As(err, &paused) {
			pauseCtx, cancel := terminalContext(ctx)
			defer cancel()
			return worker.queue.Pause(pauseCtx, lease, PauseResult{RunID: paused.RunID, Token: paused.Token})
		}
		// If the worker context itself is gone (shutdown), release the lease on
		// a detached context so the job does not stay leased until expiry; a
		// cancelled context would otherwise make Fail a no-op for many queues.
		failCtx, cancel := terminalContext(ctx)
		defer cancel()
		return worker.queue.Fail(failCtx, lease, err)
	}
	completeCtx, cancel := terminalContext(ctx)
	defer cancel()
	return worker.queue.Complete(completeCtx, lease)
}

// terminalContext returns ctx unchanged while it is still live, or a short-lived
// detached context when ctx is already cancelled, so terminal queue updates
// (Complete/Fail) can still be persisted during shutdown.
func terminalContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx.Err() == nil {
		return ctx, func() {}
	}
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

func (worker *Worker) watchJobCancellation(ctx context.Context, jobID string, cancel context.CancelFunc) func() {
	done := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	go func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				job, err := worker.queue.Load(ctx, jobID)
				if err != nil {
					continue
				}
				if job.State == JobCancelled {
					cancel()
					return
				}
			}
		}
	}()
	return stop
}

func (worker *Worker) startLeaseRenewal(ctx context.Context, lease Lease) {
	renewer, ok := worker.queue.(LeaseRenewer)
	if !ok {
		return
	}
	go func() {
		ticker := time.NewTicker(worker.renewInterval)
		defer ticker.Stop()
		current := lease
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				renewed, ok, err := renewer.Renew(ctx, current, worker.leaseTTL)
				if err != nil || !ok {
					return
				}
				current = renewed
			}
		}
	}()
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
