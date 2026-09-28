.PHONY: build test lint help

# Version printed by `okdp-compile --version`, without the leading "v" of the tag.
VERSION ?= $(or $(shell git describe --tags --always --dirty 2>/dev/null | sed 's/^v//'),dev)
LDFLAGS := -X main.version=$(VERSION)

build: ## Compile the CLI to bin/okdp-compile
	go build -ldflags "$(LDFLAGS)" -o bin/okdp-compile ./cmd/okdp-compile

test: ## Run all tests
	go test ./...

lint: ## Run golangci-lint
	golangci-lint run ./...

help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.DEFAULT_GOAL := help
