.PHONY: build test race cover lint fmt tidy example fixtures check help

# Version printed by `okdp-compile --version`, without the leading "v" of the tag.
VERSION ?= $(or $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//'),dev)
LDFLAGS := -X main.version=$(VERSION)

# golangci-lint at the version tools/go.mod pins: a module of its own, its
# dependencies stay out of the library's go.mod.
GOLANGCI_LINT := go tool -modfile=tools/go.mod golangci-lint

# The Go files, but those of dot directories (.claude/worktrees...).
GOFILES = $(shell find . -name '*.go' -not -path './.*')

build: ## Compile the CLI to bin/okdp-compile
	go build -ldflags "$(LDFLAGS)" -o bin/okdp-compile ./cmd/okdp-compile

test: ## Run all tests
	go test ./...

race: ## Run all tests with the race detector
	go test -race ./...

cover: ## Run all tests with coverage: cover.out and the total
	go test -coverprofile=cover.out ./...
	go tool cover -func=cover.out | tail -n 1

lint: ## Run golangci-lint, the version tools/go.mod pins
	$(GOLANGCI_LINT) run ./...

fmt: ## Format the Go files (gofmt -w), listing those changed
	@gofmt -l -w $(GOFILES)

tidy: ## Tidy go.mod and tools/go.mod
	go mod tidy
	go -C tools mod tidy

example: ## Rewrite example/output/ from the current compiler
	go test ./internal/fsio -run '^TestExample$$' -update

fixtures: ## Regenerate testdata/helm (helm, python3, UMBRELLA_HELMS)
	go test ./internal/chart -run '^TestHelmValues$$' -update
	go test ./internal/fsio -run '^TestHelmUmbrella$$' -update
	UMBRELLA_HELMS="$(UMBRELLA_HELMS)" testdata/helm/generate.sh

check: ## Run what CI runs: gofmt, vet, lint, tidy, build and race tests
	@out=$$(gofmt -l $(GOFILES)); test -z "$$out" || { echo "not formatted, run make fmt:" >&2; echo "$$out" >&2; exit 1; }
	go vet ./...
	$(GOLANGCI_LINT) run ./...
	go mod tidy -diff
	go -C tools mod tidy -diff
	go build ./...
	go test -race ./...

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
