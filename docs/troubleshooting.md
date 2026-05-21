# 排障指南

本文档汇总 agentflow-go 新手最常见的报错、原因与修复方式。调试能力概览与推荐路径见下文；编排细节见 [orchestration-flow.md](./orchestration-flow.md)，字段定义见 [configuration-reference.md](./configuration-reference.md)。

---

## 推荐调试顺序

```bash
# 1. YAML 结构与引用 + testutil 接线
go run ./examples/go/validate examples/autonomous.yaml

# 2. 最小 in-process 运行
go run ./examples/go/minimal/main.go

# 3. HTTP + 异步 Worker（库 Handler 挂到自有 Server）
go run ./examples/go/http-worker/main.go
```

`examples/go/validate` 在加载 YAML 后调用 `ValidateWiring`（含 `testutil.WiringOptions`）。库集成时请在 `agentflow.New()` 前用相同的 `opts` 调用 `ValidateWiring`。

---

## 常见报错

### `config: ...`

**含义**：场景 YAML 在加载阶段未通过语义校验。

| 典型消息 | 原因 | 修复 |
|----------|------|------|
| `agents.x.llm references unknown llm "y"` | Agent 引用了不存在的 LLM profile | 在 `scenario.llms` 中补 profile，或改 agent 的 `llm` 字段 |
| `workflow node "x" references unknown tool "y"` | Workflow 节点 ref 无效 | 在 `scenario.tools` 声明工具，或修正 `ref` |
| `workflow graph contains a cycle` | Workflow 有环 | 调整 `depends_on` / `edges`，保证 DAG |
| `orchestration.mode "x" is unsupported` | mode 拼写错误 | 使用 `autonomous` / `fixed_workflow` / `hybrid` |
| `workflow node "x" kind "y" is unsupported` | 节点 kind 无效 | 见 [configuration-reference — 编排](./configuration-reference.md#编排) |

**处理**：先 `go run ./examples/go/validate your.yaml`，按前缀 `config:` 定位字段。

---

### `agentflow: wiring: tool "x" (...) has no executor or resolver`

**含义**：场景声明了工具，但当前 Framework 选项里没有 executor，也没有 `WithToolResolver`。

**常见原因**

1. 只跑了 `validate`，没有 `--wiring`，直到 `run` 才发现。
2. 工具类型不是 testutil 内置的（如 `builtin.http`、`knowledge.retriever`、`mcp.tool`），需显式 `WithToolExecutor`。
3. 库集成时忘记 `WithToolExecutor` / `KnowledgeWiringOptions` / `WireMCPTools`。

**修复**

```bash
# 提前发现
go run ./examples/go/validate scenario.yaml
```

```go
// 库集成示例
httpTool, _ := agentflow.NewHTTPToolExecutor(agentflow.HTTPToolConfig{AllowedHosts: []string{"https://status.internal"}})
fw, err := agentflow.New(scenario,
    agentflow.WithLLMGateway(gateway),
    agentflow.WithToolExecutor("http.status", httpTool),
)
// 或
err := agentflow.ValidateWiring(scenario, agentflow.WithToolExecutor("http.status", httpTool), ...)
```

**Demo 自动注册的工具类型**：`builtin.echo`、`builtin.repo_search`、`builtin.git`、`builtin.ticket`（见 `demo.go`）。

---

### `agentflow: wiring: memory "x" (file) has no repository`

**含义**：Agent 引用了非 `in_memory` 的 memory，但未 `WithMemoryRepository`。

**修复**：注册对应 backend，或改为 `type: in_memory` 做本地试验。

---

### `agentflow: wiring: human-in-the-loop is enabled but no HumanGate or HITL token secret is configured`

**含义**：启用了 HITL，但未配置 gate 或 token secret。

**修复**

- 示例：`examples/go/hitl-resume` 使用 `dev-secret`；生产请使用强密钥。
- 库：`WithHITLTokenSecret([]byte("secret"), tokenWriter)` 或 `WithHumanGate(gate)`。

---

### Run 成功但输出像「复读」用户输入

**含义**：正在使用 **Mock LLM Fallback**（`provider: mock`），未 queue 响应时会 echo 最后一条 user 消息。

**这不是 bug**。要验证真实 LLM：

- Debug UI 选 real model 场景，或
- 库中 `WithLLMGateway(realGateway)`，或
- 测试中使用 `pkg/llm/mock.Gateway` 的 `QueueChat` / `QueueToolCall`。

---

### `humangate: ...` / `human cli: ...` / token 无效

**含义**：HITL resume 时 token 校验失败。

| 原因 | 修复 |
|------|------|
| Run 与 resume 使用的 HITL secret 不一致 | 同一 `WithHITLTokenSecret` 字节序列 |
| 未持久化 RunState，resume 找不到 snapshot | `WithRunStateRepository` 使用 file/postgres/redis |
| token 过期 | 调大 `WithHITLTokenTTL` 或尽快 resume |
| 复制 token 不完整 | HITL token 由 `WithHITLTokenSecret` 的 writer 输出 |

**完整 HITL 续跑示例**

```bash
go run ./examples/go/hitl-resume/main.go
```

---

### `runstate: snapshot not found` / `ErrNotFound`

**含义**：请求的 `run_id` 在 RunState 仓库中不存在。

**修复**：确认 `run_id` 一致；使用持久化 `RunStateRepository`；进程重启后内存 run 会丢失。

---

### `runstate: stale snapshot` / CAS 冲突

**含义**：并发更新同一 run（多 worker / 重复 resume）。

**修复**：同一 run 单 writer；异步场景检查 worker 租约与 job 重试策略。见 [postgres-runstate.md](./persistence/postgres-runstate.md)。

---

### `security: unauthenticated` / `unauthorized`

**含义**：HTTP/API 请求未带有效身份或未通过 RBAC。

**修复**：检查 API key、JWT、租户 header。生产见 [security-auth-tenancy.md](./security-auth-tenancy.md)。

---

### `mock llm: no response queued`

**含义**：测试用 mock gateway 没有为 profile queue 响应。

**修复**：在测试中 `gateway.QueueChat(profile, response)` 或使用 `NewMockLLMGateway` / Fallback。

---

### Workflow 节点被跳过 / 条件不生效

**含义**：`condition` 或 edge `condition` 求值为 false。

**修复**

1. 用 `--verbose` 看 `StepStarted` / `StepCompleted` 事件。
2. 路径以 `steps.<node_id>` 开头；tool 输出多在 `steps.<id>.output.*`。
3. 从 RunState 加载 snapshot 查看 `step_outputs`。
4. 表达式支持：`eq` / `ne` / `exists` / `missing` / `true` / `false`。

---

### Hybrid 模式 Phase-2 Agent 没有 workflow 上下文

**含义**：Phase-1 workflow 未完成或 step 输出未写入 RunState。

**修复**：确认 workflow 节点都 `Completed`；检查 `RunRequest.Agent` 是否指向 Phase-2 综合 agent。

---

### 检索 / RAG 无结果或命名空间错误

**含义**：`knowledge.retriever` 未 wiring，或 namespace / tenant 前缀不对。

**修复**

```go
knowledgeOpts, _ := agentflow.KnowledgeWiringOptions(scenario, agentflow.KnowledgeRegistry{
    Embedder: embedder,
    Store:    store,
})
```

YAML 中 `knowledge.collections[].tenant_scoped: true` 会在运行时注入 `tenant_id/` 前缀。见 [knowledge-rag.md](./knowledge-rag.md)。

---

## 库集成调试建议

| 做法 | 作用 |
|------|------|
| `ValidateWiring` + `testutil.WiringOptions` | 启动前发现缺 executor / HITL 配置 |
| `WithEventSink(NewSlogEventSink(logger))` | 将 runtime 事件打到结构化日志 |
| `WithRunStateRepository`（file/postgres） | 持久化 run，支持跨请求 resume |
| `NewObservabilityHTTPHandler` | 查询 runs、events、step outputs（需自建路由与认证） |

---

## 日志与可观测性（库集成）

| 组件 | 用法 |
|------|------|
| 运行时事件 | `WithEventSink(NewSlogEventSink(logger))` 或 `NewVerboseSlogEventSink` |
| 审计 | `WithAuditSink(NewSlogAuditSink(logger))` |
| 指标 + Dashboard | `NewObservabilityEventSink` + `NewEventStoreSink` + `NewObservabilityHTTPHandler` |
| Warning | `WithLogger(pkg/log.Logger)` |

详见 [observability-governance.md](./observability-governance.md)、[observability-dashboard.md](./observability-dashboard.md)。

---

## 仍无法定位时

1. **最小复现**：从 `examples/autonomous.yaml` 开始，逐步换成你的场景字段。
2. **对比 wiring**：`validate --wiring` 与 `examples/` 里最接近的示例 diff。
3. **看事件时序**：`run --verbose` 或 Debug UI Event 列表。
4. **查 RunState**：`--state-dir` 下 `{state-dir}/runs/` JSON 文件，或 Dashboard API。
5. **编排选型**：是否该用 `fixed_workflow` 而非 `autonomous`？见 [orchestration-flow.md §九](./orchestration-flow.md#九编排模式与节点选型指南)。

---

## 相关文档

- [library-integration.md](./library-integration.md) — ValidateWiring、testutil.WiringOptions
- [orchestration-flow.md](./orchestration-flow.md) — 执行链路与 HITL
- [configuration-reference.md](./configuration-reference.md) — YAML 字段
- [async-runtime.md](./async-runtime.md) — 异步 job 与 resume.continue
