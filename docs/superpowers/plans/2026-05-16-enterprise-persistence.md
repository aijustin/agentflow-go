# Enterprise Persistence Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add production-grade persistence foundations for run state and large outputs.

**Architecture:** Keep the existing `runstate.Repository` and `runstate.BlobStore` contracts stable. Add production adapters behind constructors, with integration tests gated by build tags and no ORM dependency.

**Tech Stack:** Go 1.25.10+, `database/sql`, PostgreSQL-compatible SQL, S3-compatible object storage in a later slice, build-tagged integration tests.

---

## File Structure

- Create: `internal/adapter/runstate/postgres/repository.go` for PostgreSQL-compatible run state persistence.
- Create: `internal/adapter/runstate/postgres/repository_test.go` for repository contract tests using a local test driver or integration build tag.
- Modify: `framework.go` to expose a root constructor when the adapter is ready for public use.
- Modify: `README.md` and `README.zh-CN.md` with production persistence examples.
- Modify: `docs/enterprise-roadmap.md` as milestones complete.

## Task 1: PostgreSQL RunStateRepository

- [x] Write a failing test for `Save` inserting version 1 when expected version is 0.
- [x] Write a failing test for `Save` returning `runstate.ErrStaleSnapshot` when the expected version does not match.
- [x] Write a failing test for `Load` returning `runstate.ErrNotFound` when no row exists.
- [x] Implement `Repository` with a caller-provided `*sql.DB`.
- [x] Store the full snapshot as JSON and expose key columns for lookup and cleanup.
- [x] Use parameterized SQL only.
- [x] Use conditional updates to preserve CAS semantics.
- [x] Run `CGO_ENABLED=0 go test -ldflags="-w" ./...`.

## Task 2: Public Constructor

- [x] Add `NewPostgresRunStateRepository(db *sql.DB, tableName ...string)` to the root facade.
- [x] Document that callers must register their PostgreSQL driver in application code.
- [x] Add constructor tests for invalid public inputs and repository contract tests for the adapter.
- [x] Run `CGO_ENABLED=0 go test -ldflags="-w" ./...`.

## Task 3: S3-Compatible BlobStore

- [x] Decide dependency strategy before adding an SDK.
- [x] Add an adapter that supports endpoint override for MinIO/private S3.
- [x] Validate checksum and size on read.
- [x] Add unit tests with an HTTP S3-compatible fake.
- [x] Document required credentials and endpoint configuration.

## Task 4: Redis Coordination Adapter

- [x] Decide whether Redis is used for run state, locks, or queues in the first slice.
- [x] Add a small lock/lease interface if Redis is only used for coordination.
- [x] Add atomic renew/release behavior through Redis Lua scripts.
- [x] Add unit tests with a fake Redis server.

## Task 5: Retention And Cleanup

- [ ] Add cleanup API for completed/failed/cancelled runs older than a cutoff.
- [ ] Add orphan blob cleanup strategy.
- [ ] Document operational retention policies.

## Verification

- [ ] `gofmt -w .`
- [ ] `CGO_ENABLED=0 go test -ldflags="-w" ./...`
- [ ] `CGO_ENABLED=0 go vet ./...`
- [ ] `make build`
- [ ] Integration tests with local PostgreSQL/Redis/MinIO when available.

## Notes

- Do not introduce an ORM.
- Do not require a specific PostgreSQL driver from the root module; accept `*sql.DB` and let applications import their preferred driver.
- Keep migrations as reviewed SQL artifacts in a later production deployment package.