.DEFAULT_GOAL := help

BINARY  := api-mock-go
VERSION ?= $(shell git describe --tags --exact-match 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/kupelaphiri/api-mock-go/internal/server.Version=$(VERSION)

.PHONY: help
help: ## Show this help
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-14s\033[0m %s\n", $$1, $$2}'

.PHONY: build
build: ## Build the binary for this machine
	go build -trimpath -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/api-mock-go

.PHONY: install
install: ## Install the binary into GOBIN
	go install -trimpath -ldflags "$(LDFLAGS)" ./cmd/api-mock-go

.PHONY: test
test: ## Run the tests
	go test ./...

.PHONY: cover
cover: ## Run the tests and open a coverage report
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -1
	go tool cover -html=coverage.out

.PHONY: race
race: ## Run the tests with the race detector
	go test -race ./...

.PHONY: bench
bench: ## Run the benchmarks
	go test -bench=. -benchmem -run='^$$' ./...

.PHONY: lint
lint: ## Check formatting and run go vet
	@unformatted=$$(gofmt -l . ); \
	if [ -n "$$unformatted" ]; then \
		echo "These files need gofmt:"; echo "$$unformatted"; exit 1; \
	fi
	go vet ./...

.PHONY: fmt
fmt: ## Format the source
	gofmt -w .

.PHONY: check
check: lint test ## Run lint and tests, as CI does

# The scripts are invoked through bash rather than executed directly, so that a
# lost executable bit — which editing over a Windows mount will do — cannot
# break the build.
.PHONY: dist
dist: ## Cross-compile every released platform into dist/
	bash scripts/build.sh

.PHONY: npm-pack
npm-pack: ## Assemble the npm packages into dist/npm/
	bash scripts/npm-pack.sh

.PHONY: test-publish
test-publish: ## Rehearse a release against a throwaway local npm registry
	bash scripts/test-publish.sh

.PHONY: run
run: build ## Run against the bundled example spec
	./$(BINARY) --schema examples/openapi.yaml --cors

.PHONY: run-config
run-config: build ## Run against the bundled example route list
	./$(BINARY) --config examples/mocks.yaml

.PHONY: clean
clean: ## Remove build output
	rm -rf $(BINARY) dist coverage.out
