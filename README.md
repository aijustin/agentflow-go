# agentflow-go

[![Go Reference](https://pkg.go.dev/badge/github.com/aijustin/agentflow-go.svg)](https://pkg.go.dev/github.com/aijustin/agentflow-go)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](./LICENSE)

[English](./README.md) | [简体中文](./README.zh-CN.md)

Scenario-configured Go toolkit for composing Agents, Tools, Skills, LLM gateways, Memory, Run State, and Human-in-the-loop orchestration.

The project can be used both as a Go library and as a CLI/HTTP application. Scenarios are declared in YAML, then wired into agents, tools, LLM profiles, memory backends, run-state persistence, and orchestration policies.

## Demo

```sh
go run ./cmd/agentctl validate -f examples/autonomous.yaml
go run ./cmd/agentctl run -f examples/autonomous.yaml --prompt "hello" --json
```

Before tagging a release, run `GOTOOLCHAIN=auto make release-check`. See [docs/release-checklist.md](docs/release-checklist.md) and [docs/api-stability.md](docs/api-stability.md) for release validation and v0 compatibility policy.

For a polished guided overview, open the standalone HTML manual at [docs/manual.html](docs/manual.html).

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

When an agent has tools and the configured LLM gateway supports `CapToolCall`, the runtime runs an autonomous tool loop: send tool specs to the LLM, validate returned tool calls against the agent whitelist, enforce approval and per-run `rate_cap`, retry transient LLM/tool errors from `retry_limit`/`max_retries`, execute registered tool executors, append tool results, and continue until the LLM returns a final answer or `max_steps` is reached.

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

For asynchronous production execution, use a queue plus workers. The PostgreSQL queue adapter uses `database/sql` and does not force a driver dependency:

```go
queue, err := agentflow.NewPostgresJobQueue(db)
if err != nil {
  log.Fatal(err)
}

runHandler, err := agentflow.NewFrameworkRunJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
if err != nil {
  log.Fatal(err)
}

worker, err := async.NewWorker(queue, runHandler, async.WorkerConfig{
  WorkerID:    "worker-1",
  Concurrency: 4,
  LeaseTTL:    time.Minute,
  JobTimeout:  5 * time.Minute,
})
```

`agentflow.NewProductionHTTPHandler` mounts `/healthz`, `/readyz`, and async run submit/status/cancel endpoints under `/v1/runs`. See [docs/async-runtime.md](docs/async-runtime.md) and [docs/persistence/postgres-queue.md](docs/persistence/postgres-queue.md).

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

For a local enterprise stack with PostgreSQL+pgvector, Redis, MinIO, and `agent-http`, use the Compose template in [deploy/enterprise](deploy/enterprise):

```sh
cd deploy/enterprise
cp .env.example .env
docker compose up --build
```

See [docs/deployment-enterprise.md](docs/deployment-enterprise.md) for how the services map to the root facade constructors, production migration SQL, and Kubernetes base manifests.

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

Built-in tool adapters are documented in [docs/tools-http.md](docs/tools-http.md), [docs/mcp-tools.md](docs/mcp-tools.md), and [docs/knowledge-rag.md](docs/knowledge-rag.md).

### Install dependencies

```sh
go mod download
```

### Validate an example scenario

```sh
go run ./cmd/agentctl validate -f examples/autonomous.yaml
```

Expected output:

```text
ok
```

### Run a scenario

```sh
go run ./cmd/agentctl run \
  -f examples/autonomous.yaml \
  --prompt "hello agent" \
  --json
```

When no concrete LLM gateway is wired by the CLI, the runtime echoes the prompt. Library users can wire real gateways through `WithLLMGateway`.

### Build binaries

```sh
make build
```

This builds:

- `agentctl`: CLI for validating, running, and resuming scenarios.
- `agent-http`: HTTP/Webhook bridge for human-in-the-loop resume callbacks.

## CLI usage

### `agentctl validate`

Validates YAML shape, references, orchestration mode, and fixed-workflow graph integrity.

```sh
go run ./cmd/agentctl validate -f examples/fixed_workflow.yaml
```

### `agentctl run`

Runs a scenario.

```sh
go run ./cmd/agentctl run -f examples/autonomous.yaml --prompt "review this change"
```

Useful flags:

| Flag | Description |
| --- | --- |
| `-f, --file` | Scenario YAML file. Required. |
| `--prompt` | User prompt passed to the runtime. |
| `--run-id` | Optional run ID. Generated if omitted. |
| `--token-secret` | HMAC secret for HITL tokens. Defaults to `dev-secret` for local demos; use a strong secret for shared environments. |
| `--token-ttl` | HITL token lifetime. Defaults to `15m`. |
| `--state-dir` | Directory for durable run state and blobs. Required for resume across separate CLI processes. |
| `--json` | Emit machine-readable result JSON. |

### `agentctl resume`

Resumes a paused human-in-the-loop run from a signed token.

```sh
go run ./cmd/agentctl resume \
  --token "$TOKEN" \
  --decision approve \
  --token-secret "strong-secret" \
  --state-dir ./data/agentflow
```

Supported decisions:

- `approve`: continue.
- `reject`: cancel the run.
- `amend`: continue with amendment data.

Example with amendment:

```sh
go run ./cmd/agentctl resume \
  --token "$TOKEN" \
  --decision amend \
  --amendment '{"instruction":"make the answer shorter"}' \
  --state-dir ./data/agentflow
```

Use the same `--state-dir` for `run` and `resume` when a paused run must survive a separate CLI process or terminal session.

## HTTP/Webhook HITL bridge

Start the browser debug console and HTTP resume bridge:

```sh
AGENT_TOKEN_SECRET=strong-secret go run ./cmd/agent-http
```

Open:

```text
http://localhost:18080
```

By default the debug console binds to `127.0.0.1:18080` and may use the development secret. If `AGENT_HTTP_ADDR` is set to a non-loopback listener such as `:18080`, `AGENT_TOKEN_SECRET` and `AGENT_HTTP_API_KEY` are required. Optional `AGENT_HTTP_AUDIT_FILE` writes audit events as JSONL.

The debug console lets you:

- Select built-in scenarios: autonomous mock, fixed workflow, human-in-loop, real local model, and context governance.
- Edit scenario YAML in the browser.
- Enter prompts, runtime context JSON, and run scenarios.
- Configure an OpenAI-compatible local model endpoint for real model calls.
- Exercise context governance with sliding-window and summary compression.
- Inspect run result JSON, run-state snapshots, step outputs, tokens, and event timeline.
- Resume HITL checkpoints with `approve`, `reject`, or `amend`.

Resume endpoint:

```sh
curl -X POST http://localhost:18080 \
  -H 'Content-Type: application/json' \
  -d '{
    "token": "'"$TOKEN"'",
    "decision": "approve"
  }'
```

Response:

```json
{"status":"ok"}
```

Network-delivered tokens are HMAC signed. In production, always set `AGENT_TOKEN_SECRET` to a strong secret and use a persistent run-state repository.

## YAML scenario format

All scenario configuration lives under one `scenario:` root.

For editor completion, enum discovery, and CI validation, use the JSON Schema at [schemas/agentflow.scenario.schema.json](schemas/agentflow.scenario.schema.json). A full human-readable field reference is available in [docs/configuration-reference.md](docs/configuration-reference.md), and the CLI can print the same schema with `agentctl schema`.

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
| `skills` | Declarative prompt/policy/workflow packages. Skills are not runtime actors. |
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

Supported context strategies are `none`, `sliding_window`, and `sliding_window_with_summary`. Before each LLM call, the runtime emits `ContextPrepared` with before/after token estimates, dropped message count, summary status, and active input budget.

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
| root package | Framework facade: load YAML, validate, run, resume, wire options. |
| `pkg/async` | Job queue, lease, handler, and worker contracts for asynchronous execution. |
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

Example: expose async run submit/status/cancel endpoints.

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
  agentctl/
  agent-http/
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

- `Skill = prompt fragments + tool whitelist/policy + inline-able workflow sub-graph`.
- `Tool = schema-backed execution unit`.
- `Agent = entity that owns LLM and memory binding`.
- `RunStateRepository` is separate from Memory and handles resumable workflow snapshots.
- Context governance is profile-scoped: different agents/tools can route to different LLM profiles with different window, output, thinking, and compression policies.
- Autonomous execution supports LLM tool-calling loops with tool whitelist checks, approval-policy denial, per-run rate caps, retry, tool result feedback, and LLM/tool lifecycle events.
- Structured output runs use an agent-level `output_schema` and provider `StructuredOutputter`; streaming runs use provider `Streamer` and persist the final accumulated answer.
- Memory bindings are connected to runtime reads/writes for conversation and session history.
- Fixed workflows run from graph dependencies and edges, with bounded parallel batches, false-condition skips, retry policy, transform nodes, agent nodes, human-gate nodes, and CAS-safe step output persistence.
- Workflow human-gate nodes pause with persisted `CurrentNodeID`/`PendingGate` and can resume downstream execution after approval.
- `sub_agents` are available to supervisor agents as virtual delegation tools during autonomous execution.
- Skill prompt fragments and skill workflow segments are expanded during scenario build with namespaced workflow node IDs.
- File-backed RunState, BlobStore, and Memory adapters are available from the root facade for durable local persistence; PostgreSQL-backed RunState is available for production database persistence; S3-compatible BlobStore is available for large runtime/workflow outputs; Redis-backed leases are available for worker coordination; async queue and worker contracts are available for long-running execution; large step outputs are externalized to BlobStore when `step_output_threshold` is exceeded.
- Enterprise identity context, API key middleware, authorization middleware, RBAC policy contracts, and runtime tool authorization are available through `pkg/identity`, `pkg/security`, `NewStaticAPIKeyAuthenticator`, `NewAPIKeyMiddleware`, `NewAuthorizationMiddleware`, and `WithSecurityPolicy`.
- Audit event contracts and noop/in-memory/file sinks are available through `pkg/audit`, `NewNoopAuditSink`, `NewInMemoryAuditSink`, `NewFileAuditSink`, and `WithAuditSink`.
- Enterprise auth/tenancy and observability/governance designs are documented in [docs/security-auth-tenancy.md](docs/security-auth-tenancy.md) and [docs/observability-governance.md](docs/observability-governance.md).
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
- Autonomous runtime engine
- Fixed-workflow runner wired through the root facade
- In-memory Memory, RunStateRepository, and BlobStore
- LLM abstractions plus root constructors for OpenAI-compatible, Anthropic, local, router, and mock testing paths
- Autonomous tool-calling loop for registered tools and OpenAI-compatible function calling
- Runtime memory integration for injected history and persisted user/assistant/tool observations
- Fixed-workflow graph scheduler with dependencies, parallelism, retries, conditions, transform/agent/human-gate nodes, and CAS-safe output saves
- Workflow-level HITL pause/resume with saved scheduler position
- Multi-agent delegation through virtual sub-agent tools and persisted delegated outputs
- Skill prompt/workflow expansion during scenario build
- File-backed durable adapters for run state, blobs, and memory, plus PostgreSQL-backed run state, PostgreSQL-backed async queue, and S3-compatible blob storage
- Redis-backed distributed lease adapter for worker and workflow coordination
- Async job queue and worker contracts with in-memory/PostgreSQL queue adapters, framework-run worker handler, and HTTP submit/status/cancel handler
- Enterprise identity context, API key middleware, authorization middleware, RBAC policy contracts, and runtime tool authorization
- Audit event model with noop, in-memory, JSONL file, and structured `slog` sinks, plus framework audit wiring
- Governance hooks for tool budgets, tool side-effect ceilings, and persisted output redaction
- Durable CLI resume via `--state-dir`
- Runtime hardening: global/agent/profile timeouts, context-aware LLM/tool retry, tool rate caps, failed-run status persistence, and blob externalization for large outputs
- Structured output and streaming runtime paths exposed through the root facade
- Context governance with sliding-window trimming, heuristic summary compression, richer LLM profile config, and `ContextPrepared` events
- CLI and HTTP HITL surfaces with expiring CLI tokens and safer debug-console secret defaults
- GitHub Actions CI, golangci-lint configuration, govulncheck/CodeQL workflows, Dependabot, GoReleaser, Dockerfile, and security/community docs
- Unit and integration tests

Not yet production-complete:

- Redis run-state storage adapter beyond lease coordination
- Production queue adapter and framework handler for executing queued run jobs
- Advanced autonomous planning beyond the tool-calling loop
- Richer skill policy expansion beyond prompts and workflow segments
- Full provider feature parity for streaming, tool calls, structured output, embeddings
- Production auth layer for HTTP bridge beyond token HMAC verification

## Contributing

See [CONTRIBUTING.md](./CONTRIBUTING.md).

## License

Licensed under the [Apache License 2.0](./LICENSE).
