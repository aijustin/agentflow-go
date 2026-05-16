# Observability, Audit, and Governance Design

Enterprise agent runtimes need always-on signals that explain what happened, who caused it, what it cost, and why a decision was allowed or denied.

## Signals

### Logs

Use `log/slog` for structured logs. Runtime event and audit `slog` sinks are available through `agentflow.NewSlogEventSink` and `agentflow.NewSlogAuditSink`. Standard fields:

- `run_id`
- `job_id`
- `tenant_id`
- `workspace_id`
- `project_id`
- `agent`
- `tool`
- `workflow_node`
- `event_type`
- `trace_id`

Do not log prompt bodies, tool credentials, provider API keys, HITL tokens, or raw structured outputs unless a redaction policy explicitly allows it.

### Metrics

Initial Prometheus metrics should cover:

- Run count by status.
- Run duration histogram.
- Workflow step duration histogram.
- LLM request count, latency, token usage, and error count.
- Tool request count, latency, side-effect level, and error count.
- Queue depth, lease recovery count, retry count, and dead-letter count.
- HITL pause count and decision count.

Keep labels bounded. Use route patterns and enum values, not user IDs or raw prompts.

### Traces

OpenTelemetry spans should wrap:

- HTTP request handling.
- Queue enqueue, lease, complete, fail, and cancel operations.
- Runtime run execution.
- Workflow node execution.
- LLM calls.
- Tool calls.
- Memory reads/writes.
- RunState and BlobStore operations.

### Audit

Audit events answer compliance questions:

- Who submitted a run?
- Who approved, rejected, or amended a HITL checkpoint?
- Which tools were invoked and with what side-effect classification?
- Which policy denied an action?
- Which tenant/workspace/project owned the action?

Audit events should be durable, append-only, and redacted.

## Governance Policies

Initial policies:

- Maximum cost per run.
- Maximum LLM calls per run.
- Maximum tool calls per run. Implemented as `governance.NewToolBudgetPolicy`.
- Tool side-effect approvals. Implemented as `governance.NewMaxSideEffectPolicy` and wired through `agentflow.WithToolGovernancePolicy`.
- Tenant data boundary enforcement.
- Output redaction before persistence or external delivery. Implemented for persisted runtime step outputs through `governance.NewJSONFieldRedactor` and `agentflow.WithOutputRedactor`.
- Provider capability fallback rules.

Policy checks should emit both observability events and audit records.

## First Implementation Slice

1. Add an observability port that accepts runtime operation events. Done through `core.EventSink`.
2. Add a no-op implementation and a `slog` implementation. Done through `NewSlogEventSink`.
3. Add an audit sink port and in-memory/file adapters. Done in `pkg/audit`, `NewInMemoryAuditSink`, and `NewFileAuditSink`.
4. Add policy interfaces for budget, tool side effects, and output redaction. Done in `pkg/governance`; runtime tool governance and persisted output redaction are wired.
5. Add metrics/tracing behind optional dependencies or integration packages after dependency review.