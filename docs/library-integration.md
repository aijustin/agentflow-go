# Library integration guide

This guide helps teams embed `github.com/aijustin/agentflow-go` as a Go library.

For API stability rules see [api-stability.md](./api-stability.md).

## Choose an integration path

```text
Need synchronous in-process runs?
  └─ yes → Minimal embed (New + Run)
  └─ no  → HTTP API + async worker

Need durable run state?
  └─ yes → Postgres / Redis / file repositories
  └─ no  → defaults (in-memory)

Need human approval (HITL)?
  └─ yes → WithHITLTokenSecret or WithHumanGate
  └─ no  → skip HITL options

Need multi-tenant isolation?
  └─ yes → identity.WithPrincipal + runstate tenant filters
  └─ no  → single-tenant defaults
```

## Minimal embed

```go
scenario, _ := agentflow.LoadScenarioFile("scenario.yaml")
workDir, _ := agentflow.DemoWorkDir("scenario.yaml")
opts, _ := agentflow.DevelopmentOptions(scenario, agentflow.DevelopmentConfig{WorkDir: workDir})
if err := agentflow.ValidateWiring(scenario, opts...); err != nil { /* fail fast */ }
fw, _ := agentflow.New(scenario, opts...)
defer fw.Close(context.Background())
result, _ := fw.Run(ctx, agentflow.RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "hello"})
```

Runnable example: [examples/go/minimal/main.go](../examples/go/minimal/main.go)

## Production-style wiring

Use the public production helpers instead of copying `internal/cmd/agentruntime`:

```go
cfg, _ := agentflow.LoadProductionConfigFromEnv()
fw, _ := agentflow.NewProduction(cfg, os.Stderr)
defer fw.Close(context.Background())
queue, _ := agentflow.NewProductionQueue(cfg, &db)
handler, _ := agentflow.BuildProductionHTTPHandler(cfg, fw, queue)
worker, _ := agentflow.NewProductionWorker(cfg, queue, fw)
```

When opening Postgres yourself, register cleanup:

```go
db, _ := sql.Open("pgx", dsn)
fw, _ := agentflow.New(scenario,
    agentflow.WithRunStateRepository(repo),
    agentflow.WithDatabase(db),
)
```

Runnable examples:

- [examples/go/postgres/main.go](../examples/go/postgres/main.go)
- [examples/go/http-worker/main.go](../examples/go/http-worker/main.go)
- [examples/go/hitl-resume/main.go](../examples/go/hitl-resume/main.go)

## Wiring validation

Call `ValidateWiring` before `New` to catch missing executors, memories, or HITL config:

```go
agentflow.ValidateWiring(scenario, opts...)
```

Use `WithRequireLLM()` when mock echo fallback is unacceptable in production.

## Extension ports (`pkg/`)

| Package | Use when |
|---------|----------|
| `pkg/core` | Defining agents, tools, workflows programmatically |
| `pkg/llm` | Implementing custom LLM gateways |
| `pkg/llm/mock` | Local development and deterministic tests |
| `pkg/runstate` | Custom persistence or tenant enforcement |
| `pkg/toolticket` | Custom ticket store backends |
| `pkg/log` | Custom runtime log sinks (`WithLogger`) |
| `pkg/testutil` | Test helpers (`NewTestFramework`, `StaticGateway`) |
| `pkg/observability/prometheus` | Prometheus metrics recorder |

## Schema and version

```go
agentflow.Version          // library version
agentflow.SchemaVersion    // JSON Schema draft
agentflow.ScenarioJSONSchema()
```

## Shutdown order

1. Stop HTTP server (`server.Shutdown`)
2. Cancel worker context and wait for worker exit
3. Call `fw.Close(ctx)` to release DB and custom closers

## What not to import

Avoid `internal/` packages in application modules. They are not covered by v0 stability guarantees.
