# Redis 分布式锁

Redis 分布式锁为未来 Worker、队列和工作流协同提供小型分布式租约原语。它实现 `pkg/coordination.Locker`，并通过 `agentflow.NewRedisLocker` 暴露。

## 用法

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

## 语义

- `Acquire` 使用 `SET key owner NX PX ttl`。
- `Renew` 使用原子 Lua compare-and-`PEXPIRE` 操作。
- `Release` 使用原子 Lua compare-and-`DEL` 操作。
- Worker 只能续租或释放自己持有的租约。
- 租约 key 和 owner 在发送 Redis 命令前会被校验。

## 依赖策略

第一版实现直接使用 Go 标准库和 RESP，避免在核心模块中引入 Redis 客户端依赖。它每次操作打开一个短连接，这对低频协调来说简单且安全。未来队列/Worker 里程碑如果需要高吞吐 Redis 使用，可以增加连接池适配器。

## 安全注意事项

- Password 通过配置传入，适配器永远不会记录 password。
- 非本地 Redis 部署应使用 TLS 或私有网络边界。
- 使用按环境划分的凭证和最小权限 Redis ACL。
- 为 agentflow 租约使用专用 key prefix。