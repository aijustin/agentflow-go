# PostgreSQL migrations

Versioned SQL for adapters that store data in PostgreSQL:

- run snapshots (`agentflow_run_snapshots`; `fence_token` added in migration `0004`)
- async job queue (`agentflow_jobs`)
- runtime events (`agentflow_runtime_events`)
- transactional event outbox (`agentflow_outbox`, migration `0005`; drained by the framework's `WithOutboxRelay` loop)
- knowledge embeddings (`agentflow_knowledge_embeddings`)
- memory tier warm records (`agentflow_memory_tier_records`, migration `0002`)

## Applying

Preferred: the embedded Go runner. It records applied versions in
`agentflow_schema_migrations`, skips versions already applied, serializes
concurrent runners with `pg_advisory_lock`, and commits each migration with
its version record in one transaction:

```sh
./examples/deploy/init/apply-migrations.sh          # uses AGENT_POSTGRES_DSN
go run ./migrations/postgres/cmd/apply-migrations <dsn>
```

From Go:

```go
import migrations "github.com/aijustin/agentflow-go/migrations/postgres"

err := migrations.Migrate(ctx, db)        // idempotent, safe on every boot
version, err := migrations.AppliedVersion(ctx, db) // 0 when never migrated
```

The raw `*.up.sql` files remain usable with any migration runner; migration
`0004` creates the version table and records itself. `ValidateWiring` refuses
to start a PostgreSQL run-state repository against a schema older than
`migrations.RequiredVersion` so missing columns surface at boot.

`NewPostgresEventStore` can create `agentflow_runtime_events` at startup for local development. In locked-down environments, apply the migrations first and pass `SkipSchemaSetup: true`.

The vector table uses `vector(1536)`; change the dimension if your embedding model differs.
