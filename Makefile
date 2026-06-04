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
	@! git ls-files | grep -E '(^|/)internal/' || (echo "internal/ directory found" && exit 1)
	@echo ">> no leaked upstream hosts in served code (cassettes and tests exempt)"
	@! git ls-files '*.go' | grep -vE '(_test\.go|testdata/)' | xargs grep -nE 'api\.github\.com|//github\.com' 2>/dev/null || (echo "upstream host referenced in served code" && exit 1)
	@echo "gates ok"

clean:
	rm -rf $(BIN)
