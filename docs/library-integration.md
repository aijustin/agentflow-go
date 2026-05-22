# Library integration guide

This guide helps teams embed `github.com/aijustin/agentflow-go` as a Go library.

For API stability rules see [api-stability.md](./api-stability.md). For a browser-friendly guide see [manual.html](./manual.html).

## Choose an integration path

```text
Need synchronous in-process runs?
  └─ yes → Minimal embed (New + Run)
  └─ no  → HTTP handler + async worker in your service

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
import (
    agentflow "github.com/aijustin/agentflow-go"
    "github.com/aijustin/agentflow-go/pkg/builder"
)

scenario := builder.MinimalAutonomous("assistant")
if err := agentflow.ValidateScenario(scenario); err != nil { /* fail fast */ }
workDir, _ := testutil.ScenarioWorkDir("autonomous-echo")
opts, _ := testutil.WiringOptions(scenario, testutil.WiringConfig{WorkDir: workDir})
if err := agentflow.ValidateWiring(scenario, opts...); err != nil { /* fail fast */ }
fw, _ := agentflow.New(scenario, opts...)
defer fw.Close(context.Background())
result, _ := fw.Run(ctx, agentflow.RunRequest{RunID: "run-1", Agent: "assistant", Prompt: "hello"})
```

`testutil.WiringOptions` is for examples and tests only. Production embedders must register real LLM gateways and tool executors with `WithLLMGateway` and `WithToolExecutor` / `WithToolResolver`.

Runnable example: [examples/go/minimal/main.go](../examples/go/minimal/main.go)

## Go DSL builder (default)

Construct scenarios in Go with shared presets, typed constants, and reusable workflow graphs:

```go
scenario := builder.MinimalAutonomous("assistant")
if err := agentflow.ValidateScenario(scenario); err != nil { /* fail fast */ }
fw, _ := agentflow.New(scenario, opts...)
```

Validate all catalog stacks:

```sh
go run ./examples/go/validate -kind builder all
make validate-builder
```

Reference: [builder-reference.md](./builder-reference.md) · [examples/go/builder/main.go](../examples/go/builder/main.go)

## Production-style wiring in your service

Wire explicitly in your `main` or DI layer:

```go
scenario := builder.MinimalTicketHandling("support")
fw, _ := agentflow.New(scenario,
  agentflow.WithLLMGateway(yourGateway),
  agentflow.WithRunStateRepository(repo),
  agentflow.WithToolExecutor("ticket", executor),
  agentflow.WithHITLTokenSecret(secret, os.Stderr),
)
queue, _ := agentflow.NewPostgresJobQueue(db) // or NewInMemoryJobQueue()
handler, _ := agentflow.NewProductionHTTPHandler(agentflow.ProductionHTTPHandlerConfig{
  Queue: queue, Policy: policy, Framework: fw,
})
jobHandler, _ := agentflow.NewFrameworkJobHandler(agentflow.FrameworkRunJobHandlerConfig{Framework: fw})
worker, _ := async.NewWorker(queue, jobHandler, async.WorkerConfig{WorkerID: "w1"})
```

Runnable examples:

- [examples/go/postgres/main.go](../examples/go/postgres/main.go)
- [examples/go/http-worker/main.go](../examples/go/http-worker/main.go)
- [examples/go/hitl-resume/main.go](../examples/go/hitl-resume/main.go)
- [examples/go/event-trigger/main.go](../examples/go/event-trigger/main.go)

## Wiring validation

```go
agentflow.ValidateWiring(scenario, opts...)
```

Builder stacks and catalog manifests:

```sh
go run ./examples/go/validate -kind builder all
go run ./examples/go/validate -kind tool examples/catalog/tools/echo.tool.yaml
make validate-catalog
```

Use `WithRequireLLM()` when mock echo fallback is unacceptable.

## PostgreSQL schema

Apply [migrations/postgres/0001_agentflow_core.up.sql](../migrations/postgres/0001_agentflow_core.up.sql) with your migration runner before using Postgres adapters in production.

## Extension ports (`pkg/`)

See [README.md](../README.md) for the full list of extension packages (memory, runstate, knowledge, async, governance, identity, security, etc.).

## Deprecated YAML loading

`LoadScenarioFile` / `NewFromFile` remain available but are **deprecated**. New scenarios should use `pkg/builder`. See [product-direction.md](./product-direction.md).
