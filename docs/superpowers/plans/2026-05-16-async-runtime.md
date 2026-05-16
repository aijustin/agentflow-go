# Async Runtime Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add asynchronous job execution so long-running agent workflows can run outside request paths.

**Architecture:** Keep the synchronous framework facade as the embedded API. Add public async queue/worker contracts, local adapters, then production queue adapters and HTTP submit/status/cancel APIs.

**Tech Stack:** Go 1.25.10+, `context`, standard-library concurrency primitives, optional PostgreSQL/Redis queue adapters in later slices.

---

## Task 1: Public Async Contracts

- [x] Add `pkg/async.Job`, `JobState`, `Lease`, `Queue`, `Handler`, and `Worker`.
- [x] Add worker validation and structured concurrency with context cancellation.
- [x] Add worker tests for processing and cancellation.

## Task 2: In-Memory Queue

- [x] Add `internal/adapter/queue/inmem`.
- [x] Support enqueue, lease, load, complete, fail, cancel.
- [x] Recover expired leases.
- [x] Retry failed jobs until `MaxAttempts`, then mark `dead_letter`.
- [x] Expose `agentflow.NewInMemoryJobQueue()`.

## Task 3: Framework Run Handler

- [x] Define a job payload for scenario run requests.
- [x] Add a handler that calls `Framework.Run` and records result status.
- [x] Preserve run IDs and cancellation behavior.
- [x] Add tests for completed and invalid jobs.

## Task 4: Production Queue Adapter

- [x] Choose PostgreSQL or Redis queue as the first production adapter.
- [x] Implement leasing with stale lease recovery.
- [x] Add database/sql fake-driver tests for adapter behavior.
- [x] Document schema or Redis key strategy.

## Task 5: HTTP Async API

- [x] Add submit endpoint that returns `run_id` and `job_id`.
- [x] Add status endpoint backed by queue.
- [x] Add cancel endpoint.
- [ ] Add debug UI integration after the API is stable.

## Verification

- [x] `CGO_ENABLED=0 go test -ldflags="-w" ./pkg/async ./internal/adapter/queue/inmem ./...`
- [x] `CGO_ENABLED=0 go vet ./...`
- [x] `make build`