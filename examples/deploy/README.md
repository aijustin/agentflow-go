# Reference deployment stack

This directory provides a **copyable reference stack** for local development and integration testing. It is not an official product deployment — host applications own production topology, secrets, and scaling.

## Services

| Service | Port | Purpose |
|---------|------|---------|
| PostgreSQL (pgvector) | 5432 | Run state, job queue, event store, knowledge vectors, **tier warm store** |
| Redis | 6379 | Run state CAS, distributed leases |
| MinIO | 9000 / 9001 | S3-compatible blob store |

## Quick start (HTTP worker)

```sh
cd examples/deploy
docker compose up -d
export AGENT_POSTGRES_DSN='postgres://agentflow:agentflow@127.0.0.1:5432/agentflow?sslmode=disable'
./init/apply-migrations.sh
go run ../go/http-worker/main.go
```

## Tier worker (memory.reconcile)

The [tier-worker](../go/tier-worker/main.go) example wires:

- `builder.TierMemoryAutonomous` via [examples/go/scenario](../go/scenario/scenario.go) with hot/warm/cold tier memory
- Postgres **warm** tier (`agentflow_memory_tier_records`, migration `0002`)
- Local **cold** tier directory (`AGENT_TIER_COLD_DIR`, default temp dir)
- Postgres job queue for async `memory.reconcile` jobs after tier writes
- Prometheus metrics at `/metrics`

```sh
cd examples/deploy
docker compose up -d
export AGENT_POSTGRES_DSN='postgres://agentflow:agentflow@127.0.0.1:5432/agentflow?sslmode=disable'
export AGENT_TIER_COLD_DIR="${TMPDIR:-/tmp}/agentflow-tier-cold"
mkdir -p "$AGENT_TIER_COLD_DIR"
./init/apply-migrations.sh
go run ../go/tier-worker/main.go
```

Enqueue a run (async worker processes `run` and subsequent `memory.reconcile` jobs):

```sh
curl -s -X POST http://127.0.0.1:7070/v1/jobs/runs \
  -H 'Content-Type: application/json' \
  -d '{"agent":"assistant","prompt":"remember tiered facts about billing"}'
curl -s http://127.0.0.1:7070/metrics | grep 'agentflow_memory_tier\|agentflow_queue'
```

Without `AGENT_POSTGRES_DSN`, tier-worker falls back to in-memory queue and the default in-memory tier store from the builder stack.

## Kubernetes / Helm

- [kubernetes/tier-worker.yaml](kubernetes/tier-worker.yaml) — minimal Deployment + Service reference
- [helm/agentflow-reference/](helm/agentflow-reference/) — copyable Helm chart skeleton for the same stack

Apply migrations to your cluster Postgres before rolling out the worker.

## Health and metrics

```sh
curl -s http://127.0.0.1:7070/healthz
curl -s http://127.0.0.1:7070/metrics | head
```

## Environment variables

| Variable | Example | Used by |
|----------|---------|---------|
| `AGENT_POSTGRES_DSN` | `postgres://agentflow:agentflow@127.0.0.1:5432/agentflow?sslmode=disable` | Postgres adapters, tier warm store, job queue |
| `AGENT_TIER_COLD_DIR` | `/var/lib/agentflow/tier-cold` | tier-worker cold tier (gzip JSON) |
| `AGENT_HTTP_ADDR` | `127.0.0.1:7060` (http-worker) / `127.0.0.1:7070` (tier-worker) | bind address |
| `AGENT_REDIS_ADDR` | `127.0.0.1:6379` | Redis run-state / locker |
| `AGENT_S3_ENDPOINT` | `http://127.0.0.1:9000` | Blob store |
| `AGENT_S3_ACCESS_KEY` | `minioadmin` | Blob store |
| `AGENT_S3_SECRET_KEY` | `minioadmin` | Blob store |
| `AGENT_S3_BUCKET` | `agentflow` | Blob store |

Wire these in your host application via `WithRunStateRepository`, `WithBlobStore`, `WithTierStore`, `WithJobQueue`, and related root options. See [docs/library-integration.md](../../docs/library-integration.md).

## Migrations

| File | Tables |
|------|--------|
| `0001_agentflow_core.up.sql` | run snapshots, jobs, events, knowledge |
| `0002_agentflow_memory_tier.up.sql` | `agentflow_memory_tier_records` (warm tier) |

Use `./init/apply-migrations.sh` or your migration runner in production.
