# 异步运行时

异步运行时基础提供任务队列、Worker，以及用于长时间运行 Agent 工作流的 HTTP 提交/状态/取消契约。它在保持既有同步框架门面不变的同时，让 Worker 可以水平扩展。

## 当前范围

- `pkg/async` 中的公共任务、租约、队列、Handler 和 Worker 契约。
- 用于测试和本地开发的内存队列适配器。
- 用于生产 Worker 的 PostgreSQL 队列适配器，通过 `agentflow.NewPostgresJobQueue` 暴露。
- Worker 循环支持有界并发、上下文取消、轮询、任务超时、租约完成和失败上报。
- 本地队列根构造函数：`agentflow.NewInMemoryJobQueue()`。
- 异步 run submit/status/cancel 的根 HTTP handler：`agentflow.NewAsyncRunHTTPHandler(...)`。
- 框架运行 Worker Handler：`agentflow.NewFrameworkRunJobHandler(...)`。
- 带 `/healthz`、`/readyz` 和 `/v1/runs` 路由的生产 HTTP Handler：`agentflow.NewProductionHTTPHandler(...)`。

## 队列语义

任务状态流转如下：

```text
queued -> running -> completed
queued -> running -> queued
queued -> running -> dead_letter
queued/running -> cancelled
```

关键规则：

- `Lease` 会将排队任务分配给一个 Worker，并设置 TTL。
- 当队列实现 `LeaseRenewer` 时，Worker 会按 `RenewInterval` 在长任务执行期间续租。
- 过期的运行中租约可以被其他 Worker 恢复。
- `Complete` 和 `Fail` 需要当前有效租约。
- 失败任务会重试，直到达到 `MaxAttempts`。
- 最终失败的任务会进入 `dead_letter`。

## Worker 使用方式

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
        WorkerID:      "worker-1",
        Concurrency:   4,
        LeaseTTL:      time.Minute,
        RenewInterval: 30 * time.Second,
        JobTimeout:    2 * time.Minute,
        PollInterval:  100 * time.Millisecond,
    },
)
if err != nil {
    log.Fatal(err)
}

if err := worker.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
    log.Fatal(err)
}
```

`POST /v1/runs` 会存储一个 `pkg/async.RunPayload`。框架 Worker Handler 会解码该载荷，保留 `run_id`，在存在提交者主体时恢复该身份，然后调用 `Framework.Run`。

## HTTP 提交/状态/取消用法

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

生产服务如果希望将健康检查、就绪探针和异步运行 API 一起挂载，可以优先使用封装 Handler：

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

端点：

- `POST /v1/runs`：入队一个运行任务，并返回 `202 Accepted` 和排队任务。
- `GET /v1/runs/{run_id}`：返回 queued/running/completed/cancelled 任务状态。
- `POST /v1/runs/{run_id}/cancel`：取消排队或运行中的任务。

配置策略后，Handler 会强制检查 `run.submit`、`run.read` 和 `run.cancel`。接受的提交/取消动作以及被拒绝的策略决策都会写入配置的审计 sink。

## 后续切片

- 增加死信查看和重试 API。
- 增加队列深度、租约恢复、重试次数和 Worker 延迟指标。