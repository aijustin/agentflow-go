# Contributing

## Prerequisites

- Go 1.25.12+
- `make`

## Setup

```sh
git clone <repo-url>
cd agentflow-go
go mod download
```

## Development workflow

Format code:

```sh
make fmt
```

Run unit tests:

```sh
make test
```

Run integration tests:

```sh
make test-integration
```

Run the full test suite with the race detector and shuffled test order (mirrors the CI unit-test job exactly):

```sh
make test-race
```

Run static and security checks:

```sh
make vet
make lint
make security
```

Run the release-oriented check before tagging or opening release PRs:

```sh
GOTOOLCHAIN=auto make release-check
```

See [docs/release-checklist.md](docs/release-checklist.md) for the full checklist.

## Testing expectations

- Add table-driven tests for validation and error paths.
- Add integration tests with `//go:build integration` when a change wires multiple packages together.
- Keep external network calls mocked with `httptest` or mock adapters.
- Run `make test` and `make vet` before submitting changes.
- Run `make test-race` before pushing changes that touch concurrency or shared state. Plain `make test` skips the race detector, while CI runs the same `go test -race -shuffle=on ./...` and fails on data races.

## Public API changes

The public API lives under the root package and `pkg/`. See [docs/api-stability.md](docs/api-stability.md) for the v0 compatibility policy. Changes to exported types or interfaces should include:

- README or godoc updates.
- Tests covering the new behavior.
- CHANGELOG entry with migration notes if the change is breaking.

## Pull request checklist

- [ ] Code is formatted with `gofmt`.
- [ ] Unit tests pass.
- [ ] Integration tests pass when relevant.
- [ ] `make vet` passes.
- [ ] `make lint` passes when `golangci-lint` is installed.
- [ ] `make security` passes before release-oriented changes.
- [ ] Documentation is updated for user-facing behavior.
