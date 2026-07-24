#!/usr/bin/env sh
# Applies the agentflow PostgreSQL migrations via the embedded Go runner:
# versions already recorded in agentflow_schema_migrations are skipped,
# concurrent runs are serialized with pg_advisory_lock, and every applied
# version is recorded. Requires Go and network access to the database.
set -eu

ROOT="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"

cd "$ROOT"
go run ./migrations/postgres/cmd/apply-migrations "$@"
