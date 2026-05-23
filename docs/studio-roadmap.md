# AgentFlow Studio 路线图（P0）

面向 Go 后端工程师的 **Graph Debug + Time Travel + 可视化 Editor UI**，嵌入现有 Observability 面板。

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

## P1（已交付）

| 能力 | 状态 |
|------|------|
| Production HTTP `GET/POST /v1/runs/{id}/steps` / `resume-from-step` | ✅ |
| Graph 节点 `resumable` / `resume_hint` 元数据 | ✅ |
| Subgraph 事件 Graph overlay | ✅ |
| `http-worker` 示例挂载 `/observability/` + Framework | ✅ |
| Checkpoint **历史链**（append-only snapshots） | ✅ |

### Checkpoint 历史链接线

```go
fw, err := agentflow.New(scenario,
    agentflow.WithCheckpointHistory(agentflow.NewInMemoryCheckpointHistory()),
)
```

Go API：

```go
checkpoints, err := fw.ListRunCheckpoints(ctx, runID, 50)
snapshot, err := fw.GetRunCheckpoint(ctx, runID, version)
result, err := fw.ResumeFromCheckpoint(ctx, runID, version)
```

HTTP（Observability + Production）：

- `GET .../checkpoints?limit=50`
- `GET .../checkpoints/{version}`
- `POST .../resume-from-checkpoint` body: `{"version": 3}`

本地示例：`go run ./examples/go/http-worker/main.go` → `http://127.0.0.1:7060/observability/`（默认端口，可用 `AGENT_HTTP_ADDR` 覆盖）

## P2（已交付 MVP）

| 能力 | 状态 |
|------|------|
| **拖拽图编辑器**（Editor 标签） | ✅ 节点拖拽 / 增删 / 连边 |
| 编辑 → `ValidateScenario` | ✅ `POST /api/studio/validate` |
| 编辑 → builder Go codegen | ✅ `POST /api/studio/codegen` |
| 多 run 对比 | ✅ `GET /api/compare?run_a=&run_b=` + Compare 标签 |
| Fork state（新 run_id） | ✅ `POST /api/runs/{id}/fork` |
| Thread 视图 | ✅ `GET /api/runs/{id}/thread` + Thread 标签 |

### Studio 编辑与校验

```go
result, err := fw.ValidateStudioGraph(ctx, editedGraph)
code, err := fw.GenerateStudioBuilderCode(ctx, editedGraph)
```

HTTP：

- `POST /observability/api/studio/validate` — body: `ScenarioGraph` JSON
- `POST /observability/api/studio/codegen` — 返回 builder Go 代码
- `GET /observability/api/compare?run_a=&run_b=`
- `GET /observability/api/runs/{id}/thread`
- `POST /observability/api/runs/{id}/fork` — body: `{"version":0}` 可选
- Production: `POST /v1/runs/{id}/fork`

### P2+（已交付）

| 能力 | 状态 |
|------|------|
| 节点 **input JSON** 可视化编辑 | ✅ Editor 选中节点 → Apply properties |
| **Subgraph** 画布内嵌编辑 | ✅ Editor 目标下拉 + Add subgraph |
| Postgres **thread / checkpoint** 索引 | ✅ migration `0003` + `NewPostgresCheckpointHistory` |

Migration: [migrations/postgres/0003_agentflow_studio_thread_checkpoint.up.sql](../migrations/postgres/0003_agentflow_studio_thread_checkpoint.up.sql)

```go
history, err := agentflow.NewPostgresCheckpointHistory(db)
fw, err := agentflow.New(scenario,
    agentflow.WithRunStateRepository(runRepo),
    agentflow.WithCheckpointHistory(history),
)
```

## P3（已交付 — Editor 体验增强）

| 能力 | 状态 |
|------|------|
| **Undo / Redo** | ✅ Editor 历史栈（50 步） |
| **节点类型面板** | ✅ Quick add chips |
| **Export YAML** | ✅ `POST /api/studio/yaml` |
| **Run graph** | ✅ `POST /api/studio/run` |
| file / redis **thread 过滤** | ✅ `ListFilter.ParentRunID` / `ThreadID` |

### Studio YAML 与试运行

```go
yamlDoc, err := fw.GenerateStudioScenarioYAML(ctx, editedGraph)
result, err := fw.RunStudioGraph(ctx, editedGraph, agentflow.RunRequest{Prompt: "hello"})
```

HTTP：

- `POST /observability/api/studio/yaml` — 返回 scenario YAML
- `POST /observability/api/studio/run` — body: `{"graph":{...},"prompt":"...","agent":"...","run_id":"..."}`
- `POST /observability/api/studio/save` — persist edited graph to host `StudioSavePath`

## P4（已交付 — 生产化与持久化）

| 能力 | 状态 |
|------|------|
| **Legacy YAML** 导出对齐 | ✅ `config/yaml.Marshal` → `scenario:` 根文档 |
| **Production Studio API** | ✅ `POST /v1/studio/{validate,codegen,yaml,run,save}` |
| **Editor 保存 scenario 文件** | ✅ `StudioSavePath` + Save scenario 按钮 |

### 宿主接线

```go
savePath := "/etc/agentflow/scenario.yaml"
dashboard, err := agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
    Store: eventStore,
    Hub:   eventHub,
    Framework: fw,
    StudioSavePath: savePath,
})
prod, err := agentflow.NewProductionHTTPHandler(agentflow.ProductionHTTPHandlerConfig{
    Queue: queue,
    Framework: fw,
    StudioSavePath: savePath,
})
```

Production HTTP：

- `POST /v1/studio/validate|codegen|yaml|run|save`

## P5（已交付 — Editor 打磨）

| 能力 | 状态 |
|------|------|
| **condition / depends_on** 节点属性编辑 | ✅ Editor 属性面板 |
| **条件边** 可视化 | ✅ 连边 prompt + 边属性面板 |
| **布局持久化** | ✅ `GraphView.layout` 随 graph 保存 |
| **Save diff 预览** | ✅ Preview save → Details 面板 |
| **Revert to loaded** | ✅ 丢弃本地编辑 |
| **http-worker 保存演示** | ✅ `StudioSavePath` 示例接线 |

## P6（已交付 — 生产体验）

| 能力 | 状态 |
|------|------|
| **结构化 API 错误码** | ✅ `pkg/studio/errors.go` + Studio HTTP |
| **错误码 UI i18n** | ✅ Observability Editor alerts |
| **Save reload + 保留 subgraph** | ✅ 保存后 reload，保留当前画布 |
| **Editor 多选 / 删边 / 拖线连边** | ✅ Shift 多选、Delete edge、port 拖线 |

## P7（已交付 — YAML 导入与 Editor 快捷键）

| 能力 | 状态 |
|------|------|
| **Import YAML** | ✅ `POST /api/studio/import-yaml`（保留 layout） |
| **Editor 快捷键** | ✅ Ctrl/Cmd+S 保存、Z/Shift+Z 撤销重做、Delete 删节点/边 |
| **Declarative interrupt 编辑** | ✅ 节点属性 `interrupt` checkbox + 画布 ⏸ 标记 |

### Studio YAML 导入

```go
result, err := fw.ImportStudioScenarioYAML(ctx, yamlBytes, currentLayoutGraph)
```

HTTP：

- `POST /observability/api/studio/import-yaml` — body: `{"yaml":"...","layout_graph":{...}}`
- `POST /v1/studio/import-yaml` — Production 同名路由

## 相关文档

- [next-milestones.md](./next-milestones.md)

- [orchestration-parity.md](./orchestration-parity.md)
- [observability-dashboard.md](./observability-dashboard.md)
- [competitive-analysis-langgraph.md](./competitive-analysis-langgraph.md)
