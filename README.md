# agentflow-go

[![Go Reference](https://pkg.go.dev/badge/github.com/aijustin/agentflow-go.svg)](https://pkg.go.dev/github.com/aijustin/agentflow-go)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](./LICENSE)

[English](./README.en.md) | 简体中文

`agentflow-go` 是面向 **Go 后端工程师** 的可嵌入 Agent **运行时库**：用 Go 代码（`pkg/builder` 或 `core.Scenario`）组合 Agent、Tool、Skill、LLM Gateway、Memory、Run State 与 Human-in-the-loop，在自有服务中显式接线后调用 `Framework.Run`。

## 快速开始

```sh
go get github.com/aijustin/agentflow-go
go run ./examples/go/minimal/main.go
go run ./examples/go/builder/main.go
make validate-builder
make test
```

产品方向：[docs/product-direction.md](docs/product-direction.md) · Builder 参考：[docs/builder-reference.md](docs/builder-reference.md)

发布前建议运行 `GOTOOLCHAIN=auto make release-check`。见 [docs/release-checklist.md](docs/release-checklist.md) 与 [docs/api-stability.md](docs/api-stability.md)。

集成指南：[docs/library-integration.md](docs/library-integration.md) · HTML 手册：[docs/manual.html](docs/manual.html) · 与 LangGraph 对比：[docs/competitive-analysis-langgraph.md](docs/competitive-analysis-langgraph.md)

## 集成路径

| 目标 | 入口 |
|------|------|
| **首选：Go DSL 构造场景** | [docs/builder-reference.md](docs/builder-reference.md) · [examples/go/builder/main.go](examples/go/builder/main.go) |
| 嵌入现有 Go 服务 | [docs/library-integration.md](docs/library-integration.md) |
| 进程内最小运行 | [examples/go/minimal/main.go](examples/go/minimal/main.go) |
| Postgres / 文件持久化 | [examples/go/postgres/main.go](examples/go/postgres/main.go) |
| HTTP + 异步 Worker | [examples/go/http-worker/main.go](examples/go/http-worker/main.go) |
| HITL 暂停与恢复 | [examples/go/hitl-resume/main.go](examples/go/hitl-resume/main.go) |
| 事件触发 | [examples/go/event-trigger/main.go](examples/go/event-trigger/main.go) |
| 测试与示例接线 | [pkg/testutil](pkg/testutil/testutil.go) |

库 API：`ValidateWiring`、`New`、`Framework.Run`、`NewProductionHTTPHandler`、`NewFrameworkJobHandler`、`NewPrometheusRecorder`、`NewOpenTelemetryTracer`、`ScenarioJSONSchema`、`Version`；Builder 栈入口见 [builder.go](builder.go)（如 `MinimalAutonomous`）。

## 示例路径对照表

### 可运行 Go 示例（`examples/go/`）

| 目录 | 说明 | 运行命令 |
|------|------|----------|
| [builder](examples/go/builder/main.go) | Go DSL 构造场景并进程内 Run（**推荐起点**） | `go run ./examples/go/builder/main.go` |
| [minimal](examples/go/minimal/main.go) | 最小嵌入：`builder` → `testutil.WiringOptions` → `New` → `Run` | `go run ./examples/go/minimal/main.go` |
| [postgres](examples/go/postgres/main.go) | Postgres / 文件 RunState 持久化 | `go run ./examples/go/postgres/main.go` |
| [http-worker](examples/go/http-worker/main.go) | 挂载 `NewProductionHTTPHandler` + 异步 Worker | `go run ./examples/go/http-worker/main.go` |
| [hitl-resume](examples/go/hitl-resume/main.go) | HITL 暂停与 `ResumeAndContinue` | `go run ./examples/go/hitl-resume/main.go` |
| [event-trigger](examples/go/event-trigger/main.go) | `scenario.triggers` 事件驱动 Run | `go run ./examples/go/event-trigger/main.go` |
| [tier-memory](examples/go/tier-memory/main.go) | 进程内 tier 记忆最小示例 | `go run ./examples/go/tier-memory/main.go` |
| [tier-worker](examples/go/tier-worker/main.go) | Postgres warm/cold tier + `memory.reconcile` 异步 Worker | 见 [examples/deploy/](examples/deploy/README.md) |
| [validate](examples/go/validate/main.go) | 校验 builder catalog 或 legacy YAML | `go run ./examples/go/validate -kind builder all` |

生产环境请用 `WithLLMGateway` / `WithToolExecutor` 替代 `testutil.WiringOptions`；测试接线见 [pkg/testutil](pkg/testutil/testutil.go)。

### Builder catalog 对照

完整 Catalog ID 与 `builder.*` 函数对照见 [docs/builder-reference.md](docs/builder-reference.md)。共享 stack 实现在 [examples/go/scenario/scenario.go](examples/go/scenario/scenario.go)。

校验全部 catalog stack：

```sh
go run ./examples/go/validate -kind builder all
make validate-builder
```

## 环境要求

- Go 1.25.10+
- macOS/Linux shell

### 作为框架在其他 Go 项目中使用

添加依赖：

```sh
go get github.com/aijustin/agentflow-go
```

引入根门面包：

```go
package main

import (
    "context"
    "fmt"
    "log"

    agentflow "github.com/aijustin/agentflow-go"
    "github.com/aijustin/agentflow-go/pkg/builder"
)

func main() {
    scenario := builder.MinimalAutonomous("assistant")
    fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(myLLMGateway))
    if err != nil {
        log.Fatal(err)
    }

    result, err := fw.Run(context.Background(), agentflow.RunRequest{
        RunID:  "run-1",
        Agent:  "assistant",
        Prompt: "hello",
    })
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(result.Output)
}
```

如需接入自定义 LLM、Memory、RunState、EventSink 或 HumanGate，可使用 Option API：

```go
scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(
    scenario,
    agentflow.WithLLMGateway(myLLMGateway),
    agentflow.WithToolExecutor("repo_search", myToolExecutor),
    agentflow.WithMemoryRepository("session", myMemoryRepo),
    agentflow.WithRunStateRepository(myRunStateRepo),
    agentflow.WithEventSink(myEventSink),
)
```

常见 LLM Provider 的构造函数已从根包暴露：

```go
gateway := agentflow.NewOpenAICompatibleGateway([]llm.Profile{{
  Name:      "default",
  Provider:  "openai-compatible",
  Model:     "qwen/qwen3.6-35b-a3b",
  Endpoint:  "http://127.0.0.1:1234/v1",
  APIKeyEnv: "AGENT_REALMODEL_API_KEY",
}}, nil)

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(gateway))
```

如果需要同时接 OpenAI-compatible 聊天与 Embedding，可使用 `NewOpenAICompatibleProvider`，并显式声明 profile 能力：

```go
provider := agentflow.NewOpenAICompatibleProvider([]llm.Profile{
  {Name: "chat", Provider: "openai-compatible", Model: "qwen/qwen3.6-35b-a3b", Endpoint: "http://127.0.0.1:1234/v1"},
  {Name: "embed", Provider: "openai-compatible", Model: "text-embedding-3-small", Endpoint: "http://127.0.0.1:1234/v1", Capabilities: []llm.Capability{llm.CapEmbed}},
}, nil)
```

混合 Provider 场景可使用 `NewLLMProviderRouter` 按 profile 路由 chat/tool/structured/stream 和 embedding 调用。能力会显式检查：Provider 不支持的能力会清晰失败，不会被静默模拟。

```go
openaiProvider := agentflow.NewOpenAICompatibleProvider(openaiProfiles, nil)
anthropicGateway := agentflow.NewAnthropicGateway(anthropicProfiles, nil)

provider := agentflow.NewLLMProviderRouter(map[string]llm.Gateway{
  "chat":  anthropicGateway,
  "embed": openaiProvider,
})
```

结构化输出：在 `agents.<name>.output_schema` 中配置 JSON Schema，并调用 `RunStructured`。LLM Gateway 需要实现 `llm.StructuredOutputter`：

```go
result, err := fw.RunStructured(ctx, agentflow.RunRequest{
    RunID:  "run-json",
    Agent:  "assistant",
    Prompt: "return JSON",
})
fmt.Println(string(result.StructuredOutput))
```

流式输出：使用实现了 `llm.Streamer` 的 Gateway：

```go
chunks, err := fw.Stream(ctx, agentflow.RunRequest{
    RunID:  "run-stream",
    Agent:  "assistant",
    Prompt: "stream the answer",
})
if err != nil {
    log.Fatal(err)
}
for chunk := range chunks {
    if chunk.Error != "" {
        log.Fatal(chunk.Error)
    }
    fmt.Print(chunk.Content)
}
```

当 Agent 配置了工具，并且 LLM Gateway 支持 `CapToolCall` 时，Runtime 会执行自主工具调用循环：向 LLM 发送工具规格，校验返回的工具调用是否在 Agent 白名单中，执行审批策略和每次运行的 `rate_cap`，按 `retry_limit`/`max_retries` 对分类后的临时 LLM/工具错误做指数退避重试，执行注册的 ToolExecutor，将受限后的工具结果回填给 LLM，直到 LLM 返回最终答案或达到 `max_steps`。`Stream` 也支持带工具的 Agent：它会运行同一套受治理工具循环，并把最终答案作为流式 chunk 输出。

配置 `orchestration.planning.enabled: true` 后，Runtime 会在自主工具循环前先执行规划 pass。规划默认使用当前执行 Agent，也可以通过 `orchestration.planning.agent` 指定专门规划 Agent；生成的简短 JSON 计划会注入后续执行上下文。设置 `orchestration.planning.execute: true` 可在 tool loop 中跟踪 plan step 完成状态（见 `builder.MultiExpertResearch()`）。

固定工作流支持 `tool`、`agent`、`skill`、`human_gate`、`transform`、`parallel_group` 和 `loop` 节点。`condition` 可使用 `exists(...)`、`missing(...)`、`eq(...)`、`ne(...)` 读取 `steps.<node_id>` 路径，`transform` 节点可用 `set`/`copy` 从前序步骤构造结构化输出。

当 Agent 绑定 `memory` 时，Runtime 会在上下文准备前读取 conversation/session 记忆并注入 LLM 上下文，执行后追加用户输入、助手回复和工具观察结果。根门面会自动为 `in_memory` 类型创建内存仓库，除非调用方显式传入自定义仓库。

启用内置 HMAC Token 的 HITL Gate：

```go
scenario := builder.MinimalHumanInLoop("assistant")
fw, err := agentflow.New(scenario,
    agentflow.WithHITLTokenSecret([]byte("strong-secret"), nil),
)
if err != nil {
    log.Fatal(err)
}

result, err := fw.Run(ctx, agentflow.RunRequest{RunID: "run-1", Prompt: "needs approval"})
if err != nil {
    log.Fatal(err)
}

if result.Token != "" {
    err = fw.Resume(ctx, result.Token, core.DecisionApprove, nil)
}
```

需要进程重启后仍能恢复运行时，可使用文件持久化适配器：

```go
runs, _ := agentflow.NewFileRunStateRepository("./data/runs")
blobs, _ := agentflow.NewFileBlobStore("./data/blobs")
memoryRepo, _ := agentflow.NewFileMemoryRepository("./data/memory")

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithRunStateRepository(runs),
    agentflow.WithBlobStore(blobs),
    agentflow.WithMemoryRepository("session", memoryRepo),
)
```

生产环境需要 PostgreSQL RunState 时，可在应用侧注册 `database/sql` driver，并把初始化后的连接池传给根门面构造器：

```go
db, err := sql.Open("pgx", os.Getenv("AGENTFLOW_POSTGRES_DSN"))
if err != nil {
  log.Fatal(err)
}
runs, err := agentflow.NewPostgresRunStateRepository(db)
if err != nil {
  log.Fatal(err)
}

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithRunStateRepository(runs),
)
```

表结构契约和运维注意事项见 [docs/persistence/postgres-runstate.md](docs/persistence/postgres-runstate.md)。

如果希望使用 Redis 存储低延迟 CAS RunState，也可以使用 Redis RunState 适配器：

```go
runs, err := agentflow.NewRedisRunStateRepository(agentflow.RedisRunStateRepositoryConfig{
  Addr:      os.Getenv("AGENTFLOW_REDIS_ADDR"),
  Password:  os.Getenv("AGENTFLOW_REDIS_PASSWORD"),
  KeyPrefix: "agentflow:runstate:",
})
if err != nil {
  log.Fatal(err)
}
```

存储语义和运维注意事项见 [docs/persistence/redis-runstate.md](docs/persistence/redis-runstate.md)。

生产环境异步执行可使用队列和 Worker。PostgreSQL 队列适配器基于 `database/sql`，不强制绑定具体驱动：

```go
queue, err := agentflow.NewPostgresJobQueue(db)
if err != nil {
  log.Fatal(err)
}

runHandler, err := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
if err != nil {
  log.Fatal(err)
}

worker, err := async.NewWorker(queue, runHandler, async.WorkerConfig{
  WorkerID:      "worker-1",
  Concurrency:   4,
  LeaseTTL:      time.Minute,
  RenewInterval: 30 * time.Second,
  JobTimeout:    5 * time.Minute,
})
```

`agentflow.NewProductionHTTPHandler` 会挂载 `/healthz`、`/readyz`、异步 run/event/resume job API；当配置 `Framework` 时还会挂载同步 `/v1/events` 和 `/v1/hitl/resume`。更多说明见 [docs/async-runtime.md](docs/async-runtime.md) 和 [docs/persistence/postgres-queue.md](docs/persistence/postgres-queue.md)。

MCP Server 可以通过适配器变成普通受治理工具，无需改变 runtime core：

```go
mcpClient, err := agentflow.NewMCPHTTPClient("http://127.0.0.1:3333/mcp", nil)
if err != nil {
  log.Fatal(err)
}
searchTool, err := agentflow.NewMCPToolExecutor(mcpClient, "search")
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.New(builder.MinimalMCPTool("assistant"),
  agentflow.WithToolExecutor("docs.search", searchTool),
)
```

适配模型和安全注意事项见 [docs/mcp-tools.md](docs/mcp-tools.md)。

重型或租户隔离的工具不需要在框架启动时全部构造。可以先在 `scenario.tools` 声明 manifest，然后通过 `WithToolResolver` 在运行时完成 allowlist、审批、RBAC、治理策略和 rate cap 检查后，再按需解析真正的 executor：

```go
resolver := agentflow.ToolResolverFunc(func(ctx context.Context, tool core.Tool) (core.ToolExecutor, error) {
  switch tool.Type {
  case "builtin.sql":
    return newTenantSQLTool(ctx, tool.Metadata)
  case "mcp.tool":
    return newTenantMCPTool(ctx, tool.Metadata)
  default:
    return nil, fmt.Errorf("unsupported tool type %q", tool.Type)
  }
})

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithToolResolver(resolver),
)
```

`WithToolExecutor` 仍适合轻量或常驻工具，并且优先级高于 resolver。resolver 解析出的 executor 会按场景工具名缓存在 framework 生命周期内。Skill 不负责初始化工具；它只在场景构建阶段展开 prompt 片段、策略覆盖和 workflow 片段，真实 executor 绑定由 resolver 在调用时完成。

读取内部 API 可注册受限 HTTP Tool Executor：

```go
httpTool, err := agentflow.NewHTTPToolExecutor(agentflow.HTTPToolConfig{
  AllowedHosts: []string{"https://status.example.internal"},
})
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.New(builder.MinimalHTTPTool("assistant"),
  agentflow.WithToolExecutor("http.status", httpTool),
)
```

该执行器必须配置 host allowlist，默认只允许 `GET`/`HEAD`。详见 [docs/tools-http.md](docs/tools-http.md)。

读取本地 runbook 或已检出的文档，可注册受限文件系统读取 Tool Executor：

```go
filesystemTool, err := agentflow.NewFilesystemToolExecutor(agentflow.FilesystemToolConfig{
  AllowedRoots: []string{"/srv/agentflow/runbooks"},
})
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.New(builder.MinimalFilesystemTool("assistant"),
  agentflow.WithToolExecutor("fs.read", filesystemTool),
)
```

该执行器必须配置 root allowlist，会拒绝路径逃逸和符号链接逃逸，并限制文件大小。详见 [docs/tools-filesystem.md](docs/tools-filesystem.md)。

需要读取业务库、工单库或报表库时，可注册受限 SQL 查询 Tool Executor，并使用命名 allowlist 查询：

```go
sqlTool, err := agentflow.NewSQLToolExecutor(agentflow.SQLToolConfig{
  DB: db,
  AllowedQueries: map[string]string{
    "tickets.open": "SELECT id, title, status FROM tickets WHERE status = $1",
  },
  MaxRows: 20,
})
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.New(builder.MinimalSQLTool("assistant"),
  agentflow.WithToolExecutor("sql.query", sqlTool),
)
```

该执行器默认只执行命名 `SELECT` 查询，拒绝多语句 SQL，带超时并限制返回行数。详见 [docs/tools-sql.md](docs/tools-sql.md)。

SQL 工具可接入任意 `database/sql` 驱动，包括 PostgreSQL、MySQL 和 ClickHouse。宿主应用自行导入具体驱动并传入已打开的 `*sql.DB`；agentflow-go 不强制引入数据库驱动依赖。

代码审查流水线可注册只读 Git 工具：

```go
gitTool, err := agentflow.NewGitToolExecutor(agentflow.GitToolConfig{
  AllowedRoots: []string{"/workspace/repos"},
})
fw, err := agentflow.New(builder.CodeReviewPipeline(),
  agentflow.WithToolExecutor("git", gitTool),
)
```

详见 [docs/tools-git.md](docs/tools-git.md)。须通过 `WithToolExecutor`（或 `WithToolResolver`）显式注册 executor。

客服工单场景可注册 ticket 工具并注入 store：

```go
store := agentflow.NewMemoryTicketStore(map[string]agentflow.Ticket{
  "T-9": {ID: "T-9", Title: "Login issue", Status: "open"},
})
ticketTool, err := agentflow.NewTicketToolExecutor(agentflow.TicketToolConfig{Store: store})
fw, err := agentflow.New(builder.MinimalTicketHandling("support"),
  agentflow.WithToolExecutor("ticket", ticketTool),
)
```

详见 [docs/tools-ticket.md](docs/tools-ticket.md)。

RAG 场景可组合 Embedder、VectorStore 和 Retriever Tool：

```go
store, err := agentflow.NewPostgresVectorStore(agentflow.PostgresVectorStoreConfig{DB: db})
if err != nil {
  log.Fatal(err)
}
retriever, err := agentflow.NewRetrieverTool(agentflow.RetrieverToolConfig{
  Embedder:     provider,
  Store:        store,
  Profile:      "embed",
  Namespace:    "tenant-a/docs",
  DefaultLimit: 5,
})
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.New(builder.MinimalRAG("assistant"),
  agentflow.WithLLMGateway(provider),
  agentflow.WithToolExecutor("knowledge.retrieve", retriever),
)
```

公共契约和 pgvector 表结构见 [docs/knowledge-rag.md](docs/knowledge-rag.md) 与 [docs/persistence/pgvector.md](docs/persistence/pgvector.md)。

使用 [migrations/postgres](migrations/postgres) 中的 SQL，由宿主应用自己的 migration 工具建表后再接入 Postgres 适配器。见 [docs/persistence/postgres-runstate.md](docs/persistence/postgres-runstate.md) 与 [docs/persistence/postgres-queue.md](docs/persistence/postgres-queue.md)。

大输出需要进入 S3-compatible 对象存储时，可单独配置 BlobStore：

```go
blobs, err := agentflow.NewS3BlobStore(agentflow.S3BlobStoreConfig{
  Endpoint:        os.Getenv("AGENTFLOW_S3_ENDPOINT"),
  Bucket:          os.Getenv("AGENTFLOW_S3_BUCKET"),
  Region:          os.Getenv("AGENTFLOW_S3_REGION"),
  Prefix:          "agentflow/outputs",
  AccessKeyID:     os.Getenv("AGENTFLOW_S3_ACCESS_KEY_ID"),
  SecretAccessKey: os.Getenv("AGENTFLOW_S3_SECRET_ACCESS_KEY"),
})
if err != nil {
  log.Fatal(err)
}

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithBlobStore(blobs),
)
```

对象路径和安全注意事项见 [docs/persistence/s3-blobstore.md](docs/persistence/s3-blobstore.md)。

企业级可观测和治理能力保持可选且低依赖：

```go
scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithEventSink(agentflow.NewSlogEventSink(logger)),
  agentflow.WithAuditSink(agentflow.NewSlogAuditSink(logger)),
  agentflow.WithToolGovernancePolicy(governance.ChainToolPolicies(
    governance.NewToolBudgetPolicy(8),
    governance.NewMaxSideEffectPolicy(core.SideEffectRead),
  )),
  agentflow.WithOutputRedactor(governance.NewJSONFieldRedactor("secret", "token")),
)
```

治理策略会在工具执行前生效，输出脱敏会在运行时 step output 持久化前执行。

AgentFlow 也内置了运行时可观测面板，用于查看实时会话、编排时序和事件详情。PostgreSQL 事件仓库默认自动创建表和索引，开启面板只需要接入事件 sink 并挂载 HTTP handler：

```go
eventStore, err := agentflow.NewPostgresEventStore(ctx, agentflow.PostgresEventStoreConfig{DB: db})
if err != nil {
  log.Fatal(err)
}
eventHub := agentflow.NewEventHub()

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithEventSink(agentflow.NewEventFanoutSink(
    agentflow.NewEventStoreSink(eventStore, eventHub),
    agentflow.NewSlogEventSink(logger),
  )),
)

dashboard, err := agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
  Store: eventStore,
  Hub:   eventHub,
})
mux.Handle("/observability/", http.StripPrefix("/observability", dashboard))
```

数据库配置、自动建表、接口列表和安全建议见 [docs/observability-dashboard.md](docs/observability-dashboard.md)。

底层扩展接口位于：

- `github.com/aijustin/agentflow-go/pkg/core`
- `github.com/aijustin/agentflow-go/pkg/llm`
- `github.com/aijustin/agentflow-go/pkg/contextwindow`
- `github.com/aijustin/agentflow-go/pkg/async`
- `github.com/aijustin/agentflow-go/pkg/audit`
- `github.com/aijustin/agentflow-go/pkg/governance`
- `github.com/aijustin/agentflow-go/pkg/identity`
- `github.com/aijustin/agentflow-go/pkg/knowledge`
- `github.com/aijustin/agentflow-go/pkg/mcp`
- `github.com/aijustin/agentflow-go/pkg/memory`
- `github.com/aijustin/agentflow-go/pkg/runstate`
- `github.com/aijustin/agentflow-go/pkg/security`

内置工具适配器说明见 [docs/tools-http.md](docs/tools-http.md)、[docs/tools-filesystem.md](docs/tools-filesystem.md)、[docs/tools-sql.md](docs/tools-sql.md)、[docs/tools-git.md](docs/tools-git.md)、[docs/tools-ticket.md](docs/tools-ticket.md)、[docs/mcp-tools.md](docs/mcp-tools.md) 和 [docs/knowledge-rag.md](docs/knowledge-rag.md)。

### 安装依赖

```sh
go mod download
```

### 校验示例场景

```sh
go run ./examples/go/validate -kind builder all
make validate-builder
```

### 可运行示例

| 示例 | 说明 |
| --- | --- |
| [examples/go/minimal](examples/go/minimal/main.go) | 进程内 `Run` + 测试接线 |
| [examples/go/postgres](examples/go/postgres/main.go) | 文件或 Postgres RunState |
| [examples/go/http-worker](examples/go/http-worker/main.go) | 生产 HTTP Handler + 异步 Worker |
| [examples/go/hitl-resume](examples/go/hitl-resume/main.go) | HITL 暂停与 `ResumeAndContinue` |
| [examples/go/event-trigger](examples/go/event-trigger/main.go) | `HandleEvent` 与 triggers |

将示例中的 `testutil.WiringOptions` 替换为显式的 `WithLLMGateway` / `WithToolExecutor` 即可用于生产。

排障见 [docs/troubleshooting.md](docs/troubleshooting.md)。

## HTTP 集成

在自有服务中挂载库提供的 Handler，例如：

```sh
go run ./examples/go/http-worker/main.go
```

生产环境 HITL 续跑使用 `NewProductionHTTPHandler` 或 `NewHumanHTTPHandler` 的 `POST /v1/hitl/resume`。设置 `"continue": true` 会调用 `ResumeAndContinue`：

```sh
curl -X POST http://localhost:8080/v1/hitl/resume \
  -H 'Content-Type: application/json' \
  -d '{
    "token": "'"$TOKEN"'",
    "decision": "approve",
    "continue": true
  }'
```

Webhook 事件在配置 `Framework` 时使用 `POST /v1/events`。详见 [docs/async-runtime.md](docs/async-runtime.md)。

网络传递的 Token 使用 HMAC 签名。生产环境必须设置强密钥，并使用持久化 RunState 仓库。

## YAML 场景格式

所有场景配置都位于一个 `scenario:` 根节点下。

如需编辑器补全、枚举提示和 CI 校验，可使用 JSON Schema：[schemas/agentflow.scenario.schema.json](schemas/agentflow.scenario.schema.json)。完整字段参考见 [docs/configuration-reference.md](docs/configuration-reference.md)，编排执行流程与**模式/节点选型指南**见 [docs/orchestration-flow.md](docs/orchestration-flow.md)，Go 中可通过 `agentflow.ScenarioJSONSchema()` 加载同一份 schema。

示例场景：

| 文件 | 说明 |
| --- | --- |
| `builder.MinimalAutonomous("assistant")` | 自主工具循环基线 |
| `builder.MinimalFixedWorkflowReview("reviewer")` | 图工作流 + 条件 + HITL |
| `builder.MinimalHumanInLoop("assistant")` | HITL 暂停与恢复 |
| `builder.MinimalTicketHandling("support")` | Ticket 工具 + triggers + 事件路由 |
| `builder.CodeReviewPipeline()` | Git 工具 + `parallel_group` 工作流 |
| `builder.MultiExpertResearch()` | Hybrid 模式 + planning.execute |

```yaml
# yaml-language-server: $schema=schemas/agentflow.scenario.schema.json
```

```yaml
scenario:
  name: autonomous-echo
  llms:
    default:
      provider: mock
      model: test
  memories:
    session:
      type: in_memory
      scope: session
  tools:
    echo:
      type: builtin.echo
      approval: never
      rate_cap: 5
  agents:
    assistant:
      llm: default
      memory: session
      tools: [echo]
      timeout: 30s
      retry_limit: 1
      output_schema:
        type: object
        properties:
          answer:
            type: string
      instructions: "Answer the user clearly."
  orchestration:
    mode: autonomous
    human_in_loop:
      enabled: false
  runtime:
    timeout: 2m
    max_steps: 8
    max_retries: 1
    step_output_threshold: 65536
```

### 顶层配置段

| 配置段 | 作用 |
| --- | --- |
| `llms` | 命名 LLM Profile。Agent 和 Tool 可以绑定不同 Profile。 |
| `memories` | 命名 Memory 后端和作用域。当前支持内存和文件持久化仓库。 |
| `tools` | Tool 声明、副作用等级、审批策略、可选 LLM 覆盖和每次运行的 `rate_cap`。 |
| `skills` | 声明式 prompt/policy/workflow 包。Skill 不是独立运行时 Actor，也不初始化工具；它会在场景构建阶段展开为 Agent 指令、工具策略和 workflow 子图。 |
| `agents` | Agent 角色、指令、LLM 绑定、Memory 绑定、工具和技能。 |
| `orchestration` | autonomous、fixed_workflow 或 hybrid 编排策略。 |
| `runtime` | Runtime 限制、输出阈值、密钥和运行参数。 |

### LLM Profile 与上下文治理

每个 LLM Profile 可定义提供商参数、输出限制、thinking/reasoning 选项、提供商扩展字段和上下文窗口策略：

```yaml
scenario:
  llms:
    default:
      provider: openai-compatible
      model: qwen/qwen3.6-35b-a3b
      endpoint: http://127.0.0.1:1234/v1
      api_key_env: AGENT_REALMODEL_API_KEY
      context_window_tokens: 1400
      max_output_tokens: 1024
      temperature: 0
      top_p: 0.8
      thinking:
        enabled: true
        budget_tokens: 768
      reasoning_effort: high
      extra_body:
        custom_provider_flag: true
      context:
        strategy: sliding_window_with_summary
        max_input_tokens: 220
        reserved_output_tokens: 1024
        summary_tokens: 80
        tool_result_max_tokens: 400
        memory_recall_limit: 8
        system_prompt_protection: true
        compression:
          enabled: true
          trigger_ratio: 0.5
```

支持的上下文策略包括 `none`、`sliding_window`、`sliding_window_with_summary`。每次 LLM 调用前，Runtime 会发出 `ContextPrepared` 事件，包含裁剪前后 token 估算、丢弃消息数、是否生成摘要和当前输入预算。

对于本地 Qwen reasoning 模型，应将 `max_output_tokens` 设置得足够大，因为一些 OpenAI-compatible 服务会把 reasoning output 计入 `max_tokens`。如果响应为空且 `finish_reason=length`，Runtime 会返回错误，避免把错误配置误判成成功的空回答。

### 编排模式

| 模式 | 说明 |
| --- | --- |
| `autonomous` | LLM 驱动的规划/执行。Orchestrator 负责工具调度和审批检查。 |
| `fixed_workflow` | 确定性图工作流。执行前校验节点和边。 |
| `hybrid` | 为固定流程 + 自主子步骤 + HITL Gate 的组合场景预留。 |

### Human-in-the-loop

通过 checkpoints 启用 HITL：

```yaml
scenario:
  orchestration:
    mode: autonomous
    human_in_loop:
      enabled: true
      checkpoints:
        - before_final_answer
```

Checkpoint 打开时，Runtime 会持久化 `RunSnapshot`，签发包含 `(RunID, Version)` 的 Token，并等待人工决策。

## 库 API

大多数应用只需要引入根门面：

```go
import agentflow "github.com/aijustin/agentflow-go"
```

公共包：

| 包 | 作用 |
| --- | --- |
| root package | 框架门面：加载 YAML、校验、运行、恢复、事件处理、注入扩展。 |
| `pkg/async` | 异步执行所需的 Job Queue、Lease、Handler 和 Worker 契约。 |
| `pkg/eventrouter` | 外部事件类型与 `scenario.triggers` 到 RunRequest 的路由。 |
| `pkg/audit` | 合规记录所需的 Audit Event 模型和 Sink 契约。 |
| `pkg/coordination` | 用于 Worker 和工作流协调的分布式租约契约。 |
| `pkg/core` | Agent、Tool、Skill、Scenario、Workflow、HumanGate、Event 类型。 |
| `pkg/llm` | 提供商无关的 LLM 能力接口和请求/响应类型。 |
| `pkg/contextwindow` | 上下文窗口策略管理、token 估算、裁剪和压缩统计。 |
| `pkg/identity` | Principal、角色、租户/工作区/项目作用域和 context helpers。 |
| `pkg/memory` | Memory Namespace 和 Repository 契约。 |
| `pkg/runstate` | RunSnapshot、CAS Repository 端口、Blob 引用和 Token 签名。 |
| `pkg/security` | API Key 认证器、授权 action/resource 和 RBAC policy 契约。 |

创建并保存运行快照：

```go
repo := runstateinmem.NewRepository()
snapshot := runstate.RunSnapshot{
    RunID:        "run-1",
    ScenarioName: "demo",
    Status:       runstate.RunStatusRunning,
}
if err := repo.Save(context.Background(), &snapshot, 0); err != nil {
    log.Fatal(err)
}
```

签发并验证 HITL Token：

```go
signer, err := runstate.NewTokenSigner([]byte("secret"))
if err != nil {
    log.Fatal(err)
}
token, err := signer.Sign(runstate.TokenPayload{RunID: "run-1", Version: 1})
if err != nil {
    log.Fatal(err)
}
payload, err := signer.Verify(token)
if err != nil {
    log.Fatal(err)
}
fmt.Println(payload.RunID)
```

获取 Redis 分布式租约，用于 Worker 协调：

```go
locker, err := agentflow.NewRedisLocker(agentflow.RedisLockerConfig{
  Addr:      os.Getenv("AGENTFLOW_REDIS_ADDR"),
  Password:  os.Getenv("AGENTFLOW_REDIS_PASSWORD"),
  KeyPrefix: "agentflow:",
})
if err != nil {
  log.Fatal(err)
}
lease, acquired, err := locker.Acquire(ctx, "run:123", "worker:alpha", 30*time.Second)
if err != nil {
  log.Fatal(err)
}
if acquired {
  defer func() { _ = locker.Release(ctx, lease) }()
}
```

租约语义和运维注意事项见 [docs/persistence/redis-locker.md](docs/persistence/redis-locker.md)。

通过 async worker foundation 执行异步任务：

```go
queue := agentflow.NewInMemoryJobQueue()
worker, err := async.NewWorker(
  queue,
  async.HandlerFunc(func(ctx context.Context, job async.Job) error {
    return nil
  }),
  async.WorkerConfig{WorkerID: "worker-1", Concurrency: 4},
)
if err != nil {
  log.Fatal(err)
}
```

队列状态、Worker 行为和后续生产化切片见 [docs/async-runtime.md](docs/async-runtime.md)。

暴露异步 run/event/resume job endpoints：

```go
queue := agentflow.NewInMemoryJobQueue()
handler, err := agentflow.NewAsyncRunHTTPHandler(agentflow.AsyncRunHTTPHandlerConfig{
  Queue:  queue,
  Policy: security.NewDefaultRolePolicy(),
  Audit:  auditSink,
})
if err != nil {
  log.Fatal(err)
}
http.Handle("/v1/", middleware(handler))
```

生产 Handler 可同时挂载可选的同步 event/HITL 路由：

```go
api, err := agentflow.NewProductionHTTPHandler(agentflow.ProductionHTTPHandlerConfig{
  Queue:     queue,
  Framework: fw,
  Policy:    security.NewDefaultRolePolicy(),
  Audit:     auditSink,
  Version:   "v0.1.0",
})
```

完整路由矩阵见 [docs/async-runtime.md](docs/async-runtime.md)（`/v1/runs`、`/v1/jobs/events`、`/v1/jobs/hitl/resume`、`/v1/events`、`/v1/hitl/resume`）。

使用 API Key 保护 HTTP handler，并把企业 Principal 注入 request context：

```go
auth, err := agentflow.NewStaticAPIKeyAuthenticator(map[string]identity.Principal{
  os.Getenv("AGENTFLOW_SERVICE_API_KEY"): {
    ID:    "svc-agent-runner",
    Type:  identity.PrincipalService,
    Scope: identity.Scope{TenantID: "tenant-1"},
    Roles: []identity.Role{identity.RoleService},
  },
})
if err != nil {
  log.Fatal(err)
}
middleware, err := agentflow.NewAPIKeyMiddleware(agentflow.APIKeyMiddlewareConfig{Authenticator: auth})
if err != nil {
  log.Fatal(err)
}
handler := middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
  principal, _ := identity.RequirePrincipal(r.Context())
  _ = principal
}))
```

生产 OIDC/OAuth2 网关可使用 OIDC Discovery/JWKS 自动刷新来校验 JWT：

```go
auth, err := agentflow.NewOIDCJWTAuthenticator(agentflow.OIDCJWTAuthenticatorConfig{
  Issuer:          "https://issuer.example.com",
  Audience:        "agentflow-api",
  DiscoveryURL:    "https://issuer.example.com/.well-known/openid-configuration",
  RefreshInterval: 5 * time.Minute,
})
if err != nil {
  log.Fatal(err)
}
middleware, err := agentflow.NewJWTMiddleware(agentflow.JWTMiddlewareConfig{Authenticator: auth})
```

为 HTTP handler 添加授权检查：

```go
authz, err := agentflow.NewAuthorizationMiddleware(agentflow.AuthorizationMiddlewareConfig{
  Policy:   security.NewDefaultRolePolicy(),
  Action:   security.ActionRunSubmit,
  Resource: security.Resource{Type: "run"},
  Audit:    auditSink,
})
if err != nil {
  log.Fatal(err)
}
handler = middleware(authz(handler))
```

使用 runtime 工具授权和审计记录运行框架：

```go
fw, err := agentflow.New(
  scenario,
  agentflow.WithSecurityPolicy(security.NewDefaultRolePolicy()),
  agentflow.WithAuditSink(auditSink),
)
ctx := identity.WithPrincipal(context.Background(), identity.Principal{
  ID:    "svc-agent-runner",
  Type:  identity.PrincipalService,
  Scope: identity.Scope{TenantID: "tenant-1"},
  Roles: []identity.Role{identity.RoleService},
})
result, err := fw.Run(ctx, agentflow.RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "hello"})
```

将审计事件写入 append-only JSONL 文件：

```go
auditSink, err := agentflow.NewFileAuditSink("./data/audit/events.jsonl")
if err != nil {
  log.Fatal(err)
}
err = auditSink.Record(ctx, audit.Event{
  Type:    audit.EventRunSubmitted,
  RunID:   "run-1",
  Outcome: "accepted",
})
```

## 架构

项目采用 DDD 风格分层和 Hexagonal Ports/Adapters：

```text
examples/
  go/          # 可复制的集成 main（minimal、validate、builder、http-worker 等）
  deploy/      # 参考 Compose 栈（Postgres、Redis、MinIO）
pkg/
  core/
  builder/     # Go DSL 构造 core.Scenario
  catalog/     # Tool/Skill manifest 加载与校验
  llm/
  contextwindow/
  memory/
  runstate/
internal/
  application/
    runtime/
    orchestration/
    scenario/
  adapter/
    config/yaml/
    human/cli/
    human/http/
    llm/openai/
    llm/anthropic/
    llm/local/
    llm/mock/
    memory/inmem/
    runstate/inmem/
    blob/inmem/
```

设计边界：

- `Skill = prompt fragments + tool whitelist/policy + 可内联的 workflow 子图`。
- `Tool = 带 Schema 的执行单元`。
- `Agent = 拥有 LLM 和 Memory 绑定的实体`。
- `RunStateRepository` 与 Memory 分离，专门处理可恢复的运行快照。
- 上下文治理按 LLM Profile 生效：不同 Agent/Tool 可以路由到具有不同窗口、输出、thinking 和压缩策略的 LLM Profile。
- 自主执行支持可选 planning pass、LLM 工具调用循环、工具白名单、审批拒绝、每次运行 rate cap、分类重试、受限工具结果回填和生命周期事件。
- 结构化输出使用 Agent 级 `output_schema` 和 Provider 的 `StructuredOutputter`；普通流式输出使用 `Streamer`，带工具 Agent 的流式输出会复用受治理工具循环，并在结束后持久化累积的最终答案。
- Memory 绑定已接入 Runtime 读写，用于 conversation/session 历史。
- 固定工作流按图依赖和边执行，支持有限并行、`parallel_group`/`loop` 节点、条件跳过、重试、transform/agent/human-gate 节点和 CAS 安全输出保存。
- Workflow human-gate 节点会持久化 `CurrentNodeID`/`PendingGate`，审批后可继续执行下游图；`ResumeAndContinue` 还支持自主、工作流和工具审批暂停路径的续跑。
- 外部事件通过 `scenario.triggers` 映射到 `Framework.HandleEvent`、Webhook HTTP（`NewWebhookHTTPHandler`）、同步 `/v1/events` 和异步 `event` job。
- `sub_agents` 会在自主执行中作为虚拟 delegation tool 暴露给 supervisor Agent。
- Skill prompt fragments、Agent policy、Tool policy 和 workflow segments 会在场景构建阶段展开为命名空间化的 workflow 节点。
- Tool 的声明面和执行面已经分离：`scenario.tools` 向 LLM 和校验器暴露 manifest，`WithToolExecutor` 提前注册轻量 executor，`WithToolResolver` 则在允许的调用真正进入执行阶段时按需绑定重型或租户隔离 executor。
- 文件版 RunState、BlobStore 和 Memory 适配器可通过根门面使用；PostgreSQL RunState 和 Redis RunState 可用于生产持久化；S3-compatible BlobStore 可用于大输出对象存储，支持 MinIO/AWS S3 风格 endpoint，以及经过验证的腾讯云 COS/阿里云 OSS S3 兼容接口；Redis 分布式租约可用于 Worker 协调；异步队列和 Worker 契约支持 `run`、`event`、`resume.continue` 任务（`NewFrameworkJobHandler`），HTTP 路由由 `NewAsyncRunHTTPHandler` 和 `NewProductionHTTPHandler` 提供；当输出超过 `step_output_threshold` 时会外置到 BlobStore。
- 企业 identity context、API Key middleware、静态和 OIDC/JWKS JWT middleware、授权 middleware、RBAC policy 契约和 runtime tool authorization 可通过 `pkg/identity`、`pkg/security`、`NewStaticAPIKeyAuthenticator`、`NewOIDCJWTAuthenticator`、`NewAPIKeyMiddleware`、`NewJWTMiddleware`、`NewAuthorizationMiddleware` 和 `WithSecurityPolicy` 使用。
- Audit event 契约和 noop/内存/文件 sink 可通过 `pkg/audit`、`NewNoopAuditSink`、`NewInMemoryAuditSink`、`NewFileAuditSink` 和 `WithAuditSink` 使用。
- 运行时可观测面板、事件仓库、实时 EventHub 和 PostgreSQL 自动建表可通过 `NewPostgresEventStore`、`NewInMemoryEventStore`、`NewEventStoreSink`、`NewEventHub` 和 `NewObservabilityHTTPHandler` 使用。
- 企业认证/租户和可观测/治理设计见 [docs/security-auth-tenancy.md](docs/security-auth-tenancy.md)、[docs/observability-governance.md](docs/observability-governance.md) 与 [docs/observability-dashboard.md](docs/observability-dashboard.md)。
- 内存适配器是并发安全的，并按 run/session 命名空间隔离。

## 测试

默认单元测试：

```sh
make test
```

集成测试：

```sh
make test-integration
```

真实本地模型流程测试：

```sh
export AGENT_REALMODEL_BASE_URL="http://127.0.0.1:1234/v1"
export AGENT_REALMODEL_MODEL="qwen/qwen3.6-35b-a3b"
export AGENT_REALMODEL_API_KEY="..."
make test-realmodel
```

并发内存适配器 Race 测试：

```sh
make test-race
```

静态检查和漏洞扫描：

```sh
make vet
make lint
make security
```

直接运行：

```sh
CGO_ENABLED=0 go test -ldflags="-w" ./...
CGO_ENABLED=0 go test -ldflags="-w" -tags=integration ./...
CGO_ENABLED=0 go test -ldflags="-w" -tags=realmodel -run TestRealModel -v .
go test -race ./internal/adapter/memory/inmem ./internal/adapter/runstate/inmem ./internal/adapter/blob/inmem
```

在较旧的 Darwin 本地工具链 + `CGO_ENABLED=0` 环境中，`-ldflags="-w"` 可规避本地 `dyld` 测试二进制问题。

## 当前状态

已实现：

- YAML loader 和 validator
- Autonomous runtime engine，包含自主执行前的可选 planning pass
- 已接入根门面的 Fixed-workflow runner
- In-memory Memory、RunStateRepository、BlobStore
- LLM 抽象，以及 OpenAI-compatible、Anthropic、local、router 和 mock 测试路径的根包构造函数
- 注册工具、OpenAI-compatible function calling 和 Anthropic Messages tool use 的自主工具调用循环
- 通过 `WithToolResolver` 在运行时策略检查后惰性绑定重型或租户隔离工具 executor
- Runtime memory integration：注入历史并持久化用户/助手/工具观察结果
- 固定工作流图调度：依赖、并行、`parallel_group`/`loop` 节点、重试、条件、transform/agent/human-gate 节点、CAS 安全输出保存
- Workflow-level HITL pause/resume，以及 `ResumeAndContinue` 续跑路径
- 事件触发器（`scenario.triggers`）、`HandleEvent`、Webhook HTTP 和异步 `event` job
- 内置 Git / ticket 工具 executor，适用于代码审查和客服工单场景
- 自主运行中的 planning pass 执行跟踪
- 通过虚拟 sub-agent tools 实现多 Agent delegation baseline
- Skill prompt/workflow expansion、compatible-agent 校验、Agent policy overlay 和 Tool policy overlay
- 文件版 RunState、Blob、Memory 持久化适配器，以及 PostgreSQL RunState、Redis RunState 和 S3-compatible BlobStore 持久化适配器
- 用于 Worker 和工作流协调的 Redis 分布式租约适配器
- 异步 Job Queue 和 Worker 契约、内存/PostgreSQL 队列适配器、租约续租、支持 `run`/`event`/`resume.continue` 的 framework job handler，以及带可选同步 event/HITL 路由的生产 HTTP handler
- 企业 identity context、API Key middleware、静态/JWKS Discovery JWT middleware、授权 middleware、RBAC policy 契约和 runtime tool authorization
- Audit event 模型，以及 noop、内存和 JSONL 文件 sink，加上 framework audit wiring
- `ResumeAndContinue` 与 `HandleEvent` 事件驱动运行
- Runtime hardening：全局/Agent/Profile timeout、分类 LLM/Tool retry + 指数退避、Tool rate cap、工具结果回填上限、失败状态持久化、大输出 Blob 外置
- 结构化输出和流式输出 Runtime 路径，包含带工具 Agent 的流式运行
- 上下文治理：滑动窗口、启发式摘要压缩、丰富 LLM Profile 配置、`ContextPrepared` 事件
- HTTP HITL 与 Webhook 路由（`NewHumanHTTPHandler`、`NewWebhookHTTPHandler`）
- GitHub Actions CI、golangci-lint、govulncheck/CodeQL、Dependabot 与模块发版检查
- 单元测试和集成测试

后续生产路线：

- 在现有 recorder/tracer 端口之上补充具体 Prometheus/OpenTelemetry exporter
- 在当前 Compose 和 Kustomize base 之外补充 Helm chart 打包
- 完善 Tool/Skill catalog manifest 校验、打包流程，以及针对托管服务的集成测试矩阵

## 贡献

参见 [CONTRIBUTING.md](./CONTRIBUTING.md)。

## License

本项目使用 [Apache License 2.0](./LICENSE)。
