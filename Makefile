.PHONY: build test test-integration test-race test-realmodel vet lint security fmt validate-builder validate-catalog release-check

GO_TEST_LDFLAGS ?= -w

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

release-check: fmt test vet security validate-builder validate-catalog
