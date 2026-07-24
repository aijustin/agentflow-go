package async

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/aijustin/agentflow-go/internal/safecall"
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

type WorkerOption func(*Worker)

// WithDrainTimeout enables graceful shutdown draining: when the worker
// context is cancelled the worker stops leasing new jobs, then waits up to d
// for in-flight jobs to finish before falling back to the cancel-and-requeue
// path. Lease renewal keeps running during the drain window so drained jobs
// do not lose their leases. The zero value (the default) preserves the
// previous behaviour of cancelling in-flight jobs immediately.
func WithDrainTimeout(d time.Duration) WorkerOption {
	return func(worker *Worker) {
		if d > 0 {
			worker.drainTimeout = d
		}
	}
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
	drainTimeout  time.Duration
}

func NewWorker(queue Queue, handler Handler, config WorkerConfig, opts ...WorkerOption) (*Worker, error) {
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
	worker := &Worker{
		queue:         queue,
		handler:       handler,
		workerID:      config.WorkerID,
		concurrency:   concurrency,
		leaseTTL:      leaseTTL,
		renewInterval: renewInterval,
		jobTimeout:    config.JobTimeout,
		pollInterval:  pollInterval,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(worker)
		}
	}
	return worker, nil
}

func (worker *Worker) Run(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	// drainCtx trails ctx by the drain timeout: once ctx is cancelled the
	// lease loops stop taking new jobs, while in-flight jobs (their handler
	// context, lease renewal, and cancellation watcher, all derived from
	// drainCtx) keep running until they finish or the drain window elapses.
	// Only then are they cancelled and requeued. Without a configured drain
	// timeout drainCtx is cancelled together with ctx, preserving the
	// previous immediate-cancel behaviour.
	drainCtx, stopDrain := context.WithCancel(context.WithoutCancel(ctx))
	defer stopDrain()
	stopAfter := context.AfterFunc(ctx, func() {
		if worker.drainTimeout <= 0 {
			stopDrain()
			return
		}
		time.AfterFunc(worker.drainTimeout, stopDrain)
	})
	defer stopAfter()
	errCh := make(chan error, 1)
	var wg sync.WaitGroup
	for range worker.concurrency {
		wg.Add(1)
		safecall.GoSafe("async worker: loop", func(err error) {
			// A panicking loop is equivalent to a loop returning an error:
			// shut the worker down with the panic as the cause instead of
			// crashing the process.
			select {
			case errCh <- err:
				cancel()
			default:
			}
		}, func() {
			defer wg.Done()
			if err := worker.loop(ctx, drainCtx); err != nil {
				select {
				case errCh <- err:
					cancel()
				default:
				}
			}
		})
	}
	waitCh := make(chan struct{})
	safecall.GoSafe("async worker: wait", nil, func() {
		defer close(waitCh)
		wg.Wait()
	})
	select {
	case err := <-errCh:
		<-waitCh
		return err
	case <-waitCh:
		return ctx.Err()
	}
}

func (worker *Worker) loop(ctx context.Context, drainCtx context.Context) error {
	leaseFailures := 0
	for {
		if err := ctx.Err(); err != nil {
			return nil
		}
		lease, ok, err := worker.queue.Lease(ctx, worker.workerID, worker.leaseTTL)
		if err != nil {
			// A transient queue outage (DB blip, network partition) must not
			// kill the worker: back off exponentially and keep polling
			// instead of tearing down every concurrent loop on the first
			// error.
			leaseFailures++
			if waitErr := wait(ctx, leaseFailureDelay(leaseFailures)); waitErr != nil {
				return nil
			}
			continue
		}
		leaseFailures = 0
		if !ok {
			if err := wait(ctx, worker.pollInterval); err != nil {
				return nil
			}
			continue
		}
		// The job runs on the drain context, not on ctx: when the worker is
		// asked to stop, an in-flight job keeps running (with lease renewal)
		// until it finishes or the drain window elapses.
		if err := worker.handleLeasedJob(drainCtx, lease); err != nil {
			return err
		}
	}
}

// leaseFailureDelay returns the exponential backoff before the next lease
// poll after consecutive lease failures: 100ms doubling to a 5s cap.
func leaseFailureDelay(failures int) time.Duration {
	delay := 100 * time.Millisecond
	for i := 1; i < failures; i++ {
		delay *= 2
		if delay >= 5*time.Second {
			return 5 * time.Second
		}
	}
	return delay
}

// handleLeasedJob executes one leased job. ctx is the job scope context (the
// worker's drain context): it stays live while the worker drains during
// shutdown and is cancelled only when the job must actually stop — drain
// window elapsed, lease lost, or the job was cancelled through the queue.
func (worker *Worker) handleLeasedJob(ctx context.Context, lease Lease) error {
	job, err := worker.queue.Load(ctx, lease.JobID)
	if err != nil {
		// A transient Load failure after a successful Lease (DB blip, network
		// partition) must not kill the worker the way a returned error would:
		// fail the job through the normal retry/dead-letter semantics and keep
		// polling. If even Fail fails (queue mid-outage), release the lease
		// best-effort so the job does not sit leased until expiry.
		failCtx, cancel := terminalContext(ctx)
		defer cancel()
		if failErr := worker.queue.Fail(failCtx, lease, err); failErr != nil {
			slog.Warn("async worker: fail after job load error failed; releasing lease", "job_id", lease.JobID, "load_error", err, "fail_error", failErr)
			if releaser, ok := worker.queue.(LeaseReleaser); ok {
				if releaseErr := releaser.Release(failCtx, lease); releaseErr != nil {
					slog.Warn("async worker: lease release after job load error failed", "job_id", lease.JobID, "error", releaseErr)
				}
			}
		}
		return nil
	}
	jobCtx, jobCancel := context.WithCancel(ctx)
	defer jobCancel()
	if worker.jobTimeout > 0 {
		var timeoutCancel context.CancelFunc
		jobCtx, timeoutCancel = context.WithTimeout(jobCtx, worker.jobTimeout)
		defer timeoutCancel()
	}
	renewCtx, stopRenew := context.WithCancel(ctx)
	defer stopRenew()
	// A lost job lease must cancel the job: letting it run unowned duplicates
	// work when the queue re-leases the job to another worker.
	worker.startLeaseRenewal(renewCtx, lease, jobCancel)
	stopWatch := worker.watchJobCancellation(ctx, lease.JobID, jobCancel)
	defer stopWatch()
	// A panicking job handler must not kill the worker process: convert the
	// panic into an error so the job fails through the normal Fail path.
	err = safecall.Do("async worker: handle job", func() error {
		return worker.handler.HandleJob(jobCtx, job)
	})
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
			return worker.queue.Pause(pauseCtx, lease, PauseResult(paused))
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
	safecall.GoSafe("async worker: job cancellation watcher", func(err error) {
		// The watcher is best-effort: losing it stops cancel propagation for
		// this job but must never take the worker down.
		slog.Warn("async worker: job cancellation watcher crashed", "job_id", jobID, "error", err)
	}, func() {
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		loadFailures := 0
		for {
			select {
			case <-ctx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				job, err := worker.queue.Load(ctx, jobID)
				if err != nil {
					// Back off on repeated load failures instead of spinning
					// at the 200ms tick while the queue is down.
					loadFailures++
					if waitErr := wait(ctx, leaseFailureDelay(loadFailures)); waitErr != nil {
						return
					}
					continue
				}
				loadFailures = 0
				if job.State == JobCancelled {
					cancel()
					return
				}
			}
		}
	})
	return stop
}

// startLeaseRenewal renews the job lease in the background and calls onLost
// when renewal fails or reports the lease as no longer held, so the job is
// cancelled instead of completing unowned.
func (worker *Worker) startLeaseRenewal(ctx context.Context, lease Lease, onLost func()) {
	renewer, ok := worker.queue.(LeaseRenewer)
	if !ok {
		return
	}
	safecall.GoSafe("async worker: job lease renewal", func(err error) {
		// A crashed renewer can no longer prove ownership of the job lease;
		// treat it as lost so the job is cancelled instead of completing
		// unowned while the queue re-leases it to another worker.
		slog.Warn("async worker: job lease renewal crashed; cancelling job", "job_id", lease.JobID, "error", err)
		onLost()
	}, func() {
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
					onLost()
					return
				}
				current = renewed
			}
		}
	})
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
