# intropy CLI — Makefile
#
# Common tasks for building, testing, and quality checks.
# Run `make help` to see available targets.

BINARY      := intropy
CMD_DIR     := ./cmd/intropy
BUILD_DIR   := ./bin

VERSION     := $(shell git describe --tags --always 2>/dev/null || echo dev)
COMMIT      := $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE  := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)

LDFLAGS     := -X main.version=$(VERSION) \
               -X main.commit=$(COMMIT) \
               -X main.date=$(BUILD_DATE)

GOFLAGS     :=
TESTFLAGS   := -v
RACEFLAGS   := -race

# ---------------------------------------------------------------------------
# Build
# ---------------------------------------------------------------------------

.PHONY: build
build: ## Build the binary into ./bin/intropy
	@mkdir -p $(BUILD_DIR)
	go build $(GOFLAGS) -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)

.PHONY: install
install: ## Install the binary to $GOPATH/bin
	go install $(GOFLAGS) -ldflags "$(LDFLAGS)" $(CMD_DIR)

.PHONY: clean
clean: ## Remove build artifacts
	rm -rf $(BUILD_DIR)

# ---------------------------------------------------------------------------
# Test
# ---------------------------------------------------------------------------

.PHONY: test
test: ## Run all tests
	go test $(TESTFLAGS) ./...

.PHONY: test-race
test-race: ## Run all tests with the race detector
	go test $(TESTFLAGS) $(RACEFLAGS) ./...

.PHONY: test-cover
test-cover: ## Run tests and open coverage report
	go test $(TESTFLAGS) -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

# ---------------------------------------------------------------------------
# Quality
# ---------------------------------------------------------------------------

.PHONY: fmt
fmt: ## Format Go source files
	go fmt ./...

.PHONY: vet
vet: ## Run go vet
	go vet ./...

.PHONY: lint
lint: ## Run golangci-lint (install with: go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest)
	@which golangci-lint >/dev/null 2>&1 || { \
		echo "golangci-lint not found. Install it:"; \
		echo "  go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest"; \
		exit 1; \
	}
	golangci-lint run ./...

.PHONY: tidy
tidy: ## Tidy and verify Go module dependencies
	go mod tidy
	go mod verify

.PHONY: check
check: fmt vet test ## Run fmt, vet, and tests (lightweight CI check)

.PHONY: ci
ci: tidy fmt vet lint test-race ## Full CI pipeline — use before pushing

# ---------------------------------------------------------------------------
# Development helpers
# ---------------------------------------------------------------------------

.PHONY: run
run: build ## Build and run the binary (pass args with: make run ARGS="version")
	$(BUILD_DIR)/$(BINARY) $(ARGS)

.PHONY: version
version: ## Print build metadata
	@echo "version: $(VERSION)"
	@echo "commit:  $(COMMIT)"
	@echo "date:    $(BUILD_DATE)"

.PHONY: help
help: ## Show this help message
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'
