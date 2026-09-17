.DEFAULT_GOAL := help
SHELL := /bin/sh
TOOL := go tool -modfile=tools/go.mod
PKGS := ./...

.PHONY: help setup tools fmt fmt-check vet lint tidy-check test test-integration cover vuln build snapshot check ci clean

help: ## Show this help
	@awk 'BEGIN {FS = ":.*##"} /^[a-zA-Z_-]+:.*##/ {printf "  %-18s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

setup: tools ## One-time machine setup: git hooks and tool warm-up
	git config core.hooksPath githooks

tools: ## Build the pinned dev tools into the Go build cache
	$(TOOL) gofumpt -version
	$(TOOL) govulncheck -version
	$(TOOL) golangci-lint version
	$(TOOL) goreleaser --version

fmt: ## Format Go files in place (gofumpt and import grouping)
	$(TOOL) golangci-lint fmt

fmt-check: ## Fail if any Go file is not gofumpt-formatted
	@out="$$($(TOOL) gofumpt -l .)"; if [ -n "$$out" ]; then echo "$$out"; echo "run: make fmt"; exit 1; fi

vet: ## go vet
	go vet $(PKGS)

lint: ## golangci-lint, config verified first
	$(TOOL) golangci-lint config verify
	$(TOOL) golangci-lint run

tidy-check: ## Fail if either module is untidy
	go mod tidy -diff
	cd tools && go mod tidy -diff

test: ## Unit tests with the race detector and shuffling
	go test -race -shuffle=on -count=1 $(PKGS)

test-integration: ## Integration tests (build tag)
	go test -race -tags integration -count=1 $(PKGS)

cover: ## Coverage profile and the hard gate on the pure packages
	go test -coverprofile=coverage.out -covermode=atomic $(PKGS)
	go tool cover -func=coverage.out | tail -1
	sh scripts/coverage-gate.sh

vuln: ## govulncheck
	$(TOOL) govulncheck $(PKGS)

build: ## Build the binary into ./gaugewire
	go build -trimpath -o gaugewire ./cmd/gaugewire

snapshot: ## goreleaser snapshot build for every platform into dist/
	$(TOOL) goreleaser release --snapshot --clean

check: fmt-check vet lint test ## The pre-commit gate plus unit tests

ci: tidy-check check test-integration cover vuln snapshot ## Everything CI runs, except the macOS job

clean: ## Remove build outputs
	rm -rf gaugewire gaugewire.exe dist coverage.out
