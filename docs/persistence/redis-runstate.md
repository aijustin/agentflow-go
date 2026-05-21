# Redis RunState Repository

`agentflow.NewRedisRunStateRepository` provides a Redis-backed implementation of `runstate.Repository` for production deployments that prefer Redis over PostgreSQL for workflow snapshots.

```go
runs, err := agentflow.NewRedisRunStateRepository(agentflow.RedisRunStateRepositoryConfig{
  Addr:      os.Getenv("AGENTFLOW_REDIS_ADDR"),
  Password:  os.Getenv("AGENTFLOW_REDIS_PASSWORD"),
  DB:        0,
  KeyPrefix: "agentflow:runstate:",
})
if err != nil {
  log.Fatal(err)
}

fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithRunStateRepository(runs),
)
```

## Storage Model

Each run is stored as one Redis hash:

- key: `<KeyPrefix><run_id>`
- field `version`: current `RunSnapshot.Version`
- field `snapshot`: JSON-encoded `runstate.RunSnapshot`（含 `created_at`、`updated_at`、`tenant_id`）

`Save` uses a Lua compare-and-swap script. The write succeeds only when the stored version matches `expectedVersion`; otherwise it returns `runstate.ErrStaleSnapshot`. This preserves the same optimistic concurrency contract as the file and PostgreSQL repositories.

每次保存都会刷新 snapshot JSON 内的 `updated_at`，供 `Framework.PurgeExpired` 按 age 清理终态 run。

## Operational Notes

- Use a dedicated prefix per environment and tenant boundary when Redis is shared.
- Enable Redis authentication and TLS at the infrastructure layer when crossing trusted networks.
- Size Redis memory and eviction policy so run-state keys are not evicted unexpectedly. Prefer a no-eviction policy for stateful runs.
- Store large step outputs in a BlobStore by configuring `runtime.step_output_threshold`; RunState should keep references, not large payload bodies.
- Redis persistence settings (`appendonly`, snapshots, managed-service backups) determine recovery guarantees after Redis process or node failure.

## When To Use

Use Redis RunState when low-latency snapshot access and operational simplicity matter more than relational querying. Use PostgreSQL RunState when you need database-level backup consistency, SQL inspection, or a single durable store alongside the PostgreSQL queue.