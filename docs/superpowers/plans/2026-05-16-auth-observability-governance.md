# Auth Observability Governance Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add enterprise identity, authorization, audit, observability, and governance foundations.

**Architecture:** Add identity and policy ports before wiring them into HTTP/runtime paths. Keep debug UI separate from production API behavior.

**Tech Stack:** Go 1.25.10+, `context`, `log/slog`, optional Prometheus/OpenTelemetry dependencies after explicit dependency review.

---

## Task 1: Identity And Tenant Context

- [x] Add public principal, tenant, workspace, and project context types.
- [x] Add context helpers with unexported keys.
- [x] Add tests for context propagation and missing identity behavior.

## Task 2: API Key Middleware

- [x] Add API key authenticator port.
- [x] Add static/in-memory authenticator for tests and small deployments.
- [x] Add HTTP middleware that returns consistent 401 responses.
- [x] Ensure secrets are never logged.

## Task 3: Authorization Policy

- [x] Add action/resource policy port.
- [x] Add role-based allowlist implementation.
- [x] Enforce debug HTTP run submit/read and HITL resume checks when authentication/policy are configured.
- [x] Enforce runtime tool invocation checks.
- [x] Enforce run cancel checks in the async HTTP cancel API.
- [x] Add denied-action tests.

## Task 4: Audit Sink

- [x] Add audit event model.
- [x] Add no-op, in-memory, and file audit sinks.
- [x] Emit audit events for run submit, HITL decisions, tool invocation, and policy denial.
- [x] Add defensive-copy tests for mutable audit fields.

## Task 5: Observability Ports

- [x] Add structured runtime operation event port.
- [x] Add `slog` implementation.
- [ ] Define metric names and labels before adding Prometheus dependency.
- [ ] Define span names before adding OpenTelemetry dependency.

## Task 6: Governance Policies

- [x] Add budget policy interface.
- [x] Add tool side-effect policy interface.
- [x] Add output redaction interface.
- [x] Wire policy checks into tool invocation and result persistence.

## Verification

- [x] `gofmt -w .`
- [x] `CGO_ENABLED=0 go test -ldflags="-w" ./...`
- [x] `CGO_ENABLED=0 go vet ./...`
- [x] `make build`