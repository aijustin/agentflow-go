# AgentFlow Studio 路线图（P0）

面向 Go 后端工程师的 **只读 Graph Debug + Time Travel UI**，嵌入现有 Observability 面板；全功能可视化编辑器另立项。

## P0 已交付（本迭代）

| 能力 | API | 说明 |
|------|-----|------|
| 场景图导出 | `GET /observability/api/graph` | 嵌套 workflow + subgraph 拓扑 |
| Step 列表 | `GET /observability/api/runs/{id}/steps` | 对齐 `Framework.ListRunSteps` |
| 定点重跑 | `POST /observability/api/runs/{id}/resume-from-step` | body: `{"node_id":"..."}` |
| Graph View | Observability UI → **Graph** 标签 | 节点高亮（done/current） |
| Time Travel | Graph 中选节点 → **Resume from selected node** | 需配置 `Framework` |
| Subgraph v2 | 运行时 | 内层 step 命名空间 `{parent}::{inner}` |
| Map → subgraph | `map.branch.kind: subgraph` | 每项 fan-out 执行命名子图 |

### 接线

```go
dashboard, err := agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
    Store:     eventStore,
    Hub:       eventHub,
    Framework: fw, // 启用 graph / steps / resume-from-step
})
```

Go API：

```go
graph := fw.ExportScenarioGraph()
steps, err := fw.ListRunSteps(ctx, runID)
result, err := fw.ResumeFromStep(ctx, runID, "review")
```

## P1（下一步）

- Checkpoint **历史链**（append-only snapshots，真正逐步回放）
- Graph 运行时 overlay：SubgraphStarted/Completed 事件驱动嵌套高亮
- `loop` / `human_gate` 的 time-travel 边界与 UI 提示
- Production HTTP：`POST /v1/runs/{id}/resume-from-step`（与 Observability 并列）

## P2（产品级，另立项）

- LangSmith Studio 级 **拖拽图编辑器**
- 编辑 → 生成 builder 代码 / `ValidateScenario`
- 多 run 对比、fork state、thread 视图

## 相关文档

- [orchestration-parity.md](./orchestration-parity.md)
- [observability-dashboard.md](./observability-dashboard.md)
- [competitive-analysis-langgraph.md](./competitive-analysis-langgraph.md)
