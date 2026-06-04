GO       ?= go
BIN      := bin
PKGS     := ./...

.PHONY: all build test lint vet fmt tidy gates clean

all: build

build:
	$(GO) build -o $(BIN)/githome ./cmd/githome
	$(GO) build -o $(BIN)/githome-migrate ./cmd/githome-migrate

test:
	$(GO) test $(PKGS)

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
