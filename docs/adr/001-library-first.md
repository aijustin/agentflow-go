# ADR 001: Library-first integration model

- Status: accepted
- Date: 2026-05-21

## Context

agentflow-go began with first-party binaries (`agentctl`, `agent-http`, `agent-worker`), deployment templates (`deploy/`, Helm, Docker), and environment-driven constructors (`NewProduction*`). This created two problems:

1. Host teams already run Go services and need to embed Agent capabilities, not adopt a separate runtime product.
2. Shipping opinionated deployment artifacts implied operational ownership the project cannot maintain across every enterprise environment.

## Decision

Position agentflow-go as a **library-first** framework:

- Public surface: root facade + `pkg/*` ports + documented YAML scenario fields.
- Integration path: `go get`, explicit `With*` wiring, mount HTTP handlers on the host `http.Server`.
- Reference material lives under `examples/go/` and `examples/deploy/` as copyable templates, not supported products.
- PostgreSQL migrations ship as SQL files; the host applies them with its own migration toolchain.
- Releases validate via `make release-check` and publish Go module tags — no official binary artifacts.

## Consequences

### Positive

- Clear ownership boundary: host owns topology, secrets, scaling, and observability export wiring.
- Minimal root-module dependencies; infrastructure drivers are injected by the host.
- Easier adoption inside existing Go microservices and monoliths.

### Negative

- No single-command “production install” — teams copy examples and adapt.
- CLI workflows (`validate`, `resume`, `trigger`) are example programs, not a guaranteed-stable CLI.
- Documentation must stay aligned with the library model to avoid confusion.

## Alternatives considered

1. **CLI-first product** — rejected; duplicates host HTTP servers and deployment stacks.
2. **Hybrid (library + official daemon)** — rejected for v0; doubles maintenance and blurs support boundaries.
3. **SaaS-only** — out of scope for an open embeddable framework.

## Follow-up

- Provide reference Compose/Kustomize under `examples/deploy/`.
- Expose Prometheus metrics via `NewPrometheusRecorder` and `/metrics` on production HTTP handlers.
- Tool/Skill catalog manifests with `examples/go/validate -kind tool|skill`.
- v1 API freeze after host feedback window (see `docs/api-stability.md`).
