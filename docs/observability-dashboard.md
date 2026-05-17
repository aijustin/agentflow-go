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

    fw, err := agentflow.NewFromFile(
        "scenario.yaml",
        agentflow.WithEventSink(agentflow.NewEventFanoutSink(
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
    log.Fatal(http.ListenAndServe(":8080", mux))
}
```

Open `http://localhost:8080/observability/` to view live runs.

## Local Development Without PostgreSQL

Use the in-memory store when persistence is not required:

```go
eventStore := agentflow.NewInMemoryEventStore()
eventHub := agentflow.NewEventHub()

fw, err := agentflow.NewFromFile(
    "scenario.yaml",
    agentflow.WithEventSink(agentflow.NewEventStoreSink(eventStore, eventHub)),
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

For production environments where DDL must be reviewed, run outside application startup, or coordinated for large existing tables, apply [deploy/migrations/postgres/0001_agentflow_core.up.sql](../deploy/migrations/postgres/0001_agentflow_core.up.sql) and then disable automatic schema setup:

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

Protect the handler with the same middleware used for production APIs:

```go
authMiddleware, err := agentflow.NewJWTMiddleware(agentflow.JWTMiddlewareConfig{Authenticator: authenticator})
if err != nil {
    log.Fatal(err)
}
dashboard, err := agentflow.NewObservabilityHTTPHandler(agentflow.ObservabilityHTTPHandlerConfig{
    Store:          eventStore,
    Hub:            eventHub,
    AuthMiddleware: authMiddleware,
})
```

## Data Safety

The detail pane renders event payload JSON. Runtime events are intended to carry operational metadata, not secrets. Keep these rules in place:

- Use `WithOutputRedactor` for persisted runtime and workflow outputs.
- Do not emit provider API keys, HITL tokens, raw credentials, or unredacted tool outputs into event payloads.
- Keep logs, metrics labels, and dashboard filters low-cardinality.
- Apply HTTP authentication before exposing the dashboard outside a trusted network.

Audit logs remain separate from runtime observability. Use audit sinks for compliance questions such as who submitted a run, approved a HITL gate, or triggered a protected tool.