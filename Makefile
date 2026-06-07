GO       ?= go
BIN      := bin
PKGS     := ./...

# Local Postgres for the dual-dialect tests. podman is the default engine;
# override with COMPOSE="docker compose" to use docker. The DSN matches the one
# CI sets, so `make test-postgres` runs what the CI postgres leg runs.
COMPOSE      ?= podman compose
COMPOSE_FILE := docker/postgres/compose.yaml
PG_DSN       ?= postgres://githome:githome@localhost:5432/githome_test?sslmode=disable

.PHONY: all build test test-postgres lint vet fmt tidy gates clean pg-up pg-down pg-down-clean

all: build

build:
	$(GO) build -o $(BIN)/githome ./cmd/githome
	$(GO) build -o $(BIN)/githome-migrate ./cmd/githome-migrate

test:
	$(GO) test $(PKGS)

# test-postgres runs the suite against a running local Postgres (make pg-up),
# so the Postgres dialect is exercised the way CI exercises it.
test-postgres:
	GITHOME_TEST_POSTGRES_DSN="$(PG_DSN)" $(GO) test $(PKGS)

# pg-up starts Postgres 18 and blocks until its health check passes.
pg-up:
	$(COMPOSE) -f $(COMPOSE_FILE) up -d --wait

# pg-down stops and removes the container but keeps the data volume.
pg-down:
	$(COMPOSE) -f $(COMPOSE_FILE) down

# pg-down-clean also drops the data volume for a clean slate.
pg-down-clean:
	$(COMPOSE) -f $(COMPOSE_FILE) down -v

vet:
	$(GO) vet $(PKGS)

lint:
	golangci-lint run

fmt:
	golangci-lint fmt

tidy:
	$(GO) mod tidy

# gates mirrors the cross-cutting CI checks so they can be run locally.
gates:
	@echo ">> no internal/ directories"
	@if git ls-files | grep -E '(^|/)internal/'; then echo "internal/ directory found"; exit 1; fi
	@echo ">> no leaked upstream hosts in served code (cassettes and tests exempt)"
	@files=$$(git ls-files '*.go' | grep -vE '(_test\.go|testdata/)'); \
	if [ -n "$$files" ] && printf '%s\n' $$files | xargs grep -nE 'api\.github\.com|//github\.com'; then \
		echo "upstream host referenced in served code"; exit 1; \
	fi
	@echo "gates ok"

clean:
	rm -rf $(BIN)
