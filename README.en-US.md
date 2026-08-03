

# agentflow-go

[![Go Reference](https://pkg.go.dev/badge/github.com/aijustin/agentflow-go.svg)](https://pkg.go.dev/github.com/aijustin/agentflow-go)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](./LICENSE)
[![Release](https://img.shields.io/github/v/release/aijustin/agentflow-go?label=release)](https://github.com/aijustin/agentflow-go/releases)

[English](./README.en.md) | 简体中文

`agentflow-go` is an embeddable Agent **runtime library** tailored for **Go backend engineers**: define Agents, Tools, Skills, and workflows using Go code (`pkg/builder` or `core.Scenario`), explicitly wire up LLM Gateway, Memory, RunState, and Human-in-the-loop in your own services, then call `Framework.Run` — **no Python runtime required, and no need to host your business logic on external Agent platforms**.

**Who it's for:** Teams that already have a Go backend, want to deploy Agents/workflows in-process or within self-managed Workers, and prioritize type safety, testability, approval governance, and production observability.

## Feature Highlights

### Orchestration & Runtime

- **Three orchestration modes**: `autonomous` (ReAct tool loop), `fixed_workflow` (deterministic DAG), `hybrid` (workflow stages + autonomous stages)
- **Graph expressiveness**: subgraph nesting, `map` dynamic fan-out, `parallel_group`, `loop`, conditional edges; Builder provides `MapOver`, `RouteIf`, `ParallelGroup`, etc. DSL
- **Skill semantics**: prompt fragments + tool whitelist/policy + inline workflow subgraphs, expanded into namespaced nodes at compile time
- **Multi-Agent**: supervisor + `sub_agents` virtual delegation tool; optional planning pass (JSON plan before autonomous execution)
- **AI Auto-Composition (ComposeGraph)**: one-sentence task → agentic composer with graph composition tool loop + incremental validation feedback, producing a verifiable DAG; `catalog` mode only orchestrates registered parts, `scenario` mode can incrementally create new Agent/Skill (rejects overwriting existing IDs); defaults to ephemeral execution without modifying live scenarios (usage in [compose-graph.md](docs/compose-graph.md), mechanism in Section 11 of [orchestration-flow.md](docs/orchestration-flow.md), example `examples/go/compose-graph`)

### Production Governance

- **Tool Governance**: Agent whitelist, approval rejection, per-run rate cap, categorized LLM/Tool retries, tool result size limits
- **Human-in-the-loop**: autonomous pause, workflow `human_gate` nodes, HMAC Token, `ResumeAndContinue` for resuming runs
- **Enterprise Capabilities**: Identity context, API Key / JWT middleware, RBAC, `AuditSink` for audit events
- **Persistence & Time Travel**: File / PostgreSQL / Redis RunState; S3-compatible Blob; CAS snapshots, Checkpoint history chains, **resume or fork** from any step/checkpoint

### AgentFlow Studio (Built-in Web Dashboard)

Mount `NewObservabilityHTTPHandler` to get a visual panel at `/observability/` (defaults to Chinese). The next-gen Studio is an **AI-first composition workspace** (React + React Flow SPA, `go:embed` embedded, single binary distribution; falls back to legacy inline UI when frontend isn't built):

| View | Capabilities |
|------|------|
| **Canvas** | **AI Composition Bar** (one-sentence → catalog/scenario mode to generate graph, the graph above is an editable draft), drag-and-drop parts box, React Flow canvas editing, node Inspector, Undo/Redo, validation / YAML / Go codegen / save, dry-run panel (SSE node real-time highlighting + HITL approval) |
| **Runs** | runs list polling, Trace tree (span nesting), step output, checkpoint timeline (view snapshot / resume from version / fork), thread lineage |
| **Diff** | Dual run step-level diff (only A / only B / output mismatch highlighting) |

Frontend project at `web/studio/` (Vite + React + TS + Tailwind), `make studio-ui` builds and embeds it; HTTP API adds `POST /api/studio/compose` and `GET /api/studio/parts` (production side mirrors at `/v1/studio/compose`, `/v1/studio/parts`).

Example: `go run ./examples/go/http-worker/main.go` → `http://127.0.0.1:7060/observability/`. See [observability-dashboard.md](docs/observability-dashboard.md), [studio-roadmap.md](docs/studio-roadmap.md).

### Observability & Deployment

- **Metrics & Tracing**: Prometheus recorder, OpenTelemetry tracer, event-level `parent_span_id` propagation
- **HTTP Production Suite**: `httpx.NewProductionHTTPHandler`, Async Job Worker (`run` / `event` / `resume.continue`)
- **Memory Tier**: Postgres warm + file/S3 cold tier, migration events, optional RAG summarization coordination
- **Reference Deployments**: [Compose Stack](examples/deploy/README.md), [Helm chart](examples/deploy/helm/agentflow-reference/)

### Developer Experience

- **Builder-first (v0.2+)**: Scenario as Go code, `ValidateScenario` + `make validate-builder` can enter CI; Studio still supports YAML import/export interoperability
- **Explicit Hexagonal Wiring**: Gateway, ToolExecutor, RunState, EventSink controlled by host, unit test and mock friendly
- **Difference from LangGraph**: Go native embedding, verifiable Scenario, enterprise-readable tool contracts; borrows orchestration concepts but **does not aim for full Python parity** (see [competitive-analysis-langgraph.md](docs/competitive-analysis-langgraph.md))

## Quick Start

```sh
go get github.com/aijustin/agentflow-go
go run ./examples/go/minimal/main.go
go run ./examples/go/builder/main.go
make validate-builder
make test
```

Product Direction: [docs/product-direction.md](docs/product-direction.md) · Builder Reference: [docs/builder-reference.md](docs/builder-reference.md)

Recommended to run `GOTOOLCHAIN=auto make release-check` before release. See [docs/release-checklist.md](docs/release-checklist.md) and [docs/api-stability.md](docs/api-stability.md).

Integration Guide: [docs/library-integration.md](docs/library-integration.md) · HTML Manual: [docs/manual.html](docs/manual.html) · Comparison with LangGraph: [docs/competitive-analysis-langgraph.md](docs/competitive-analysis-langgraph.md)

## Integration Paths

| Target | Entry |
|------|------|
| **Preferred: Go DSL to construct scenarios** | [docs/builder-reference.md](docs/builder-reference.md) · [examples/go/builder/main.go](examples/go/builder/main.go) |
| Embed into existing Go services | [docs/library-integration.md](docs/library-integration.md) |
| Minimal in-process run | [examples/go/minimal/main.go](examples/go/minimal/main.go) |
| Postgres / File persistence | [examples/go/postgres/main.go](examples/go/postgres/main.go) |
| HTTP + Async Worker | [examples/go/http-worker/main.go](examples/go/http-worker/main.go) |
| HITL pause & resume | [examples/go/hitl-resume/main.go](examples/go/hitl-resume/main.go) |
| Event triggered | [examples/go/event-trigger/main.go](examples/go/event-trigger/main.go) |
| Testing & example wiring | [pkg/testutil](pkg/testutil/testutil.go) |

Library API (root package): `ValidateWiring`, `New`, `Framework.Run` / `ComposeGraph`, `NewFrameworkJobHandler`, `ScenarioJSONSchema`, `Version`; HTTP constructors in `pkg/httpx` (e.g., `NewProductionHTTPHandler`, `NewObservabilityHTTPHandler`); adapters in `pkg/adapters` (e.g., `NewPrometheusRecorder`, `NewOpenTelemetryTracer`, RunState/Blob/EventStore); Builder stack entry points in [builder.go](builder.go) (e.g., `MinimalAutonomous`).

## Example Paths Table

### Runnable Go Examples (`examples/go/`)

| Directory | Description | Run Command |
|------|------|----------|
| [builder](examples/go/builder/main.go) | Go DSL scenario construction & in-process Run (**recommended starting point**) | `go run ./examples/go/builder/main.go` |
| [minimal](examples/go/minimal/main.go) | Minimal embedding: `builder` → `testutil.WiringOptions` → `New` → `Run` | `go run ./examples/go/minimal/main.go` |
| [compose-graph](examples/go/compose-graph/main.go) | AI ComposeGraph (catalog / scenario mode) | `go run ./examples/go/compose-graph` |
| [postgres](examples/go/postgres/main.go) | Postgres / File RunState persistence | `go run ./examples/go/postgres/main.go` |
| [http-worker](examples/go/http-worker/main.go) | Mounts `httpx.NewProductionHTTPHandler` + Async Worker + Studio | `go run ./examples/go/http-worker/main.go` |
| [hitl-resume](examples/go/hitl-resume/main.go) | HITL pause & `ResumeAndContinue` | `go run ./examples/go/hitl-resume/main.go` |
| [event-trigger](examples/go/event-trigger/main.go) | Event-driven Run via `scenario.triggers` | `go run ./examples/go/event-trigger/main.go` |
| [tier-memory](examples/go/tier-memory/main.go) | Minimal in-process tier memory example | `go run ./examples/go/tier-memory/main.go` |
| [tier-worker](examples/go/tier-worker/main.go) | Postgres warm/cold tier + `memory.reconcile` async Worker | See [examples/deploy/](examples/deploy/README.md) |
| [validate](examples/go/validate/main.go) | Validate builder catalog or legacy YAML | `go run ./examples/go/validate -kind builder all` |

For production, replace `testutil.WiringOptions` with `WithLLMGateway` / `WithToolExecutor`; see `pkg/testutil` for testing wiring.

### Builder catalog mapping

Full Catalog ID to `builder.*` function mapping in [docs/builder-reference.md](docs/builder-reference.md). Shared stack implementation in [examples/go/scenario/scenario.go](examples/go/scenario/scenario.go).

Validate all catalog stacks:

```sh
go run ./examples/go/validate -kind builder all
make validate-builder
```

## Environment Requirements

- Go 1.25.12+
- macOS/Linux shell
- Building Studio SPA: Node.js 24 LTS + pnpm 10 (standard Go builds do not require Node.js)

### Using as a Framework in Other Go Projects

Add dependency:

```sh
go get github.com/aijustin/agentflow-go
```

Import the root facade package:

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

To integrate custom LLM, Memory, RunState, EventSink, or HumanGate, use the Option API:

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

Constructors for common LLM Providers are exposed from the root package:

```go
gateway := adapters.NewOpenAICompatibleGateway([]llm.Profile{{
  Name:      "default",
  Provider:  "openai-compatible",
  Model:     "qwen/qwen3.6-35b-a3b",
  Endpoint:  "http://127.0.0.1:1234/v1",
  APIKeyEnv: "AGENT_REALMODEL_API_KEY",
}}, nil)

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithLLMGateway(gateway))
```

If you need to connect both OpenAI-compatible Chat and Embedding, use `NewOpenAICompatibleProvider` and explicitly declare profile capabilities:

```go
provider := adapters.NewOpenAICompatibleProvider([]llm.Profile{
  {Name: "chat", Provider: "openai-compatible", Model: "qwen/qwen3.6-35b-a3b", Endpoint: "http://127.0.0.1:1234/v1"},
  {Name: "embed", Provider: "openai-compatible", Model: "text-embedding-3-small", Endpoint: "http://127.0.0.1:1234/v1", Capabilities: []llm.Capability{llm.CapEmbed}},
}, nil)
```

For mixed Provider scenarios, use `NewLLMProviderRouter` to route chat/tool/structured/stream and embedding calls by profile. Capabilities are explicitly checked: unsupported capabilities will fail clearly and will not be silently mocked.

```go
openaiProvider := adapters.NewOpenAICompatibleProvider(openaiProfiles, nil)
anthropicGateway := adapters.NewAnthropicGateway(anthropicProfiles, nil)

provider := adapters.NewLLMProviderRouter(map[string]llm.Gateway{
  "chat":  anthropicGateway,
  "embed": openaiProvider,
})
```

Structured Output: Configure JSON Schema in `agents.<name>.output_schema` and call `RunStructured`. The LLM Gateway must implement `llm.StructuredOutputter`:

```go
result, err := fw.RunStructured(ctx, agentflow.RunRequest{
    RunID:  "run-json",
    Agent:  "assistant",
    Prompt: "return JSON",
})
fmt.Println(string(result.StructuredOutput))
```

Streaming Output: Use a Gateway that implements `llm.Streamer`:

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

When an Agent is configured with tools and the LLM Gateway supports `CapToolCall`, the Runtime executes an autonomous tool-calling loop: sends tool specs to the LLM, validates returned tool calls against the Agent whitelist, enforces approval policies and per-run `rate_cap`, performs exponential backoff retries on categorized transient LLM/tool errors per `retry_limit`/`max_retries` (`write`/`external`/`dangerous` tools do not auto-retry by default), executes the registered `ToolExecutor`, feeds back the constrained tool results to the LLM, until the LLM returns a final answer or `max_steps` is reached.

`Stream` also supports Agents with tools: runs the same governed tool loop as `Run`, and emits `kind=tool_call` / `tool_result` (or `tool_denied`) progress chunks sequentially within the loop. The final answer is still delivered via a terminal `Done` chunk (progress is not written to the final persisted output). Each model call remains a blocking `ChatWithTools` for now (not a provider-level tool_call token stream). If the Agent configures a `before_final_answer` HITL checkpoint, `Stream` will reject it directly (use `Run` / `RunStructured` instead); tool-level `approval: pause` can still be exposed via a `Paused` chunk at the end of the stream. `RunStructured` / `Stream` will reject fixed workflows containing `agent` nodes to prevent the agent from being fully executed before a secondary autonomous/structured phase.

With `orchestration.planning.enabled: true` configured, the Runtime executes a planning pass before the autonomous tool loop. Planning defaults to the current executing Agent, or a dedicated planning Agent can be specified via `orchestration.planning.agent`; the generated short JSON plan is injected into the subsequent execution context. Setting `orchestration.planning.execute: true` allows tracking plan step completion status within the tool loop (see `builder.MultiExpertResearch()`).

Fixed workflows support `tool`, `agent`, `skill`, `human_gate`, `transform`, `parallel_group`, and `loop` nodes. `condition` can read `steps.<node_id>` paths using `exists(...)`, `missing(...)`, `eq(...)`, `ne(...)`; `transform` nodes can use `set`/`copy` to construct structured output from preceding steps.

When an Agent binds `memory`, the Runtime reads conversation/session memory before context preparation and injects it into the LLM context, appending user input, assistant replies, and tool observations after execution. The root facade automatically creates an in-memory repository for `in_memory` types, unless the caller explicitly provides a custom repository. `session` / `long_term` scopes must explicitly configure `namespace` (otherwise `Validate`/`New` fails) to avoid defaulting to `scenario:agent` and causing cross-caller session bleeding.

Enable HITL Gate with built-in HMAC Token:

```go
scenario := builder.MinimalHumanInLoop("assistant")
fw, err := agentflow.New(scenario,
    agentflow.WithHITLTokenSecret([]byte("strong-secret-16bytes"), nil),
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

To resume the runtime after process restarts, use the file persistence adapter:

```go
runs, _ := adapters.NewFileRunStateRepository("./data/runs")
blobs, _ := adapters.NewFileBlobStore("./data/blobs")
memoryRepo, _ := adapters.NewFileMemoryRepository("./data/memory")

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithRunStateRepository(runs),
    agentflow.WithBlobStore(blobs),
    agentflow.WithMemoryRepository("session", memoryRepo),
)
```

For production environments requiring PostgreSQL RunState, register a `database/sql` driver on the application side and pass the initialized connection pool to the root facade constructor:

```go
db, err := sql.Open("pgx", os.Getenv("AGENTFLOW_POSTGRES_DSN"))
if err != nil {
  log.Fatal(err)
}
runs, err := adapters.NewPostgresRunStateRepository(db)
if err != nil {
  log.Fatal(err)
}

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithRunStateRepository(runs),
)
```

Schema contracts and operational notes in [docs/persistence/postgres-runstate.md](docs/persistence/postgres-runstate.md).

If you prefer Redis for low-latency CAS RunState, you can also use the Redis RunState adapter:

```go
runs, err := adapters.NewRedisRunStateRepository(adapters.RedisRunStateRepositoryConfig{
  Addr:      os.Getenv("AGENTFLOW_REDIS_ADDR"),
  Password:  os.Getenv("AGENTFLOW_REDIS_PASSWORD"),
  KeyPrefix: "agentflow:runstate:",
})
if err != nil {
  log.Fatal(err)
}
```

Storage semantics and operational notes in [docs/persistence/redis-runstate.md](docs/persistence/redis-runstate.md).

Asynchronous execution in production can use queues and Workers. The PostgreSQL queue adapter is based on `database/sql` and does not force-bind to a specific driver:

```go
queue, err := adapters.NewPostgresJobQueue(db)
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

`httpx.NewProductionHTTPHandler` mounts `/healthz`, `/readyz`, async run/event/resume job APIs; when `Framework` is configured, it also mounts sync `/v1/events` and `/v1/hitl/resume`. The constructor defaults to requiring `AuthMiddleware` + `Policy`; only local loopback development can explicitly set `InsecureAllowNoAuth`. More details in [docs/async-runtime.md](docs/async-runtime.md) and [docs/persistence/postgres-queue.md](docs/persistence/postgres-queue.md).

MCP Servers can become standard governed tools via adapters, without altering the runtime core:

```go
mcpClient, err := adapters.NewMCPHTTPClient("http://127.0.0.1:3333/mcp", nil)
if err != nil {
  log.Fatal(err)
}
searchTool, err := adapters.NewMCPToolExecutor(mcpClient, "search")
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.New(builder.MinimalMCPTool("assistant"),
  agentflow.WithToolExecutor("docs.search", searchTool),
)
```

Adapter patterns and security notes in [docs/mcp-tools.md](docs/mcp-tools.md).

Heavyweight or tenant-isolated tools do not need to be fully constructed at framework startup. You can first declare a manifest in `scenario.tools`, then use `WithToolResolver` to lazily resolve the actual executor at runtime after allowlist, approval, RBAC, governance policy, and rate cap checks:

```go
resolver := adapters.ToolResolverFunc(func(ctx context.Context, tool core.Tool) (core.ToolExecutor, error) {
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

`WithToolExecutor` remains suitable for lightweight or resident tools, and takes precedence over the resolver. Executors resolved by the resolver are cached per scenario tool name for the framework's lifetime. Skills do not initialize tools; they only expand prompt fragments, policy overrides, and workflow segments during scenario construction, with actual executor binding completed by the resolver at invocation.

To read internal APIs, register a constrained HTTP Tool Executor:

```go
httpTool, err := adapters.NewHTTPToolExecutor(adapters.HTTPToolConfig{
  AllowedHosts: []string{"https://status.example.internal"},
})
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.New(builder.MinimalHTTPTool("assistant"),
  agentflow.WithToolExecutor("http.status", httpTool),
)
```

This executor requires a host allowlist configuration and defaults to allowing only `GET`/`HEAD`. See [docs/tools-http.md](docs/tools-http.md).

To read local runbooks or checked-out documentation, register a constrained Filesystem Read Tool Executor:

```go
filesystemTool, err := adapters.NewFilesystemToolExecutor(adapters.FilesystemToolConfig{
  AllowedRoots: []string{"/srv/agentflow/runbooks"},
})
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.New(builder.MinimalFilesystemTool("assistant"),
  agentflow.WithToolExecutor("fs.read", filesystemTool),
)
```

This executor requires a root allowlist, rejects path traversal and symlink escape, and limits file size. See [docs/tools-filesystem.md](docs/tools-filesystem.md).

When reading business, ticket, or reporting databases, register a constrained SQL Query Tool Executor and use named allowlist queries:

```go
sqlTool, err := adapters.NewSQLToolExecutor(adapters.SQLToolConfig{
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

This executor defaults to executing only named `SELECT` queries, rejects multi-statement SQL, includes timeouts, and limits returned rows. See [docs/tools-sql.md](docs/tools-sql.md).

SQL tools can connect to any `database/sql` driver, including PostgreSQL, MySQL, and ClickHouse. The host application imports the specific driver and passes an opened `*sql.DB`; `agentflow-go` does not enforce database driver dependencies.

Code review pipelines can register a read-only Git tool:

```go
gitTool, err := adapters.NewGitToolExecutor(adapters.GitToolConfig{
  AllowedRoots: []string{"/workspace/repos"},
})
fw, err := agentflow.New(builder.CodeReviewPipeline(),
  agentflow.WithToolExecutor("git", gitTool),
)
```

See [docs/tools-git.md](docs/tools-git.md). Executors must be explicitly registered via `WithToolExecutor` (or `WithToolResolver`).

Customer service ticket scenarios can register a ticket tool and inject a store:

```go
store := adapters.NewMemoryTicketStore(map[string]adapters.Ticket{
  "T-9": {ID: "T-9", Title: "Login issue", Status: "open"},
})
ticketTool, err := adapters.NewTicketToolExecutor(adapters.TicketToolConfig{Store: store})
fw, err := agentflow.New(builder.MinimalTicketHandling("support"),
  agentflow.WithToolExecutor("ticket", ticketTool),
)
```

See [docs/tools-ticket.md](docs/tools-ticket.md).

RAG scenarios can compose Embedder, VectorStore, and Retriever Tool:

```go
store, err := adapters.NewPostgresVectorStore(adapters.PostgresVectorStoreConfig{DB: db})
if err != nil {
  log.Fatal(err)
}
retriever, err := adapters.NewRetrieverTool(adapters.RetrieverToolConfig{
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

Public contracts and pgvector table schema in [docs/knowledge-rag.md](docs/knowledge-rag.md) and [docs/persistence/pgvector.md](docs/persistence/pgvector.md).

Use the SQL in [migrations/postgres](migrations/postgres), and let the host application's own migration tool create tables before connecting the Postgres adapter. See [docs/persistence/postgres-runstate.md](docs/persistence/postgres-runstate.md) and [docs/persistence/postgres-queue.md](docs/persistence/postgres-queue.md).

When large outputs need to go to S3-compatible object storage, configure `BlobStore` separately:

```go
blobs, err := adapters.NewS3BlobStore(adapters.S3BlobStoreConfig{
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

Object paths and security notes in [docs/persistence/s3-blobstore.md](docs/persistence/s3-blobstore.md).

Enterprise-grade observability and governance capabilities remain optional and low-dependency:

```go
scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithEventSink(adapters.NewSlogEventSink(logger)),
  agentflow.WithAuditSink(adapters.NewSlogAuditSink(logger)),
  agentflow.WithToolGovernancePolicy(governance.ChainToolPolicies(
    governance.NewToolBudgetPolicy(8),
    governance.NewMaxSideEffectPolicy(core.SideEffectRead),
  )),
  agentflow.WithOutputRedactor(governance.NewJSONFieldRedactor("secret", "token")),
)
```

Governance policies take effect before tool execution, and output redaction is performed before runtime step output persistence.

AgentFlow also includes a built-in runtime observability panel for viewing real-time sessions, orchestration timelines, and event details. The PostgreSQL event store automatically creates tables and indexes by default; enabling the panel only requires wiring up an event sink and mounting the HTTP handler:

```go
eventStore, err := adapters.NewPostgresEventStore(ctx, adapters.PostgresEventStoreConfig{DB: db})
if err != nil {
  log.Fatal(err)
}
eventHub := adapters.NewEventHub()

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithEventSink(adapters.NewEventFanoutSink(
    adapters.NewEventStoreSink(eventStore, eventHub),
    adapters.NewSlogEventSink(logger),
  )),
)

dashboard, err := httpx.NewObservabilityHTTPHandler(httpx.ObservabilityHTTPHandlerConfig{
  Store:               eventStore,
  Hub:                 eventHub,
  InsecureAllowNoAuth: true, // Local dev only; production must configure AuthMiddleware
})
mux.Handle("/observability/", http.StripPrefix("/observability", dashboard))
```

Database configuration, auto-table creation, endpoint list, and security recommendations in [docs/observability-dashboard.md](docs/observability-dashboard.md).

Low-level extension interfaces are located at:

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

Built-in tool adapter documentation in [docs/tools-http.md](docs/tools-http.md), [docs/tools-filesystem.md](docs/tools-filesystem.md), [docs/tools-sql.md](docs/tools-sql.md), [docs/tools-git.md](docs/tools-git.md), [docs/tools-ticket.md](docs/tools-ticket.md), [docs/mcp-tools.md](docs/mcp-tools.md), and [docs/knowledge-rag.md](docs/knowledge-rag.md).

### Install Dependencies

```sh
go mod download
```

### Validate Example Scenarios

```sh
go run ./examples/go/validate -kind builder all
make validate-builder
```

### Runnable Examples

| Example | Description |
| --- | --- |
| [examples/go/minimal](examples/go/minimal/main.go) | In-process `Run` + test wiring |
| [examples/go/postgres](examples/go/postgres/main.go) | File or Postgres RunState |
| [examples/go/http-worker](examples/go/http-worker/main.go) | Production HTTP Handler + Async Worker |
| [examples/go/hitl-resume](examples/go/hitl-resume/main.go) | HITL pause & `ResumeAndContinue` |
| [examples/go/event-trigger](examples/go/event-trigger/main.go) | `HandleEvent` and triggers |

Replace `testutil.WiringOptions` in the examples with explicit `WithLLMGateway` / `WithToolExecutor` for production use.

Troubleshooting in [docs/troubleshooting.md](docs/troubleshooting.md).

## HTTP Integration

Mount the library-provided Handler in your own service, e.g.:

```sh
go run ./examples/go/http-worker/main.go
```

Listens on `127.0.0.1:7060` by default (overridable via `AGENT_HTTP_ADDR`); Studio panel: `http://127.0.0.1:7060/observability/`.

For production HITL resume, use `POST /v1/hitl/resume` from `httpx.NewProductionHTTPHandler` or `httpx.NewHumanHTTPHandler`. Setting `"continue": true` invokes `ResumeAndContinue`:

```sh
curl -X POST http://localhost:7060/v1/hitl/resume \
  -H 'Content-Type: application/json' \
  -d '{
    "token": "'"$TOKEN"'",
    "decision": "approve",
    "continue": true
  }'
```

Webhook events use `POST /v1/events` when `Framework` is configured. See [docs/async-runtime.md](docs/async-runtime.md).

Tokens passed over the network use HMAC signatures. Production environments must set strong secrets and use a persistent RunState repository.

## YAML Scenario Format (Studio Interoperability)

> Define new scenarios using [`pkg/builder`](docs/builder-reference.md) in Go. YAML is only for **Studio import/export** and field mapping; public loading APIs like `LoadScenarioFile` / `NewFromFile` are no longer provided.

- Field Reference: [docs/configuration-reference.md](docs/configuration-reference.md)
- JSON Schema: [schemas/agentflow.scenario.schema.json](schemas/agentflow.scenario.schema.json) (Go: `agentflow.ScenarioJSONSchema()`)
- Orchestration Flow: [docs/orchestration-flow.md](docs/orchestration-flow.md)
- Studio Import: `Framework.ImportStudioScenarioYAML` · Export: `GenerateStudioScenarioYAML` / `SaveStudioGraph`

Example stacks (Go builder, not YAML files):

| Builder | Description |
| --- | --- |
| `builder.MinimalAutonomous("assistant")` | Autonomous tool loop baseline |
| `builder.MinimalFixedWorkflowReview("reviewer")` | Graph workflow + conditions + HITL |
| `builder.CodeReviewPipeline()` | Git tools + `parallel_group` |
| `builder.MultiExpertResearch()` | Hybrid + planning |

Default CI catalog (`CoreCatalog`, autonomous): `make validate-builder`; full 19 items: `go run ./examples/go/validate -kind builder full`

## Library API

Most applications only need to import the root facade:

```go
import agentflow "github.com/aijustin/agentflow-go"
```

Public Packages:

| Package | Purpose |
| --- | --- |
| root package | Framework facade: validation, execution, resume, event handling, Studio interoperability, and extension injection. |
| `pkg/adapters` | Concrete adapter constructors (run-state/blob/memory storage, job queues, LLM providers, knowledge, MCP, tool executors, tiered memory, observability), independent of the root package. |
| `pkg/httpx` | HTTP adapter constructors (checkpoint, retention, studio, webhook/HITL, async jobs, production composition, observability dashboard) and knowledge/MCP wiring. |
| `pkg/async` | Job Queue, Lease, Handler, and Worker contracts required for async execution. |
| `pkg/eventrouter` | External event types and routing from `scenario.triggers` to `RunRequest`. |
| `pkg/audit` | Audit Event models and Sink contracts for compliance logging. |
| `pkg/coordination` | Distributed lease contracts for Worker and workflow coordination. |
| `pkg/core` | Agent, Tool, Skill, Scenario, Workflow, HumanGate, Event types. |
| `pkg/llm` | Provider-agnostic LLM capability interfaces and request/response types. |
| `pkg/contextwindow` | Context window strategy management, token estimation, trimming, and compression stats. |
| `pkg/identity` | Principal, role, tenant/workspace/project scopes, and context helpers. |
| `pkg/memory` | Memory Namespace and Repository contracts. |
| `pkg/runstate` | RunSnapshot, CAS Repository ports, Blob references, and Token signing. |
| `pkg/security` | API Key authenticator, authorization action/resource, and RBAC policy contracts. |

Create and save a run snapshot:

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

Sign and verify HITL Token:

```go
signer, err := runstate.NewTokenSigner([]byte("replace-with-at-least-16-bytes"))
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

Acquire a Redis distributed lease for Worker coordination:

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

Lease semantics and operational notes in [docs/persistence/redis-locker.md](docs/persistence/redis-locker.md).

Execute async tasks via the async worker foundation:

```go
queue := adapters.NewInMemoryJobQueue()
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

Queue status, Worker behavior, and subsequent productionization slices in [docs/async-runtime.md](docs/async-runtime.md).

Expose async run/event/resume job endpoints:

```go
queue := adapters.NewInMemoryJobQueue()
handler, err := httpx.NewAsyncRunHTTPHandler(httpx.AsyncRunHTTPHandlerConfig{
  Queue:  queue,
  Policy: security.NewDefaultRolePolicy(),
  Audit:  auditSink,
})
if err != nil {
  log.Fatal(err)
}
http.Handle("/v1/", middleware(handler))
```

Production Handlers can simultaneously mount optional sync event/HITL routes:

```go
api, err := httpx.NewProductionHTTPHandler(httpx.ProductionHTTPHandlerConfig{
  Queue:     queue,
  Framework: fw,
  AuthMiddleware: authMiddleware,
  Policy:    security.NewDefaultRolePolicy(),
  Audit:     auditSink,
  Version:   agentflow.Version,
})
```

Full routing matrix in [docs/async-runtime.md](docs/async-runtime.md) (`/v1/runs`, `/v1/jobs/events`, `/v1/jobs/hitl/resume`, `/v1/events`, `/v1/hitl/resume`).

Protect HTTP handlers with API Keys and inject enterprise Principals into the request context:

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

Production OIDC/OAuth2 gateways can use OIDC Discovery/JWKS auto-refresh to validate JWTs:

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

Add authorization checks to HTTP handlers:

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

Run the framework with runtime tool authorization and audit logging:

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

Write audit events to an append-only JSONL file:

```go
auditSink, err := adapters.NewFileAuditSink("./data/audit/events.jsonl")
if err != nil {
  log.Fatal(err)
}
err = auditSink.Record(ctx, audit.Event{
  Type:    audit.EventRunSubmitted,
  RunID:   "run-1",
  Outcome: "accepted",
})
```

## Architecture

The project adopts DDD-style layering and Hexagonal Ports/Adapters:

```text
examples/
  go/          # Replicable integration mains (minimal, validate, builder, http-worker, etc.)
  deploy/      # Reference Compose stack (Postgres, Redis, MinIO)
pkg/
  core/
  builder/     # Go DSL constructs core.Scenario
  catalog/     # Tool/Skill manifest loading & validation
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

Design Boundaries:

- `Skill = prompt fragments + tool whitelist/policy + inline-able workflow subgraph`.
- `Tool = execution unit with Schema`.
- `Agent = entity with LLM and Memory bindings`.
- `RunStateRepository` is separated from Memory, specifically handling resumable run snapshots.
- Context governance applies per LLM Profile: different Agents/Tools can be routed to LLM Profiles with varying window, output, thinking, and compression strategies.
- Autonomous execution supports optional planning pass, LLM tool-calling loop, tool whitelist, approval rejection, per-run rate cap, categorized retries, constrained tool result feedback, and lifecycle events.
- Structured output uses Agent-level `output_schema` and Provider's `StructuredOutputter`; standard streaming output uses `Streamer`; `Stream` for Agents with tools emits incremental `tool_call`/`tool_result` (or `tool_denied`) chunks within a governed tool loop, and delivers the final answer via a terminal `Done` chunk (and persists it). `before_final_answer` and `fixed_workflow` with `agent` nodes are not supported by `Stream`/`RunStructured`.
- Memory bindings are wired into Runtime read/write for conversation/session history.
- Fixed workflows execute based on graph dependencies and edges, supporting limited parallelism, `parallel_group`/`loop` nodes, conditional skipping, retries, `transform`/`agent`/`human-gate` nodes, and CAS-secure output saving.
- Workflow `human_gate` nodes persist `CurrentNodeID`/`PendingGate`, allowing downstream graph execution after approval; `ResumeAndContinue` also supports resuming runs for autonomous, workflow, and tool approval pause paths.
- External events map via `scenario.triggers` to `Framework.HandleEvent`, Webhook HTTP (`NewWebhookHTTPHandler`), sync `/v1/events`, and async `event` jobs.
- `sub_agents` are exposed to the supervisor Agent as virtual delegation tools during autonomous execution.
- Skill prompt fragments, Agent policy, Tool policy, and workflow segments are expanded into namespaced workflow nodes during scenario construction.
- Tool declaration and execution planes are separated: `scenario.tools` exposes manifests to LLM and validators, `WithToolExecutor` pre-registers lightweight executors, and `WithToolResolver` lazily binds heavyweight or tenant-isolated executors when allowed calls truly enter the execution phase.
- File / PostgreSQL / Redis RunState, S3-compatible BlobStore, and Memory adapters are constructed via `pkg/adapters`; Redis distributed lease (`NewRedisLocker`) remains in the root package; async queue and Worker contracts support `run`, `event`, `resume.continue` (`NewFrameworkJobHandler`), HTTP routing is provided by `httpx.NewAsyncRunHTTPHandler` / `httpx.NewProductionHTTPHandler`; outputs exceeding `step_output_threshold` are offloaded to `BlobStore`.
- Enterprise identity context, API Key middleware, static and OIDC/JWKS JWT middleware, authorization middleware, RBAC policy contracts, and runtime tool authorization are available via `pkg/identity`, `pkg/security`, root package `NewStaticAPIKeyAuthenticator` / `NewOIDCJWTAuthenticator` / `NewAPIKeyMiddleware` / `NewJWTMiddleware` / `NewAuthorizationMiddleware`, and `WithSecurityPolicy`.
- Audit event contracts and sinks are available via `pkg/audit` and `adapters.NewNoopAuditSink` / `NewInMemoryAuditSink` / `NewFileAuditSink`, along with `WithAuditSink`.
- Runtime observability panel, event store, real-time EventHub, and PostgreSQL auto-table creation are available via `adapters.NewPostgresEventStore`, `NewInMemoryEventStore`, `NewEventStoreSink`, `NewEventHub`, and `httpx.NewObservabilityHTTPHandler`; Studio SPA additionally provides ComposeGraph / parts APIs.
- Enterprise auth/tenancy and observability/governance design in [docs/security-auth-tenancy.md](docs/security-auth-tenancy.md), [docs/observability-governance.md](docs/observability-governance.md), and [docs/observability-dashboard.md](docs/observability-dashboard.md).
- In-memory adapters are concurrency-safe and isolated by run/session namespace.

## Testing

Default unit tests:

```sh
make test
```

Integration tests:

```sh
make test-integration
```

Real local model pipeline tests:

```sh
export AGENT_REALMODEL_BASE_URL="http://127.0.0.1:1234/v1"
export AGENT_REALMODEL_MODEL="qwen/qwen3.6-35b-a3b"
export AGENT_REALMODEL_API_KEY="..."
make test-realmodel
```

Concurrency Race tests for in-memory adapters:

```sh
make test-race
```

Static checks and vulnerability scanning:

```sh
make vet
make lint
make security
```

Direct execution:

```sh
CGO_ENABLED=0 go test -ldflags="-w" ./...
CGO_ENABLED=0 go test -ldflags="-w" -tags=integration ./...
CGO_ENABLED=0 go test -ldflags="-w" -tags=realmodel -run TestRealModel -v .
go test -race ./internal/adapter/memory/inmem ./internal/adapter/runstate/inmem ./internal/adapter/blob/inmem
```

In older Darwin local toolchain + `CGO_ENABLED=0` environments, `-ldflags="-w"` can avoid local `dyld` test binary issues.

## Current Status

**Current Release: v0.5.1** — Fixes an issue where observation masks were displaced by governance denials, dropping successful business tool results based on v0.5.0; full changes in [CHANGELOG.md](CHANGELOG.md).

Core modules are available:

- **Scenario Construction**: `pkg/builder` (`CoreCatalog` + legacy `ExampleCatalog`), `ValidateScenario`, Studio YAML interoperability, `ComposeGraph`; three-mode stances in [docs/orchestration-modes.md](docs/orchestration-modes.md)
- **Runtime**: autonomous / fixed_workflow / hybrid, subgraph / map / loop / parallel, planning pass, Skill expansion
- **Governance**: tool whitelist & approval, HITL, Identity/RBAC/Audit, timeout & categorized retries
- **Persistence**: File / Postgres / Redis RunState, S3 Blob, Checkpoint history, Memory Tier
- **Integration**: `httpx` Production HTTP, Async Worker, Webhook/Event triggers, Prometheus + OTel (`pkg/adapters`)
- **Studio**: AI-first SPA (ComposeBar + parts box + dry-run), run tracing / checkpoint time travel, dual run diff

Future directions (non-blocking): Versioned Tool/Skill catalog manifests, hosted environment integration test matrix expansion, `http-worker` example wiring `TraceExploreURL`. Product boundaries in [product-direction.md](docs/product-direction.md) (no `agent_loop` graph nodes, no full LangGraph Store parity).

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

This project is licensed under the [Apache License 2.0](./LICENSE).
