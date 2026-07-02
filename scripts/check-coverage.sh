#!/usr/bin/env bash
set -euo pipefail

MIN_COVERAGE="${MIN_COVERAGE:-80}"

go list ./... | grep -vE '/examples/|/schemas$|/testutil$|/toolticket$|/llm/mock$|/memory/tier/postgres$|/memory/tier/coldindex$|/integration$|/runstate/postgres$|/queue/postgres$|/vector/postgres$|/observability/postgres$' | xargs go test -coverprofile=coverage.out -covermode=atomic
TOTAL="$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/,"",$3); print $3}')"
awk -v total="$TOTAL" -v min="$MIN_COVERAGE" 'BEGIN {
  if (total+0 < min+0) {
    printf "coverage %.1f%% is below minimum %.1f%%\n", total, min
    exit 1
  }
  printf "coverage %.1f%% meets minimum %.1f%%\n", total, min
}'
