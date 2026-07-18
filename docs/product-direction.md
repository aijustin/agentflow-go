# 产品方向（Go 后端工程师优先）

## 定位

`agentflow-go` 是 **可嵌入 Go 服务的 Agent 运行时库**，不是配置平台。

| 维度 | 选择 |
|------|------|
| 目标用户 | Go 后端工程师 |
| 场景定义 | **Go 代码**（`pkg/builder` 或 `core.Scenario`） |
| 运行时入口 | `agentflow.New(scenario, opts...)` |
| 编排 | 三模式光谱见下；**默认与承诺深度 = `autonomous`** |
| 竞品关系 | 借鉴 LangGraph 编排概念，**不做全量 parity** |

三模式设计理由、选型与激活触发条件：[orchestration-modes.md](./orchestration-modes.md)。  
Codex 对照（吸收 / 不吸收）：[competitive-notes-codex.md](./competitive-notes-codex.md)。

## 编排模式与投资优先级

三模式（`autonomous` / `fixed_workflow` / `hybrid`）覆盖 Workflow ↔ Agent 光谱，是**稳定扩展契约**，不是三条同等权重的产品主线。

| 优先级 | 模式 | 态度 |
|--------|------|------|
| 1（现在） | `autonomous` | **做深**：HITL、StreamRun、治理、context、并行工具等 |
| 2（骨架） | `hybrid` | **保持能跑通**；文档写清选型；不扩新组合栈 |
| 3（冻结扩面） | `fixed_workflow` | **已实现 capability**；新节点 / 新 RAG·审批 catalog / Studio 多场景 → Frozen |

**激活** fixed_workflow / hybrid 投入的触发条件见 [orchestration-modes.md §5](./orchestration-modes.md#5-激活-fixed_workflow--hybrid-投入的触发条件)。

## 推荐路径

```go
scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(gw), ...)
result, err := fw.Run(ctx, agentflow.RunRequest{...})
```

预设栈（与历史 YAML 示例等价）：见 [builder-reference.md](./builder-reference.md)、根包 [builder.go](../builder.go)、可运行示例 [examples/go/scenario](../examples/go/scenario/scenario.go)。

校验：

- 默认（CI）：`make validate-builder` → `builder.CoreCatalog()`（autonomous 核心）
- 全量示例：`go run ./examples/go/validate -kind builder full` → `ExampleCatalog()`

## YAML 状态

公共 YAML 加载 API（`LoadScenarioFile` / `LoadScenario` / `NewFromFile`）**已移除**（v0.2 起）。

- 新场景：**仅** `pkg/builder` 或 `core.Scenario`
- Studio：`ImportStudioScenarioYAML` / `GenerateStudioScenarioYAML` / `SaveStudioGraph` 仍使用内部 YAML 编解码
- 校验：`agentflow.ValidateScenario(scenario)` 与 JSON Schema 仍可用于 struct 字段对照
- `examples/go/validate` 仅支持 `-kind builder|tool|skill`

## 编排路线图（裁剪后）

| 项 | 状态 |
|----|------|
| subgraph / map / ListRunSteps / ResumeFromStep | ✅ 已落地 |
| Declarative interrupt（`interrupt: true` post-step pause） | ✅ workflow 节点 + builder + Studio P7 |
| Studio Graph 调试（P9–P10） | ✅ Inspector、checkpoint scrub、autonomous trace、subgraph 钻取 |
| Phase 4 `agent_loop` 节点 | ⏸ 不做（用 `hybrid` + `autonomous`） |
| LangGraph Store 语义对齐 | ⏸ 不做 |
| Studio 级图编辑器 | ✅ P0–P10 已交付；见 [studio-roadmap.md](./studio-roadmap.md) |
| 新 workflow 节点 / RAG·审批 catalog 扩面 | 🧊 Frozen（见 [orchestration-modes.md](./orchestration-modes.md) 触发条件） |
| Studio 多场景体验扩面 | 🧊 Frozen（同上） |

## 差异化（对外叙事）

- **类型安全 + IDE 重构**：场景即 Go 代码
- **显式接线**：Gateway、ToolExecutor、RunState 由宿主控制
- **可测试**：`CoreCatalog` 与 `ValidateScenario` 进默认 CI
- **Go 原生**：无 Python 运行时依赖
- **默认路径 = autonomous**：workflow / hybrid 是扩展能力，不是默认叙事主角

与 LangGraph：**我们更偏嵌入与显式治理；LangGraph 更偏 Python 生态与运行时灵活。**
