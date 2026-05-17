# 可观测性、审计与治理设计

企业级 Agent 运行时需要始终开启的信号，用来解释发生了什么、由谁触发、成本是多少，以及某个决策为什么被允许或拒绝。

## 信号

### 日志

使用 `log/slog` 输出结构化日志。运行时事件和审计的 `slog` sink 可通过 `agentflow.NewSlogEventSink` 与 `agentflow.NewSlogAuditSink` 使用。标准字段包括：

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

框架已提供轻量级指标端口 `pkg/observability.Recorder` 和事件适配器 `agentflow.NewObservabilityEventSink`。当前适配器会把 `core.EventSink` 事件转换为低基数指标，例如 `agentflow_runtime_events_total`，并可继续转发给已有事件 sink。Prometheus exporter 仍建议放在应用侧或后续可选适配器中，以免核心库强制引入运行时依赖。

```go
eventSink := agentflow.NewObservabilityEventSink(
	prometheusRecorder,
	otelTracer,
	agentflow.NewSlogEventSink(logger),
)

fw, err := agentflow.NewFromFile("scenario.yaml", agentflow.WithEventSink(eventSink))
```

初始 Prometheus 指标应覆盖：

- 按状态统计运行数量。
- 运行耗时直方图。
- 工作流步骤耗时直方图。
- LLM 请求数、延迟、token 用量和错误数。
- 工具请求数、延迟、副作用等级和错误数。
- 队列深度、租约恢复次数、重试次数和死信数量。
- HITL 暂停次数和决策次数。

标签必须保持有限基数。使用路由模式和枚举值，不要使用用户 ID 或原始 prompt。

### 链路追踪

框架已定义 `pkg/observability.Tracer` 与标准 span 名称，例如 `agentflow.runtime.event`、`agentflow.run`、`agentflow.tool.call` 和 `agentflow.queue.job`。`NewObservabilityEventSink` 可以把运行时事件映射为追踪 span；完整 OpenTelemetry SDK 接入由宿主应用注入具体 tracer/exporter。

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