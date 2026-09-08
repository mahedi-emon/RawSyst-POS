# RawSyst, from the top.
#
# The backend has its own Makefile with the Go targets; this one is for the
# things that span the whole repository — the front ends, the verification
# scripts, and the disk a working copy quietly consumes.
#
# `make help` lists everything.

.DEFAULT_GOAL := help
SHELL := /bin/bash

# The test database is a SEPARATE database from the development one, and that
# is not a detail. Running the integration suite against `rawsyst_dev` is what
# produced a migration-hash deadlock that blocked every backend test for four
# days: a long-lived database accumulates a history the committed migration
# chain does not have.
TEST_DSN ?= postgres://rawsyst:RawSyst_App_2026%21dev@127.0.0.1:5432/rawsyst_test?sslmode=disable
DEV_DSN  ?= postgres://rawsyst:RawSyst_App_2026%21dev@127.0.0.1:5432/rawsyst_dev?sslmode=disable

.PHONY: help
help: ## Show this help
	@grep -hE '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'

# --- resources ---------------------------------------------------------------

.PHONY: doctor
doctor: ## What this working copy costs on disk, and what is over its threshold
	@bash scripts/maintenance.sh report

.PHONY: resource-check
resource-check: ## Same, but exits non-zero when something is over threshold (for CI)
	@bash scripts/maintenance.sh check

.PHONY: maintenance
maintenance: ## Clean only what is over its threshold
	@bash scripts/maintenance.sh clean

.PHONY: cleanup
cleanup: ## Clean every disposable cache and build artefact
	@bash scripts/maintenance.sh clean --all

# --- verification ------------------------------------------------------------
#
# Staged rather than parallel, deliberately. This machine has 8GB, and running
# the Go suite, the Next build and a database at once is how a build agent gets
# a process killed by the OOM reaper — which reads as a flaky test rather than
# as a machine that ran out of memory.

.PHONY: fresh-db
fresh-db: ## Rebuild the TEST database from the committed migration chain
	@cd backend && RAWSYST_DB_DSN='$(TEST_DSN)' go run -tags=freshcheck ./cmd/freshcheck

.PHONY: fresh-dev
fresh-dev: ## Rebuild and reseed the DEVELOPMENT database
	@cd backend && RAWSYST_DB_DSN='$(DEV_DSN)' go run -tags=freshcheck ./cmd/freshcheck
	@cd backend && set -a && . ./.env && set +a && \
	  go run ./cmd/devseed -password 'DevPassw0rd!2026' -platform-email ops@example.test
	@$(MAKE) --no-print-directory dev-regulatory

.PHONY: dev-regulatory
dev-regulatory: ## Stage development figures for the legal values that block calculations
	@cd backend && set -a && . ./.env && set +a && go run ./cmd/devregulatory

.PHONY: test-backend
test-backend: ## The Go suite against the test database, in two stages
	@cd backend && RAWSYST_DB_DSN='$(TEST_DSN)' \
	  go test -tags=integration -count=1 -timeout=15m \
	    $$(go list ./... | grep -v 'internal/api$$')
	@cd backend && RAWSYST_DB_DSN='$(TEST_DSN)' \
	  go test -tags=integration -count=1 -timeout=15m ./internal/api/

.PHONY: test-web
test-web: ## The front-end and shared suites
	@cd shared   && npx vitest run
	@cd web-next && npx vitest run

.PHONY: typecheck
typecheck: ## TypeScript, both workspaces
	@cd shared   && npx tsc --noEmit
	@cd web-next && npx tsc --noEmit

.PHONY: build-web
build-web: ## Production build of the back office
	@cd web-next && npm run build

.PHONY: contract
contract: ## The generated API contract still matches the router
	@cd web-next && node scripts/generate-api-contract.mjs --check

.PHONY: verify-api
verify-api: ## Screen contracts, against a running API
	@cd web-next && node scripts/verify-against-api.mjs

.PHONY: verify-rbac
verify-rbac: ## Permission boundaries, against a running API
	@cd web-next && node scripts/verify-rbac.mjs

.PHONY: verify
verify: ## Everything that does not need a running server, in memory order
	@$(MAKE) typecheck
	@$(MAKE) test-web
	@$(MAKE) contract
	@$(MAKE) fresh-db
	@$(MAKE) test-backend
	@$(MAKE) build-web
	@echo
	@echo "Static and offline verification passed."
	@echo "The two that need a running API are: make verify-api, make verify-rbac"

# --- docker ------------------------------------------------------------------

.PHONY: images
images: ## Build both production images and report their sizes
	@docker build -f backend/Dockerfile -t rawsyst/backend:local ./backend
	@docker build -f web-next/Dockerfile -t rawsyst/web:local .
	@docker images --format '{{.Repository}}:{{.Tag}}\t{{.Size}}' | grep '^rawsyst/'

.PHONY: up-small
up-small: ## Bring the stack up with the 8GB profile
	@docker compose -f docker-compose.yml -f docker-compose.small.yml up -d --build

.PHONY: down
down: ## Stop the stack, keeping every volume
	@docker compose down
