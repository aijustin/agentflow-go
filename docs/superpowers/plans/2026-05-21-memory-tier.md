# Memory Tier 实施计划

设计规格：[2026-05-21-memory-tier-design.md](../specs/2026-05-21-memory-tier-design.md)

## Phase 1：契约与策略（M5 记忆子项）

**目标**：落地 `pkg/memory/tier` 公共契约与可测试的升降级策略，无 runtime 接线。

- [x] 设计规格文档
- [x] `pkg/memory/tier`：`Level`、`Record`、`Policy`、`RecallBudget`
- [x] `Policy.ShouldPromote` / `ShouldDemote` / `TargetTier` 单元测试

**验收**：

- `go test ./pkg/memory/tier/...` 通过
- competitive-analysis 中「分层记忆」可引用本计划

## Phase 2：Runtime 与 YAML（M5）

**目标**：Framework 可声明 tier 策略，runtime 读写走 TierManager。

- [x] `scenario.memories.<name>.tiers` schema 与校验
- [x] `internal/adapter/memory/tier/inmem` CompositeStore
- [x] `TierManager` Remember/Recall/Reconcile 实现
- [x] `Framework` Option：`WithTierMemory` / `WithTierStore`
- [x] `runtime_memory.go` 分支：tier enabled 时走 Manager
- [x] 事件：`EventMemoryPromoted` / `EventMemoryDemoted` / `EventMemoryEvicted`
- [x] 示例 YAML + `examples/go/tier-memory/main.go`

**验收**：

- 集成测试：hot 满 demote、access promote、recall budget 分配
- `make validate-examples` 含 tier 场景

## Phase 3：生产适配器（M5/M6）

**目标**：warm/cold 持久化与 async reconcile。

- [x] Postgres warm store（JSONB + tier 索引）
- [x] S3/Blob cold store（gzip JSON lines）
- [x] Async job type `memory.reconcile` + Worker handler
- [x] Prometheus 指标与 OTel span
- [x] 租户隔离集成测试

**验收**：

- 重启后 hot 丢失、warm/cold 可 recall
- `/metrics` 暴露 tier 深度与迁移计数

## Phase 4：认知记忆统一（M5）

**目标**：`CognitiveMemory` 与 TierManager 合并 recall 路径。

- [x] `Remember` 双写 cognitive score + tier metadata
- [x] `Recall` 统一 `RankMemories` + tier budget
- [x] competitive-analysis「认知记忆端口」🟡 → ✅

## 依赖与顺序

```text
Phase 1 (pkg/memory/tier 契约)
    → Phase 2 (runtime + YAML)
        → Phase 3 (持久化 + worker)
            → Phase 4 (cognitive 统一)
```

## 里程碑映射

| 里程碑 | 内容 |
|--------|------|
| **M5** | Phase 1–4 全部；RAG 与 tier cold 摘要协同 |
| **M6** | Phase 3 reconcile job 纳入 reference Compose / Helm 示例 |

## M6：Reference 部署（已完成）

- [x] `examples/go/tier-worker` — Postgres warm + file cold + `WithJobQueue`
- [x] `examples/deploy/init/apply-migrations.sh` — 0001 + 0002
- [x] `examples/deploy/kubernetes/tier-worker.yaml`
- [x] `examples/deploy/helm/agentflow-reference/`
- [x] `http-worker` 共享 queue 与 `WithJobQueue` 接线
