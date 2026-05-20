# 异步运行时

异步运行时基础提供任务队列、Worker，以及用于长时间运行 Agent 工作流的 HTTP 提交/状态/取消契约。它在保持既有同步框架门面不变的同时，让 Worker 可以水平扩展。

## 当前范围

- `pkg/async` 中的公共任务、租约、队列、Handler 和 Worker 契约。
- 用于测试和本地开发的内存队列适配器。
- 用于生产 Worker 的 PostgreSQL 队列适配器，通过 `agentflow.NewPostgresJobQueue` 暴露。
- Worker 循环支持有界并发、上下文取消、轮询、任务超时、租约完成和失败上报。
- 本地队列根构造函数：`agentflow.NewInMemoryJobQueue()`。
- 异步 run / event / resume.continue submit/status/cancel 的根 HTTP handler：`agentflow.NewAsyncRunHTTPHandler(...)`。
- 框架 Worker Handler：`agentflow.NewFrameworkJobHandler(...)`（`NewFrameworkRunJobHandler` 为其别名）。
- 带健康检查、异步 API，以及可选同步事件/HITL 路由的生产 HTTP Handler：`agentflow.NewProductionHTTPHandler(...)`。

## 任务类型

框架 Worker Handler 支持以下 job type：

| 类型 | 常量 | 载荷 | Worker 行为 |
| --- | --- | --- | --- |
| `run` | `async.RunJobType` | `async.RunPayload` | 调用 `Framework.Run` |
| `event` | `async.EventJobType` | `async.EventPayload` | 调用 `Framework.HandleEvent` |
| `resume.continue` | `async.ResumeContinueJobType` | `async.ResumeContinuePayload` | 调用 `Framework.ResumeAndContinue` |

`RunPayload`、`EventPayload` 和 `ResumeContinuePayload` 都可以携带 `Principal`，Worker 会在执行任务前将其写回 `context.Context`，以便 RBAC 和审计与同步路径一致。

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

jobHandler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
if err != nil {
    log.Fatal(err)
}

worker, err := async.NewWorker(
    queue,
    jobHandler,
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

- `POST /v1/runs` 会存储 `async.RunPayload`，Worker 调用 `Framework.Run`。
- `POST /v1/jobs/events` 会存储 `async.EventPayload`，Worker 调用 `Framework.HandleEvent`。
- `POST /v1/jobs/hitl/resume` 会存储 `async.ResumeContinuePayload`，Worker 调用 `Framework.ResumeAndContinue`。

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

### 异步 run API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/v1/runs` | 入队 `run` 任务，返回 `202 Accepted` |
| `GET` | `/v1/runs/{run_id}` | 查询任务状态 |
| `POST` | `/v1/runs/{run_id}/cancel` | 取消 queued/running 任务 |

Run 请求体示例：

```json
{
  "run_id": "run-1",
  "agent": "assistant",
  "prompt": "hello",
  "context": {"ticket_id": "T-9"}
}
```

### 异步 event / resume.continue API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/v1/jobs/events` | 入队 `event` 任务 |
| `POST` | `/v1/jobs/hitl/resume` | 入队 `resume.continue` 任务 |
| `GET` | `/v1/jobs/{job_id}` | 查询任意 job 状态 |
| `POST` | `/v1/jobs/{job_id}/cancel` | 取消 queued/running 任务 |

Event 请求体示例：

```json
{
  "type": "ticket.created",
  "run_id": "T-9",
  "payload": {"body": {"ticket_id": "T-9", "summary": "Need help"}},
  "job_id": "job-event-1"
}
```

Resume continue 请求体示例：

```json
{
  "token": "signed-hitl-token",
  "decision": "approve",
  "amendment": null,
  "job_id": "job-resume-1"
}
```

配置策略后：

- run / event 提交需要 `run.submit` 权限（默认 `service` 角色）。
- resume.continue 提交需要 `hitl.resume` 权限（默认 `admin` / `approver` 角色）。
- 状态查询需要 `run.read`；取消需要 `run.cancel`。

## 生产 HTTP Handler

生产服务如果希望将健康检查、就绪探针、异步 API，以及可选的同步事件/HITL 路由一起挂载，可以优先使用封装 Handler：

```go
fw, err := agentflow.NewFromFile("examples/ticket_handling.yaml")
if err != nil {
    log.Fatal(err)
}

api, err := agentflow.NewProductionHTTPHandler(agentflow.ProductionHTTPHandlerConfig{
    Queue:          queue,
    Framework:      fw,
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

当 `Framework` 不为空时，会额外挂载：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/v1/events` | 同步调用 `Framework.HandleEvent`，立即返回运行结果 |
| `POST` | `/v1/hitl/resume` | 同步 HITL 恢复；`continue: true` 时调用 `ResumeAndContinue` |

始终挂载：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `GET` | `/healthz` | 健康检查 |
| `GET` | `/readyz` | 就绪探针 |
| `POST` | `/v1/runs` | 异步 run 入队 |
| `GET/POST` | `/v1/runs/{id}`、`/v1/runs/{id}/cancel` | run 任务状态/取消 |
| `POST` | `/v1/jobs/events` | 异步 event 入队 |
| `POST` | `/v1/jobs/hitl/resume` | 异步 resume.continue 入队 |
| `GET/POST` | `/v1/jobs/{id}`、`/v1/jobs/{id}/cancel` | 任意 job 状态/取消 |

同步事件请求体与 `pkg/eventrouter.Event` 一致：

```json
{
  "type": "ticket.created",
  "run_id": "T-9",
  "payload": {"body": {"ticket_id": "T-9", "summary": "Need help"}}
}
```

同步 HITL 请求体：

```json
{
  "token": "signed-hitl-token",
  "decision": "approve",
  "continue": true
}
```

- `continue: false` 或未设置：只更新 RunState（`Framework.Resume`）。
- `continue: true`：恢复并继续执行直到完成或下一次暂停（`Framework.ResumeAndContinue`）。

也可单独构造同步 Handler：

```go
events, _ := agentflow.NewWebhookHTTPHandler(agentflow.WebhookHTTPHandlerConfig{Framework: fw})
hitl := agentflow.NewHumanHTTPHandler(agentflow.HumanHTTPHandlerConfig{Framework: fw})
```

## CLI 等价操作

```sh
# 同步触发事件
agentctl trigger -f examples/ticket_handling.yaml \
  --event ticket.created \
  --payload '{"body":{"ticket_id":"T-9","summary":"Need help"}}'

# 同步续跑（ResumeAndContinue）
agentctl resume -f examples/human_in_loop.yaml \
  --continue \
  --token "$TOKEN" \
  --decision approve
```

CLI 的 `--continue` 与 HTTP `continue: true`、异步 `resume.continue` job 语义一致。

`Framework.ResolveEvent` 仅解析 trigger 为 `RunRequest`，不执行运行；适合预检或自定义队列封装。

## 后续切片

- 增加死信查看和重试 API。
- 增加队列深度、租约恢复、重试次数和 Worker 延迟指标。
