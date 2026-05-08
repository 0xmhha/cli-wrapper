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

.PHONY: bench
bench:
	$(GO) test -run=^$$ -bench=. -benchtime=2s ./test/bench/...

# bench-pty includes PTY-mode benchmarks (gated by //go:build bench because
# they spawn real agents and are slower). Use this for the full suite.
.PHONY: bench-pty
bench-pty: fixtures
	$(GO) test -run=^$$ -bench=. -benchtime=2s -tags=bench ./test/bench/...

# bench-baseline writes the current benchmark output to bench-baseline.txt.
# Run this on a known-good commit (typically the merge base of a PR or
# main tip), then run `make bench-current` + `make bench-compare` to diff.
.PHONY: bench-baseline
bench-baseline: fixtures
	@echo "Saving baseline to bench-baseline.txt..."
	$(GO) test -run=^$$ -bench=. -benchtime=2s -count=10 -tags=bench ./test/bench/... | tee bench-baseline.txt

# bench-current runs the current tree's benchmarks and writes bench-current.txt.
.PHONY: bench-current
bench-current: fixtures
	@echo "Saving current run to bench-current.txt..."
	$(GO) test -run=^$$ -bench=. -benchtime=2s -count=10 -tags=bench ./test/bench/... | tee bench-current.txt

# bench-compare diffs bench-current.txt vs bench-baseline.txt with benchstat.
# Install benchstat first via `make bench-tools` if not already on PATH.
.PHONY: bench-compare
bench-compare:
	@if ! command -v benchstat >/dev/null 2>&1; then \
		echo "benchstat not on PATH; run 'make bench-tools' first."; exit 1; \
	fi
	@if [ ! -f bench-baseline.txt ]; then \
		echo "bench-baseline.txt missing; run 'make bench-baseline' first."; exit 1; \
	fi
	@if [ ! -f bench-current.txt ]; then \
		echo "bench-current.txt missing; run 'make bench-current' first."; exit 1; \
	fi
	benchstat bench-baseline.txt bench-current.txt

# bench-tools installs benchstat into $GOBIN.
.PHONY: bench-tools
bench-tools:
	$(GO) install golang.org/x/perf/cmd/benchstat@latest

.PHONY: integration
integration: fixtures
	$(GO) test -count=1 -race -v ./test/integration/...

.PHONY: chaos
chaos:
	$(GO) test -count=1 -race -v ./test/chaos/...

.PHONY: fuzz-short
fuzz-short:
	$(GO) test -run=^$$ -fuzz=FuzzFrameReader -fuzztime=30s ./internal/ipc/...
