# Release Checklist

Use this checklist before tagging a public release.

## Required Checks

Run the release check target:

```sh
GOTOOLCHAIN=auto make release-check
```

This target runs formatting, unit tests, `go vet`, binary builds, `govulncheck`, and validation for every example scenario under `examples/`.

## Recommended Manual Checks

Run these when the release touches deployment, persistence, or concurrency behavior:

```sh
GOTOOLCHAIN=auto make test-integration
GOTOOLCHAIN=auto make test-race
docker compose -f deploy/enterprise/docker-compose.yml config
kubectl kustomize deploy/kubernetes/base
```

Run real model tests only when a local or compatible endpoint is intentionally configured:

```sh
GOTOOLCHAIN=auto make test-realmodel
```

## Documentation Checks

- README and README.zh-CN describe new user-facing behavior.
- New examples validate with `agentctl validate`.
- `CHANGELOG.md` includes public API, scenario, CLI, and deployment changes.
- Breaking changes include migration notes.
- Security-sensitive features document safe defaults and required operator responsibilities.

## Public API Checks

- Root facade constructors exist for public adapters that users are expected to wire.
- New stable contracts live under `pkg/`.
- `internal/` adapters are not imported from examples intended for framework consumers.
- Public interfaces avoid concrete provider or infrastructure coupling unless the coupling is the point of the adapter.

## Release Notes

Before tagging, summarize:

- Major features.
- Security or governance changes.
- Migration notes.
- Known limitations.
- Validation evidence from `make release-check`.