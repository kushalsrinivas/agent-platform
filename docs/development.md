# Development

Everything a new engineer needs from `git clone` to a running stack.

## Prerequisites

| Tool | Version | Check |
|---|---|---|
| Docker + Compose v2 | Docker 24+ | `docker compose version` |
| Go | 1.26+ | `go version` |
| Node.js | 20.9+ (22 recommended) | `node --version` |
| uv | 0.6+ | `uv --version` |
| GNU Make | 3.81+ | `make --version` |
| protoc + plugins | only for `make proto` | `protoc --version` |

`make setup` verifies these and prints a clear error if one is missing. Local Go/Python/Node toolchains are only
needed for running tests and services outside Docker.

## Quick start

```bash
make setup     # dependencies, .env, Python/Node installs
make dev       # builds images, runs migrations, starts the stack
```

That is the entire path. `make dev` is the single documented command once dependencies are installed.

| Service | URL | Notes |
|---|---|---|
| Dashboard | http://localhost:3000 | Demo data until the dashboard wiring phase |
| API | http://localhost:8080 | `/healthz`, `/readyz`, `/v1/meta` |
| Orchestrator gRPC | localhost:9090 | reflection enabled in development |
| Orchestrator HTTP | http://localhost:8081 | health, version, operator messaging status/redrive |
| Worker HTTP | http://localhost:8082 | health, version, `/v1/consumer` delivery counters, runtime and drain state |
| PostgreSQL | localhost:5432 | `agent_platform`, user `postgres`/`postgres` |
| NATS | localhost:4222 / 8222 | JetStream enabled; 8222 is monitoring |
| Redis | localhost:6379 | |
| Object storage | http://localhost:9000 | S3 API; console at :9001 (`minioadmin` / `minioadmin123`) |

The ports above are defaults. Every published port is configurable in `.env` (`POSTGRES_PORT`, `NATS_PORT`,
`REDIS_PORT`, `MINIO_API_PORT`, `API_PORT`, `ORCHESTRATOR_GRPC_PORT`, `ORCHESTRATOR_HTTP_PORT`, `WORKER_PORT`,
`DASHBOARD_PORT`). `make integration-test` derives `API_URL`, database URLs, the NATS URL and the orchestrator
gRPC address from the same values (including `ORCHESTRATOR_GRPC_PORT`), so a remapped local stack still runs the
full suite.

Migration order is `control` then `execution`; the one-shot `migrate` service enforces it. Application containers
wait for it with `depends_on: service_completed_successfully`.

### Calling the API locally

The local stack runs the API in `AUTH_MODE=dev`. Mint a short-lived bearer token and call the control plane:

```bash
TOKEN=$(curl -s -X POST http://localhost:8080/v1/dev/token \
  -H 'Content-Type: application/json' \
  -d '{"principal_id":"user_local"}' | python3 -c 'import json,sys; print(json.load(sys.stdin)["access_token"])')

curl -s http://localhost:8080/v1/orgs -H "Authorization: Bearer $TOKEN"
curl -s -X POST http://localhost:8080/v1/orgs \
  -H "Authorization: Bearer $TOKEN" -H 'Content-Type: application/json' \
  -d '{"name":"Local Org","slug":"local-org"}'
```

If `API_PORT` is remapped in `.env`, use that port instead of 8080.

`POST /v1/dev/token` is mounted only when `ENVIRONMENT=development` and `AUTH_MODE=dev`; it is never part of
production. In production the API verifies OIDC bearer JWTs (`AUTH_MODE=oidc`, `OIDC_ISSUER_URL`,
`OIDC_CLIENT_ID`). The VPS override defaults to OIDC; do not expose a dev-mode API.

The complete Phase 04 flow (org → project → agent → publish → workflow → publish → execution) is exercised by
`make integration-test` in [`tests/integration/api_flow_test.go`](../tests/integration/api_flow_test.go).

### Object storage

Official MinIO container images were withdrawn from Docker Hub in September 2026 and the Quay repository now
requires authentication. Local development uses
[`pgsty/silo`](https://hub.docker.com/r/pgsty/silo), a maintained S3-compatible build of the same codebase, with
the same API and CLI. This is local infrastructure only: the platform targets any S3-compatible endpoint, and
nothing in the application imports a MinIO SDK. Pinning a different S3 server is a one-service change in
`docker-compose.yml`.

## Common commands

| Command | What it does |
|---|---|
| `make dev` | Build everything and run the full stack in the foreground. |
| `make dev-infra` | Start only Postgres/NATS/Redis/object storage and apply migrations. |
| `make down` | Stop containers; volumes survive. |
| `make logs` | Follow all service logs. |
| `make test` | Go unit tests (race detector) and both Python suites. |
| `make lint` | gofmt, `go vet`, golangci-lint, Ruff, mypy, Biome and `tsc`. |
| `make migrate` | Apply migrations to the running database. |
| `make integration-test` | Build and start the control plane, migrate, run the full integration suite. |
| `make build` | Binaries into `bin/`; Python wheels; dashboard production build. |
| `make images` | Build every Docker image without starting anything. |
| `make proto` | Regenerate Go code from `proto/` (requires plugin install). |
| `make reset` | **Destructive.** Deletes containers, all local data volumes and `bin/`. |

`make reset` is the only command that destroys data. It asks you to type `reset` and then removes the Postgres,
NATS, Redis and object-storage volumes. There is no recovery.

## Running services outside Docker

```bash
make dev-infra                 # data stores + migrations in Docker
set -a; source .env; set +a    # API_/ORCHESTRATOR_/WORKER_ variables

go run ./services/api/cmd/api
go run ./services/orchestrator/cmd/orchestrator
go run ./services/worker/cmd/worker
cd apps/dashboard && npm run dev
```

Environment variables are documented in `.env.example`; defaults point at localhost, so the commands above work
with no `.env` once the data stores are up.

### Python agent runtime

The worker image contains the Go host and a Python 3.12 virtual environment with `langgraph`, `grpcio`, `httpx`
and the `agent_runtime` package. Each attempt starts `python -m agent_runtime serve --socket <path>` in a private
temporary directory; the worker calls the runtime ABI over that Unix socket and kills the process group on
completion, timeout or drain. The default provider is the deterministic `echo` provider; OpenAI-compatible,
Anthropic and Gemini adapters are available by name and capability.

For host-side runs and the integration suite, sync the runtime environment once:

```bash
cd runtimes/python && uv sync --frozen
```

`make integration-test` does this automatically; CI installs `uv` and syncs before the integration suite. The
runtime emits JSON events on stdout and the worker forwards them; provider API keys are not wired yet, so only the
echo provider executes without a future credentials slice.

### Durable execution and recovery

Each attempt gets `AGENT_RUNTIME_CHECKPOINT_DIR`, a private directory inside the worker's temporary workspace. The
runtime writes atomic `ckpt-<sequence>-<sha>.json.gz` files after each recoverable tool call and at node completion.
The worker uploads them with the fenced `PutCheckpoint` RPC; the orchestrator stores states up to 256 KiB inline
and offloads larger states to `ORCHESTRATOR_ARTIFACT_DIR` (Compose mounts the `checkpoint-artifacts` volume at
`/var/lib/agent-platform/artifacts`). `GetTaskContext` returns the newest readable checkpoint on the next attempt.

The recovery manager runs on `ORCHESTRATOR_REAPER_INTERVAL_MS` and performs leases, terminated-worker reaping,
deadline enforcement, interrupted-execution repair and outbox lag reporting. The frozen retry policy is stored on
each task; `retry_on`, `backoff_base_ms` and `backoff_max_ms` can be set per node or under workflow `policy.retry`.
Cancellation is available at `POST /v1/orgs/{org}/projects/{project}/executions/{execution}:cancel`.

The Phase 08 tests restart the orchestrator and NATS and briefly pause PostgreSQL through the Docker CLI; they are
part of `make integration-test` and require the Compose stack. The runtime also recognises test-only environment
variables (`AGENT_RUNTIME_TEST_PAUSE_SECONDS`, `AGENT_RUNTIME_TEST_PAUSE_AFTER_TOOL_SECONDS`,
`AGENT_RUNTIME_TEST_MODEL_DELAY_SECONDS`, `AGENT_RUNTIME_RESUME_MARKER`,
`AGENT_RUNTIME_TOOL_COUNTER_FILE`); never set them in production.

### Worker registration and shutdown

A worker advertises `WORKER_RUNTIME`, `WORKER_RUNTIME_VERSION`, `WORKER_CAPABILITIES`, `WORKER_CPU_CORES`,
`WORKER_MEMORY_MB`, its worker pool and concurrency at registration. Heartbeats carry capacity, active attempts,
CPU/memory usage and the worker status (`starting`, `available`, `busy`, `draining`, `unhealthy`, `terminated`).

`SIGTERM`/`SIGINT` drains the worker: no new dispatches are accepted, in-flight attempts get
`WORKER_SHUTDOWN_GRACE_SECONDS` to report, then the session sends `Goodbye`. The orchestrator marks a silent worker
`unhealthy` after 15 s and `terminated` after 60 s; its leases expire independently and are retried.

Execution priority is accepted by `POST .../executions` (`priority`, -100..100, default 0). The scheduler is
priority- and tenant-fair with aging; see [phase-06-worker-manager.md](phase-06-worker-manager.md#3-scheduling-pipeline).
Optional caps: `ORCHESTRATOR_MAX_ACTIVE_TASKS_PER_TENANT`, `ORCHESTRATOR_PROVIDER_CONCURRENCY`,
`ORCHESTRATOR_TOOL_CONCURRENCY`.

## Repository layout

```
apps/dashboard/          Next.js dashboard (design system + shell)
apps/docs/               Documentation site, deferred (authored docs live in /docs)
services/api/            Go HTTP control plane
services/orchestrator/   Go lifecycle, scheduler, lease and event owner
services/worker/         Go attempt host; never touches PostgreSQL
runtimes/python/         Python packages executed inside an attempt sandbox
sdk/go/                  Go client for the public API
sdk/python/              Python client for the public API
cli/                     platformctl operator CLI
proto/                   Wire contracts (source of truth)
gen/                     Generated Go code (committed; edit proto, then make proto)
packages/contracts/      Go workflow, task and event contracts + canonical digests
packages/schemas/        Shared JSON Schemas (workflow, task, events)
packages/test-fixtures/  Cross-language fixtures and golden digest oracle
migrations/              control/ and execution/ migrations, applied in that order
infrastructure/          Dockerfiles, compose, Kubernetes/Helm placeholders, monitoring
tests/                   Integration, e2e, load and chaos suites
```

### Go module layout

One Go module at the repository root, with one `cmd` per binary and shared code in `internal/platform`. This
keeps independent builds (`go build ./services/orchestrator/...`) without `go.work` and `replace` overhead.
Split a service into its own module only when it needs an independent release cadence; nothing here does yet.

Dependency injection is manual constructor wiring in each `main.go`. No DI framework, no reflection, no magic:
the dependency graph is small enough to read.

## Migrations

- `migrations/control/0001` creates the schema, development roles and default privileges.
- `migrations/control/0002` creates the nine control tables; `0003` adds RLS, immutability triggers, grants and
  organisation discovery. `0004` adds the API membership-auth helper and the organisation list's timestamps.
- `migrations/execution/0001` creates the execution schema and roles; `0002` creates the eight execution tables,
  indexes and outbox; `0003` adds RLS, append-only audit, attempt immutability and grants.
- Both use golang-migrate and separate migration tables (`control_schema_migrations`,
  `execution_schema_migrations`) selected through the `x-migrations-table` URL parameter.
- Local migrations run as the Postgres superuser. In production, DDL runs under dedicated migration roles; see
  [`data-ownership.md`](data-ownership.md).
- Down migrations exist and are tested, but they are development-only. Production uses expand/contract and
  roll-forward.
- The schema contract, RLS model, indexes and brief-to-table mapping are documented in
  [`phase-03-contracts.md`](phase-03-contracts.md).

Add a migration by creating the next numbered `.up.sql` / `.down.sql` pair in the right directory.

## Shared contracts

Workflow, task and event contracts exist twice on purpose — once in Go and once in Python — and are proven
equivalent against the same fixtures:

- JSON Schemas: `packages/schemas` (Draft 2020-12).
- Go models and semantic validation: `packages/contracts`.
- Python models: `runtimes/python/src/contracts`.
- Fixtures + golden digests: `packages/test-fixtures`.
- Canonicalization: RFC 8785, digest `sha256:<hex>` over the workflow graph.

```bash
go test ./packages/contracts
cd runtimes/python && uv run pytest tests/test_contracts_*.py
```

Changing a fixture changes the golden digest in `packages/test-fixtures/workflow.digests.json`; both language
suites must pass before committing. Never update only one language.

## Proto generation

Generated code in `gen/` is committed so builds do not require protoc.

```bash
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
make proto
```

`proto/` is additive-only. Never renumber or reuse fields; see [`communication.md`](communication.md#8-versioning).

## Deliberate choices

These are the places where the project chose the smaller option on purpose. Revisit them when their trigger fires.

| Choice | Why | Revisit when |
|---|---|---|
| Environment-based config, no Viper | The config is flat, small and the same in every environment. `env` tags are typed and testable. | Config needs nested files, hot reload or multiple sources. |
| One root Go module | Independent per-service builds without `go.work`, `replace` directives or version skew. | A service needs an independent release cadence. |
| Manual constructor injection | Three binaries, one dependency graph each; a DI framework would hide wiring for no benefit. | The graph becomes large enough that startup wiring is unreadable. |
| `sqlc` deferred | The API repositories use explicit SQL with RLS, locking and pagination; generated queries would hide those boundaries. | Query volume or repetition makes generated code a clear win. |
| `go-playground/validator` deferred | Request bodies are small and use `encoding/json` strict decoding plus package-level validation. | A shared request-validation layer is needed. |
| Local passwords / sessions | OIDC owns registration, login, password hashing and sessions; the platform stores no passwords. | A first-party account system is a separate product decision. |
| API keys | OIDC service-principal tokens cover machine clients; API keys need a scope design and pre-tenant lookup function. | CI or partner clients need scoped non-OIDC credentials. |
| Normalized workflow node/edge tables | The immutable graph is one JSONB value, validated by the shared contract; normalizing it would create a second source of truth. | Graph querying at scale or per-node permissions (requires an ADR). |
| Deferred brief tables (`users`, `environments`, `api_keys`, `agent_deployments`, tool/MCP/approval/usage tables) | Each conflicts with a Phase 1 non-goal or lacks a design. The mapping and triggers are in [`phase-03-contracts.md`](phase-03-contracts.md). | The corresponding ADR/feature exists. |
| HTTP/2 gRPC contracts generated, no Python stubs | Python speaks to the worker over a UDS; generating stubs before the runtime ABI exists adds dead code. | Runtime ABI implementation. |
| `apps/docs` deferred | Authored docs are reviewed as Markdown in `/docs`; a docs site is packaging, not content. | Docs need search/versioning/interactive API reference. |
| `tests/e2e`, `tests/load`, `tests/chaos` placeholders | Their targets (API, execution lifecycle, fault injection) do not exist yet. | The first vertical slice and the scheduler land. |
| Dashboard fixture data | The API exists, but the dashboard still renders fixtures. | The dashboard wiring phase (list + SSE). |

## CI

`.github/workflows/ci.yml` runs five jobs:

1. Go: tidy check, build, vet, race tests, golangci-lint.
2. Python: Ruff, format check, mypy and pytest for both projects.
3. Dashboard: Biome, `tsc`, production build.
4. Compose: validates `docker-compose.yml`.
5. Integration: builds and starts the API, orchestrator and worker, applies migrations, then runs the
   infrastructure, schema/RLS/immutability, outbox, API flow, messaging, worker-registration, distribution,
   priority/fairness, lease-renewal, stale-fencing and drain suites.

No job needs secrets. External model providers are never called by tests.

## Troubleshooting

**Port already in use** — another local project owns the port. Remap it in `.env`; every host port is configurable
(`POSTGRES_PORT`, `NATS_PORT`, `REDIS_PORT`, `MINIO_API_PORT`, `API_PORT`, `ORCHESTRATOR_GRPC_PORT`,
`ORCHESTRATOR_HTTP_PORT`, `WORKER_PORT`, `DASHBOARD_PORT`). Update `API_CORS_ORIGINS` if the dashboard port changes.
The non-Docker database/NATS URLs are derived by `make integration-test`; when running services by hand, update
`API_DATABASE_URL` / `ORCHESTRATOR_DATABASE_URL` / `WORKER_NATS_URL` to match.

**`schema "control" is missing`** — migrations have not run: `make migrate`.

**A service is unhealthy but the container runs** — check `make logs`. `/readyz` fails when a dependency is
down; `/healthz` only proves the process is alive. The worker's `/readyz` also requires a registered control
session, so an orchestrator outage makes the worker unready while its consumer drains.

**Operator messaging endpoints** — set `ORCHESTRATOR_ADMIN_TOKEN` to use `GET /v1/messaging/status` and
`POST /v1/messaging/dlq/redrive` on the orchestrator HTTP port. The routes return 404 when the token is empty.
For a quick redrive:

```bash
curl -H "Authorization: Bearer $ORCHESTRATOR_ADMIN_TOKEN" \
  -X POST http://localhost:8081/v1/messaging/dlq/redrive \
  -d '{"worker_id":"wrk_worker_01","limit":10}'
```

**Image pull failure for object storage** — see [Object storage](#object-storage) above; the image is pinned and
must exist in the registry.

**Stale local state** — `make reset` deletes volumes. Back up anything you care about first.
