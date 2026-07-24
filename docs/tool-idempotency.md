# 工具副作用幂等契约

恢复重放（`RetryFailedRun`、`ResumeFromStep`、`ResumeFromCheckpoint`、workflow 节点重跑）会重新执行崩溃前的工具调用。如果工具带有副作用（写数据库、调用下游 API、扣款、发消息），重放会让副作用重复发生。框架为此给每次工具执行注入一个**幂等键**，自定义工具应按键去重副作用。

## 键的获取

幂等键通过 `context` 传入 `core.ToolExecutor`：

```go
func (t *myTool) Execute(ctx context.Context, call core.ToolCall) (core.ToolResult, error) {
    key := core.IdempotencyKeyFromContext(ctx)
    if key == "" {
        // 运行时尚未注入（例如直接单测调用 executor）：按需自行降级。
    }
    // ... 用 key 去重副作用
}
```

`ToolCalled` / `ToolReturned` 事件的 payload 也会携带同一个键（`idempotency_key` 字段），便于事件流与副作用记录对账。

## 键的组成与稳定性保证

键的组成随执行路径不同，但遵循同一条契约：

- **恢复重放同一逻辑执行时键不变**。节点执行完副作用、保存快照前崩溃，恢复后整个节点重跑，键与崩溃前相同——这正是去重的依据。
- **同一次执行内的重试键不变**。autonomous 路径上 `executeToolWithRetry` 的内存重试复用同一个键。
- **不同的逻辑执行键不同**。不同节点、不同 iteration、workflow 节点的新 attempt 都会得到不同的键。

具体组成：

- **autonomous（LLM 工具循环）**：`{run_id}:{tool_call_id}`。`tool_call_id` 由 LLM 下发，随 assistant 消息一起持久化（run memory 与 tool_approval checkpoint 的 pending calls），恢复后以同一 ID 重新分发，因此天然满足重放稳定性，框架直接复用而不另造键。若 provider 不下发 tool_call_id，框架退化为一次性随机键——该 run 内的执行仍可追踪，但重放去重依赖 provider 提供稳定的 tool_call_id。
- **workflow 工具节点**：`{run_id}:{node_id}:{attempt}`。`node_id` 含 loop/subgraph 的层级前缀（如 `loop.2.body`），`attempt` 是 `runNodeWithRetry` 的重试计数（从 1 开始）。`ResumeFromStep` 截断输出后重跑同一节点时 attempt 重新从 1 计起，因此重放键相同；节点级重试（`Retry.MaxAttempts`）的每次 attempt 是不同的逻辑执行，键不同。注意：声明了 `write`/`external`/`dangerous` 副作用的工具节点默认不会被自动重试（`attempts=1`），只有显式配置 per-node retry 才会产生多个 attempt 键。

## 自定义工具的建议实现模式

副作用按键去重，三选一（或组合）：

1. **去重表**：副作用与一行 `idempotency_key PK` 记录同事务写入；键冲突即视为已执行，直接返回上次结果。
2. **UPSERT / INSERT IGNORE**：以幂等键作为业务行的冲突键（如 `ON CONFLICT (idempotency_key) DO NOTHING`），让数据库天然去重。
3. **outbox**：副作用不产生即时外部调用，而是同事务写一行 outbox 记录（以幂等键去重），由后台 relay 投递——与框架自身的快照+事件 outbox 模式一致。

纯读工具（`SideEffectRead`/`SideEffectNone`）无需处理幂等。

## 内置工具

- **HTTP 工具**：键非空时自动把键放进 `X-Idempotency-Key` 请求头（覆盖默认头和输入头中的同名字段），由上游 API 按头去重；见 [tools-http.md](tools-http.md)。
- **SQL 工具**：内置 executor 只读（仅 `SELECT`），无副作用；基于该模式自建的 SQL 写工具应按上述模式用键做 UPSERT/INSERT IGNORE，见 [tools-sql.md](tools-sql.md)。
