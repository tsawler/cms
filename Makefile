.PHONY: help check build fmt-check test test-unit test-db vet fmt tidy

help: ## Show this help
	@grep -hE '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) | awk -F':.*?## ' '{printf "  %-12s %s\n", $$1, $$2}'

# The same four steps, in the same order, as CI's "unit tests" job — run
# this before pushing. gofmt is the one of them that go build, go vet, and
# go test all ignore, so a formatting slip otherwise gets as far as CI.
check: build vet fmt-check test-unit ## Everything CI's unit job runs (no database needed)

build: ## Compile every package
	go build ./...

fmt-check: ## Fail if anything needs gofmt, rather than reformatting it
	@unformatted=$$(gofmt -l .); \
	if [ -n "$$unformatted" ]; then \
		echo "these files need gofmt (run 'make fmt'):"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

# Each package that uses dbtest starts its own container per engine, so a
# fully parallel run asks Docker for ~20 databases at once and can flake on
# a busy machine. -p 2 keeps that to a handful without costing much wall
# clock, since the containers are the slow part, not the tests.
GOTEST_P ?= 2

test: ## Run every test against all engines (needs Docker; skips without it)
	go test -p $(GOTEST_P) ./...

test-unit: ## Run only the tests that need no database
	go test -short ./...

test-pg: ## Run the conformance suite against Postgres only
	go test -p $(GOTEST_P) ./... -run '.*/postgres' -count=1

test-mysql: ## Run the conformance suite against MySQL only
	go test -p $(GOTEST_P) ./... -run '.*/mysql' -count=1

test-mariadb: ## Run the conformance suite against MariaDB only
	go test -p $(GOTEST_P) ./... -run '.*/mariadb' -count=1

test-matrix: test-pg test-mysql test-mariadb ## Run each engine's suite in turn

vet: ## Run go vet
	go vet ./...

fmt: ## Format the tree
	go fmt ./...

tidy: ## Tidy module dependencies
	go mod tidy
