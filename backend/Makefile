.DEFAULT_GOAL := help
SHELL := /bin/bash

VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build all binaries into ./bin
	go build -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

# The race detector needs cgo and a 64-bit C toolchain. That is reliably
# present on the Linux CI runner but often not on a Windows dev machine, so it
# is opt-in locally and always on in CI.
RACE ?= -race

.PHONY: test
test: ## Run unit tests (RACE= to disable the race detector)
	go test $(RACE) -count=1 ./...

.PHONY: test-db
test-db: ## Run tests needing PostgreSQL (set RAWSYST_DB_DSN)
	go test $(RACE) -count=1 -tags=integration ./...

.PHONY: cover
cover: ## Run tests with a coverage report
	go test $(RACE) -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: vet
vet: ## Run go vet, including build-tagged files
	go vet ./...
	@# Integration tests live behind a build tag, so a plain vet never compiles
	@# them. A rename that misses a tagged file then reaches CI as a test
	@# failure instead of a vet error, which is slower and harder to read.
	go vet -tags=integration ./...

.PHONY: fmt
fmt: ## Format all Go source
	gofmt -s -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file is unformatted
	@out=$$(gofmt -s -l .); \
	if [ -n "$$out" ]; then echo "unformatted files:"; echo "$$out"; exit 1; fi

.PHONY: migrate
migrate: ## Apply pending database migrations
	go run ./cmd/migrate

.PHONY: lint-wording
lint-wording: ## Fail on compliance claims we must never make
	@# Blueprint A1: the software SUPPORTS compliance, it never guarantees it.
	@# Saying otherwise is a legal exposure, not a style preference.
	@# A plain grep flags the docs that TEACH the rule, so this is a real check
	@# that separates product output from documentation stating the prohibition.
	@go run ./cmd/lintwording ..

.PHONY: check
check: fmt-check vet lint-wording test ## Everything CI runs

.PHONY: clean
clean: ## Remove build output
	rm -rf bin coverage.out
