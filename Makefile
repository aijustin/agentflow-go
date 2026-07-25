.PHONY: build test test-integration test-race test-realmodel vet lint security fmt validate-builder validate-catalog release-check verify-release-version studio-ui verify-studio-ui

GO_TEST_LDFLAGS ?= -w

# studio-ui builds the Studio SPA (web/studio) and copies the bundle into the
# observability handler's embedded uistatic directory. The Go build works
# without it (legacy inline UI fallback), so this target is opt-in.
studio-ui:
	cd web/studio && pnpm install --frozen-lockfile && pnpm build
	rm -rf internal/adapter/observability/http/uistatic/assets internal/adapter/observability/http/uistatic/dist internal/adapter/observability/http/uistatic/index.html
	cp -R web/studio/dist/. internal/adapter/observability/http/uistatic/

verify-studio-ui:
	test -f internal/adapter/observability/http/uistatic/index.html
	test -n "$$(ls internal/adapter/observability/http/uistatic/assets/*.js)"

build:
	CGO_ENABLED=0 go build ./...

fmt:
	gofmt -w .

test:
	CGO_ENABLED=0 go test -ldflags="$(GO_TEST_LDFLAGS)" ./...

test-integration:
	CGO_ENABLED=0 go test -ldflags="$(GO_TEST_LDFLAGS)" -tags=integration ./...

# Mirrors the CI unit-test job (.github/workflows/test.yml): full suite with
# the race detector and shuffled test order. Plain `make test` does not run
# the race detector, so run this before pushing concurrency/shared-state
# changes instead of discovering data races in CI.
test-race:
	go test -race -shuffle=on -coverprofile=coverage.out ./...

test-realmodel:
	CGO_ENABLED=0 go test -ldflags="$(GO_TEST_LDFLAGS)" -tags=realmodel -run TestRealModel -v .

vet:
	CGO_ENABLED=0 go vet ./...

lint:
	golangci-lint run ./...

GOVULNCHECK ?= go run golang.org/x/vuln/cmd/govulncheck@latest

security:
	$(GOVULNCHECK) ./...

validate-builder:
	@echo "validating builder CoreCatalog (autonomous)"
	@go test ./pkg/builder/... -count=1
	@go run ./examples/go/validate -kind builder core

validate-catalog:
	@for file in examples/catalog/tools/*.yaml; do \
		echo "validating $$file"; \
		go run ./examples/go/validate -kind tool "$$file" >/dev/null; \
	done
	@for file in examples/catalog/skills/*.yaml; do \
		echo "validating $$file"; \
		go run ./examples/go/validate -kind skill "$$file" >/dev/null; \
	done

verify-release-version:
	@test -n "$(RELEASE_TAG)" || (echo "RELEASE_TAG is required" >&2; exit 2)
	@scripts/check-release-version.sh "$(RELEASE_TAG)"

release-check: fmt test vet security validate-builder validate-catalog verify-studio-ui
