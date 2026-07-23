# Session memory hydrate 契约（AF-014）

Integrator（如 agent-base）与框架共享同一 session memory store 时，按下列契约接线，避免「框架已写入 tool 消息、平台 hydrate 门控误判」导致的可观测混乱。

## 推荐时序

1. **Run 前 seed（可选）**：将 chat 历史中的 user/assistant 写入 session memory（provenance 建议 `chat_hydrate`，即 `memory.ProvenanceChatHydrate`）。
2. **Framework.Run / Stream**：框架在 tool loop 中 append tool/assistant 中间消息（provenance `tool_loop` / `run_turn`）。
3. **不要**在同一次 run 中途按「条数变少」再全量覆盖 hydrate；用内容指纹 / generation 判断是否需要 re-seed。
4. **Run 结束后**：UI transcript 只展示 `tier=conversation`（或 user/assistant）；tool_trace 可单独审计。

## Tier 存储形态下的 hydrate（单层 PG / 无 job queue）

宿主只要 PG 持久、暂不接分层迁移时，tier 体系可以单层使用，hydrate 契约映射如下：

- **单层形态**：一个 warm level 的 Postgres tier store 直接作为 `tier.Store`（`adapters.NewPostgresTierWarmStore` 或自行 `tier/postgres.NewStore`），policy 用 `tier.SingleLevelPolicy()`（不 promote/demote/evict）。`Remember` 立即落 PG；进程重启后新 manager 能 `Recall` 到——不需要 composite，也不需要 WithJobQueue。
- **框架接线**：scenario 的 memory 声明 `tiers.enabled: true`，然后 `agentflow.WithTierStore("session", pgStore, tier.SingleLevelPolicy())`（不签 policy 时 `WithTierStore(name, store, nil)` 走 YAML/default policy——单层形态务必显式给 SingleLevelPolicy）。
- **seed 写法**：`tier.MessageRecord(ns, tier.ChatMessage{Role, Content, Time}, tier.WithProvenance(memory.ProvenanceChatHydrate))` 构造记录后 `store.Put(ctx, ns, record)`（或 `manager.Remember`）。该助手与框架内部写路径逐字段一致（见 `runtime` 包一致性测试），不要自己拼 record，否则打分会把 host-seed 与框架写入的记录区别对待。
- **「不按条数全量覆写」在 tier 下的等价说法**：tier 记录以 record ID 为主键 upsert，re-seed 用相同输入会生成新 ID（追加而非覆盖）；判断是否需要 re-seed 仍靠内容指纹 / generation，命中变化时让旧记录自然过期（单层形态不过期，可显式 `store.Delete` 或直接不 seed）。
- **Recall 语义对齐扁平 repo**：tier Recall 是打分选取（`RankMemories`：semantic×词面重合 + recency×e^(-age/168h) + importance×记录重要度，权重 `RecallWeights`，零值回退默认 {0.5/0.3/0.2}），再按时间序输出。要近似扁平 repo 的「尾部最近 N 条」：空 query + recency 主导权重（如 {0.01, 1.0, 0.01}）+ 大 `RecallBudget.Total`（如 200，并把 Hot/Warm/Cold 分层配额同步调大）。细节见 `pkg/memory/tier` 包 doc。
- **可观测**：tier 路径的 `EventMemoryRead` / `EventMemoryWrite` payload 带 `tiered: true`，与扁平 repo（无此标记）可区分。

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

事件视图用预设显式选择（读侧投影，EventStore 仍存全量）：

- 聊天 SSE：`WithStreamEventFilterPreset(core.EventFilterProductUI)`（隐藏 `MemoryRead` / `ContextPrepared`）
- Debug / 导出：`EventFilterDiagnostic`（默认；保留内部事件）
- `EventStore.ListEvents` / observability HTTP：`EventQuery.Preset` 或 `?preset=product_ui|diagnostic`

## Production HTTP（AF-010）

`httpx.NewProductionHTTPHandler` 已提供健康检查、异步 job、HITL resume 等生产路由；详见 [async-runtime.md](./async-runtime.md)。

取消与 context 贯通：`POST /v1/runs/{run_id}/cancel` / `POST /v1/jobs/{job_id}/cancel` 会取消 Worker 侧 job 上下文，正在执行的 `Framework.Run` / `HandleEvent` / `ResumeAndContinue` 收到 `context.Canceled`（见 async-runtime 文档「取消」一节）。
