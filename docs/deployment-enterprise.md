# Enterprise Deployment Templates

The repository includes a local enterprise Compose stack under [deploy/enterprise](../deploy/enterprise). It is intended for development, integration testing, and as a concrete reference for production deployments.

## Included Services

- PostgreSQL with pgvector and bootstrap SQL for run state, async jobs, and knowledge embeddings.
- Redis for distributed coordination leases.
- MinIO for S3-compatible blob storage.
- `agent-http` for the debug console and HTTP HITL bridge.

## Production Migrations

Versioned PostgreSQL migrations live in [deploy/migrations/postgres](../deploy/migrations/postgres). The local Compose stack reuses the same `0001_agentflow_core.up.sql` file during database initialization.

Application teams can import these SQL files into their preferred migration runner. Adjust the pgvector dimension in an application-owned migration when the embedding model does not produce 1536-dimensional vectors.

## Quick Start

```sh
cd deploy/enterprise
cp .env.example .env
docker compose up --build
```

The debug console listens on `http://localhost:18080` by default. The API key, token secret, database password, and MinIO credentials are controlled through `.env`.

## How This Maps to AgentFlow Constructors

- PostgreSQL run snapshots: `agentflow.NewPostgresRunStateRepository(db)`
- PostgreSQL async jobs: `agentflow.NewPostgresJobQueue(db)`
- PostgreSQL pgvector knowledge: `agentflow.NewPostgresVectorStore(...)`
- Redis leases: `agentflow.NewRedisLocker(...)`
- S3-compatible blobs: `agentflow.NewS3BlobStore(...)`

The framework remains library-first. Production services should own their driver imports, connection pools, migration strategy, secrets, and worker process model.

## Next Deployment Layer

The Compose stack is the local baseline. A minimal Kustomize base lives in [deploy/kubernetes/base](../deploy/kubernetes/base) for `agent-http`. Workers are application-specific because the host service owns scenario wiring, database drivers, queue selection, and tool executors, so the repository includes `worker-deployment.example.yaml` as a template rather than a runnable generic worker.