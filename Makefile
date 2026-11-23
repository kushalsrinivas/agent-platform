SHELL := /bin/bash
.DEFAULT_GOAL := help

COMPOSE ?= docker compose
GO ?= go
UV ?= uv
NPM ?= npm

GO_PKG := github.com/agent-platform/agent-platform
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT ?= $(shell git rev-parse --short HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
POSTGRES_PORT ?= $(or $(shell sed -n 's/^POSTGRES_PORT=//p' .env 2>/dev/null | tail -1),5432)
POSTGRES_PASSWORD ?= $(or $(shell sed -n 's/^POSTGRES_PASSWORD=//p' .env 2>/dev/null | tail -1),postgres)
API_DATABASE_URL ?= postgres://app_api:dev_api_password@localhost:$(POSTGRES_PORT)/agent_platform?sslmode=disable
ORCHESTRATOR_DATABASE_URL ?= postgres://app_orchestrator:dev_orchestrator_password@localhost:$(POSTGRES_PORT)/agent_platform?sslmode=disable
POSTGRES_DATABASE_URL ?= postgres://postgres:$(POSTGRES_PASSWORD)@localhost:$(POSTGRES_PORT)/agent_platform?sslmode=disable
NATS_PORT ?= $(or $(shell sed -n 's/^NATS_PORT=//p' .env 2>/dev/null | tail -1),4222)
NATS_URL ?= nats://localhost:$(NATS_PORT)
API_PORT ?= $(or $(shell sed -n 's/^API_PORT=//p' .env 2>/dev/null | tail -1),8080)
API_URL ?= http://localhost:$(API_PORT)
LDFLAGS := -s -w \
	-X $(GO_PKG)/internal/platform/buildinfo.Version=$(VERSION) \
	-X $(GO_PKG)/internal/platform/buildinfo.Commit=$(COMMIT) \
	-X $(GO_PKG)/internal/platform/buildinfo.BuildDate=$(BUILD_DATE)

.PHONY: help setup dev dev-infra down logs ps test test-go test-python lint lint-go lint-python lint-web \
	migrate migrate-down reset build build-go images integration-test proto fmt

help: ## Show available commands
	@echo "Agent Platform — developer commands"
	@echo
	@grep -hE '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'
	@echo
	@echo "Quick start: make setup && make dev"

setup: ## Install local dependencies, verify tools and create .env
	@echo "Checking toolchain..."
	@command -v $(GO) >/dev/null || { echo "error: Go 1.26+ is required (https://go.dev/dl/)"; exit 1; }
	@$(GO) version
	@command -v $(UV) >/dev/null || { echo "error: uv is required (https://docs.astral.sh/uv/)"; exit 1; }
	@$(UV) --version
	@command -v $(NPM) >/dev/null || { echo "error: Node.js 20+ and npm are required"; exit 1; }
	@$(NPM) --version
	@command -v docker >/dev/null || { echo "error: Docker is required"; exit 1; }
	@$(COMPOSE) version
	@test -f .env || cp .env.example .env
	@echo "Downloading Go modules..."
	$(GO) mod download
	@echo "Syncing Python environments..."
	cd runtimes/python && $(UV) sync --frozen
	cd sdk/python && $(UV) sync --frozen
	@echo "Installing dashboard dependencies..."
	cd apps/dashboard && $(NPM) ci
	@echo
	@echo "Setup complete. Run 'make dev' to start the local stack."

dev: ## Build and start the full local stack (one command)
	$(COMPOSE) up --build

dev-infra: ## Start data stores and run migrations only
	$(COMPOSE) up -d postgres nats redis minio minio-init
	$(MAKE) migrate

down: ## Stop the stack, keeping data volumes
	$(COMPOSE) down --remove-orphans

logs: ## Follow logs from all services
	$(COMPOSE) logs -f --tail=100

ps: ## Show service status
	$(COMPOSE) ps

test: test-go test-python ## Run all unit tests

test-go: ## Run Go unit tests
	$(GO) test -race -count=1 ./...

test-python: ## Run Python runtime and SDK tests
	cd runtimes/python && $(UV) run pytest
	cd sdk/python && $(UV) run pytest

lint: lint-go lint-python lint-web ## Run all linters and type checks

lint-go: ## gofmt, go vet and golangci-lint
	@files="$$(gofmt -l . | grep -v '^gen/')"; \
		if [ -n "$$files" ]; then echo "gofmt required for:"; echo "$$files"; exit 1; fi
	$(GO) vet ./...
	@command -v golangci-lint >/dev/null || { \
		echo "error: golangci-lint is required (https://golangci-lint.run/usage/install/)"; exit 1; }
	golangci-lint run

lint-python: ## Ruff, format check and mypy for both Python projects
	cd runtimes/python && $(UV) run ruff check . && $(UV) run ruff format --check . && $(UV) run mypy
	cd sdk/python && $(UV) run ruff check . && $(UV) run ruff format --check . && $(UV) run mypy

lint-web: ## Biome and TypeScript checks for the dashboard
	cd apps/dashboard && $(NPM) run lint

migrate: ## Apply all PostgreSQL migrations
	$(COMPOSE) up -d postgres
	$(COMPOSE) run --rm migrate

migrate-down: ## Roll back the last migration in both schemas (development only)
	$(COMPOSE) run --rm --entrypoint /bin/sh migrate -c '\
		migrate -path /migrations/execution -database "$$EXECUTION_DATABASE_URL" down 1 && \
		migrate -path /migrations/control -database "$$CONTROL_DATABASE_URL" down 1'

reset: ## DESTRUCTIVE: delete all local data, containers, volumes and build output
	@echo "WARNING: this permanently deletes the local Postgres, NATS, Redis and MinIO volumes,"
	@echo "         plus generated build output. It cannot be undone."
	@printf "Type 'reset' to continue: "; read answer; \
		[ "$$answer" = "reset" ] || { echo "Aborted."; exit 1; }
	$(COMPOSE) down -v --remove-orphans
	rm -rf bin dist .data runtimes/python/dist sdk/python/dist apps/dashboard/.next
	@echo "Local environment reset."

build: build-go ## Build all binaries and packages
	cd runtimes/python && $(UV) build
	cd sdk/python && $(UV) build
	cd apps/dashboard && $(NPM) run build

images: ## Build every Docker image locally
	$(COMPOSE) build --pull

build-go: ## Build Go binaries into bin/
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/api ./services/api/cmd/api
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/orchestrator ./services/orchestrator/cmd/orchestrator
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/worker ./services/worker/cmd/worker
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/platformctl ./cli/cmd/platformctl

integration-test: ## Run integration tests against the Docker stack
	POSTGRES_PORT=$(POSTGRES_PORT) $(COMPOSE) up -d --build postgres nats redis minio minio-init api orchestrator
	@echo "Waiting for the API..."
	@for i in $$(seq 1 60); do \
		if curl -fsS "$(API_URL)/healthz" >/dev/null 2>&1; then break; fi; \
		sleep 2; \
	done
	@curl -fsS "$(API_URL)/healthz" >/dev/null || { echo "API did not become healthy"; $(COMPOSE) logs --no-color api; exit 1; }
	API_URL="$(API_URL)" \
	API_DATABASE_URL="$(API_DATABASE_URL)" \
	ORCHESTRATOR_DATABASE_URL="$(ORCHESTRATOR_DATABASE_URL)" \
	POSTGRES_DATABASE_URL="$(POSTGRES_DATABASE_URL)" \
	ORCHESTRATOR_NATS_URL="$(NATS_URL)" \
	$(GO) test -tags=integration -count=1 ./tests/integration/...

proto: ## Regenerate Go code from proto/ (requires protoc and plugins)
	@command -v protoc-gen-go >/dev/null || { \
		echo "install: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest"; exit 1; }
	@command -v protoc-gen-go-grpc >/dev/null || { \
		echo "install: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest"; exit 1; }
	protoc -I proto \
		--go_out=. --go_opt=module=$(GO_PKG) \
		--go-grpc_out=. --go-grpc_opt=module=$(GO_PKG) \
		proto/worker/v1/worker.proto \
		proto/orchestrator/v1/commands.proto \
		proto/runtime/v1/runtime.proto

fmt: ## Format Go, Python and TypeScript
	gofmt -w .
	cd runtimes/python && $(UV) run ruff format .
	cd sdk/python && $(UV) run ruff format .
	cd apps/dashboard && $(NPM) run format
