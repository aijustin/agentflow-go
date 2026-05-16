# PostgreSQL Run State Adapter

The PostgreSQL adapter persists `runstate.RunSnapshot` values behind the existing `runstate.Repository` contract. It is intended for production deployments that need restart-safe HITL resume and multi-instance CAS protection.

## Driver Strategy

agentflow-go does not import a PostgreSQL driver directly. Applications register their preferred `database/sql` driver and pass an initialized `*sql.DB` into the root constructor.

Example driver choices include `github.com/jackc/pgx/v5/stdlib` and `github.com/lib/pq`. Keep the driver dependency in the application that owns deployment and connection-pool policy.

```go
package main

import (
    "database/sql"
    "log"
    "time"

    agentflow "github.com/aijustin/agentflow-go"
    _ "github.com/jackc/pgx/v5/stdlib"
)

func openRunState(dsn string) agentflow.Option {
    db, err := sql.Open("pgx", dsn)
    if err != nil {
        log.Fatal(err)
    }
    db.SetMaxOpenConns(25)
    db.SetMaxIdleConns(10)
    db.SetConnMaxLifetime(5 * time.Minute)

    runs, err := agentflow.NewPostgresRunStateRepository(db)
    if err != nil {
        log.Fatal(err)
    }
    return agentflow.WithRunStateRepository(runs)
}
```

## Table Contract

The default table name is `agentflow_run_snapshots`. A schema-qualified table name such as `agentflow.run_snapshots` can be passed as the optional second argument.

The adapter expects these columns:

| Column | Purpose |
| --- | --- |
| `run_id` | Unique run identifier used for load/delete and insert conflict detection. |
| `version` | CAS version. Updates match both `run_id` and `version`. |
| `scenario_name` | Scenario name for operational lookup. |
| `status` | Current run status. |
| `current_node_id` | Workflow resume position. |
| `snapshot_json` | Full serialized `RunSnapshot` JSON payload. |
| `updated_at` | Timestamp updated by the adapter on save. |

Migration SQL should live in the deployment package that owns production schema review. The adapter intentionally does not auto-create or mutate database schema.

## CAS Semantics

- `Save(ctx, snapshot, 0)` inserts a new row and returns `runstate.ErrStaleSnapshot` if the run already exists.
- `Save(ctx, snapshot, expectedVersion)` updates only when the stored version equals `expectedVersion`.
- Successful saves increment `snapshot.Version` when the provided version is not already greater than the expected version.
- `Load` returns `runstate.ErrNotFound` for missing rows.
- `Delete` is idempotent from the framework's perspective.

## Operational Notes

- Use context deadlines on request and worker paths.
- Configure the connection pool in the application before constructing the repository.
- Keep table names static and reviewed. The constructor validates table identifiers and rejects unsafe names.
- Store large workflow outputs in a `BlobStore`; run state should hold `BlobRef` metadata, not large raw payloads.