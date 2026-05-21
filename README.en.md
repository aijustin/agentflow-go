# agentflow-go

[![Go Reference](https://pkg.go.dev/badge/github.com/aijustin/agentflow-go.svg)](https://pkg.go.dev/github.com/aijustin/agentflow-go)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](./LICENSE)

[简体中文](./README.md) | English

Scenario-configured Go library for composing Agents, Tools, Skills, LLM gateways, Memory, Run State, and Human-in-the-loop orchestration.

Import `github.com/aijustin/agentflow-go` in your service, declare scenarios in YAML, wire gateways and executors explicitly, then call `Framework.Run` in-process or mount the provided HTTP handlers in your own server.

## Quick start

```sh
go get github.com/aijustin/agentflow-go
go run ./examples/go/minimal/main.go
go run ./examples/go/validate examples/autonomous.yaml
make test
```

Before tagging a release, run `GOTOOLCHAIN=auto make release-check`. See [docs/release-checklist.md](docs/release-checklist.md) and [docs/api-stability.md](docs/api-stability.md).

For a guided HTML manual, open [docs/manual.html](docs/manual.html) in your browser.

## Integration paths

| Goal | Start here |
|------|------------|
| Embed in an existing Go service | [docs/library-integration.md](docs/library-integration.md) |
| Minimal in-process run | [examples/go/minimal/main.go](examples/go/minimal/main.go) |
| Postgres / file persistence | [examples/go/postgres/main.go](examples/go/postgres/main.go) |
| HTTP API + async worker | [examples/go/http-worker/main.go](examples/go/http-worker/main.go) |
| HITL pause and resume | [examples/go/hitl-resume/main.go](examples/go/hitl-resume/main.go) |
| Event triggers | [examples/go/event-trigger/main.go](examples/go/event-trigger/main.go) |
| Tests and examples wiring | [pkg/testutil](pkg/testutil/testutil.go) |

Library surface: `ValidateWiring`, `New`, `Framework.Run`, `NewProductionHTTPHandler`, `NewFrameworkJobHandler`, `ScenarioJSONSchema`, `Version`.

## Example paths

### Runnable Go examples (`examples/go/`)

| Directory | Purpose | Command |
|-----------|---------|---------|
| [minimal](examples/go/minimal/main.go) | Minimal in-process embed: `LoadScenario` → `testutil.WiringOptions` → `New` → `Run` | `go run ./examples/go/minimal/main.go` |
| [postgres](examples/go/postgres/main.go) | Postgres RunState / JobQueue persistence | `go run ./examples/go/postgres/main.go` |
| [http-worker](examples/go/http-worker/main.go) | `NewProductionHTTPHandler` + async worker | `go run ./examples/go/http-worker/main.go` |
| [hitl-resume](examples/go/hitl-resume/main.go) | HITL pause and `ResumeAndContinue` | `go run ./examples/go/hitl-resume/main.go` |
| [event-trigger](examples/go/event-trigger/main.go) | Event-driven runs via `scenario.triggers` | `go run ./examples/go/event-trigger/main.go` |
| [validate](examples/go/validate/main.go) | Validate scenario YAML and wiring (same as CI) | `go run ./examples/go/validate examples/autonomous.yaml` |

Use `WithLLMGateway` / `WithToolExecutor` in production instead of `testutil.WiringOptions`. See [pkg/testutil](pkg/testutil/testutil.go) for test wiring.

### Scenario YAML examples (`examples/`)

| File | Mode | Focus |
|------|------|-------|
| [autonomous.yaml](examples/autonomous.yaml) | `autonomous` | Minimal agent + echo tool |
| [human_in_loop.yaml](examples/human_in_loop.yaml) | `autonomous` | `before_final_answer` approval |
| [context_governance.yaml](examples/context_governance.yaml) | `autonomous` | Context window / summarization / stale tools |
| [fixed_workflow.yaml](examples/fixed_workflow.yaml) | `fixed_workflow` | Fixed DAG workflow |
| [workflow_enhancements.yaml](examples/workflow_enhancements.yaml) | `fixed_workflow` | `parallel_group`, `loop`, `human_gate`, etc. |
| [code_review_pipeline.yaml](examples/code_review_pipeline.yaml) | `fixed_workflow` | Git diff + parallel review + gate |
| [hybrid.yaml](examples/hybrid.yaml) | `hybrid` | Workflow phase then autonomous synthesis |
| [multi_expert_research.yaml](examples/multi_expert_research.yaml) | `hybrid` | Parallel experts + `planning.execute` |
| [adaptive_rag.yaml](examples/adaptive_rag.yaml) | `fixed_workflow` | `query_router` adaptive RAG |
| [corrective_rag.yaml](examples/corrective_rag.yaml) | `fixed_workflow` | `rag_grade` + conditional re-retrieval |
| [self_rag.yaml](examples/self_rag.yaml) | `fixed_workflow` | Self-RAG quality gate |
| [rag_knowledge.yaml](examples/rag_knowledge.yaml) | — | RAG knowledge base and citations |
| [ticket_handling.yaml](examples/ticket_handling.yaml) | `autonomous` | Ticket triggers + HITL |
| [http_tool.yaml](examples/http_tool.yaml) | — | HTTP tool declaration |
| [sql_tool.yaml](examples/sql_tool.yaml) | — | SQL tool |
| [filesystem_tool.yaml](examples/filesystem_tool.yaml) | — | Filesystem tool |
| [mcp_tool.yaml](examples/mcp_tool.yaml) | — | MCP tool integration |

Validate all scenarios: `make validate-examples` or `go run ./examples/go/validate examples/<file>.yaml`. Mode selection: [docs/orchestration-flow.md](docs/orchestration-flow.md).

## Getting started

### Requirements

- Go 1.25.10+
- macOS/Linux shell

### Use as a framework in another Go project

Add the module:

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
)

func main() {
    fw, err := agentflow.NewFromFile("scenario.yaml")
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

For custom LLM, Memory, RunState, EventSink, or HumanGate implementations, use the option-based API:

```go
fw, err := agentflow.NewFromFile(
    "scenario.yaml",
    agentflow.WithLLMGateway(myLLMGateway),
    agentflow.WithToolExecutor("repo_search", myToolExecutor),
    agentflow.WithMemoryRepository("session", myMemoryRepo),
    agentflow.WithRunStateRepository(myRunStateRepo),
    agentflow.WithEventSink(myEventSink),
)
```

Built-in provider constructors are exposed from the root package for common LLM wiring:

```go
gateway := agentflow.NewOpenAICompatibleGateway([]llm.Profile{{
  Name:      "default",
  Provider:  "openai-compatible",
  Model:     "qwen/qwen3.6-35b-a3b",
  Endpoint:  "http://127.0.0.1:1234/v1",
  APIKeyEnv: "AGENT_REALMODEL_API_KEY",
}}, nil)

fw, err := agentflow.NewFromFile("scenario.yaml", agentflow.WithLLMGateway(gateway))
```

For OpenAI-compatible chat plus embeddings, use `NewOpenAICompatibleProvider` and declare profile capabilities explicitly:

```go
provider := agentflow.NewOpenAICompatibleProvider([]llm.Profile{
  {Name: "chat", Provider: "openai-compatible", Model: "qwen/qwen3.6-35b-a3b", Endpoint: "http://127.0.0.1:1234/v1"},
  {Name: "embed", Provider: "openai-compatible", Model: "text-embedding-3-small", Endpoint: "http://127.0.0.1:1234/v1", Capabilities: []llm.Capability{llm.CapEmbed}},
}, nil)
```

For mixed-provider applications, `NewLLMProviderRouter` routes chat/tool/structured/stream and embedding calls by profile name. Capabilities are explicit: unsupported features fail clearly instead of being silently emulated.

```go
openaiProvider := agentflow.NewOpenAICompatibleProvider(openaiProfiles, nil)
anthropicGateway := agentflow.NewAnthropicGateway(anthropicProfiles, nil)

provider := agentflow.NewLLMProviderRouter(map[string]llm.Gateway{
  "chat":  anthropicGateway,
  "embed": openaiProvider,
})
```

For structured output, configure `agents.<name>.output_schema` and call `RunStructured`; the gateway must implement `llm.StructuredOutputter`:

```go
result, err := fw.RunStructured(ctx, agentflow.RunRequest{
    RunID:  "run-json",
    Agent:  "assistant",
    Prompt: "return JSON",
})
fmt.Println(string(result.StructuredOutput))
```

For streaming, use a gateway that implements `llm.Streamer`:

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

When an agent has tools and the configured LLM gateway supports `CapToolCall`, the runtime runs an autonomous tool loop: send tool specs to the LLM, validate returned tool calls against the agent whitelist, enforce approval and per-run `rate_cap`, retry classified transient LLM/tool errors from `retry_limit`/`max_retries` with exponential backoff, execute registered tool executors, append bounded tool results, and continue until the LLM returns a final answer or `max_steps` is reached. `Stream` also accepts tool-enabled agents; it runs the same governed tool loop and emits the final answer as a stream chunk.

Set `orchestration.planning.enabled: true` to add a planning pass before the autonomous tool loop. The runtime asks the executing agent, or `orchestration.planning.agent` when set, for a concise JSON plan and injects that plan into the subsequent execution context. Set `orchestration.planning.execute: true` to track plan step completion in run state during the tool loop (see `examples/multi_expert_research.yaml`).

Fixed workflows support `tool`, `agent`, `skill`, `human_gate`, `transform`, `parallel_group`, and `loop` nodes. Node `condition` expressions can read `steps.<node_id>` paths with `exists(...)`, `missing(...)`, `eq(...)`, and `ne(...)`; transform nodes can build structured outputs with `set` and `copy` mappings.

When an agent binds `memory`, the runtime reads stored conversation/session messages before context preparation, injects them into the LLM context, and appends user prompts, assistant answers, and tool observations after execution. `in_memory` repositories are auto-created by the root facade unless a custom repository is supplied.

Enable the built-in HMAC-token HITL gate:

```go
fw, err := agentflow.NewFromFile(
    "human_in_loop.yaml",
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

Use file-backed persistence when runs must survive process restarts:

```go
runs, _ := agentflow.NewFileRunStateRepository("./data/runs")
blobs, _ := agentflow.NewFileBlobStore("./data/blobs")
memoryRepo, _ := agentflow.NewFileMemoryRepository("./data/memory")

fw, err := agentflow.NewFromFile(
    "scenario.yaml",
    agentflow.WithRunStateRepository(runs),
    agentflow.WithBlobStore(blobs),
    agentflow.WithMemoryRepository("session", memoryRepo),
)
```

For production PostgreSQL-backed run state, register a `database/sql` driver in your application and pass the initialized pool to the root constructor:

```go
db, err := sql.Open("pgx", os.Getenv("AGENTFLOW_POSTGRES_DSN"))
if err != nil {
  log.Fatal(err)
}
runs, err := agentflow.NewPostgresRunStateRepository(db)
if err != nil {
  log.Fatal(err)
}

fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithRunStateRepository(runs),
)
```

See [docs/persistence/postgres-runstate.md](docs/persistence/postgres-runstate.md) for the table contract and operational notes.

Redis-backed run state is also available when you want low-latency CAS snapshots without a SQL database:

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

See [docs/persistence/redis-runstate.md](docs/persistence/redis-runstate.md) for storage semantics and operational notes.

For asynchronous production execution, use a queue plus workers. The PostgreSQL queue adapter uses `database/sql` and does not force a driver dependency:

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

`agentflow.NewProductionHTTPHandler` mounts `/healthz`, `/readyz`, async run/event/resume job APIs, and—when `Framework` is set—sync `/v1/events` and `/v1/hitl/resume`. See [docs/async-runtime.md](docs/async-runtime.md) and [docs/persistence/postgres-queue.md](docs/persistence/postgres-queue.md).

MCP servers can be adapted into regular governed tools without changing the runtime core:

```go
mcpClient, err := agentflow.NewMCPHTTPClient("http://127.0.0.1:3333/mcp", nil)
if err != nil {
  log.Fatal(err)
}
searchTool, err := agentflow.NewMCPToolExecutor(mcpClient, "search")
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.NewFromFile(
  "examples/mcp_tool.yaml",
  agentflow.WithToolExecutor("docs.search", searchTool),
)
```

See [docs/mcp-tools.md](docs/mcp-tools.md) for the adapter model and security notes.

Heavy or tenant-scoped tools do not need to be constructed during framework startup. Declare their manifest in `scenario.tools`, then resolve the executor only after the runtime has checked the agent allowlist, approval policy, RBAC, governance policy, and rate caps:

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

fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithToolResolver(resolver),
)
```

`WithToolExecutor` remains useful for light or always-on tools and takes precedence over the resolver. Resolved executors are cached by scenario tool name for the lifetime of the framework. Skills do not initialize tools; they expand prompt fragments, policy overlays, and workflow segments during scenario build, while the resolver owns real executor binding at invocation time.

For read-only internal API calls, register the constrained HTTP tool executor:

```go
httpTool, err := agentflow.NewHTTPToolExecutor(agentflow.HTTPToolConfig{
  AllowedHosts: []string{"https://status.example.internal"},
})
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.NewFromFile(
  "examples/http_tool.yaml",
  agentflow.WithToolExecutor("http.status", httpTool),
)
```

The executor requires an explicit host allowlist and defaults to `GET`/`HEAD`. See [docs/tools-http.md](docs/tools-http.md).

For local runbooks and checked-out documentation, register the constrained filesystem read tool executor:

```go
filesystemTool, err := agentflow.NewFilesystemToolExecutor(agentflow.FilesystemToolConfig{
  AllowedRoots: []string{"/srv/agentflow/runbooks"},
})
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.NewFromFile(
  "examples/filesystem_tool.yaml",
  agentflow.WithToolExecutor("fs.read", filesystemTool),
)
```

The executor requires explicit root allowlists, rejects traversal and symlink escapes, and limits file size. See [docs/tools-filesystem.md](docs/tools-filesystem.md).

For database-backed lookups, register the constrained SQL query tool executor with named allowlisted queries:

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
fw, err := agentflow.NewFromFile(
  "examples/sql_tool.yaml",
  agentflow.WithToolExecutor("sql.query", sqlTool),
)
```

The executor defaults to named `SELECT` queries, rejects multi-statement SQL, applies a timeout, and caps returned rows. See [docs/tools-sql.md](docs/tools-sql.md).

The SQL tool accepts any `database/sql` driver, including PostgreSQL, MySQL, and ClickHouse. The host application imports the concrete driver and passes the opened `*sql.DB`; agentflow-go intentionally does not force driver dependencies.

For code review pipelines, register the read-only Git tool executor:

```go
gitTool, err := agentflow.NewGitToolExecutor(agentflow.GitToolConfig{
  AllowedRoots: []string{"/workspace/repos"},
})
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.NewFromFile(
  "examples/code_review_pipeline.yaml",
  agentflow.WithToolExecutor("git", gitTool),
)
```

See [docs/tools-git.md](docs/tools-git.md). Tool executors must be registered explicitly with `WithToolExecutor` (or `WithToolResolver`).

For support-ticket workflows, register the ticket tool with a store adapter:

```go
store := agentflow.NewMemoryTicketStore(map[string]agentflow.Ticket{
  "T-9": {ID: "T-9", Title: "Login issue", Status: "open"},
})
ticketTool, err := agentflow.NewTicketToolExecutor(agentflow.TicketToolConfig{Store: store})
if err != nil {
  log.Fatal(err)
}
fw, err := agentflow.NewFromFile(
  "examples/ticket_handling.yaml",
  agentflow.WithToolExecutor("ticket", ticketTool),
)
```

See [docs/tools-ticket.md](docs/tools-ticket.md).

For RAG workloads, combine an embedder, vector store, and retriever tool:

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
fw, err := agentflow.NewFromFile(
  "examples/rag_knowledge.yaml",
  agentflow.WithLLMGateway(provider),
  agentflow.WithToolExecutor("knowledge.retrieve", retriever),
)
```

See [docs/knowledge-rag.md](docs/knowledge-rag.md) and [docs/persistence/pgvector.md](docs/persistence/pgvector.md) for the public contracts and table schema.

Apply PostgreSQL schema from [migrations/postgres](migrations/postgres) with your migration runner before using Postgres adapters. See [docs/persistence/postgres-runstate.md](docs/persistence/postgres-runstate.md) and [docs/persistence/postgres-queue.md](docs/persistence/postgres-queue.md).

For S3-compatible blob storage, configure the blob store separately from run state:

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

fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithBlobStore(blobs),
)
```

See [docs/persistence/s3-blobstore.md](docs/persistence/s3-blobstore.md) for object layout and security notes.

Enterprise observability and governance hooks are optional and dependency-light:

```go
fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithEventSink(agentflow.NewSlogEventSink(logger)),
  agentflow.WithAuditSink(agentflow.NewSlogAuditSink(logger)),
  agentflow.WithToolGovernancePolicy(governance.ChainToolPolicies(
    governance.NewToolBudgetPolicy(8),
    governance.NewMaxSideEffectPolicy(core.SideEffectRead),
  )),
  agentflow.WithOutputRedactor(governance.NewJSONFieldRedactor("secret", "token")),
)
```

Governance policies run before tool execution, and output redaction is applied before runtime step outputs are persisted.

AgentFlow also ships a runtime observability dashboard for live sessions and event detail drill-downs. The PostgreSQL event store creates its table and indexes automatically by default, so enabling the panel only requires wiring an event sink and mounting the handler:

```go
eventStore, err := agentflow.NewPostgresEventStore(ctx, agentflow.PostgresEventStoreConfig{DB: db})
if err != nil {
  log.Fatal(err)
}
eventHub := agentflow.NewEventHub()

fw, err := agentflow.NewFromFile(
  "scenario.yaml",
  agentflow.WithEventSink(agentflow.NewEventFanoutSink(
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

See [docs/observability-dashboard.md](docs/observability-dashboard.md) for database configuration, automatic schema setup, endpoints, and security notes.

Low-level extension interfaces remain available through:

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

Built-in tool adapters are documented in [docs/tools-http.md](docs/tools-http.md), [docs/tools-filesystem.md](docs/tools-filesystem.md), [docs/tools-sql.md](docs/tools-sql.md), [docs/tools-git.md](docs/tools-git.md), [docs/tools-ticket.md](docs/tools-ticket.md), [docs/mcp-tools.md](docs/mcp-tools.md), and [docs/knowledge-rag.md](docs/knowledge-rag.md).

### Install dependencies

```sh
go mod download
```

### Validate example scenarios

```sh
go run ./examples/go/validate examples/autonomous.yaml
make validate-examples
```

### Runnable examples

| Example | Purpose |
| --- | --- |
| [examples/go/minimal](examples/go/minimal/main.go) | In-process `Run` with test wiring |
| [examples/go/postgres](examples/go/postgres/main.go) | File or Postgres run state |
| [examples/go/http-worker](examples/go/http-worker/main.go) | Production HTTP handler + async worker |
| [examples/go/hitl-resume](examples/go/hitl-resume/main.go) | HITL pause and `ResumeAndContinue` |
| [examples/go/event-trigger](examples/go/event-trigger/main.go) | `HandleEvent` and scenario triggers |

Copy these `main` programs into your service and replace `testutil.WiringOptions` with explicit `WithLLMGateway` / `WithToolExecutor` registration.

See [docs/troubleshooting.md](docs/troubleshooting.md) for common errors and fixes.

## HTTP surfaces

Mount library handlers in your own HTTP server. The example listens on `127.0.0.1:8080`:

```sh
go run ./examples/go/http-worker/main.go
```

Production HITL continuation uses `NewProductionHTTPHandler` or `NewHumanHTTPHandler` at `POST /v1/hitl/resume`. Set `"continue": true` to call `ResumeAndContinue`:

```sh
curl -X POST http://localhost:8080/v1/hitl/resume \
  -H 'Content-Type: application/json' \
  -d '{
    "token": "'"$TOKEN"'",
    "decision": "approve",
    "continue": true
  }'
```

Webhook events use `POST /v1/events` on the same production handler when `Framework` is configured. See [docs/async-runtime.md](docs/async-runtime.md).

Debug resume response:

```json
{"status":"ok"}
```

Network-delivered tokens are HMAC signed. In production, always set `AGENT_TOKEN_SECRET` to a strong secret and use a persistent run-state repository.

## YAML scenario format

All scenario configuration lives under one `scenario:` root.

For editor completion, enum discovery, and CI validation, use the JSON Schema at [schemas/agentflow.scenario.schema.json](schemas/agentflow.scenario.schema.json). A full human-readable field reference is available in [docs/configuration-reference.md](docs/configuration-reference.md), orchestration execution flow and **mode/node selection guide** in [docs/orchestration-flow.md](docs/orchestration-flow.md), or load it in Go with `agentflow.ScenarioJSONSchema()`.

Example scenarios:

| File | Highlights |
| --- | --- |
| `examples/autonomous.yaml` | Autonomous tool loop baseline |
| `examples/fixed_workflow.yaml` | Graph workflow with conditions and HITL |
| `examples/human_in_loop.yaml` | HITL pause and resume |
| `examples/ticket_handling.yaml` | Ticket tool + triggers + event routing |
| `examples/code_review_pipeline.yaml` | Git tool + `parallel_group` workflow |
| `examples/multi_expert_research.yaml` | Hybrid mode + planning.execute |

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

### Top-level sections

| Section | Purpose |
| --- | --- |
| `llms` | Named LLM profiles. Agents and tools can bind to different profiles. |
| `memories` | Named memory backends and scopes. In-memory and file-backed repositories are available. |
| `tools` | Tool declarations, side-effect metadata, approval policy, optional LLM override, and per-run `rate_cap`. |
| `skills` | Declarative prompt/policy/workflow packages. Skills are not runtime actors and do not initialize tools; they expand into agent instructions, tool policies, and workflow subgraphs during scenario build. |
| `agents` | Agent role, instructions, LLM binding, memory binding, tools, and skills. |
| `orchestration` | Autonomous, fixed workflow, or hybrid HITL execution policy. |
| `runtime` | Runtime limits, output thresholds, secrets, and operational settings. |

### LLM profile and context governance

Each LLM profile can define provider settings, output limits, thinking/reasoning options, provider-specific request fields, and a context-window policy:

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

Supported context strategies are `none`, `sliding_window`, and `sliding_window_with_summary`. Before each LLM call, the runtime emits `ContextPrepared` with before/after token estimates, dropped message count, summary status, and active input budget. When `tool_result_max_tokens` is set, large tool observations are compacted before they are sent back into the next LLM turn while the full persisted step output remains available through run state/blob storage.

For local Qwen reasoning models, keep `max_output_tokens` high enough for reasoning tokens because some OpenAI-compatible servers count reasoning output against `max_tokens`. The runtime treats an empty response with `finish_reason=length` as an error so misconfigured reasoning budgets do not look like successful empty answers.

### Orchestration modes

| Mode | Description |
| --- | --- |
| `autonomous` | LLM-driven planning/execution. The orchestrator owns tool dispatch and approval checks. |
| `fixed_workflow` | Deterministic graph. Workflow nodes and edges are validated before execution. |
| `hybrid` | Designed for combining workflow control with autonomous substeps and HITL gates. |

### Human-in-the-loop

Enable HITL by adding checkpoints:

```yaml
scenario:
  orchestration:
    mode: autonomous
    human_in_loop:
      enabled: true
      checkpoints:
        - before_final_answer
```

When a checkpoint opens, runtime persists a `RunSnapshot`, signs a token containing `(RunID, Version)`, and waits for a human decision.

## Library usage

Most applications should import the root facade:

```go
import agentflow "github.com/aijustin/agentflow-go"
```

Public packages:

| Package | Purpose |
| --- | --- |
| root package | Framework facade: load YAML, validate, run, resume, handle events, wire options. |
| `pkg/async` | Job queue, lease, handler, and worker contracts for asynchronous execution. |
| `pkg/eventrouter` | External event types and trigger-to-run routing for `scenario.triggers`. |
| `pkg/audit` | Audit event model and sink contract for compliance records. |
| `pkg/coordination` | Distributed lease contract for worker and workflow coordination. |
| `pkg/core` | Agent, Tool, Skill, Scenario, Workflow, HumanGate, Event types. |
| `pkg/llm` | Provider-neutral LLM capability ports and request/response types. |
| `pkg/contextwindow` | Context-window policy manager, token estimates, trimming, and compression stats. |
| `pkg/identity` | Principal, role, tenant/workspace/project scope, and context helpers. |
| `pkg/memory` | Memory namespace and repository contract. |
| `pkg/runstate` | Run snapshots, CAS repository port, blob references, token signing. |
| `pkg/security` | API key authenticator, authorization action/resource, and RBAC policy contracts. |

Example: create and save a run snapshot.

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

Example: sign and verify a HITL token.

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

Example: acquire a Redis-backed distributed lease for worker coordination.

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

See [docs/persistence/redis-locker.md](docs/persistence/redis-locker.md) for lease semantics and operational notes.

Example: run jobs through the async worker foundation.

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

See [docs/async-runtime.md](docs/async-runtime.md) for queue states, worker behavior, and next production slices.

Example: expose async run/event/resume job endpoints.

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

Production handler with optional sync event/HITL routes:

```go
api, err := agentflow.NewProductionHTTPHandler(agentflow.ProductionHTTPHandlerConfig{
  Queue:     queue,
  Framework: fw,
  Policy:    security.NewDefaultRolePolicy(),
  Audit:     auditSink,
  Version:   "v0.1.0",
})
```

See [docs/async-runtime.md](docs/async-runtime.md) for the full route matrix (`/v1/runs`, `/v1/jobs/events`, `/v1/jobs/hitl/resume`, `/v1/events`, `/v1/hitl/resume`).

Example: protect an HTTP handler with API key authentication and attach an enterprise principal to request context.

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

For production OIDC/OAuth2 gateways, use JWKS discovery and refresh-backed JWT validation:

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

Example: enforce authorization around a handler.

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

Example: run the framework with runtime tool authorization and audit records.

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

Example: record audit events to an append-only JSONL file.

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

## Architecture

The project follows DDD-oriented layering with hexagonal ports/adapters:

```text
cmd/
  examples/go/
pkg/
  core/
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

Design boundaries:

- `Skill = prompt fragments + agent/tool policy overlays + inline-able workflow sub-graph`.
- `Tool = schema-backed execution unit`.
- `Agent = entity that owns LLM and memory binding`.
- `RunStateRepository` is separate from Memory and handles resumable workflow snapshots.
- Context governance is profile-scoped: different agents/tools can route to different LLM profiles with different window, output, thinking, and compression policies.
- Autonomous execution supports an optional planning pass plus LLM tool-calling loops with tool whitelist checks, approval-policy denial, per-run rate caps, classified retry, bounded tool result feedback, and LLM/tool lifecycle events.
- Structured output runs use an agent-level `output_schema` and provider `StructuredOutputter`; streaming runs use provider `Streamer` for normal chat and the governed tool loop for tool-enabled agents, then persist the final accumulated answer.
- Memory bindings are connected to runtime reads/writes for conversation and session history.
- Fixed workflows run from graph dependencies and edges, with bounded parallel batches, false-condition skips, retry policy, transform nodes, agent nodes, human-gate nodes, and CAS-safe step output persistence.
- Workflow human-gate nodes pause with persisted `CurrentNodeID`/`PendingGate` and can resume downstream execution after approval; `ResumeAndContinue` continues autonomous, workflow, and tool-approval pause paths until completion or the next gate.
- External events map through `scenario.triggers` to `Framework.HandleEvent`, Webhook HTTP (`NewWebhookHTTPHandler`),  sync `/v1/events`, and async `event` jobs.
- `sub_agents` are available to supervisor agents as virtual delegation tools during autonomous execution.
- Skill prompt fragments, agent policies, tool policies, and workflow segments are expanded during scenario build with namespaced workflow node IDs.
- Tools have separate declaration and execution surfaces: `scenario.tools` exposes manifests to LLMs and validators, `WithToolExecutor` eagerly registers light executors, and `WithToolResolver` lazily binds heavy or tenant-scoped executors only when a permitted invocation reaches execution.
- File-backed RunState, BlobStore, and Memory adapters are available from the root facade for durable local persistence; PostgreSQL-backed and Redis-backed RunState are available for production persistence; S3-compatible BlobStore is available for large runtime/workflow outputs and supports MinIO/AWS S3 style endpoints plus verified S3-compatible COS/OSS endpoints; Redis-backed leases are available for worker coordination; async queue and worker contracts support `run`, `event`, and `resume.continue` jobs through `NewFrameworkJobHandler`, with HTTP routes on `NewAsyncRunHTTPHandler` and `NewProductionHTTPHandler`; large step outputs are externalized to BlobStore when `step_output_threshold` is exceeded.
- Enterprise identity context, API key middleware, static and OIDC/JWKS JWT middleware, authorization middleware, RBAC policy contracts, and runtime tool authorization are available through `pkg/identity`, `pkg/security`, `NewStaticAPIKeyAuthenticator`, `NewOIDCJWTAuthenticator`, `NewAPIKeyMiddleware`, `NewJWTMiddleware`, `NewAuthorizationMiddleware`, and `WithSecurityPolicy`.
- Audit event contracts and noop/in-memory/file sinks are available through `pkg/audit`, `NewNoopAuditSink`, `NewInMemoryAuditSink`, `NewFileAuditSink`, and `WithAuditSink`.
- Runtime observability dashboard, event store, live event hub, and automatic PostgreSQL schema setup are available through `NewPostgresEventStore`, `NewInMemoryEventStore`, `NewEventStoreSink`, `NewEventHub`, and `NewObservabilityHTTPHandler`.
- Enterprise auth/tenancy and observability/governance designs are documented in [docs/security-auth-tenancy.md](docs/security-auth-tenancy.md), [docs/observability-governance.md](docs/observability-governance.md), and [docs/observability-dashboard.md](docs/observability-dashboard.md).
- In-memory adapters are concurrency-safe and namespaced by run/session where applicable.

## Testing

Default unit tests:

```sh
make test
```

Integration tests:

```sh
make test-integration
```

Real local-model flow test:

```sh
export AGENT_REALMODEL_BASE_URL="http://127.0.0.1:1234/v1"
export AGENT_REALMODEL_MODEL="qwen/qwen3.6-35b-a3b"
export AGENT_REALMODEL_API_KEY="..."
make test-realmodel
```

Race tests for concurrent in-memory adapters:

```sh
make test-race
```

Static checks and vulnerability scanning:

```sh
make vet
make lint
make security
```

Direct commands:

```sh
CGO_ENABLED=0 go test -ldflags="-w" ./...
CGO_ENABLED=0 go test -ldflags="-w" -tags=integration ./...
CGO_ENABLED=0 go test -ldflags="-w" -tags=realmodel -run TestRealModel -v .
go test -race ./internal/adapter/memory/inmem ./internal/adapter/runstate/inmem ./internal/adapter/blob/inmem
```

On older local Darwin toolchains with `CGO_ENABLED=0`, `-ldflags="-w"` avoids a local `dyld` test-binary issue.

## Current status

Implemented:

- YAML loader and validator
- Autonomous runtime engine with optional planning pass before governed execution
- Fixed-workflow runner wired through the root facade
- In-memory Memory, RunStateRepository, and BlobStore
- LLM abstractions plus root constructors for OpenAI-compatible, Anthropic, local, router, and mock testing paths
- Autonomous tool-calling loop for registered tools, OpenAI-compatible function calling, and Anthropic Messages tool use
- Lazy tool resolution through `WithToolResolver` for heavy or tenant-scoped executors after runtime policy checks
- Runtime memory integration for injected history and persisted user/assistant/tool observations
- Fixed-workflow graph scheduler with dependencies, parallelism, `parallel_group` and `loop` nodes, retries, conditions, transform/agent/human-gate nodes, and CAS-safe output saves
- Workflow-level HITL pause/resume with saved scheduler position, plus `ResumeAndContinue` for autonomous, workflow, and tool-approval pause paths
- Event triggers (`scenario.triggers`) with `HandleEvent`, Webhook HTTP,  and async `event` jobs
- Built-in Git and ticket tool executors for code-review and support-ticket scenarios
- Planning pass execution tracking during autonomous runs
- Multi-agent delegation through virtual sub-agent tools and persisted delegated outputs
- Skill prompt/workflow expansion, compatible-agent checks, agent policy overlays, and tool policy overlays during scenario build
- File-backed durable adapters for run state, blobs, and memory, plus PostgreSQL-backed run state, Redis-backed run state, PostgreSQL-backed async queue, and S3-compatible blob storage
- Redis-backed distributed lease adapter for worker and workflow coordination
- Async job queue and worker contracts with in-memory/PostgreSQL queue adapters, lease renewal, framework job handler for `run`/`event`/`resume.continue`, HTTP submit/status/cancel handler, and production handler with optional sync event/HITL routes
- Enterprise identity context, API key middleware, static/JWKS-discovered JWT middleware, authorization middleware, RBAC policy contracts, and runtime tool authorization
- Audit event model with noop, in-memory, JSONL file, and structured `slog` sinks, plus framework audit wiring
- Governance hooks for tool budgets, tool side-effect ceilings, and persisted output redaction
- `ResumeAndContinue` and `HandleEvent` for event-driven runs
- Runtime hardening: global/agent/profile timeouts, classified LLM/tool retry with exponential backoff, tool rate caps, bounded tool-result context feedback, failed-run status persistence, and blob externalization for large outputs
- Structured output and streaming runtime paths exposed through the root facade, including tool-enabled streaming runs
- Context governance with sliding-window trimming, heuristic summary compression, richer LLM profile config, and `ContextPrepared` events
- HTTP HITL and Webhook routes via `NewHumanHTTPHandler` and `NewWebhookHTTPHandler`
- GitHub Actions CI, golangci-lint, govulncheck/CodeQL, Dependabot, and module release checks
- Unit and integration tests

Remaining production roadmap:

- Concrete Prometheus/OpenTelemetry exporters on top of the existing recorder/tracer ports
- Helm chart examples for host applications (not shipped as first-party binaries)
- Tool/Skill catalog manifest validation, packaging workflows, and integration test matrices for managed services

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

Licensed under the [Apache License 2.0](./LICENSE).
