# PostgreSQL 异步队列适配器

`agentflow.NewPostgresJobQueue(db)` 提供基于 `database/sql` 的 `pkg/async.Queue` 实现。应用拥有 PostgreSQL 驱动导入、连接池配置、迁移、保留任务和运维索引。

## 表契约

适配器期望一张表，默认名为 `agentflow_jobs`，列与以下含义等价：

- `id`：唯一任务标识。
- `type`：任务类型，目前框架运行任务使用 `run`。
- `run_id`：可选运行时运行标识。
- `payload_json`：序列化任务载荷。
- `state`：`queued`、`running`、`completed`、`failed`、`cancelled` 或 `dead_letter` 之一。
- `attempts`：当前租约/执行尝试次数。
- `max_attempts`：进入 `dead_letter` 前的重试预算。
- `last_error`：最近一次失败原因。
- `created_at`、`updated_at`、`available_at`：调度时间戳。
- `lease_worker_id`、`lease_expires_at`：当前租约持有者。

具体 DDL 应使用外部已评审的迁移工具管理。队列查询假设存在高效查询路径：按 `created_at` 排序查找排队任务，以及查找过期的运行中租约。

## 租约语义

- `Lease` 使用原子 `UPDATE ... WHERE id = (SELECT ... FOR UPDATE SKIP LOCKED) ... RETURNING ...` 模式。
- 当 `available_at <= now` 时，排队任务可被租约获取。
- 当 `lease_expires_at < now` 时，运行中任务可被恢复。
- `Complete` 和 `Fail` 需要当前 Worker ID 和尝试次数。
- 失败任务会回到 `queued`，直到 `attempts >= max_attempts`，之后进入 `dead_letter`。

## 用法

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