# 分层记忆（Memory Tier）设计

## 背景

当前 `pkg/memory` 通过 `Scope`（`conversation` / `session` / `long_term` / `audit`）和 LLM profile 的 `memory_recall_limit` 实现基础记忆边界，但缺少：

- 按访问频率/重要性/成本的 **hot / warm / cold** 分层
- 跨层 **自动升降级** 与容量回收
- 与 `CognitiveMemory` 的统一 recall 预算分配
- 可观测的 tier 迁移事件与租户隔离

本设计引入 `pkg/memory/tier`，作为 **Scope 之上的存储与召回策略层**，不替代现有 `Repository` 与 `CognitiveMemory` 端口。

## 目标

1. 支持 hot / warm / cold 三层（可扩展 archive）记忆存储与召回。
2. 基于访问模式、重要性、容量压力自动 promote / demote。
3. 与现有 runtime 记忆读写、`memory_recall_limit`、RBAC、脱敏兼容。
4. 保持 hexagonal 风格：核心契约在 `pkg/memory/tier`，适配器在 `internal/adapter/memory/*`。
5. 可通过 scenario YAML 或 `Framework` Option 声明 tier 策略。

## 非目标（第一切片）

- 向量语义检索（继续由 `CognitiveMemory.RankMemories` / RAG 负责）。
- 替换现有 `Scope` 枚举或 `Repository` 接口。
- 强制引入 Redis/S3 依赖（适配器由宿主注入）。
- 跨租户记忆共享或联邦检索。

## 概念模型

```text
Agent Run
   │
   ▼
TierManager.Recall(query, budget)
   ├─ hot store   (低延迟，小容量，最近 N 次访问)
   ├─ warm store  (中延迟，中容量，会话/用户长期事实)
   └─ cold store  (高延迟，大容量，归档与低频知识)
   │
   ▼
合并排序 → LLM context / Cognitive rank
```

### 与现有组件关系

| 组件 | 角色 | 关系 |
|------|------|------|
| `memory.Scope` | 命名空间边界 | Tier 在 `long_term` / `session` scope 内生效；`conversation` 默认仅 hot |
| `memory.Repository` | KV/append 持久化 | 每层 tier 可映射到不同 Repository 或同一 repo 的不同 key 前缀 |
| `memory.CognitiveMemory` | 打分 recall | TierManager 输出候选集，Cognitive rank 做最终排序 |
| `contextwindow.Policy.MemoryRecallLimit` | LLM 注入条数上限 | Tier recall budget 先按 tier 分配，再受全局 limit 截断 |

## 核心类型（`pkg/memory/tier`）

### Tier 枚举

```go
type Level string

const (
    LevelHot  Level = "hot"   // 进程内 / Redis，毫秒级
    LevelWarm Level = "warm"  // Postgres / file，十毫秒级
    LevelCold Level = "cold"  // 对象存储 / 压缩 blob，百毫秒级+
)
```

### Record

在 `CognitiveRecord` 之上增加 tier 元数据：

```go
type Record struct {
    memory.CognitiveRecord
    Tier         Level
    AccessCount  int
    LastAccessAt time.Time
    PromotedAt   time.Time
    SizeBytes    int64
    Pinned       bool // 为 true 时跳过 demotion
}
```

### Store 端口

```go
type Store interface {
    Put(ctx context.Context, ns memory.Namespace, record Record) error
    Get(ctx context.Context, ns memory.Namespace, id string) (Record, error)
    List(ctx context.Context, ns memory.Namespace, level Level, limit int) ([]Record, error)
    Delete(ctx context.Context, ns memory.Namespace, id string) error
}
```

实现策略：

- **CompositeStore**：包装 hot/warm/cold 三个底层 `Store` 或 `Repository`。
- **InMemoryStore**（第一切片）：单进程测试与示例。
- **PostgresStore / BlobStore**（后续切片）：warm/cold 生产适配器。

### Policy 端口

```go
type Policy struct {
    HotCapacity   int           // 每层最大条目数，0=不限
    WarmCapacity  int
    ColdCapacity  int
    HotTTL        time.Duration // 超过 TTL 且无 pinned 则 demote
    WarmTTL       time.Duration
    PromoteAccess int           // AccessCount 达到阈值 promote
    DemoteIdle    time.Duration // LastAccessAt 超过则 demote
}

func (p Policy) TargetTier(record Record, now time.Time) Level
func (p Policy) ShouldPromote(record Record, now time.Time) bool
func (p Policy) ShouldDemote(record Record, now time.Time) bool
```

升降级规则（默认）：

| 事件 | 动作 |
|------|------|
| 新写入 | 默认 `hot` |
| `AccessCount >= PromoteAccess` 且当前 warm | → 保持 hot |
| hot 超 `HotCapacity` 或超 `HotTTL` 且未 pinned | hot → warm |
| warm 超 `WarmCapacity` 或超 `WarmTTL` | warm → cold |
| cold 超 `ColdCapacity` | 删除或归档（可配置 `EvictionMode`） |
| `Pinned=true` | 不参与 demotion |

### Manager 端口

```go
type Manager interface {
    Remember(ctx context.Context, ns memory.Namespace, record Record) error
    Recall(ctx context.Context, ns memory.Namespace, query string, budget RecallBudget) ([]Record, error)
    Reconcile(ctx context.Context, ns memory.Namespace) (MigrationReport, error)
}

type RecallBudget struct {
    Total int
    Hot   int
    Warm  int
    Cold  int
}
```

`Recall` 流程：

1. 并行/顺序从 hot → warm → cold 拉取候选（受 `RecallBudget` 约束）。
2. 更新 `AccessCount` / `LastAccessAt`。
3. 对命中记录评估 `ShouldPromote`，必要时迁移。
4. 使用 `memory.RankMemories` 对合并结果排序。
5. 返回 top-N（N = `budget.Total`）。

`Reconcile` 由 async worker 或定时任务调用，批量 demote / evict。

## Scenario YAML 扩展

```yaml
scenario:
  memories:
    agent_memory:
      type: custom
      scope: long_term
      namespace: support
      tiers:
        enabled: true
        hot_capacity: 50
        warm_capacity: 500
        cold_capacity: 5000
        hot_ttl: 24h
        warm_ttl: 720h
        promote_access: 3
        demote_idle: 168h
        recall_budget:
          total: 20
          hot: 12
          warm: 6
          cold: 2
```

校验规则：

- `tiers.enabled: true` 时 `type` 必须为 `custom` 或宿主已注册 tier store。
- `recall_budget.total` 不得超过 agent LLM profile 的 `memory_recall_limit`（警告或校验失败）。
- 租户隔离：`namespace` 前缀自动拼接 `tenant_id`（与 retriever 一致）。

## Framework 集成

```go
// 根门面（计划）
agentflow.WithTierMemory(name string, manager tier.Manager)
agentflow.WithTierStore(name string, store tier.Store, policy tier.Policy)
```

Runtime 变更（Phase 2）：

1. `readMemory`：若 agent 绑定 tier memory，走 `TierManager.Recall` 而非直接 `Repository.Get("messages")`。
2. `writeMemory`：assistant/user/tool 观测写入 `TierManager.Remember`，hot 层同步，warm/cold 异步 reconcile。
3. 事件：`EventMemoryPromoted`、`EventMemoryDemoted`、`EventMemoryEvicted`（`pkg/core/event.go` 追加）。
4. RBAC：沿用 `ActionMemoryRead` / `ActionMemoryWrite`，resource metadata 增加 `tier`。
5. 审计：迁移记录 `audit.EventMemoryTierChanged`。

## 可观测性

| 信号 | 字段 |
|------|------|
| 指标 | `agentflow_memory_tier_records{level,scenario}` gauge |
| 指标 | `agentflow_memory_tier_migrations_total{from,to}` counter |
| 指标 | `agentflow_memory_recall_latency_seconds{tier}` histogram |
| 追踪 | span `agentflow.memory.tier.recall` / `.migrate` |
| 日志 | `run_id`, `tenant_id`, `memory`, `from_tier`, `to_tier`, `record_id` |

## 安全与治理

- 租户：`memory.Namespace` + tenant 前缀，与 `runstate.StampTenant` 一致。
- 脱敏：写入 cold 前必须经过 `OutputRedactor`（与 `writeMemory` 相同路径）。
- Pinned 记录仅 `RoleAdmin` / `RoleOperator` 可设置（HTTP API 后续切片）。
- Cold 层读取计入 RBAC `ActionMemoryRead`；批量 reconcile 使用 service principal。

## 测试策略

| 层级 | 内容 |
|------|------|
| 单元 | `Policy.ShouldPromote/Demote`、`RecallBudget` 分配、`RankMemories` 集成 |
| 单元 | `CompositeStore` 迁移 CAS |
| 集成 | hot 满 → demote warm；access 达阈值 → promote |
| 集成 | `memory_recall_limit` 与 tier budget 叠加 |
| 集成 | 租户 A 无法 recall 租户 B cold 记录 |

## 交付阶段

见 [../plans/2026-05-21-memory-tier.md](../plans/2026-05-21-memory-tier.md)。

### Phase 1（本 PR 范围）

- [x] 设计规格（本文档）
- [x] `pkg/memory/tier` 类型、`Policy`、纯函数升降级逻辑、单元测试
- [ ] Framework / runtime 接线（Phase 2）
- [ ] Postgres/Blob 适配器（Phase 3）

## 开放问题

1. **Cold 删除 vs 归档**：默认 delete，可选 `archive_blob_ref` 写入 audit-only scope。
2. **Tool 结果记忆**：是否默认 cold？建议 tool 大结果直接 Blob，tier 只存摘要。
3. **跨 Agent 共享 warm**：同一 `session` namespace 下多 Agent 是否共享 warm 层？默认 per-agent 隔离，与现 `session:agent` 键一致。

## 参考

- [competitive-analysis.md](../../competitive-analysis.md)
- [configuration-reference.md](../../configuration-reference.md) 记忆章节
- `pkg/memory/memory.go`、`pkg/memory/cognitive.go`
- `internal/application/runtime/runtime_memory.go`
