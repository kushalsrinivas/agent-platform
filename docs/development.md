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
| Dashboard | http://localhost:3000 | Demo data until Phase 04 |
| API | http://localhost:8080 | `/healthz`, `/readyz`, `/v1/meta` |
| Orchestrator gRPC | localhost:9090 | reflection enabled in development |
| Orchestrator HTTP | http://localhost:8081 | health and version |
| Worker HTTP | http://localhost:8082 | health and version |
| PostgreSQL | localhost:5432 | `agent_platform`, user `postgres`/`postgres` |
| NATS | localhost:4222 / 8222 | JetStream enabled; 8222 is monitoring |
| Redis | localhost:6379 | |
| Object storage | http://localhost:9000 | S3 API; console at :9001 (`minioadmin` / `minioadmin123`) |

Migration order is `control` then `execution`; the one-shot `migrate` service enforces it. Application containers
wait for it with `depends_on: service_completed_successfully`.

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
| `make integration-test` | Start data stores, migrate, run `tests/integration`. |
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
  organisation discovery.
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
| `sqlc` deferred | The tables exist, but there are no query-heavy repositories yet; the outbox uses hand-written SQL with explicit locking. | The first CRUD/scheduling repository in Phase 04. |
| `go-playground/validator` deferred | No request bodies exist yet. Add it with the first API resource. | Phase 04 request models. |
| Normalized workflow node/edge tables | The immutable graph is one JSONB value, validated by the shared contract; normalizing it would create a second source of truth. | Graph querying at scale or per-node permissions (requires an ADR). |
| Deferred brief tables (`users`, `environments`, `api_keys`, `agent_deployments`, tool/MCP/approval/usage tables) | Each conflicts with a Phase 1 non-goal or lacks a design. The mapping and triggers are in [`phase-03-contracts.md`](phase-03-contracts.md). | The corresponding ADR/feature exists. |
| HTTP/2 gRPC contracts generated, no Python stubs | Python speaks to the worker over a UDS; generating stubs before the runtime ABI exists adds dead code. | Runtime ABI implementation. |
| `apps/docs` deferred | Authored docs are reviewed as Markdown in `/docs`; a docs site is packaging, not content. | Docs need search/versioning/interactive API reference. |
| `tests/e2e`, `tests/load`, `tests/chaos` placeholders | Their targets (API, execution lifecycle, fault injection) do not exist yet. | The first vertical slice and the scheduler land. |
| Dashboard fixture data | The API has no execution endpoints yet. Fixtures are Zod-validated and labelled in the UI. | Phase 04 list/SSE endpoints. |

## CI

`.github/workflows/ci.yml` runs five jobs:

1. Go: tidy check, build, vet, race tests, golangci-lint.
2. Python: Ruff, format check, mypy and pytest for both projects.
3. Dashboard: Biome, `tsc`, production build.
4. Compose: validates `docker-compose.yml`.
5. Integration: starts data stores, applies migrations, runs the infrastructure, schema/RLS and outbox suites.

No job needs secrets. External model providers are never called by tests.

## Troubleshooting

**Port already in use** — stop the competing service or remap the port in `docker-compose.yml`. Postgres can be
remapped without touching anything else: set `POSTGRES_PORT=55432` in `.env`, then use the same value in
`API_DATABASE_URL` / `ORCHESTRATOR_DATABASE_URL` when running services outside Docker.

**`schema "control" is missing`** — migrations have not run: `make migrate`.

**A service is unhealthy but the container runs** — check `make logs`. `/readyz` fails when a dependency is
down; `/healthz` only proves the process is alive.

**Image pull failure for object storage** — see [Object storage](#object-storage) above; the image is pinned and
must exist in the registry.

**Stale local state** — `make reset` deletes volumes. Back up anything you care about first.
