# Async Runtime

The async runtime foundation provides job queue, worker, and HTTP submit/status/cancel contracts for long-running agent workflows. It is designed to keep the existing synchronous framework facade intact while enabling horizontally scalable workers.

## Current Scope

- Public job, lease, queue, handler, and worker contracts in `pkg/async`.
- In-memory queue adapter for tests and local development.
- PostgreSQL queue adapter for production workers, exposed through `agentflow.NewPostgresJobQueue`.
- Worker loop with bounded concurrency, context cancellation, polling, job timeout, lease completion, and failure reporting.
- Root constructor for local queues: `agentflow.NewInMemoryJobQueue()`.
- Root HTTP handler for async run submit/status/cancel: `agentflow.NewAsyncRunHTTPHandler(...)`.
- Root framework-run worker handler: `agentflow.NewFrameworkRunJobHandler(...)`.
- Production HTTP handler with `/healthz`, `/readyz`, and `/v1/runs` routing: `agentflow.NewProductionHTTPHandler(...)`.

## Queue Semantics

Jobs move through these states:

```text
queued -> running -> completed
queued -> running -> queued
queued -> running -> dead_letter
queued/running -> cancelled
```

Important rules:

- `Lease` assigns a queued job to a worker for a TTL.
- Expired running leases can be recovered by another worker.
- `Complete` and `Fail` require a current lease.
- Failed jobs retry until `MaxAttempts` is reached.
- Final failed jobs move to `dead_letter`.

## Worker Usage

```go
queue, err := agentflow.NewPostgresJobQueue(db)
if err != nil {
    log.Fatal(err)
}

fw, err := agentflow.NewFromFile("scenario.yaml")
if err != nil {
    log.Fatal(err)
}

runHandler, err := agentflow.NewFrameworkRunJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
if err != nil {
    log.Fatal(err)
}

worker, err := async.NewWorker(
    queue,
    runHandler,
    async.WorkerConfig{
        WorkerID:     "worker-1",
        Concurrency:  4,
        LeaseTTL:     time.Minute,
        JobTimeout:   2 * time.Minute,
        PollInterval: 100 * time.Millisecond,
    },
)
if err != nil {
    log.Fatal(err)
}

if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
    log.Fatal(err)
}
```

`POST /v1/runs` stores a `pkg/async.RunPayload`. The framework worker handler decodes that payload, preserves `run_id`, restores the submitting principal when present, and calls `Framework.Run`.

## HTTP Submit/Status/Cancel Usage

```go
queue := agentflow.NewInMemoryJobQueue()
auditSink := agentflow.NewInMemoryAuditSink(1000)

handler, err := agentflow.NewAsyncRunHTTPHandler(agentflow.AsyncRunHTTPHandlerConfig{
    Queue:  queue,
    Policy: security.NewDefaultRolePolicy(),
    Audit:  auditSink,
})
if err != nil {
    log.Fatal(err)
}

http.Handle("/v1/", apiKeyMiddleware(handler))
```

For production services, prefer the wrapper handler when you want health/readiness probes and the async run API mounted together:

```go
api, err := agentflow.NewProductionHTTPHandler(agentflow.ProductionHTTPHandlerConfig{
    Queue:          queue,
    AuthMiddleware: apiKeyMiddleware,
    Policy:         security.NewDefaultRolePolicy(),
    Audit:          auditSink,
    Version:        "v0.1.0",
})
if err != nil {
    log.Fatal(err)
}

http.ListenAndServe(":8080", api)
```

Endpoints:

- `POST /v1/runs`: enqueues a run job and returns `202 Accepted` with the queued job.
- `GET /v1/runs/{run_id}`: returns the queued/running/completed/cancelled job state.
- `POST /v1/runs/{run_id}/cancel`: cancels queued or running jobs.

When a policy is configured, the handler enforces `run.submit`, `run.read`, and `run.cancel`. Accepted submit/cancel actions and denied policy decisions are emitted to the configured audit sink.

## Next Slices

- Add dead-letter inspection and retry APIs.
- Add metrics for queue depth, lease recovery, retry counts, and worker latency.