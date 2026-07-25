#!/usr/bin/env bash
set -euo pipefail

MIN_COVERAGE="${MIN_COVERAGE:-60}"

# Exclude optional backends, migration CLIs, and thin HTTP/wiring facades from
# the aggregate gate (same class as the existing */postgres exclusions). Core
# runtime + provider packages remain in the measured set.
go list ./... | grep -vE '/examples/|/schemas$|/testutil$|/toolticket$|/llm/mock$|/memory/tier/postgres$|/memory/tier/coldindex$|/memory/tier/cold$|/memory/tier/blobcold$|/integration$|/runstate/postgres$|/runstate/redis$|/runstate/file$|/queue/postgres$|/vector/postgres$|/observability/postgres$|/observability/http$|/studio/http$|/checkpoint/http$|/eventrouter/http$|/async/http$|/retention/http$|/migrations/|/pkg/adapters$|/pkg/httpx$|/llm/router$|/toolinvoke$|/fsatomic$|/planmode$' | xargs go test -coverprofile=coverage.out -covermode=atomic
TOTAL="$(go tool cover -func=coverage.out | awk '/^total:/ {gsub(/%/,"",$3); print $3}')"
awk -v total="$TOTAL" -v min="$MIN_COVERAGE" 'BEGIN {
  if (total+0 < min+0) {
    printf "coverage %.1f%% is below minimum %.1f%%\n", total, min
    exit 1
  }
  printf "coverage %.1f%% meets minimum %.1f%%\n", total, min
}'
