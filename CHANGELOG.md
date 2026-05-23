# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).

## [Unreleased]

### Added

- Editor **live run preview**: Studio Run stays on Editor with done/current node highlighting during SSE updates.
- Inspector **trace/span tree** with optional external trace link (`TraceExploreURL` / `GET /api/ui-config`).
- Event `parent_span_id` propagation for nested OTel spans.

## [0.2.1] - 2026-05-22

### Added

- Observability Graph **node inspector**: step output, related workflow events, Timeline ↔ Graph `node_id` linkage.
- Checkpoint **timeline scrub**, revision step diff, and **fork from checkpoint** in Time Travel bar.
- **Autonomous trace** panel under Graph for hybrid/autonomous LLM and tool events (P9.2).
- Workflow events from agent/tool nodes include **`node_id`** in payload for Timeline ↔ Graph linkage (P9.2).
- Builder workflow DSL: `StepPath`, `ConditionEq/Ne/Exists/Missing`, `MapOver`, `Map*Branch`, `RouteIf` (P8).
- Studio **P10**: live Graph refresh during runs, subgraph drill-down, inspector event payloads, compare step output diff.
- Builder DSL sugar: `ParallelGroup`, `ParallelTools`, `RouteIfNe/Exists/Missing`; codegen emits `MapOver`/`RouteIf` when possible.
- Helm reference chart: production defaults (probes, resources, securityContext).
- Cross-process run-state integration test.

## [0.2.0] - 2026-05-22

### Removed

- Public YAML scenario loaders: `LoadScenarioFile`, `LoadScenario`, `NewFromFile`. Use `pkg/builder` or `core.Scenario`; Studio import/export APIs remain.
- `examples/go/validate -kind scenario` and `testutil.ScenarioWorkDir`.

### Changed

- **Breaking:** builder-first embed path; scenario YAML is Studio interchange only. Migration: replace `NewFromFile` / `LoadScenarioFile` with `builder.*` stacks and `agentflow.New(scenario, opts...)`. See [docs/library-integration.md](docs/library-integration.md#migrating-from-v01-yaml-loaders).
- README YAML sections condensed; field details live in `docs/configuration-reference.md`.
- `docs/superpowers/plans/*` marked as historical implementation records.

### Fixed

- `ImportStudioScenarioYAML` nil panic when `layout.Workflow` is omitted.

## [0.1.10] - 2026-05-22

### Added

- `pkg/builder` fluent Go DSL for constructing `core.Scenario` without YAML; 18 stack functions aligned with `examples/*.yaml`.
- Root package re-exports for common builder stacks and workflows (`builder.go`).
- `examples/go/validate -kind builder` and `make validate-builder` for catalog validation in CI (`release-check`).
- [docs/builder-reference.md](docs/builder-reference.md) and Go DSL section in [docs/manual.html](docs/manual.html).

### Fixed

- golangci-lint: gofmt on `pkg/builder`, remove unused dead code, fix ineffectual stream assignments in LLM gateways.

## [0.1.9] - 2026-05-22

### Added

- Memory tier runtime, cognitive recall, tier-worker reference deploy, and related documentation.

### Changed

- `release-check` runs govulncheck via `go run`; OpenTelemetry bumped to v1.40.0.

## Earlier v0.1.x (aggregated)

### Changed

- **Library-only positioning:** removed `cmd/agentctl`, `cmd/agent-http`, `cmd/agent-server`, `cmd/agent-worker`, debug UI, and `deploy/` templates. Integrate via `go get` and `examples/go/*`.
- Moved test/example wiring to `pkg/testutil` (`WiringOptions`). Removed `DemoOptions`, `DevelopmentOptions`, and env-based `NewProduction*` helpers from the root package.
- PostgreSQL migrations live under `migrations/postgres/` (was `deploy/migrations/postgres/`).
- Release workflow runs `make release-check` only (no binary artifacts).

### Added

- Memory tier design spec and Phase 1 `pkg/memory/tier` contract (hot/warm/cold policy, recall budget). See `docs/superpowers/specs/2026-05-21-memory-tier-design.md`.
- Memory tier Phase 2: YAML `memories.*.tiers`, `TierManager` Remember/Recall/Reconcile, in-memory tier store, `WithTierMemory` / `WithTierStore`, runtime tier read/write path, migration event types, and `examples/tier_memory.yaml`.
- Memory tier Phase 3: Postgres warm store, gzip file cold store, `CompositeStore`, `WithJobQueue` + `memory.reconcile` async job, tier Prometheus metrics and OTel spans, tenant-scoped memory namespaces.
- Memory tier Phase 4: cognitive/tier unified recall (`RankMemories` + tier budget), `DualWriteManager`, `CognitiveAdapter`, `WithCognitiveMemory`, runtime query-aware tier recall.
- M6 reference deploy: `examples/go/tier-worker`, migration init script, Kubernetes and Helm skeletons, `memory.reconcile` via shared Postgres/in-memory job queue.
- `examples/go/validate` for scenario YAML wiring checks in CI and local dev; supports `-kind tool|skill` for catalog manifests.
- `NewPrometheusRecorder` and `PrometheusMetricsHandler` with `/metrics` mounting on `NewProductionHTTPHandler`.
- OpenTelemetry adapter in `pkg/observability/otel`: `NewOpenTelemetryTracer`, `NewOpenTelemetryStdoutTracerProvider`, runtime `Run`/`ToolCall` spans.
- Tool/Skill catalog manifest loaders: `LoadToolManifestFile`, `LoadSkillManifestFile`, `ValidateToolManifest`, `ValidateSkillManifest`.
- Reference local stack under `examples/deploy/` (PostgreSQL, Redis, MinIO Compose).
- ADR documenting library-first integration: [docs/adr/001-library-first.md](docs/adr/001-library-first.md).
- `NewMockLLMGateway` remains on the root package; demo tool wiring is in `pkg/testutil`.
- Workflow dynamic edge condition routing at runtime (`edges[].condition`).
- Workflow node input templates: `copy_from` and `prompt_from` in node `input`.
- `runstate.HydrateStepContext` for hybrid Phase 2 workflow context hydration.
- Context window `compression.trigger_ratio` pre-compression for tool messages.
- `memory_recall_limit` enforcement when replaying session memory into LLM context.
- Workflow step output redaction via `WithOutputRedactor` on `WorkflowRunner`.
- Memory write redaction using the configured `OutputRedactor`.
- `Framework.ResumeAndContinue` for continuing paused autonomous, workflow, and tool-approval runs after HITL approval.
- Tool approval policy `pause` for pausing before risky tool execution and resuming with `ResumeAndContinue`.
- Workflow nodes `parallel_group` and `loop` for multi-agent parallelism and bounded iteration.
- Built-in Git and ticket tool executors (`NewGitToolExecutor`, `NewTicketToolExecutor`).
- Planning pass execution tracking during autonomous runs.
- Event routing via `scenario.triggers`, `Framework.HandleEvent`, and `NewWebhookHTTPHandler`. Use `examples/go/event-trigger` or host HTTP `POST /v1/events` instead of removed `agentctl trigger`.
- Production HTTP handler sync routes `POST /v1/events` and `POST /v1/hitl/resume` when `Framework` is configured.
- Async job types `event` and `resume.continue` with HTTP enqueue routes `POST /v1/jobs/events` and `POST /v1/jobs/hitl/resume`.
- `NewFrameworkJobHandler` composite worker handler.
- HTTP HITL `continue: true` for `ResumeAndContinue`. Use `examples/go/hitl-resume` or `POST /v1/hitl/resume` instead of removed `agentctl resume --continue`.
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
