# 后续里程碑（P6 + 企业 + Memory Tier）

在 Studio P0–P5 交付之后，三条并行演进线：

## Studio P6 — Editor 生产体验

| 项 | 状态 |
|----|------|
| 结构化 API 错误码 + UI i18n | ✅ `pkg/studio/errors.go` |
| Save 后 reload + 保留 subgraph 上下文 | ✅ |
| Editor 多选 / 删边 / 拖线连边 | ✅ |
| API 错误全路由 i18n | ✅ Observability API 结构化错误 + UI i18n |
| Save 响应携带 graph（省 GET） | ✅ Save 响应 merge layout |

## 企业 M1 — Retention / GC

| 项 | 状态 |
|----|------|
| `PurgeRuns` / `PurgeExpired` / `PurgeOrphanBlobs` | ✅ |
| `PurgeWithPolicy` 终态语义与 `PurgeExpired` 对齐 | ✅ |
| 跨进程 file runstate + blob 集成测试 | ✅ `internal/integration/retention_integration_test.go` |
| S3 `BlobAdmin` List/Delete | ✅ `internal/adapter/blob/s3/store.go` |
| 定时 Retention worker / HTTP admin | ✅ `/v1/admin/retention/*` + http-worker ticker |

## Memory Tier — Warm/Cold 生产化

| 项 | 状态 |
|----|------|
| Postgres warm + file gzip cold | ✅ |
| `RecordTierDepth` 指标（tier-worker） | ✅ |
| Blob/S3 cold tier adapter | ✅ `internal/adapter/memory/tier/blobcold` + tier-worker `AGENT_TIER_COLD_BACKEND=blob` |
| 迁移事件 `EventMemoryPromoted/Demoted` | ✅ `pkg/memory/tier/event_observer.go` |
| RAG + cold 摘要协同（M5） | ✅ cold_summary + 可选向量索引 + LLM 摘要（`summary_profile` / `WithTierColdSummarizer`） |

## Studio P7 — YAML 导入与 interrupt 编辑

| 项 | 状态 |
|----|------|
| Import YAML API + Editor UI | ✅ |
| Editor 快捷键（Save/Undo/Delete） | ✅ |
| Declarative interrupt 节点编辑 | ✅ |

## Studio P9 — Graph 调试体验（已完成）

| 项 | 状态 |
|----|------|
| **Graph 节点 Inspector**（step 输出 + 关联事件） | ✅ |
| **Timeline ↔ Graph 节点联动**（事件 payload `node_id`） | ✅ |
| **Checkpoint 时间轴 scrub** + revision diff | ✅ |
| **从此 checkpoint 分叉**（Time Travel 栏） | ✅ |
| Autonomous tool loop overlay（Graph 下方 LLM/Tool 时间线） | ✅ P9.2 |
| Builder `MapOver` / `RouteIf` / 条件 DSL | ✅ P8 |

## Builder P8 — Workflow DSL（已完成）

| 项 | 状态 |
|----|------|
| `StepPath` / `ConditionEq` 等条件 helper | ✅ |
| `MapOver` + `Map*Branch` fan-out | ✅ |
| `RouteIf` 条件边 | ✅ |

## Studio P10 — Graph 调试增强（已完成）

| 项 | 状态 |
|----|------|
| 运行中 Graph 实时高亮（SSE + steps 刷新） | ✅ |
| Subgraph 钻取（双击 / Inspector 按钮） | ✅ |
| Inspector 内嵌事件 payload 预览 | ✅ |
| Compare 共享步骤输出 diff | ✅ |

## 相关文档

- [studio-roadmap.md](./studio-roadmap.md)
- [enterprise-roadmap.md](./enterprise-roadmap.md)
- [data-lifecycle.md](./data-lifecycle.md)
- [superpowers/plans/2026-05-21-memory-tier.md](./superpowers/plans/2026-05-21-memory-tier.md)
