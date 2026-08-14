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

.PHONY: test
test: ## Run unit tests
	go test -race -count=1 ./...

.PHONY: test-db
test-db: ## Run tests that need PostgreSQL (set RAWSYST_TEST_DB_DSN)
	go test -race -count=1 -tags=integration ./...

.PHONY: cover
cover: ## Run tests with a coverage report
	go test -race -count=1 -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1

.PHONY: vet
vet: ## Run go vet
	go vet ./...

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
	@if grep -rniE 'zatca[- ]certified|certified compliant|guaranteed compliant|never at legal risk' \
	     --include='*.go' --include='*.sql' --include='*.md' --include='*.json' \
	     --include='*.ts' --include='*.tsx' . ; then \
	  echo ""; \
	  echo "Forbidden compliance wording found. Use 'supports ZATCA requirements',"; \
	  echo "'built to support ZATCA and PDPL requirements', or 'WPS/Mudad-ready'."; \
	  exit 1; \
	fi
	@echo "wording check passed"

.PHONY: check
check: fmt-check vet lint-wording test ## Everything CI runs

.PHONY: clean
clean: ## Remove build output
	rm -rf bin coverage.out
