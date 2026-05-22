# Builder API 速查

`pkg/builder` 是 **默认** 的场景构造方式：链式 Go DSL 生成 `core.Scenario`，统一走 `agentflow.ValidateScenario` 校验。

## 快速开始

```go
import (
    agentflow "github.com/aijustin/agentflow-go"
    "github.com/aijustin/agentflow-go/pkg/builder"
)

scenario := builder.MinimalAutonomous("assistant")
if err := agentflow.ValidateScenario(scenario); err != nil {
    log.Fatal(err)
}
fw, err := agentflow.New(scenario, opts...)
```

根包也 re-export 了常用入口（`agentflow.MinimalAutonomous` 等），见 [builder.go](../builder.go)。

## 校验

```sh
make validate-builder
go run ./examples/go/validate -kind builder all
go run ./examples/go/validate -kind builder autonomous-echo

# catalog manifest（tool/skill）
go run ./examples/go/validate -kind tool examples/catalog/tools/echo.tool.yaml
make validate-catalog
```

`make release-check` 已包含 `validate-builder`。

## Workflow 节点（subgraph / map）

```go
prep := builder.NewWorkflow().
    NodeTransform("mark", json.RawMessage(`{"set":{"ready":true}}`)).
    Build()

mainWF := builder.NewWorkflow().
    NodeSubgraph("run_prep", "prep").
    Build()

scenario := builder.New("demo").
    DefaultMockLLM().
    Agent("assistant").DefaultLLM().Done().
    NamedWorkflow("prep", prep).
    FixedWorkflow(mainWF).
    Scenario()

fanout := builder.NewWorkflow().
    NodeTransform("items", json.RawMessage(`{"set":{"list":["a","b"]}}`)).
    NodeMap("fanout", builder.MapNodeInput("steps.items.list", builder.MapBranch{
        Kind: builder.NodeTransform,
        Input: json.RawMessage(`{"set":{"tag":"mapped"}}`),
    })).
    Edge("items", "fanout").
    Build()
```

## Catalog 对照表

| Catalog ID | Builder 函数 |
|-----------|----------------|
| `autonomous-echo` | `MinimalAutonomous("assistant")` |
| `human-in-loop` | `MinimalHumanInLoop("assistant")` |
| `context-governance` | `ContextGovernance("assistant")` |
| `fixed-workflow-review` | `MinimalFixedWorkflowReview("reviewer")` |
| `workflow-enhancements` | `WorkflowEnhancements()` |
| `code-review-pipeline` | `CodeReviewPipeline()` |
| `hybrid-research` | `HybridResearch("analyst")` |
| `multi-expert-research` | `MultiExpertResearch()` |
| `adaptive-rag` | `AdaptiveRAG("assistant")` |
| `corrective-rag` | `CorrectiveRAG("assistant")` |
| `self-rag` | `SelfRAG("assistant")` |
| `rag-knowledge` | `MinimalRAG("assistant")` |
| `ticket-handling` | `MinimalTicketHandling("support")` |
| `tier-memory` | `TierMemoryAutonomous("assistant")` |
| `http-tool` | `MinimalHTTPTool("assistant")` |
| `sql-tool` | `MinimalSQLTool("assistant")` |
| `filesystem-tool` | `MinimalFilesystemTool("assistant")` |
| `mcp-tool` | `MinimalMCPTool("assistant")` |

完整列表见 `builder.ExampleCatalog()`（[catalog.go](../pkg/builder/catalog.go)）。

## API 分层

| 层 | 包路径 | 职责 |
|----|--------|------|
| **Stack** | `Minimal*` / `*RAG` / `CodeReviewPipeline` 等 | 一行还原常见场景 |
| **Preset** | `DefaultMockLLM`、`EchoTool`、`RAGStack` 等 | 声明 + 引用成对注册 |
| **Workflow** | `NewWorkflow`、`NodeSubgraph`、`NodeMap` 等 | 可复用 DAG 图 |
| **底层** | `New(...).Agent(...).Autonomous().Scenario()` | 完全显式组合 |

## 常用常量（节选）

```go
builder.NameDefaultLLM
builder.NameEchoTool
builder.NodeSubgraph
builder.NodeMap
builder.CheckpointBeforeFinalAnswer
```

完整列表见 [consts.go](../pkg/builder/consts.go)。

## 相关文档

- [product-direction.md](./product-direction.md)
- [configuration-reference.md](./configuration-reference.md) — `core.Scenario` 字段说明
- [examples/go/scenario](../examples/go/scenario/scenario.go) — 可运行示例共享 stack
