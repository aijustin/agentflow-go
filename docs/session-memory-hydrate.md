# Session memory hydrate 契约（AF-014）

Integrator（如 agent-base）与框架共享同一 session memory store 时，按下列契约接线，避免「框架已写入 tool 消息、平台 hydrate 门控误判」导致的可观测混乱。

## 推荐时序

1. **Run 前 seed（可选）**：将 chat 历史中的 user/assistant 写入 session memory（provenance 建议 `chat_hydrate`）。
2. **Framework.Run / Stream**：框架在 tool loop 中 append tool/assistant 中间消息（provenance `tool_loop` / `run_turn`）。
3. **不要**在同一次 run 中途按「条数变少」再全量覆盖 hydrate；用内容指纹 / generation 判断是否需要 re-seed。
4. **Run 结束后**：UI transcript 只展示 `tier=conversation`（或 user/assistant）；tool_trace 可单独审计。

## MemoryRead 可观测字段

框架 `EventMemoryRead` 至少包含：

- `stored_messages` / `messages`
- `messages_by_role`
- `messages_by_provenance`

Integrator 自定义字段（如 `memory_hydrated_from_chat`）应表示**本次 run 是否执行了 chat→memory seed**，而不是「memory 是否为空」。

## MemoryWrite 可观测字段（AF-013）

- `message_bytes`：本次写入内容字节数
- `tool_name`：单条 tool 写入时的工具名
- `tier`：`conversation` 或 `tool_trace`
- `transformed`：是否经过 ToolOutputTransform / 截断

## Profile 接线（AF-011）

`ContextPrepared.max_input_tokens` 推导：

1. 若 `LLMProfileRef.Context.MaxInputTokens > 0` → 使用该值（`policy_source=context_policy`）
2. 否则若 `ContextWindowTokens > 0` → `max_input_tokens = ContextWindowTokens - ReservedOutputTokens`（`policy_source=profile`）
3. 否则兜底 **8192**（`policy_source=default_8192`，`fallback_applied=true`）

请确保平台配置的 `context_window_tokens` 写入传给框架的 `LLMProfileRef`，并按需启用：

```yaml
context:
  strategy: sliding_window_with_summary
  tool_result_max_tokens: 400
  tool_output_max_bytes: 32768
  stale_tool_turns: 2
  compression:
    enabled: true
```

## TrustMode（AF-016）

`RunRequest.TrustMode = "full_trust"` 时，框架跳过 tool approval pause，并将需批准工具视为已批准执行。

## StreamRun（AF-004）

优先使用 `Framework.StreamRun` 获取统一的 `StreamFrame`（token / event / done），避免自行 merge `Stream` + EventHub 并猜测 drain 时长。

## Production HTTP（AF-010）

`agentflow.NewProductionHTTPHandler` 已提供健康检查、异步 job、HITL resume 等生产路由；详见 [async-runtime.md](./async-runtime.md)。

取消与 context 贯通：`POST /v1/runs/{run_id}/cancel` / `POST /v1/jobs/{job_id}/cancel` 会取消 Worker 侧 job 上下文，正在执行的 `Framework.Run` / `HandleEvent` / `ResumeAndContinue` 收到 `context.Canceled`（见 async-runtime 文档「取消」一节）。
