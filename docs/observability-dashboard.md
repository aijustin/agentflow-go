# Observability Dashboard

AgentFlow can persist runtime events and serve a lightweight operations dashboard for live sessions. The dashboard is designed for product and support teams that need to see which sessions are running, how an agent moved through orchestration, which tools/skills/LLM calls happened, and what payload metadata was emitted for each step.

## What It Shows

- Sessions with status, scenario name, last event time, and event count.
- A real-time timeline for each run, backed by Server-Sent Events.
- Runtime event details including run ID, sequence, timestamps, trace/span IDs, and JSON payload.
- Tool and LLM call counts derived from the same event stream.

The dashboard uses the existing `core.EventSink` signal path, so no runtime fork is required. Events are appended once and can be fanned out to storage, metrics/tracing, and logs.

## Quick Start With PostgreSQL

Register a `database/sql` driver in the host application, open a pool, and create the event store. `NewPostgresEventStore` creates the table and indexes automatically unless `SkipSchemaSetup` is set. Automatic setup is intended to make first enablement simple; teams with locked-down database permissions should apply the migration SQL first and then disable startup DDL.

```go
package main

import (
    "context"
    "database/sql"
    "log"
    "net/http"
    "os"
    "time"

    agentflow "github.com/aijustin/agentflow-go"
    _ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
    ctx := context.Background()
    db, err := sql.Open("pgx", os.Getenv("AGENTFLOW_POSTGRES_DSN"))
    if err != nil {
        log.Fatal(err)
    }
    db.SetMaxOpenConns(20)
    db.SetMaxIdleConns(5)
    db.SetConnMaxLifetime(30 * time.Minute)

    eventStore, err := agentflow.NewPostgresEventStore(ctx, agentflow.PostgresEventStoreConfig{DB: db})
    if err != nil {
        log.Fatal(err)
    }
    eventHub := agentflow.NewEventHub()

    scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithEventSink(agentflow.NewEventFanoutSink(
            agentflow.NewEventStoreSink(eventStore, eventHub),
            agentflow.NewSlogEventSink(nil),
        )),
    )
    if err != nil {
        log.Fatal(err)
    }
    _ = fw

    dashboard, err := agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
        Store: eventStore,
        Hub:   eventHub,
    })
    if err != nil {
        log.Fatal(err)
    }

    mux := http.NewServeMux()
    mux.Handle("/observability/", http.StripPrefix("/observability", dashboard))
    log.Fatal(http.ListenAndServe(":7070", mux))
}
```

Open `http://localhost:7070/observability/` to view live runs.

## Local Development Without PostgreSQL

Use the in-memory store when persistence is not required:

```go
eventStore := agentflow.NewInMemoryEventStore()
eventHub := agentflow.NewEventHub()

scenario := builder.MinimalAutonomous("assistant")
fw, err := agentflow.New(scenario, agentflow.WithEventSink(agentflow.NewEventStoreSink(eventStore, eventHub)),
)
```

The in-memory store is concurrency-safe and assigns per-run event sequences. Data is lost when the process exits.

## Database Setup

The default PostgreSQL table is `agentflow_runtime_events`:

```sql
CREATE TABLE IF NOT EXISTS agentflow_runtime_events (
  id bigserial PRIMARY KEY,
  run_id text NOT NULL,
  sequence bigint NOT NULL,
  event_type text NOT NULL,
  scenario_name text NOT NULL DEFAULT '',
  trace_id text NOT NULL DEFAULT '',
  span_id text NOT NULL DEFAULT '',
  occurred_at timestamptz NOT NULL,
  payload_json jsonb NOT NULL DEFAULT '{}'::jsonb,
  created_at timestamptz NOT NULL DEFAULT now(),
  UNIQUE (run_id, sequence)
);
```

The constructor also creates these indexes:

- `agentflow_runtime_events_run_sequence_idx` on `(run_id, sequence)`.
- `agentflow_runtime_events_run_updated_idx` on `(run_id, occurred_at DESC)`.
- `agentflow_runtime_events_type_time_idx` on `(event_type, occurred_at DESC)`.

Use a custom table name when the application owns a schema prefix:

```go
eventStore, err := agentflow.NewPostgresEventStore(ctx, agentflow.PostgresEventStoreConfig{
    DB:        db,
    TableName: "agentflow.runtime_events",
})
```

For production environments where DDL must be reviewed, run outside application startup, or coordinated for large existing tables, apply [migrations/postgres/0001_agentflow_core.up.sql](../migrations/postgres/0001_agentflow_core.up.sql) and then disable automatic schema setup:

```go
eventStore, err := agentflow.NewPostgresEventStore(ctx, agentflow.PostgresEventStoreConfig{
    DB:              db,
    SkipSchemaSetup: true,
})
```

## HTTP API

The handler serves both the UI and JSON/SSE APIs. When mounted under `/observability/`, endpoints are:

- `GET /observability/` serves the dashboard.
- `GET /observability/api/runs?status=running&limit=50` lists session summaries.
- `GET /observability/api/runs/{run_id}/events?after_sequence=10&limit=200` lists timeline events.
- `GET /observability/api/runs/{run_id}/stream?after_sequence=10` streams new events as Server-Sent Events.

When `ObservabilityHTTPHandlerConfig.Framework` is set, Studio endpoints are also available:

- `GET /observability/api/graph` — scenario workflow topology (nested subgraphs).
- `GET /observability/api/runs/{run_id}/steps` — checkpointed step outputs (`ListRunSteps`).
- `POST /observability/api/runs/{run_id}/resume-from-step` — time-travel rerun from a node (`{"node_id":"..."}`).
- `GET /observability/api/runs/{run_id}/checkpoints?limit=50` — append-only snapshot revisions.
- `GET /observability/api/runs/{run_id}/checkpoints/{version}` — load one historical snapshot.
- `POST /observability/api/runs/{run_id}/resume-from-checkpoint` — restore and rerun from a revision (`{"version":3}`).
- `POST /observability/api/studio/validate` — validate an edited graph (`ScenarioGraph` JSON).
- `POST /observability/api/studio/codegen` — export builder Go code for an edited graph.
- `POST /observability/api/studio/yaml` — export legacy scenario YAML for an edited graph.
- `POST /observability/api/studio/run` — validate and execute an edited graph (`{"graph":{...},"prompt":"..."}`).
- `POST /observability/api/studio/save` — write edited graph to the host-configured scenario file (`StudioSavePath`).

Production routes (when `Framework` is wired via `NewProductionHTTPHandler`):

- `POST /v1/studio/validate|codegen|yaml|run|save`
- `GET /observability/api/compare?run_a=&run_b=` — diff step outputs across two runs.
- `GET /observability/api/runs/{run_id}/thread` — list fork/thread group runs.
- `POST /observability/api/runs/{run_id}/fork` — copy run state to a new run ID.

Open the dashboard and switch to the **Graph** tab for read-only graph debug, **Editor** for drag-and-drop topology edits (per-node input JSON, `condition`, `depends_on`, conditional edges, canvas layout persisted in `GraphView.layout`, subgraph canvas switching, Preview save diff, and Revert to loaded), **Compare** for multi-run diffs, and **Thread** for fork lineage. The UI defaults to **中文**; use the header language selector for **English**. Preference is stored in `localStorage` (`obs-lang`). See [studio-roadmap.md](./studio-roadmap.md).

Protect the handler with the same middleware used for production APIs:

```go
authMiddleware, err := agentflow.NewJWTMiddleware(agentflow.JWTMiddlewareConfig{Authenticator: authenticator})
if err != nil {
    log.Fatal(err)
}
dashboard, err := agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
    Store:          eventStore,
    Hub:            eventHub,
    Framework:      fw,
    AuthMiddleware: authMiddleware,
})
```

Production API (when `ProductionHTTPHandlerConfig.Framework` is set):

- `GET /v1/runs/{run_id}/steps`
- `POST /v1/runs/{run_id}/resume-from-step` with `{"node_id":"..."}`
- `GET /v1/runs/{run_id}/checkpoints?limit=50`
- `GET /v1/runs/{run_id}/checkpoints/{version}`
- `POST /v1/runs/{run_id}/resume-from-checkpoint` with `{"version":3}`

Checkpoint history requires `WithCheckpointHistory(agentflow.NewInMemoryCheckpointHistory())` (or a custom `runstate.CheckpointHistory` adapter).

See [studio-roadmap.md](./studio-roadmap.md). Example: `go run ./examples/go/http-worker/main.go` → `http://127.0.0.1:7060/observability/`.

### Editor live preview & trace links

- **Editor → Run graph** keeps you on the Editor tab; nodes highlight `done` / `current` while the run streams.
- **Editor subgraph drill-down**: double-click a `subgraph` node (or use **Drill into subgraph** in node properties) to edit the inner canvas; **Back to main graph** returns to the parent workflow. Step highlighting uses `{parent}::{inner}` when drilled during a live run.
- **Graph / Inspector → Trace / Span tree** shows nested spans when `parent_span_id` is present; click a row to jump to Timeline.
- Optional external trace UI:

```go
dashboard, err := agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
    Store:           eventStore,
    Hub:             eventHub,
    Framework:       fw,
    TraceExploreURL: "https://jaeger.example.com/trace/{trace_id}",
})
```

The UI reads `GET /observability/api/ui-config` for `trace_explore_url`.

## Troubleshooting

| Symptom | Likely cause | Fix |
|---------|--------------|-----|
| Graph/Editor show “Framework not configured” | `ObservabilityHTTPHandlerConfig.Framework` is nil | Pass `Framework: fw` when creating the handler |
| Graph empty for autonomous scenarios | No `fixed_workflow` topology (expected) | Use Timeline / Autonomous trace; design workflow in Editor if needed |
| API calls hit `/api/*` instead of `/observability/api/*` | Observability mounted under a sub-path | Use trailing slash URL (`/observability/`) or upgrade to a build with `apiURL()` path helper |
| Time Travel / Compare / Thread disabled | Same as Framework not configured | Wire Framework + checkpoint history for full Studio |

Minimum wiring for **timeline-only** vs **full Studio**:

```go
// Timeline + SSE only
agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
    Store: store,
    Hub:   hub,
})

// Full Studio (graph, editor, time travel, compare, thread)
agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
    Store:          store,
    Hub:            hub,
    Framework:      fw,
    StudioSavePath: "/path/to/scenario.yaml",
})
```

## Data Safety

The detail pane renders event payload JSON. Runtime events are intended to carry operational metadata, not secrets. Keep these rules in place:

- Use `WithOutputRedactor` for persisted runtime and workflow outputs.
- Do not emit provider API keys, HITL tokens, raw credentials, or unredacted tool outputs into event payloads.
- Keep logs, metrics labels, and dashboard filters low-cardinality.
- Apply HTTP authentication before exposing the dashboard outside a trusted network.

Audit logs remain separate from runtime observability. Use audit sinks for compliance questions such as who submitted a run, approved a HITL gate, or triggered a protected tool.