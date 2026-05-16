# Security, Auth, and Tenancy Design

This document defines the enterprise control-plane boundary for agentflow-go. The goal is to expose runtime APIs safely across departments, projects, and service accounts without mixing tenant data or bypassing tool approvals.

## Identity Model

The production API should carry an explicit identity context:

| Field | Purpose |
| --- | --- |
| `tenant_id` | Hard isolation boundary for data, run state, memory, blobs, audit, and metrics. |
| `workspace_id` | Team or business unit boundary inside a tenant. |
| `project_id` | Product/application boundary for scenarios and tools. |
| `principal_id` | User or service principal identity. |
| `principal_type` | `user`, `service`, or `system`. |
| `roles` | Bounded role set for authorization. |

Recommended roles:

- `admin`: manage configuration and credentials.
- `operator`: run and cancel workflows.
- `viewer`: inspect run state and outputs.
- `approver`: approve, reject, or amend HITL checkpoints.
- `service`: submit machine-to-machine runs with scoped permissions.

## Authentication Layers

Implement authentication in this order:

1. API key middleware for service-to-service and first production deployments.
2. OIDC/OAuth2 middleware for SSO users.
3. Optional mTLS or private-network enforcement for internal deployments.

Debug UI and production API should remain distinct deployment modes. Debug endpoints may keep local development defaults only on loopback listeners.

## Authorization Policy

Authorization should be a port, not hardcoded into HTTP handlers:

```go
type Policy interface {
    Authorize(ctx context.Context, principal Principal, action Action, resource Resource) error
}
```

Initial actions:

- `run.submit`
- `run.read`
- `run.cancel`
- `hitl.resume`
- `tool.invoke`
- `memory.read`
- `memory.write`
- `admin.configure`

Authorization must run before dangerous tool execution and before HITL decisions are accepted.

## Tenant Isolation

Tenant context must flow through:

- Run snapshots.
- Job queue payload and status.
- Memory namespace.
- Blob references and object prefixes.
- Event sink and audit sink.
- Metrics labels with bounded cardinality.

Tenant IDs should be explicit fields on future persistent records rather than inferred from run IDs.

## Secret Handling

- Secrets are read from trusted configuration or secret managers.
- Secrets never appear in logs, events, snapshots, debug responses, or audit payloads.
- Provider credentials and tool credentials should be scoped by tenant/workspace/project.
- Redaction must fail closed for known secret fields.

## First Implementation Slice

1. Add `pkg/security` or `pkg/identity` principal and tenant context types. Done in `pkg/identity`.
2. Add API-key middleware for HTTP handlers. Done through `NewStaticAPIKeyAuthenticator` and `NewAPIKeyMiddleware`.
3. Add a policy port with an allowlist implementation for tests. Done in `pkg/security`.
4. Add authorization checks around run submit, resume, read, cancel, and tool invocation. Debug HTTP run submit/read/resume, async HTTP submit/status/cancel, and runtime tool invocation are implemented.
5. Add audit events for accepted and denied decisions. Implemented for debug run submit, HITL decisions, runtime tool invocation, and policy denial.