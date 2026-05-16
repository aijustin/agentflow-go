# API Stability and Migration Policy

This project is pre-v1. The goal is to keep the first public releases useful and predictable while leaving enough room to adjust early APIs from real user feedback.

## Stability Surface

The intended public surface is:

- The root package `github.com/aijustin/agentflow-go`.
- Packages under `pkg/`.
- YAML scenario fields documented in examples and README.
- CLI commands under `cmd/agentctl` for validation, run, resume, and version.
- Production HTTP behavior documented under `docs/`.

The following are not stable public APIs:

- Packages under `internal/`.
- Concrete adapter internals, test drivers, and helper functions.
- Debug UI implementation details.
- Deployment templates before they are explicitly marked production-stable.

## v0 Compatibility Rules

During v0, the project follows these rules:

- Additive changes to public structs, options, scenario fields, and constructors are allowed in minor releases.
- Breaking changes must be called out in `CHANGELOG.md` with a migration note.
- Public constructors should prefer new option/config fields over changing existing behavior.
- Public interfaces in `pkg/` should change only when the existing contract blocks a major enterprise use case.
- Scenario YAML fields should keep backward-compatible defaults whenever possible.
- `internal/` packages may change without migration guarantees.

## Deprecation Policy

When a public API needs replacement:

- Keep the old API for at least one minor release when practical.
- Mark the old API as deprecated in Go doc comments.
- Add the replacement path to `CHANGELOG.md`.
- Update README examples to use the new API.

## Migration Notes

Every breaking change should include:

- What changed.
- Why it changed.
- Who is affected.
- Before and after examples when the migration is not mechanical.
- Any data migration or scenario YAML migration steps.

## Driver and Adapter Policy

The framework avoids forcing concrete infrastructure dependencies through the root module. Host applications provide their own database drivers, object storage credentials, HTTP clients, LLM gateways, and enterprise integrations, then pass stable interfaces or root facade configs into agentflow-go.

This keeps the core module small while allowing teams to standardize adapters internally.