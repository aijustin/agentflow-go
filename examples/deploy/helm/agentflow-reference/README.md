# agentflow-reference Helm chart

Reference skeleton for deploying a tier-worker that consumes Postgres job queue and warm tier storage.

## Prerequisites

- PostgreSQL with migrations `0001` and `0002` applied
- Container image built from `examples/go/tier-worker`
- Secret `agentflow-postgres` with key `dsn`

## Install

```sh
kubectl create secret generic agentflow-postgres \
  --from-literal=dsn='postgres://agentflow:agentflow@postgres:5432/agentflow?sslmode=disable'

helm upgrade --install agentflow ./examples/deploy/helm/agentflow-reference
```

## Values

| Key | Default | Description |
|-----|---------|-------------|
| `image.repository` | `your-registry/agentflow-tier-worker` | Worker image |
| `image.tag` | `0.2.2` | Image tag (align with `agentflow.Version`) |
| `postgres.secretName` | `agentflow-postgres` | DSN secret |
| `tier.coldDir` | `/data/tier-cold` | Cold tier mount path |
| `probes.readiness.path` | `/readyz` | Readiness probe path |
| `probes.liveness.path` | `/healthz` | Liveness probe path |
| `resources` | requests/limits | CPU/memory defaults for production |
| `securityContext` | non-root | Container security context |

See [../../README.md](../../README.md) for the full reference stack.
