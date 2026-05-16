# Enterprise Roadmap

This roadmap turns agentflow-go from an embeddable agent framework into an enterprise-ready foundation for internal agent platforms.

## Execution Principles

- Build the runtime foundation before expanding the ecosystem.
- Keep every milestone independently testable and releasable.
- Prefer ports and adapters over provider-specific coupling.
- Keep local development simple while adding production-grade backends.
- Treat security, observability, and auditability as runtime features, not deployment afterthoughts.

## Milestone Order

### M1: Production Persistence and Recovery

Goal: runs, workflow checkpoints, step outputs, and large blobs survive restarts and multi-instance deployment.

Deliverables:

- PostgreSQL `RunStateRepository` adapter.
- S3-compatible `BlobStore` adapter for MinIO, AWS S3 path-style endpoints, and private object stores. Implemented as a standard-library SigV4 adapter.
- Redis-based lease adapter for distributed coordination. Implemented with `SET NX PX` and atomic Lua renew/release.
- Retention and cleanup APIs for expired snapshots, completed runs, and orphaned blobs.
- Integration tests for cross-process resume, CAS conflict handling, and blob checksum validation.

Acceptance criteria:

- A paused run can be resumed by a different process or instance.
- Concurrent resume attempts preserve CAS semantics and only one succeeds.
- Large step outputs can be externalized and retrieved from object storage.
- Storage failures return explicit, typed errors where possible.

### M2: Async Runtime and Workers

Goal: support long-running enterprise workflows through asynchronous job submission and horizontally scalable workers.

Deliverables:

- Job, queue, worker, and lease abstractions. Initial `pkg/async` contracts are implemented.
- In-memory queue for tests and local development. Implemented as `internal/adapter/queue/inmem` and exposed through `NewInMemoryJobQueue`.
- PostgreSQL queue adapter for production baseline.
- HTTP API for submit/status/cancel flows plus production health/readiness wrapper.
- Retry, timeout, dead-letter, and cancellation semantics.

Acceptance criteria:

- HTTP submission returns a `run_id` without blocking on completion.
- Multiple workers do not execute the same leased job at the same time.
- Cancellation propagates through runtime context.
- Failed jobs can be retried and eventually marked dead-lettered.

### M3: Enterprise Auth, Tenancy, and RBAC

Goal: make runtime APIs safe for shared company use.

Design: [security-auth-tenancy.md](security-auth-tenancy.md)

Deliverables:

- Tenant/workspace/project context model.
- API key authentication middleware.
- OIDC/OAuth2 authentication adapter.
- RBAC policy port with roles for admin, operator, viewer, approver, and service principals.
- Tenant-scoped run state, memory, blobs, events, and audit records.

Acceptance criteria:

- Tenant data cannot be loaded or resumed across tenant boundaries.
- Dangerous tools and HITL decisions enforce role checks.
- Approval records include actor identity.
- HTTP APIs return consistent 401/403 responses.

### M4: Observability, Audit, and Governance

Goal: make production behavior diagnosable, measurable, and governable.

Design: [observability-governance.md](observability-governance.md)

Deliverables:

- Structured `slog` fields for run, tenant, agent, tool, step, and trace identifiers.
- OpenTelemetry tracing.
- Prometheus metrics for runtime, LLM, tool, workflow, and queue behavior.
- Audit sink interface and durable audit event adapters.
- Redaction hooks for secrets and sensitive data.
- Policy baseline for budget limits, tool side effects, approval gates, and output checks.

Acceptance criteria:

- A run can be traced from HTTP request through workflow steps, LLM calls, and tools.
- Metrics expose latency, error counts, token usage, and queue depth.
- Audit logs answer who approved, rejected, amended, or invoked risky tools.
- Sensitive values do not appear in logs, events, snapshots, or debug responses.

### M5: Provider Matrix, RAG, and Knowledge

Goal: support enterprise knowledge workflows and predictable model behavior.

Deliverables:

- Provider capability matrix for streaming, tool calls, structured output, embeddings, and usage accounting. Initial capability helpers are implemented.
- Embedding provider port. `llm.Embedder` is implemented by the OpenAI-compatible and mock adapters.
- Vector store port and pgvector baseline adapter. Initial `pkg/knowledge` and PostgreSQL pgvector adapter are implemented.
- Document loader, chunker, indexer, retriever tool, and citation/source tracking. File and HTTP loaders are implemented.
- Retriever tool. Initial semantic retrieval executor is implemented.
- Tenant-isolated knowledge collections.

Acceptance criteria:

- Scenarios can bind agents to knowledge collections through YAML.
- Retrieval results include source metadata and citation identifiers.
- Unsupported provider capabilities fail clearly or route through configured fallbacks.
- Tenant isolation applies to indexed documents and retrieval.

### M6: Skill/Tool Ecosystem and Deployment Templates

Goal: make agentflow-go easy for teams to extend, package, deploy, and maintain.

Deliverables:

- Skill package format, versioning, and compatibility validation.
- Tool package format, schema validation, and side-effect metadata.
- Registry interface for internal skill/tool catalogs.
- Built-in enterprise tools for HTTP, SQL, Git, filesystem, tickets, and chatops. Initial constrained HTTP, filesystem read, and SQL query tool executors are implemented.
- Docker Compose local enterprise stack. Initial PostgreSQL+pgvector, Redis, MinIO, and `agent-http` stack is implemented under `deploy/enterprise`.
- Helm chart and Kubernetes manifests. Initial Kustomize base for `agent-http` is implemented under `deploy/kubernetes/base`.
- Example scenarios for approvals, code review, ticket handling, RAG Q&A, and multi-agent workflows.
- v0 API stability policy and migration guide. Implemented in `docs/api-stability.md` with release validation guidance in `docs/release-checklist.md`.

Acceptance criteria:

- Teams can register new tools and skills without modifying core runtime packages.
- Packages carry version and compatibility metadata.
- A local enterprise stack starts with one command.
- Kubernetes deployment includes runtime, worker, metrics, and health probes.

## Recommended Delivery Sequence

1. M1 persistence and recovery.
2. M2 async runtime and workers.
3. M3 auth, tenancy, and RBAC.
4. M4 observability, audit, and governance.
5. M5 provider matrix and RAG.
6. M6 ecosystem and deployment templates.

## Current Focus

M1-M4 foundations are implemented as library-grade slices: durable run state/blob/memory adapters, async queue/worker execution, enterprise identity/RBAC/audit, structured `slog` sinks, tool governance, output redaction, and production async HTTP routing. M5 now includes provider capability helpers, OpenAI-compatible embeddings, MCP tool adapters, `pkg/knowledge`, file/HTTP document loading, chunking/indexing, pgvector storage, explicit retriever citations, and metadata filtering. M6 has started with a local enterprise Compose stack, production SQL migrations, a Kustomize base, constrained HTTP, filesystem read, and SQL query tool executors, plus v0 API stability and release-check guidance. The next focus is more specialized ingestion connectors, Helm chart packaging, and additional built-in enterprise tools.