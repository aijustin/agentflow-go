# 异步运行时实施计划

> **历史文档**：本计划为 2026-05 实施记录，勾选框反映当时进度，不再作为当前 backlog。产品方向见 [product-direction.md](../../product-direction.md)。

> **给 agentic worker：** 必需子技能：使用 superpowers:subagent-driven-development（推荐）或 superpowers:executing-plans，按任务逐步实施本计划。步骤使用复选框（`- [ ]`）语法跟踪。

**目标：** 增加异步任务执行能力，让长时间运行的 Agent 工作流可以脱离请求路径执行。

**架构：** 保持同步框架门面作为嵌入式 API。增加公共异步队列/Worker 契约、本地适配器，然后再增加生产队列适配器和 HTTP 提交/状态/取消 API。

**技术栈：** Go 1.25.10+、`context`、标准库并发原语，后续切片中可选 PostgreSQL/Redis 队列适配器。

---

## 任务 1：公共异步契约

- [x] 增加 `pkg/async.Job`、`JobState`、`Lease`、`Queue`、`Handler` 和 `Worker`。
- [x] 增加 Worker 校验，以及基于上下文取消的结构化并发。
- [x] 增加 Worker 处理和取消行为测试。

## 任务 2：内存队列

- [x] 增加 `internal/adapter/queue/inmem`。
- [x] 支持入队、租约获取、加载、完成、失败、取消。
- [x] 恢复过期租约。
- [x] 失败任务重试直到 `MaxAttempts`，然后标记为 `dead_letter`。
- [x] 暴露 `agentflow.NewInMemoryJobQueue()`。

## 任务 3：框架运行 Handler

- [x] 为场景运行请求定义任务载荷。
- [x] 增加调用 `Framework.Run` 并记录结果状态的 Handler。
- [x] 保留 run ID 和 cancellation 行为。
- [x] 增加已完成和无效任务测试。

## 任务 4：生产队列适配器

- [x] 选择 PostgreSQL 或 Redis 队列作为第一批生产适配器。
- [x] 实现租约，并支持过期租约恢复。
- [x] 增加基于 database/sql fake-driver 的适配器行为测试。
- [x] 记录 schema 或 Redis key 策略。

## 任务 5：HTTP 异步 API

- [x] 增加返回 `run_id` 和 `job_id` 的提交端点。
- [x] 增加由队列支撑的状态端点。
- [x] 增加取消端点。
- [ ] API 稳定后增加 debug UI 集成。

## 验证

- [x] `CGO_ENABLED=0 go test -ldflags="-w" ./pkg/async ./internal/adapter/queue/inmem ./...`
- [x] `CGO_ENABLED=0 go vet ./...`
- [x] `make build`