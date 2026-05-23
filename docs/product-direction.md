# 产品方向（Go 后端工程师优先）

## 定位

`agentflow-go` 是 **可嵌入 Go 服务的 Agent 运行时库**，不是配置平台。

| 维度 | 选择 |
|------|------|
| 目标用户 | Go 后端工程师 |
| 场景定义 | **Go 代码**（`pkg/builder` 或 `core.Scenario`） |
| 运行时入口 | `agentflow.New(scenario, opts...)` |
| 编排 | `fixed_workflow` / `hybrid` / `autonomous`，够用即可 |
| 竞品关系 | 借鉴 LangGraph 编排概念，**不做全量 parity** |

## 推荐路径

```go
scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(gw), ...)
result, err := fw.Run(ctx, agentflow.RunRequest{...})
```

预设栈（与历史 YAML 示例等价）：见 [builder-reference.md](./builder-reference.md)、根包 [builder.go](../builder.go)、可运行示例 [examples/go/scenario](../examples/go/scenario/scenario.go)。

校验：`make validate-builder` 或 `agentflow.ValidateScenario(scenario)`。

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
| Phase 4 `agent_loop` 节点 | ⏸ 不做（用 `hybrid` + `autonomous`） |
| LangGraph Store 语义对齐 | ⏸ 不做 |
| Studio 级图编辑器 | ✅ P0–P7 已交付（Graph / Editor / Compare / Thread / YAML 导入导出 / interrupt 编辑 / i18n）；见 [studio-roadmap.md](./studio-roadmap.md) |

## 差异化（对外叙事）

- **类型安全 + IDE 重构**：场景即 Go 代码
- **显式接线**：Gateway、ToolExecutor、RunState 由宿主控制
- **可测试**：builder catalog 与 `ValidateScenario` 进 CI
- **Go 原生**：无 Python 运行时依赖

与 LangGraph：**我们更偏嵌入与显式治理；LangGraph 更偏 Python 生态与运行时灵活。**
