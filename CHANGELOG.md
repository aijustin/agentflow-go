# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- `Framework.ResumeAndContinue` for continuing paused autonomous, workflow, and tool-approval runs after HITL approval.
- Tool approval policy `pause` for pausing before risky tool execution and resuming with `ResumeAndContinue`.
- Workflow nodes `parallel_group` and `loop` for multi-agent parallelism and bounded iteration.
- Built-in Git and ticket tool executors (`NewGitToolExecutor`, `NewTicketToolExecutor`).
- Planning pass execution tracking during autonomous runs.
- Event routing via `scenario.triggers`, `Framework.HandleEvent`, `NewWebhookHTTPHandler`, and `agentctl trigger`.
- Production HTTP handler sync routes `POST /v1/events` and `POST /v1/hitl/resume` when `Framework` is configured.
- Async job types `event` and `resume.continue` with HTTP enqueue routes `POST /v1/jobs/events` and `POST /v1/jobs/hitl/resume`.
- `NewFrameworkJobHandler` composite worker handler (`NewFrameworkRunJobHandler` alias).
- `agentctl resume --continue` and HTTP HITL `continue: true` for `ResumeAndContinue`.
- Example scenarios: `ticket_handling.yaml`, `code_review_pipeline.yaml`, `multi_expert_research.yaml`.

## [0.1.0] - 2026-05-17

### Added

- Public packages for core agent concepts, LLM gateway contracts, memory, and run state.
- Root facade package for using the project as an importable Go framework.
- Root facade constructors for OpenAI-compatible, Anthropic, local, and routed LLM gateways.
- YAML scenario loader and validator.
- In-memory memory, run-state, and blob adapters.
- CLI commands for `validate`, `run`, `resume`, and `version`.
- Durable CLI run/resume state through `--state-dir`, plus expiring HITL tokens with `--token-ttl`.
- HTTP/Webhook resume handler for human-in-the-loop flows.
- Safer debug HTTP startup defaults: non-loopback listeners require `AGENT_TOKEN_SECRET`.
- Browser debug console for running built-in scenarios, editing YAML, observing event timelines, inspecting run state, and testing real local-model calls.
- Open-source project scaffolding: Apache-2.0 license, GitHub Actions CI, golangci-lint config, govulncheck/CodeQL security workflows, Dependabot, GoReleaser config, Dockerfile, SECURITY.md, and CODE_OF_CONDUCT.md.
- Context governance package and runtime wiring for sliding-window trimming, summary compression, and `ContextPrepared` observability events.
- Richer LLM profile configuration, including context window size, output budget, temperature, top-p, thinking budget, reasoning effort, timeouts, and provider-specific `extra_body` fields.
- Autonomous tool-calling runtime loop with agent tool whitelist checks, approval-policy denial, tool dispatch, tool-result feedback to the LLM, `max_steps` enforcement, and LLM/tool lifecycle events.
- Lazy tool resolution through `core.ToolResolver` and root `WithToolResolver`, allowing heavy or tenant-scoped tool executors to be bound only after allowlist, approval, RBAC, governance, and rate-cap checks pass.
- OpenAI-compatible function-calling request/response support plus router/mock propagation for `ToolCaller`.
- Runtime memory integration for agent-bound conversation/session history, including memory injection before context preparation and writes for user prompts, assistant answers, and tool observations.
- Fixed-workflow scheduler for graph dependencies, `depends_on`, edge conditions, bounded parallel batches, retry policy, transform nodes, agent nodes, human-gate nodes, and CAS-safe parallel step output persistence.
- Root facade execution for `fixed_workflow` scenarios.
- Workflow-level HITL pause/resume support: human-gate nodes persist `CurrentNodeID`/`PendingGate`, return a typed pause error with token, and resume downstream graph execution after approval.
- Multi-agent delegation baseline: `sub_agents` are exposed as virtual delegation tools in the autonomous loop, supervisor agents can call sub-agents, and delegated outputs are persisted and fed back to the supervisor.
- Skill workflow expansion during scenario build: skill prompt fragments merge into agent instructions and skill workflow nodes/edges are namespaced into the parent workflow.
- File-backed durable adapters for RunStateRepository, BlobStore, and MemoryRepository, exposed through the root facade.
- PostgreSQL-compatible RunStateRepository exposed through the root facade without forcing a specific database driver dependency.
- S3-compatible BlobStore exposed through the root facade with standard-library AWS Signature Version 4 signing.
- Redis-backed distributed lease adapter exposed through the root facade for worker and workflow coordination.
- Async job queue and worker contracts with an in-memory queue adapter for local development and tests.
- Framework run job payload and worker handler for executing queued `Framework.Run` jobs.
- PostgreSQL async job queue adapter exposed through `NewPostgresJobQueue`.
- Production HTTP handler with `/healthz`, `/readyz`, and async `/v1/runs` submit/status/cancel routing.
- Enterprise identity context, API key middleware, and RBAC policy contracts.
- Audit event model plus noop, in-memory, and JSONL file audit sinks.
- Structured `slog` runtime event and audit sinks.
- Governance package with tool budget, tool side-effect policy, policy chaining, and JSON field output redaction.
- Authorization middleware and framework security/audit options for runtime tool invocation enforcement.
- Async run HTTP submit/status/cancel handler with RBAC and audit wiring.
- Optional API key protection for `agent-http`; non-loopback listeners now require `AGENT_HTTP_API_KEY` in addition to `AGENT_TOKEN_SECRET`.
- Stricter scenario validation for memory type/scope, tool approval/side-effect policies, workflow `depends_on`, node kinds, and node references.
- Production hardening baseline: runtime/agent/profile timeouts, context-aware LLM and tool retries, per-run tool rate caps, failed-run status persistence, and BlobStore externalization for large runtime/workflow outputs.
- Structured output and streaming runtime paths, exposed through the root facade and wired through mock/router/OpenAI-compatible adapters.
- Provider capability helpers for parsing and checking explicit profile capabilities.
- MCP public client contracts plus HTTP JSON-RPC client and MCP tool executor adapters.
- Knowledge/RAG foundation with public vector-store contracts, OpenAI-compatible embedding support, mock embedding queues, pgvector baseline adapter, and retriever tool.
- Knowledge ingestion pipeline with filesystem and HTTP loading, text chunking, batch embedding/upsert indexing, and explicit retriever citations.
- Root facade constructors for MCP clients/executors, OpenAI-compatible chat+embedding providers, retriever tools, and PostgreSQL vector stores.
- Local enterprise deployment template with PostgreSQL+pgvector, Redis, MinIO, agent-http, bootstrap SQL, reusable PostgreSQL migrations, and an initial Kustomize base.
- Constrained built-in HTTP, filesystem read, and SQL query tool executors with allowlists, size/row limits, timeouts, root facade constructors, and SQL validator compatibility for PostgreSQL, MySQL, and ClickHouse-style read-only queries.
- Release validation target and v0 API stability policy, including public surface, migration notes, and release checklist documentation.
- Public module, documentation, import examples, and deployment image references now target `github.com/aijustin/agentflow-go`.
- MCP and RAG example scenarios plus documentation for MCP tools, knowledge retrieval, and pgvector persistence.
- LLM adapters for OpenAI-compatible APIs, Anthropic, local OpenAI-compatible endpoints, and mock testing.
- Unit and integration tests covering configuration, runtime, HITL, run state, memory, LLM routing, and workflow execution.
- Environment-driven `realmodel` integration tests for OpenAI-compatible local model endpoints, including long-context governance with a real model.

### Fixed

- Fixed-workflow `agent` nodes now execute through the runtime-backed agent path instead of saving a dummy completion output, so LLM calls, memory, tools, and observability events are preserved.

### Known limitations

- Autonomous planning beyond tool-calling loops is still incomplete.
- Redis run-state storage adapter beyond lease coordination is still pending.
- Prometheus metrics and OpenTelemetry tracing adapters are still pending.
- Specialized ingestion connectors, Helm chart packaging, and additional built-in enterprise tool packages for chatops integrations are still pending.
