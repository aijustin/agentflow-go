# 编排能力对齐 LangGraph 路线图

本文档定义 agentflow-go 与 **LangGraph 在编排与图能力上**的对齐目标与进度。  
对比总览见 [competitive-analysis-langgraph.md](./competitive-analysis-langgraph.md)。

**产品方向**：面向 Go 后端工程师；场景用 **Go 代码** 定义（`pkg/builder`），YAML 加载已 deprecated。不做 LangGraph 全量 parity。详见 [product-direction.md](./product-direction.md)。

## 对齐原则

1. **代码是一等公民**：`builder` / `core.Scenario` 为主路径；YAML 仅 legacy。
2. **够用图编排**：subgraph、map、checkpoint 已落地；不追加 `agent_loop` / 任意 interrupt 节点。
3. **Checkpoint 语义清晰**：RunState CAS + `ListRunSteps` / `ResumeFromStep`。
4. **显式接线**：Gateway、ToolExecutor、RunState 由宿主 Go 服务控制。

---

## 能力对照与进度

| LangGraph 能力 | agentflow-go 对应 / 目标 | 状态 |
|----------------|-------------------------|------|
| StateGraph 节点/边 | `orchestration.workflow` nodes/edges | ✅ |
| conditional edges | `edges[].condition` + 节点 `condition` | ✅ |
| 并行 super-step | `max_parallel` + 就绪批次调度 | ✅ |
| `parallel_group` / 多 agent | `parallel_group`、`supervisor` 节点 | ✅ |
| 有界循环 | `loop` 节点 | ✅ |
| **Subgraph（运行时嵌套）** | `orchestration.workflows` + `subgraph` 节点 | ✅ 已落地 |
| **Send / 动态 fan-out** | `map` 节点（按 `steps.*` 数组展开） | ✅ 已落地 |
| `interrupt()` 任意点暂停 | `human_gate` + tool pause + `before_final_answer` | ⚠️ 部分（缺任意节点 declarative interrupt） |
| Checkpoint 列表 / time-travel | RunState 版本 + StepOutputs | ✅ 已落地（`ListRunSteps` / `ResumeFromStep`） |
| Store 长期记忆 | tier memory + CognitiveMemory | ⚠️ 模型不同，语义对齐中 |
| Autonomous 作为图节点 | `agent_loop` 节点（图内 ReAct） | ⏸ 不做（用 `hybrid` + `autonomous`） |
| 流式图事件 | EventSink step/llm/tool 事件 | ✅ |
| Studio 可视化 | Observability HTTP + 事件详情 | ⚠️ 无专用图编辑器 |

图例：✅ 已有 · ⚠️ 部分 · 🔲 计划

---

## Phase 1：运行时 Subgraph（已完成）

### 配置

```yaml
orchestration:
  mode: fixed_workflow
  workflows:
    prep:
      nodes:
        - id: mark
          kind: transform
          input:
            set:
              ready: true
  workflow:
    nodes:
      - id: run_prep
        kind: subgraph
        ref: prep
```

### 行为

- `subgraph` 节点执行 `orchestration.workflows[ref]` 中的 DAG。
- 子图内 step 写入同一 `RunSnapshot.StepOutputs`（节点 ID 需在子图内唯一）。
- 父节点输出：`{ "subgraph": "<ref>", "steps": { "<inner_id>": ... } }`。

### 代码

- `pkg/core/workflow.go`：`NodeSubgraph`
- `pkg/core/types.go`：`Orchestration.Workflows`
- `internal/application/orchestration/workflow_subgraph.go`

---

## Phase 2：动态 Fan-out（`map` 节点，已完成）

**目标**：对齐 LangGraph `Send` — 运行时根据 `steps.*` 数组动态调度 N 个并行分支。

```yaml
- id: fanout
  kind: map
  input:
    items_path: steps.items.list
    branch:
      kind: agent
      ref: analyst
    on_error: collect_errors  # 可选，与 parallel_group 相同
```

**行为**

- 从 `items_path` 读取数组（如 `steps.items.list`），为每个元素并行执行 `branch`。
- 分支支持 `agent`、`tool`、`transform`；当前元素注入 branch input 的 `item` 字段（可用 `item_field` 改名）。
- 父节点输出：`{ "members": { "0": ..., "1": ... }, "errors": [...] }`（与 `parallel_group` 一致）。

**代码**

- `pkg/core/workflow.go`：`NodeMap`
- `internal/application/orchestration/workflow_map.go`

---

## Phase 3：Checkpoint 浏览与定点恢复（已完成）

**目标**：对齐 LangGraph time-travel（在 CAS 模型上）。

- `Framework.ListRunSteps(runID)` — 返回 step 输出与 snapshot 版本
- `Framework.ResumeFromStep(runID, nodeID)` — 从指定节点重跑

**截断规则**

- 删除 `nodeID` 及其 **下游** workflow 节点的 `StepOutputs`（按 `edges` / `depends_on` 传递依赖）。
- 同步删除动态 fan-out 子 step（如 `fanout.agent.analyst.0` 等 `{nodeID}.*` 前缀）。
- 保留上游节点输出；清除 `final` 与 `PendingGate`；snapshot 状态重置为 `running` 后重跑。
- 不支持从 `loop` body 或 `human_gate` 节点定点恢复（gate 请走 `ResumeAndContinue`）。

**代码**

- `framework_checkpoint.go`：`ListRunSteps`、`ResumeFromStep`
- `internal/application/orchestration/workflow_checkpoint.go`：下游截断与 runner 入口

---

## Phase 4–5（已裁剪）

- **`agent_loop`**：与 `hybrid` / `autonomous` 重叠，不单独做节点类型。
- **Declarative interrupt**：保留 `human_gate` + tool pause；不做任意节点 `interrupt:` 字段。
- **Studio / Store parity**：不在库范围内。

---

## 示例选型（编排视角，非治理视角）

| 目标 | 推荐 | 示例 |
|------|------|------|
| 多步 Research + 综合 | `hybrid` 或 subgraph + `agent_loop` | `multi_expert_research.yaml` |
| Agentic RAG 闭环 | `fixed_workflow` + RAG 节点 | `adaptive_rag.yaml` |
| 可复用子流程 | **`subgraph`** | 见 Phase 1 YAML |
| 开放 tool 循环 | `autonomous` | `autonomous.yaml` |
| 动态并行 | **`map`（计划）** | — |

---

## 相关文档

- [orchestration-flow.md](./orchestration-flow.md)
- [configuration-reference.md](./configuration-reference.md)
- [competitive-analysis-langgraph.md](./competitive-analysis-langgraph.md)
