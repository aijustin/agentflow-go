# 可观测性、审计与治理设计

企业级 Agent 运行时需要始终开启的信号，用来解释发生了什么、由谁触发、成本是多少，以及某个决策为什么被允许或拒绝。

## 信号

### 日志

使用 `log/slog` 输出结构化日志。运行时事件和审计的 `slog` sink 可通过 `adapters.NewSlogEventSink` 与 `adapters.NewSlogAuditSink` 使用。标准字段包括：

- `run_id`
- `job_id`
- `tenant_id`
- `workspace_id`
- `project_id`
- `agent`
- `tool`
- `workflow_node`
- `event_type`
- `trace_id`

除非脱敏策略明确允许，否则不要记录 prompt 正文、工具凭证、Provider API key、HITL token 或原始结构化输出。

### 指标

框架已提供轻量级指标端口 `pkg/observability.Recorder` 和事件适配器 `adapters.NewObservabilityEventSink`。`adapters.NewPrometheusRecorder` 提供零依赖的 Prometheus text exposition，`PrometheusMetricsHandler` 可挂载到 `NewProductionHTTPHandler` 的 `/metrics` 路由。

```go
recorder := adapters.NewPrometheusRecorder()
eventSink := adapters.NewObservabilityEventSink(
	recorder,
	otelTracer,
	adapters.NewSlogEventSink(logger),
)

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithRecorder(recorder),
	agentflow.WithEventSink(eventSink),
)

handler, err := httpx.NewProductionHTTPHandler(httpx.ProductionHTTPHandlerConfig{
	Queue:          queue,
	Framework:      fw,
	MetricsHandler: adapters.PrometheusMetricsHandler(recorder),
})
```

初始 Prometheus 指标应覆盖：

- 按状态统计运行数量。已通过 `agentflow_runtime_events_total{event, scenario}` 实现。
- 运行耗时直方图。已通过 `agentflow_run_duration_seconds` 实现。
- 工作流步骤耗时直方图。
- LLM 请求数、延迟、token 用量和错误数。已通过 `agentflow_llm_duration_seconds{profile}`、`agentflow_llm_tokens_total{profile, kind=prompt|completion}`、`agentflow_llm_errors_total{profile}` 实现；一次逻辑调用（含重试）计一次延迟/错误，retry attempt 只作为 span 属性。
- 工具请求数、延迟、副作用等级和错误数。延迟为 `agentflow_tool_duration_seconds{tool, scenario}`，错误数为 `agentflow_tool_errors_total{tool, scenario}`（按失败 attempt 计数，与工具 span 的 RecordError 一致）。
- 队列深度、租约恢复次数、重试次数和死信数量。
- HITL 暂停次数和决策次数。

标签必须保持有限基数。使用路由模式和枚举值，不要使用用户 ID 或原始 prompt。

### LLM 事件 payload 脱敏

`LLMCalled` 事件默认不持久化 prompt/messages 明文（可能含 PII）：payload 只携带 `message_count`、各消息的 `role`/`content_chars` 及工具路由元数据，外加截断到 16 个 hex 字符的 `messages_hash`（sha256），用于关联相同输入。调试需要明文时用 `agentflow.WithLLMPayloadCapture(true)` 显式开启；开启后 payload 附带 messages 正文与 `prompt` 字段（Studio / Debug drawer 的 LLM 入参展示仅在此模式下可用），且仍会先经过 `WithOutputRedactor` 配置的脱敏器再写入事件存储。

### 可观测面板

框架现在提供面向运行时会话的可观测面板。它通过 `core.EventSink` 采集事件，并将事件追加到 `observability.EventStore`，再通过 `EventHub` 向浏览器提供 Server-Sent Events 实时更新。面板可以查看：

- 当前和历史会话、状态、场景名、最后更新时间和事件数。
- 会话内的编排时序，包括 run、step、tool、LLM、memory、human gate 等事件。
- 每个事件的 trace/span、时间戳、序号和 JSON payload，便于展开输入/输出或工具返回摘要。

最小接入方式：

```go
eventStore, err := adapters.NewPostgresEventStore(ctx, adapters.PostgresEventStoreConfig{DB: db})
if err != nil {
	log.Fatal(err)
}
eventHub := adapters.NewEventHub()

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithEventSink(adapters.NewEventFanoutSink(
		adapters.NewEventStoreSink(eventStore, eventHub),
		adapters.NewObservabilityEventSink(recorder, tracer, adapters.NewSlogEventSink(logger)),
	)),
)

dashboard, err := httpx.NewObservabilityHTTPHandler(httpx.ObservabilityHTTPHandlerConfig{
	Store: eventStore,
	Hub:   eventHub,
})
```

`NewPostgresEventStore` 默认自动执行幂等建表，创建 `agentflow_runtime_events` 和查询索引；受控生产环境也可以先执行 [migrations/postgres/0001_agentflow_core.up.sql](../migrations/postgres/0001_agentflow_core.up.sql)，再设置 `SkipSchemaSetup: true`。完整说明见 [docs/observability-dashboard.md](observability-dashboard.md)。

### 链路追踪

框架已定义 `pkg/observability.Tracer` 与标准 span 名称，例如 `agentflow.runtime.event`、`agentflow.run`、`agentflow.tool.call`、`agentflow.llm.call` 和 `agentflow.queue.job`。`NewObservabilityEventSink` 会把运行时事件映射为追踪 span；运行时 `Run` 与工具调用也会创建对应 span。

`agentflow.llm.call` 包裹一次逻辑 LLM 调用（chat / 工具循环逐步 / structured / 流式 StreamChat）：属性含 `run_id`、`agent`、`profile`、`model`（若 profile 声明）、`attempt`（重试次数，随每次 attempt 更新）、`tools`/`step`/`stream`/`structured`（按路径）；一次逻辑调用一个 span，失败时 `RecordError` + `SetStatus(Error)`。流式 span 随流结束闭合（成功 / 错误 chunk / 取消三路都会 End）。`RunHybrid` 的所有失败分支（agent 解析、output_schema 校验、answer 错误）与 `Run` 一样在 run span 上 `RecordError`。

OpenTelemetry 接入方式：

1. **宿主已有 TracerProvider**：注入 `go.opentelemetry.io/otel/trace.Tracer`。
2. **本地开发**：使用 `NewOpenTelemetryStdoutTracerProvider` 导出 JSON span 到 stdout。

```go
provider, err := adapters.NewOpenTelemetryStdoutTracerProvider(ctx, adapters.OpenTelemetryTracerProviderConfig{
	ServiceName:    "my-service",
	ServiceVersion: agentflow.Version,
})
if err != nil {
	log.Fatal(err)
}
defer provider.Shutdown(ctx)

tracer := adapters.OpenTelemetryTracerFromProvider(provider, "my-service/agentflow")

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithTracer(tracer),
	agentflow.WithEventSink(adapters.NewObservabilityEventSink(
		recorder,
		tracer,
		adapters.NewSlogEventSink(logger),
	)),
)
```

生产环境通常由宿主配置 OTLP/Jaeger 等 exporter，再通过 `NewOpenTelemetryTracer(hostTracer)` 接入；框架会把 OTel span context 同步到 `core.Event` 的 `trace_id`/`span_id` 字段。

OpenTelemetry span 应包裹：

- HTTP 请求处理。
- 队列入队、租约、完成、失败和取消操作。
- 运行时执行。
- 工作流节点执行。
- LLM 调用。
- 工具调用。
- 记忆读写。
- RunState 和 BlobStore 操作。

### 审计

审计事件应能回答合规问题：

- 谁提交了运行？
- 谁批准、拒绝或修订了 HITL checkpoint？
- 哪些工具被调用，以及它们的副作用分类是什么？
- 哪个策略拒绝了某个动作？
- 哪个租户、工作区或项目拥有该动作？

审计事件应该持久化、仅追加，并完成脱敏。

## 治理策略

初始策略包括：

- 单次运行最大成本。
- 单次运行最大 LLM 调用次数。
- 单次运行最大工具调用次数。已通过 `governance.NewToolBudgetPolicy` 实现。
- 工具副作用审批。已通过 `governance.NewMaxSideEffectPolicy` 实现，并通过 `agentflow.WithToolGovernancePolicy` 接入。
- 租户数据边界强制执行。
- 持久化或外部交付前的输出脱敏。已通过 `governance.NewJSONFieldRedactor` 和 `agentflow.WithOutputRedactor` 对 runtime step output 持久化路径实现。
- Provider 能力降级规则。

策略检查应同时发出可观测性事件和审计记录。

## 第一批实现切片

1. 增加接收运行时操作事件的可观测性端口。已通过 `core.EventSink` 完成。
2. 增加 no-op 实现和 `slog` 实现。已通过 `NewSlogEventSink` 完成。
3. 增加审计 sink 端口和内存/文件适配器。已在 `pkg/audit`、`NewInMemoryAuditSink`、`NewFileAuditSink` 中完成。
4. 增加预算、工具副作用、输出脱敏的策略接口。已在 `pkg/governance` 中完成；运行时工具治理和持久化输出脱敏已接入。
5. 指标与追踪端口已通过 `pkg/observability` 和 `NewObservabilityEventSink` 实现；依赖评审后可在可选集成包中增加 Prometheus/OpenTelemetry 具体适配器。
6. 运行时事件仓库、PostgreSQL 自动建表、实时 EventHub 和可观测 HTTP 面板已通过 `NewPostgresEventStore`、`NewEventStoreSink`、`NewEventHub` 和 `NewObservabilityHTTPHandler` 提供。