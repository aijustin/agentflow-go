# Builder API 速查

`pkg/builder` 提供链式 Go DSL，用于构造 `core.Scenario`，避免手写大量字符串字面量。构建结果与 `examples/*.yaml` 对齐，统一走 `agentflow.ValidateScenario` 校验。

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
# 全部 builder stack（18 个，与 examples/*.yaml 一一对应）
make validate-builder

# 或直接用 validate CLI
go run ./examples/go/validate -kind builder all
go run ./examples/go/validate -kind builder autonomous-echo
go run ./examples/go/validate -kind builder examples/autonomous.yaml

# 单元测试（含 YAML 对照）
go test ./pkg/builder/... -run ExampleCatalog
```

`make release-check` 已包含 `validate-builder`。

## 一行式 Stack 对照表

| YAML 示例 | Builder 函数 |
|-----------|----------------|
| `autonomous.yaml` | `MinimalAutonomous("assistant")` |
| `human_in_loop.yaml` | `MinimalHumanInLoop("assistant")` |
| `context_governance.yaml` | `ContextGovernance("assistant")` |
| `fixed_workflow.yaml` | `MinimalFixedWorkflowReview("reviewer")` |
| `workflow_enhancements.yaml` | `WorkflowEnhancements()` |
| `code_review_pipeline.yaml` | `CodeReviewPipeline()` |
| `hybrid.yaml` | `HybridResearch("analyst")` |
| `multi_expert_research.yaml` | `MultiExpertResearch()` |
| `adaptive_rag.yaml` | `AdaptiveRAG("assistant")` |
| `corrective_rag.yaml` | `CorrectiveRAG("assistant")` |
| `self_rag.yaml` | `SelfRAG("assistant")` |
| `rag_knowledge.yaml` | `MinimalRAG("assistant")` |
| `ticket_handling.yaml` | `MinimalTicketHandling("support")` |
| `tier_memory.yaml` | `TierMemoryAutonomous("assistant")` |
| `http_tool.yaml` | `MinimalHTTPTool("assistant")` |
| `sql_tool.yaml` | `MinimalSQLTool("assistant")` |
| `filesystem_tool.yaml` | `MinimalFilesystemTool("assistant")` |
| `mcp_tool.yaml` | `MinimalMCPTool("assistant")` |

完整列表见 `builder.ExampleCatalog()`（[catalog.go](../pkg/builder/catalog.go)）。

## API 分层

| 层 | 包路径 | 职责 |
|----|--------|------|
| **Stack** | `Minimal*` / `*RAG` / `CodeReviewPipeline` 等 | 一行还原 YAML 示例 |
| **Preset** | `DefaultMockLLM`、`EchoTool`、`RAGStack` 等 | 声明 + 引用成对注册 |
| **常量** | [consts.go](../pkg/builder/consts.go) | 资源名、tool type、HITL checkpoint、MCP 元数据 |
| **Workflow** | `AdaptiveRAGWorkflow`、`CodeReviewPipelineWorkflow` 等 | 可复用 DAG 图 |
| **底层** | `New(...).Agent(...).Autonomous().Scenario()` | 完全显式组合 |

## 常用常量（节选）

```go
builder.NameDefaultLLM      // "default"
builder.NameSessionMemory   // "session"
builder.NameEchoTool        // "echo"
builder.ToolTypeEcho        // "builtin.echo"
builder.ApprovalNever
builder.ModeAutonomous
builder.CheckpointBeforeFinalAnswer
builder.NameKnowledgeRetrieveTool
```

## 手动组合示例

```go
scenario := builder.New("my-app").
    DefaultMockLLM().
    SessionMemory().
    EchoTool().
    Agent("assistant").
        DefaultLLM().
        SessionMemory().
        EchoTool().
        Instructions("Answer clearly.").
    Autonomous().
    Scenario()
```

## 与 YAML 的关系

- **YAML**：运维/产品协作、CI schema 校验、可视化编辑
- **Builder**：Go 代码内动态构造、测试 fixture、避免 magic string
- **运行时接线**（`WithLLMGateway`、`WithToolExecutor`）两种方式相同，DSL 只负责场景结构

## 可运行示例

| 示例 | 说明 |
|------|------|
| [examples/go/builder/main.go](../examples/go/builder/main.go) | 最小 autonomous 运行 |
| [examples/go/validate/main.go](../examples/go/validate/main.go) | `-kind builder` 批量校验全部 stack |
