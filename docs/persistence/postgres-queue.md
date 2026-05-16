# PostgreSQL Async Queue Adapter

`agentflow.NewPostgresJobQueue(db)` provides a `pkg/async.Queue` implementation backed by `database/sql`. Applications own the PostgreSQL driver import, connection pool configuration, migrations, retention jobs, and operational indexes.

## Table Contract

The adapter expects a table, defaulting to `agentflow_jobs`, with columns equivalent to:

- `id`: unique job identifier.
- `type`: job type, currently `run` for framework run jobs.
- `run_id`: optional runtime run identifier.
- `payload_json`: serialized job payload.
- `state`: one of `queued`, `running`, `completed`, `failed`, `cancelled`, or `dead_letter`.
- `attempts`: current lease/execution attempt count.
- `max_attempts`: retry budget before `dead_letter`.
- `last_error`: most recent failure reason.
- `created_at`, `updated_at`, `available_at`: scheduling timestamps.
- `lease_worker_id`, `lease_expires_at`: active lease ownership.

Use an externally reviewed migration tool for the concrete DDL. The queue queries assume an efficient lookup path for queued jobs ordered by `created_at` and for expired running leases.

## Leasing Semantics

- `Lease` uses an atomic `UPDATE ... WHERE id = (SELECT ... FOR UPDATE SKIP LOCKED) ... RETURNING ...` pattern.
- Queued jobs are leaseable when `available_at <= now`.
- Running jobs are recoverable when `lease_expires_at < now`.
- `Complete` and `Fail` require the current worker ID and attempt number.
- Failed jobs return to `queued` until `attempts >= max_attempts`, then move to `dead_letter`.

## Usage

```go
queue, err := agentflow.NewPostgresJobQueue(db)
if err != nil {
    log.Fatal(err)
}

workerHandler, err := agentflow.NewFrameworkRunJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
if err != nil {
    log.Fatal(err)
}

worker, err := async.NewWorker(queue, workerHandler, async.WorkerConfig{
    WorkerID:    "worker-1",
    Concurrency: 4,
    LeaseTTL:    time.Minute,
    JobTimeout:  5 * time.Minute,
})
```