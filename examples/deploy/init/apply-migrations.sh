#!/usr/bin/env sh
set -eu

ROOT="$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)"
DSN="${AGENT_POSTGRES_DSN:-postgres://agentflow:agentflow@127.0.0.1:5432/agentflow?sslmode=disable}"

for file in "$ROOT/migrations/postgres/"*.up.sql; do
  echo "applying $(basename "$file")"
  psql "$DSN" -v ON_ERROR_STOP=1 -f "$file"
done

echo "migrations applied"
