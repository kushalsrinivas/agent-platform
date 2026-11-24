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
NATS_USER ?= $(or $(shell sed -n 's/^NATS_USER=//p' .env 2>/dev/null | tail -1),platform)
NATS_PASSWORD ?= $(or $(shell sed -n 's/^NATS_PASSWORD=//p' .env 2>/dev/null | tail -1),dev-nats-password)
NATS_URL ?= nats://$(NATS_USER):$(NATS_PASSWORD)@localhost:$(NATS_PORT)
API_PORT ?= $(or $(shell sed -n 's/^API_PORT=//p' .env 2>/dev/null | tail -1),8080)
API_URL ?= http://localhost:$(API_PORT)
ORCHESTRATOR_GRPC_PORT ?= $(or $(shell sed -n 's/^ORCHESTRATOR_GRPC_PORT=//p' .env 2>/dev/null | tail -1),9090)
ORCHESTRATOR_GRPC_ADDR ?= localhost:$(ORCHESTRATOR_GRPC_PORT)
LDFLAGS := -s -w \
	-X $(GO_PKG)/internal/platform/buildinfo.Version=$(VERSION) \
	-X $(GO_PKG)/internal/platform/buildinfo.Commit=$(COMMIT) \
	-X $(GO_PKG)/internal/platform/buildinfo.BuildDate=$(BUILD_DATE)

.PHONY: help setup dev dev-infra down logs ps test test-go test-python lint lint-go lint-python lint-web \
	migrate migrate-down reset build build-go images integration-test chaos-test loadgen load-stack load-api \
	load-baseline load-moderate load-target load-queue-pressure load-burst load-runtime manifests helm-package migrate-check \
	backup-verify security-audit release-check proto fmt

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
	docker build -f infrastructure/docker/cli.Dockerfile --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg BUILD_DATE=$(BUILD_DATE) -t agent-platform-cli:$(VERSION) .
	docker build -f infrastructure/docker/migrate.Dockerfile -t agent-platform-migrate:$(VERSION) .

scan-images: ## Scan locally built images with Trivy (requires trivy)
	@command -v trivy >/dev/null || { echo "install Trivy: https://trivy.dev"; exit 1; }
	@for image in agent-platform-api agent-platform-orchestrator agent-platform-worker agent-platform-dashboard agent-platform-cli agent-platform-migrate; do \
		trivy image --exit-code 1 --severity HIGH,CRITICAL --ignore-unfixed "$$image:$(VERSION)"; \
	done

build-go: ## Build Go binaries into bin/
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/api ./services/api/cmd/api
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/orchestrator ./services/orchestrator/cmd/orchestrator
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/worker ./services/worker/cmd/worker
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/platformctl ./cli/cmd/platformctl
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/agentctl ./cli/cmd/agentctl

integration-test: ## Run integration tests against the Docker stack
	POSTGRES_PORT=$(POSTGRES_PORT) API_RATE_LIMIT_PER_MINUTE=1000 $(COMPOSE) up -d --build postgres nats redis minio minio-init api orchestrator worker
	@echo "Waiting for the API..."
	@for i in $$(seq 1 60); do \
		if curl -fsS "$(API_URL)/healthz" >/dev/null 2>&1; then break; fi; \
		sleep 2; \
	done
	@curl -fsS "$(API_URL)/healthz" >/dev/null || { echo "API did not become healthy"; $(COMPOSE) logs --no-color api; exit 1; }
	cd runtimes/python && $(UV) sync --frozen
	API_URL="$(API_URL)" \
	API_DATABASE_URL="$(API_DATABASE_URL)" \
	ORCHESTRATOR_DATABASE_URL="$(ORCHESTRATOR_DATABASE_URL)" \
	POSTGRES_DATABASE_URL="$(POSTGRES_DATABASE_URL)" \
	ORCHESTRATOR_NATS_URL="$(NATS_URL)" \
	ORCHESTRATOR_GRPC_ADDR="$(ORCHESTRATOR_GRPC_ADDR)" \
	$(GO) test -tags=integration -count=1 ./tests/integration/...

chaos-test: ## Run the Phase 16 chaos suite against the Docker stack
	POSTGRES_PORT=$(POSTGRES_PORT) API_RATE_LIMIT_PER_MINUTE=1000 API_RATE_LIMIT_BURST=200 \
		$(COMPOSE) up -d --build postgres nats redis minio minio-init api orchestrator worker
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
	ORCHESTRATOR_GRPC_ADDR="$(ORCHESTRATOR_GRPC_ADDR)" \
	$(GO) test -tags=integration -run 'TestChaos' -count=1 -timeout 15m ./tests/integration/...

loadgen: ## Build the execution workload generator
	mkdir -p bin
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o bin/loadgen ./cmd/loadgen

load-stack: ## Recreate API, Python worker and echo worker with load-test limits
	API_RATE_LIMIT_PER_MINUTE=100000 API_RATE_LIMIT_BURST=5000 \
		ORCHESTRATOR_MAX_ACTIVE_EXECUTIONS=5000 ORCHESTRATOR_MAX_QUEUED_EXECUTIONS=20000 \
		ORCHESTRATOR_DISPATCH_INTERVAL_MS=100 ORCHESTRATOR_DISPATCH_BATCH=200 \
		WORKER_MAX_CONCURRENCY=8 WORKER_RUNTIME_STARTUP_SECONDS=60 \
		$(COMPOSE) --profile loadtest up -d api orchestrator worker worker-echo

load-api: ## Run the k6 public API load test (requires k6)
	@command -v k6 >/dev/null || { echo "install k6: brew install k6"; exit 1; }
	API_URL="$(API_URL)" k6 run loadtest/k6/api.js

load-baseline: loadgen ## 100 executions
	bin/loadgen -api "$(API_URL)" -db "$(POSTGRES_DATABASE_URL)" -pool loadtest -count 100 -concurrency 16 -timeout 5m \
		-report loadtest/results/baseline.json

load-moderate: loadgen ## 500 executions
	bin/loadgen -api "$(API_URL)" -db "$(POSTGRES_DATABASE_URL)" -pool loadtest -count 500 -concurrency 32 -timeout 10m \
		-report loadtest/results/moderate.json

load-target: loadgen ## 1,000 executions
	bin/loadgen -api "$(API_URL)" -db "$(POSTGRES_DATABASE_URL)" -pool loadtest -count 1000 -concurrency 64 -timeout 20m \
		-report loadtest/results/target.json

load-queue-pressure: loadgen ## 5,000 submissions with the worker stopped or saturated
	bin/loadgen -api "$(API_URL)" -db "$(POSTGRES_DATABASE_URL)" -pool loadtest -count 5000 -concurrency 128 -timeout 3m -allow-open \
		-report loadtest/results/queue-pressure.json

load-burst: loadgen ## 10,000 submission burst
	bin/loadgen -api "$(API_URL)" -db "$(POSTGRES_DATABASE_URL)" -pool loadtest -count 10000 -concurrency 256 -timeout 5m -allow-open \
		-report loadtest/results/burst.json

load-runtime: loadgen ## 100 executions through the real Python runtime
	bin/loadgen -api "$(API_URL)" -db "$(POSTGRES_DATABASE_URL)" -pool default -count 100 -concurrency 8 \
		-timeout 10m -report loadtest/results/runtime.json

manifests: ## Lint and render Helm and Kustomize
	helm lint infrastructure/helm/agent-platform
	helm template agent-platform infrastructure/helm/agent-platform --namespace agent-platform >/dev/null
	kubectl kustomize infrastructure/kubernetes >/dev/null

helm-package: manifests ## Package the Helm chart into dist/
	mkdir -p dist
	helm package infrastructure/helm/agent-platform -d dist

migrate-check: ## Apply migrations to a scratch database and verify core tables
	./scripts/migrate-check.sh

backup-verify: ## Dump the local database, restore it, and verify core tables
	./scripts/verify-backup.sh

security-audit: ## Run dependency, secret and manifest scans
	./scripts/security-audit.sh

release-check: test lint manifests security-audit ## Release gate without the integration suite
	@echo "Release static gate passed. Run 'make integration-test && make chaos-test' before tagging."

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
	cd runtimes/python && $(UV) run python -m grpc_tools.protoc -I../../proto \
		--python_out=src/agent_runtime/proto_gen \
		--grpc_python_out=src/agent_runtime/proto_gen \
		runtime/v1/runtime.proto worker/v1/worker.proto
	mv runtimes/python/src/agent_runtime/proto_gen/runtime/v1/*.py runtimes/python/src/agent_runtime/proto_gen/ 2>/dev/null || true
	mv runtimes/python/src/agent_runtime/proto_gen/worker/v1/*.py runtimes/python/src/agent_runtime/proto_gen/ 2>/dev/null || true
	rmdir runtimes/python/src/agent_runtime/proto_gen/runtime/v1 runtimes/python/src/agent_runtime/proto_gen/runtime 2>/dev/null || true
	rmdir runtimes/python/src/agent_runtime/proto_gen/worker/v1 runtimes/python/src/agent_runtime/proto_gen/worker 2>/dev/null || true
	python3 scripts/patch_runtime_stubs.py

fmt: ## Format Go, Python and TypeScript
	gofmt -w .
	cd runtimes/python && $(UV) run ruff format .
	cd sdk/python && $(UV) run ruff format .
	cd apps/dashboard && $(NPM) run format
