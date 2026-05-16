# Redis Locker

The Redis locker provides a small distributed lease primitive for future worker, queue, and workflow coordination. It implements `pkg/coordination.Locker` and is exposed through `agentflow.NewRedisLocker`.

## Usage

```go
locker, err := agentflow.NewRedisLocker(agentflow.RedisLockerConfig{
    Addr:      os.Getenv("AGENTFLOW_REDIS_ADDR"),
    Password:  os.Getenv("AGENTFLOW_REDIS_PASSWORD"),
    DB:        0,
    KeyPrefix: "agentflow:",
})
if err != nil {
    log.Fatal(err)
}

lease, acquired, err := locker.Acquire(ctx, "run:123", "worker:alpha", 30*time.Second)
if err != nil {
    log.Fatal(err)
}
if acquired {
    defer func() { _ = locker.Release(ctx, lease) }()
}
```

## Semantics

- `Acquire` uses `SET key owner NX PX ttl`.
- `Renew` uses an atomic Lua compare-and-`PEXPIRE` operation.
- `Release` uses an atomic Lua compare-and-`DEL` operation.
- A worker can renew or release only leases it owns.
- Lease keys and owners are validated before Redis commands are sent.

## Dependency Strategy

The first implementation uses the Go standard library and RESP directly, avoiding a Redis client dependency in the core module. It opens a short-lived connection per operation, which is simple and safe for low-frequency coordination. A future queue/worker milestone can add a pooled adapter if high-throughput Redis usage becomes necessary.

## Security Notes

- Passwords are accepted through configuration and are never logged by the adapter.
- Use TLS or a private network boundary for non-local Redis deployments.
- Use per-environment credentials and least-privilege Redis ACLs.
- Prefer a dedicated key prefix for agentflow leases.