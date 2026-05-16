# Enterprise Runtime Design

## Goal

Build agentflow-go into an enterprise-grade foundation for company agent platforms while preserving its current library-first API and scenario-driven architecture.

## Scope

This design covers P0-P2 enterprise capabilities across persistence, async execution, authentication, tenancy, observability, governance, provider maturity, RAG, skill/tool packaging, and deployment templates.

## Architecture

Enterprise capabilities will be added as ports and adapters around the existing runtime:

- Core contracts remain in `pkg/`.
- Production adapters live under `internal/adapter/` and are exposed through root facade constructors when they are safe public extension points.
- Runtime behavior remains scenario-driven; production configuration adds durable infrastructure and policy around the same YAML model.
- HTTP services become a deployable control plane over the same framework facade.

## Milestones

### M1: Production Persistence and Recovery

Add database/object-store persistence while preserving `runstate.Repository` and `runstate.BlobStore` contracts. This milestone focuses on correctness under concurrency, restart recovery, and clear failure behavior.

### M2: Async Runtime and Workers

Add job queues and workers so runs can execute outside the request path. The existing synchronous facade remains the local and embedding API, while the worker runtime handles production queues.

### M3: Enterprise Auth, Tenancy, and RBAC

Add authenticated request context, tenant scoping, and role checks. Tenancy must flow through run state, memory, blob storage, events, audit, and HTTP APIs.

### M4: Observability, Audit, and Governance

Add standardized logs, traces, metrics, audit events, redaction, and policy hooks. Governance should apply before tool execution, during HITL decisions, and before sensitive outputs are persisted or emitted.

### M5: Provider Matrix, RAG, and Knowledge

Stabilize provider capabilities and add knowledge retrieval as first-class runtime infrastructure. RAG should be tenant-scoped and source-traceable.

### M6: Skill/Tool Ecosystem and Deployment Templates

Package reusable enterprise capabilities and provide deployment assets for local and Kubernetes environments.

## Data Flow

1. A user, service, or scheduler submits a run through the library facade, CLI, HTTP API, or async queue.
2. The runtime loads scenario configuration, tenant context, policy context, memory, and persisted run state when resuming.
3. Agent, workflow, tool, LLM, HITL, and memory steps emit events, logs, traces, metrics, and audit records.
4. Step outputs are persisted inline or externalized to BlobStore based on configured thresholds.
5. Pause, resume, completion, failure, and cancellation states are saved through a CAS-safe repository.

## Error Handling

- Storage adapters must distinguish not found, stale version, validation failure, and infrastructure errors.
- Queue workers must record retryable versus terminal failures.
- Auth middleware must return consistent unauthenticated and unauthorized errors.
- Provider adapters must report unsupported capabilities explicitly.
- Redaction must fail closed for known secret fields.

## Testing Strategy

- Unit tests for contract behavior and edge cases.
- Integration tests behind build tags for PostgreSQL, Redis, object storage, provider calls, and queue workers.
- Race tests for concurrent CAS and in-memory adapters.
- Golden or example tests for YAML scenario compatibility.
- End-to-end tests for async submit/resume/cancel flows.

## Non-Goals For The First Slice

- Replacing the existing synchronous facade.
- Introducing an ORM.
- Making provider-specific APIs part of core runtime contracts.
- Building a UI beyond the existing debug console.

## First Slice

Implement a PostgreSQL-compatible run state adapter without adding a database driver dependency. The adapter should accept a caller-provided `*sql.DB`, use parameterized SQL, preserve CAS semantics, and expose a root constructor.