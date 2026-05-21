# Reference deployment stack

This directory provides a **copyable reference stack** for local development and integration testing. It is not an official product deployment — host applications own production topology, secrets, and scaling.

## Services

| Service | Port | Purpose |
|---------|------|---------|
| PostgreSQL (pgvector) | 5432 | Run state, job queue, event store, knowledge vectors |
| Redis | 6379 | Run state CAS, distributed leases |
| MinIO | 9000 / 9001 | S3-compatible blob store |

## Quick start

```sh
cd examples/deploy
docker compose up -d
export AGENT_POSTGRES_DSN='postgres://agentflow:agentflow@127.0.0.1:5432/agentflow?sslmode=disable'
psql "$AGENT_POSTGRES_DSN" -f ../../migrations/postgres/0001_agentflow_core.up.sql
go run ../go/http-worker/main.go
```

Health and metrics:

```sh
curl -s http://127.0.0.1:8080/healthz
curl -s http://127.0.0.1:8080/metrics | head
```

## Environment variables

| Variable | Example | Used by |
|----------|---------|---------|
| `AGENT_POSTGRES_DSN` | `postgres://agentflow:agentflow@127.0.0.1:5432/agentflow?sslmode=disable` | Postgres adapters |
| `AGENT_REDIS_ADDR` | `127.0.0.1:6379` | Redis run-state / locker |
| `AGENT_S3_ENDPOINT` | `http://127.0.0.1:9000` | Blob store |
| `AGENT_S3_ACCESS_KEY` | `minioadmin` | Blob store |
| `AGENT_S3_SECRET_KEY` | `minioadmin` | Blob store |
| `AGENT_S3_BUCKET` | `agentflow` | Blob store |

Wire these in your host application via `WithRunStateRepository`, `WithBlobStore`, `WithDatabase`, and related root options. See [docs/library-integration.md](../../docs/library-integration.md).
