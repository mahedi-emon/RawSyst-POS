.DEFAULT_GOAL := help
SHELL := /bin/bash

VERSION ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
LDFLAGS := -X main.version=$(VERSION)

# Pin the compiler explicitly rather than relying on go.mod's toolchain
# directive, which Go does not reliably honour for a patch-level bump: a local
# go1.26.5 kept building with 1.26.5 despite `toolchain go1.26.6` in go.mod.
#
# The version matters — 1.26.6 carries the fixes for four stdlib advisories
# (crypto/tls, net/http, encoding/xml, encoding/asn1) that govulncheck reports
# against 1.26.5. Anything that builds a shippable artifact goes through here,
# so the patched compiler is not a per-developer accident.
export GOTOOLCHAIN := go1.26.6

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
	  awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-16s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build all binaries into ./bin
	go build -ldflags "$(LDFLAGS)" -o bin/ ./cmd/...

# The race detector needs cgo and a 64-bit C toolchain, and on Windows that is
# a real obstacle rather than a formality: the compiler answering to `gcc` is
# often a 32-bit MinGW, which fails with
#
#   cc1.exe: sorry, unimplemented: 64-bit mode not compiled in
#
# which says nothing at all about what to install. So this looks for a usable
# compiler rather than assuming one, and says plainly what is wrong when there
# is none.
#
# P16: the detector was opt-out locally and CI was the only place it ever ran,
# which makes CI the first place anybody would ever see a data race. It runs
# here now.
RACE ?= -race

# `default` matters as much as `undefined`. Make defines CC = cc out of the box,
# so testing only for undefined never fires and then hands cgo a compiler that
# does not exist on this machine. Only an explicit CC — environment, command
# line, or set in this file — is left alone, because somebody who set one had a
# reason.
RACE_CC := $(CC)
ifneq ($(filter default undefined,$(origin CC)),)
  RACE_CC := gcc
  CC_TARGET := $(shell gcc -dumpmachine 2>/dev/null)

  ifeq ($(findstring x86_64,$(CC_TARGET)),)
    # The compiler on PATH cannot build 64-bit objects. Look where a 64-bit
    # mingw-w64 usually lands on Windows: the winget package, MSYS2, or a
    # plainly unpacked WinLibs. First hit wins.
    #
    # LOCALAPPDATA arrives with backslashes, which $(wildcard) will not match
    # against a forward-slash pattern — it fails silently, which is the worst
    # way for a path to be wrong.
    LOCAL_APP := $(subst \,/,$(LOCALAPPDATA))
    CC_FOUND := $(firstword $(wildcard $(LOCAL_APP)/Microsoft/WinGet/Packages/BrechtSanders.WinLibs*/mingw64/bin/gcc.exe C:/msys64/mingw64/bin/gcc.exe C:/mingw64/bin/gcc.exe))

    ifneq ($(CC_FOUND),)
      # Pointed at, not prepended to PATH: a 32-bit MinGW that other tools on
      # this machine depend on keeps working exactly as it did.
      RACE_CC := $(CC_FOUND)
      export CC := $(CC_FOUND)
    endif
  endif
endif

.PHONY: race-doctor
race-doctor: ## Say whether the race detector can run here, and what to install
	@target="$$("$(RACE_CC)" -dumpmachine 2>/dev/null)"; \
	if [ -z "$$target" ]; then \
	  echo "No C compiler found. The race detector needs one:"; \
	  echo "  winget install BrechtSanders.WinLibs.POSIX.UCRT"; \
	  exit 1; \
	fi; \
	case "$$target" in \
	  *x86_64*) \
	    echo "Race detector ready."; \
	    echo "  compiler: $(RACE_CC)"; \
	    echo "  target:   $$target" ;; \
	  *) \
	    echo "The C compiler targets $$target and cannot build 64-bit objects,"; \
	    echo "which is what the race detector needs. Install one:"; \
	    echo "  winget install BrechtSanders.WinLibs.POSIX.UCRT"; \
	    echo "or set CC to a 64-bit compiler you already have."; \
	    exit 1 ;; \
	esac

.PHONY: test
test: ## Run unit tests (RACE= to disable the race detector)
	go test $(RACE) -count=1 ./...

.PHONY: test-db
test-db: ## Run tests needing PostgreSQL (set RAWSYST_DB_DSN)
	go test $(RACE) -count=1 -tags=integration ./...

.PHONY: test-db-reset
test-db-reset: ## Empty the test database and migrate it again
	@# The test database only grows, and that is not a leak — it is the cost of
	@# a guarantee working.
	@#
	@# Fixtures do try to clean up: each seeded tenant gets a t.Cleanup that
	@# deletes it, and ON DELETE CASCADE should take the company, the users and
	@# the documents with it. That cascade reaches session_refresh_token,
	@# sales_invoice, journal_entry and every other table carrying a
	@# reject_delete() trigger, and the trigger does exactly what it exists to
	@# do: refuses. Posted history is never deleted, and a test tenant's history
	@# is history. So the cleanup succeeds only for a tenant that never logged
	@# in, which in practice is none of them.
	@#
	@# Rather than weaken the trigger for the convenience of tests — a
	@# production invariant traded for a smaller database is a bad trade — the
	@# test database is treated as what it is: disposable.
	@#
	@# It drops the SCHEMA rather than the database. That matters for a reason
	@# learned the hard way: dropping the database needs CREATEDB to put it
	@# back, and the application role deliberately does not have CREATEDB (it is
	@# unprivileged so that row-level security actually applies to it). A DROP
	@# DATABASE therefore succeeds and the CREATE that follows fails, leaving no
	@# database at all. Dropping the schema needs only ownership, which the
	@# migrations already conferred, and DDL is not subject to the row triggers.
	@test -n "$(RAWSYST_DB_DSN)" || { 	  echo "RAWSYST_DB_DSN is not set. It names the database to empty."; 	  exit 1; 	}
	@db=$$(printf '%s' "$(RAWSYST_DB_DSN)" | sed -e 's|.*/||' -e 's|?.*||'); 	case "$$db" in 	  *test*) ;; 	  *) echo "Refusing to empty \"$$db\": this only touches a database with"; 	     echo "\"test\" in its name. Emptying a development database would lose"; 	     echo "whatever was being worked on in it."; 	     exit 1 ;; 	esac; 	echo "Emptying $$db ..."; 	psql "$(RAWSYST_DB_DSN)" -v ON_ERROR_STOP=1 -q 	  -c "DROP SCHEMA public CASCADE" 	  -c "CREATE SCHEMA public" || exit 1; 	go run ./cmd/migrate

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

.PHONY: vuln
vuln: ## Fail on known vulnerabilities in code we actually reach
	@# govulncheck reports only advisories on call paths this code can reach,
	@# so a finding here is exploitable rather than merely present in the
	@# dependency graph. For a system holding tax records, shipping one is not
	@# a trade worth making.
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

.PHONY: check
check: fmt-check vet lint-wording test vuln ## Everything CI runs

.PHONY: clean
clean: ## Remove build output
	rm -rf bin coverage.out
