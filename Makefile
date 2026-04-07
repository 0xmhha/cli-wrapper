GO := go
PKG := ./...

.PHONY: test
test:
	$(GO) test -race -count=1 $(PKG)

.PHONY: test-short
test-short:
	$(GO) test -short -count=1 $(PKG)

.PHONY: vet
vet:
	$(GO) vet $(PKG)

.PHONY: fmt
fmt:
	$(GO) fmt $(PKG)

.PHONY: lint
lint: fmt vet

.PHONY: cover
cover:
	$(GO) test -race -coverprofile=coverage.txt -covermode=atomic $(PKG)
	$(GO) tool cover -func=coverage.txt | tail -n 1

.PHONY: fixtures
fixtures:
	mkdir -p test/fixtures/bin
	$(GO) build -o test/fixtures/bin/fixture-echo   ./test/fixtures/echo
	$(GO) build -o test/fixtures/bin/fixture-noisy  ./test/fixtures/noisy
	$(GO) build -o test/fixtures/bin/fixture-crasher ./test/fixtures/crasher
	$(GO) build -o test/fixtures/bin/fixture-hanger ./test/fixtures/hanger
