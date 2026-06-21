BINARY    := gravity
PKG       := ./cmd/gravity
VERSION   ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS   := -X main.version=$(VERSION)

.DEFAULT_GOAL := build

.PHONY: build
build: ## Build the gravity binary into ./bin
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o bin/$(BINARY) $(PKG)

.PHONY: install
install: ## go install the binary
	go install -ldflags "$(LDFLAGS)" $(PKG)

.PHONY: test
test: ## Run all tests
	go test ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: fmt
fmt: ## Format all Go source
	gofmt -w .

.PHONY: fmt-check
fmt-check: ## Fail if any file needs gofmt
	@out="$$(gofmt -l .)"; \
	if [ -n "$$out" ]; then \
		echo "gofmt needed on:"; echo "$$out"; exit 1; \
	fi

.PHONY: lint
lint: vet fmt-check ## vet + gofmt check

.PHONY: tidy
tidy: ## Tidy go.mod/go.sum
	go mod tidy

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf bin

.PHONY: ci
ci: lint test build ## Everything CI should run

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
