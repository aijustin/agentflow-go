.PHONY: test test-integration test-race test-realmodel vet lint security fmt validate-examples release-check

GO_TEST_LDFLAGS ?= -w

fmt:
	gofmt -w .

test:
	CGO_ENABLED=0 go test -ldflags="$(GO_TEST_LDFLAGS)" ./...

test-integration:
	CGO_ENABLED=0 go test -ldflags="$(GO_TEST_LDFLAGS)" -tags=integration ./...

test-race:
	go test -race ./internal/adapter/memory/inmem ./internal/adapter/runstate/inmem ./internal/adapter/blob/inmem

test-realmodel:
	CGO_ENABLED=0 go test -ldflags="$(GO_TEST_LDFLAGS)" -tags=realmodel -run TestRealModel -v .

vet:
	CGO_ENABLED=0 go vet ./...

lint:
	golangci-lint run ./...

security:
	govulncheck ./...

validate-examples:
	@for file in examples/*.yaml; do \
		echo "validating $$file"; \
		go run ./examples/go/validate "$$file" >/dev/null; \
	done

release-check: fmt test vet security validate-examples
