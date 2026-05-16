# Enterprise Local Stack

This Compose stack starts the infrastructure pieces used by the enterprise adapters:

- PostgreSQL with pgvector for run state, async jobs, and knowledge embeddings.
- Redis for distributed leases.
- MinIO for S3-compatible blob storage.
- `agent-http` for the built-in debug and HITL console.

## Start

```sh
cd deploy/enterprise
cp .env.example .env
docker compose up --build
```

Open the debug console at <http://localhost:18080>. Send the API key from `.env` as a bearer token when calling protected HTTP endpoints directly.

## Connection Values

From the host:

```text
PostgreSQL DSN: postgres://agentflow:agentflow-dev-password@localhost:5432/agentflow?sslmode=disable
Redis:          localhost:6379
MinIO API:      http://localhost:9000
MinIO Console:  http://localhost:9001
S3 bucket:      agentflow
```

From another Compose service on the same network:

```text
PostgreSQL DSN: postgres://agentflow:agentflow-dev-password@postgres:5432/agentflow?sslmode=disable
Redis:          redis:6379
MinIO API:      http://minio:9000
```

## Schema

The bootstrap SQL in `postgres/init/001-agentflow.sql` runs the production migration in `../migrations/postgres/0001_agentflow_core.up.sql`, creating the default tables expected by:

- `NewPostgresRunStateRepository`
- `NewPostgresJobQueue`
- `NewPostgresVectorStore`

The vector table uses `vector(1536)`. Change that dimension if your embedding model produces a different vector size.

## Production Notes

- Replace all example secrets before exposing the stack outside a developer machine.
- Use a migration tool for reviewed production schema changes instead of relying on Compose init scripts.
- Run application-specific workers as separate services that import `agentflow-go`, open the database/Redis/S3 clients, and wire the root facade constructors.
- Keep MinIO private by default; the init container creates the bucket with anonymous access disabled.